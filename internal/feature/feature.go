// Package feature implements the M3 IDE capabilities — completion,
// diagnostics, navigation and rename — on top of the indexed PSI snapshot.
//
// The package is intentionally thin: every operation reads from the same
// index.Indexer the IPC layer already exposes. Cross-file resolution is
// best-effort name-based: the bootstrap M2 backends do not perform full
// semantic analysis, so a "definition" is the symbol whose Name matches.
// Once a real tree-sitter grammar lands behind treesitter.Backend the
// resolver can grow type information without touching the IPC surface.
package feature

import (
	"sort"
	"strings"

	"github.com/zixiao-labs/ines/internal/index"
	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

// Service bundles every IDE feature behind one struct so the IPC server can
// hold a single reference. It is safe for concurrent use because every
// method only reads the indexer snapshot.
type Service struct {
	idx *index.Indexer
}

// New returns a Service backed by idx.
func New(idx *index.Indexer) *Service {
	return &Service{idx: idx}
}

// Location is the half-open range identifying a span inside a file.
type Location struct {
	Path  string
	Start int
	End   int
}

// CompletionItem is the result row used by Completion.
type CompletionItem struct {
	Label  string
	Kind   psi.Kind
	Detail string
	Path   string
}

// TextEdit represents a single replacement.
type TextEdit struct {
	Path    string
	Start   int
	End     int
	NewText string
}

// Completion returns the symbols whose name starts with prefix. Ordering
// favours symbols defined in the same file as path and falls back to
// alphabetical for the rest. Limit caps the result count; pass 0 for the
// default of 50.
func (s *Service) Completion(path, prefix string, limit int) []CompletionItem {
	if limit <= 0 {
		limit = 50
	}
	prefix = strings.ToLower(prefix)
	var items []CompletionItem
	seen := map[string]bool{}
	add := func(name string, kind psi.Kind, detail, sourcePath string) {
		if name == "" {
			return
		}
		key := sourcePath + "::" + name + "::" + string(kind)
		if seen[key] {
			return
		}
		seen[key] = true
		items = append(items, CompletionItem{
			Label:  name,
			Kind:   kind,
			Detail: detail,
			Path:   sourcePath,
		})
	}

	collect := func(entry *index.Entry) {
		if entry == nil || entry.File == nil {
			return
		}
		psi.Walk(entry.File, psi.VisitorFunc(func(el psi.Element) {
			if el == entry.File {
				return
			}
			if prefix != "" && !strings.HasPrefix(strings.ToLower(el.Name()), prefix) {
				return
			}
			add(el.Name(), el.Kind(), elementDetail(el), entry.Path)
		}))
	}

	if path != "" {
		collect(s.idx.Lookup(path))
	}
	for _, entry := range s.idx.Snapshot() {
		if entry == nil || entry.Path == path {
			continue
		}
		collect(entry)
	}

	sort.SliceStable(items, func(i, j int) bool {
		ai, aj := items[i].Path == path, items[j].Path == path
		if ai != aj {
			return ai
		}
		return items[i].Label < items[j].Label
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

// Definition returns the locations of every declaration whose Name matches
// the symbol at offset inside path. Cross-file matches are returned because
// the bootstrap parsers do not yet resolve fully-qualified names.
func (s *Service) Definition(path string, offset int) []Location {
	name := s.identifierAt(path, offset)
	if name == "" {
		return nil
	}
	return s.findDeclarations(name)
}

// References returns every offset where name is mentioned in the workspace.
// includeDeclaration switches on whether declaration sites are part of the
// result. Matching is identifier-aware: occurrences inside another word do
// not count.
func (s *Service) References(path string, offset int, includeDeclaration bool) []Location {
	name := s.identifierAt(path, offset)
	if name == "" {
		return nil
	}
	out := s.scanWorkspaceFor(name)
	if !includeDeclaration {
		decls := map[string]bool{}
		for _, d := range s.findDeclarations(name) {
			decls[locationKey(d)] = true
		}
		filtered := out[:0]
		for _, loc := range out {
			if !decls[locationKey(loc)] {
				filtered = append(filtered, loc)
			}
		}
		out = filtered
	}
	return out
}

// Rename returns the text edits that replace every occurrence of the
// identifier at offset with newName. Edits are grouped by file in source
// order so callers can apply them top-to-bottom without rebasing offsets.
func (s *Service) Rename(path string, offset int, newName string) (string, []TextEdit) {
	name := s.identifierAt(path, offset)
	if name == "" || newName == "" || name == newName {
		return name, nil
	}
	occurrences := s.scanWorkspaceFor(name)
	edits := make([]TextEdit, 0, len(occurrences))
	for _, occ := range occurrences {
		edits = append(edits, TextEdit{
			Path:    occ.Path,
			Start:   occ.Start,
			End:     occ.End,
			NewText: newName,
		})
	}
	sort.Slice(edits, func(i, j int) bool {
		if edits[i].Path != edits[j].Path {
			return edits[i].Path < edits[j].Path
		}
		return edits[i].Start < edits[j].Start
	})
	return name, edits
}

// Diagnostics returns the cached diagnostics for path. When path is empty,
// every indexed file's diagnostics are returned.
func (s *Service) Diagnostics(path string) map[string][]parser.Diagnostic {
	out := map[string][]parser.Diagnostic{}
	if path != "" {
		entry := s.idx.Lookup(path)
		if entry != nil && len(entry.Diagnostics) > 0 {
			out[path] = entry.Diagnostics
		}
		return out
	}
	for p, entry := range s.idx.Snapshot() {
		if entry != nil && len(entry.Diagnostics) > 0 {
			out[p] = entry.Diagnostics
		}
	}
	return out
}

// identifierAt extracts the identifier the byte offset falls inside.
func (s *Service) identifierAt(path string, offset int) string {
	entry := s.idx.Lookup(path)
	if entry == nil {
		return ""
	}
	src := entry.Source
	if src == nil {
		return ""
	}
	if offset < 0 || offset > len(src) {
		return ""
	}
	start := offset
	for start > 0 && isIdentRune(src[start-1]) {
		start--
	}
	end := offset
	for end < len(src) && isIdentRune(src[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return string(src[start:end])
}

// nameRanged is the optional surface implemented by PSI nodes that expose the
// identifier sub-range. We use it to keep declaration locations aligned with
// the byte-level matches scanOccurrencesIn returns; without it the comparison
// in References would never line up because Range() spans the whole body.
type nameRanged interface {
	NameRange() psi.Range
}

// detailed and signed are the optional surfaces implemented by PSI nodes that
// expose richer information about a declaration. Completion prefers the full
// signature when available so the renderer can show "func Foo(x int) error"
// rather than just the language id.
type detailed interface {
	Detail() string
}

type signed interface {
	Signature() string
}

func elementDetail(el psi.Element) string {
	if s, ok := el.(signed); ok {
		if sig := s.Signature(); sig != "" {
			return sig
		}
	}
	if d, ok := el.(detailed); ok {
		return d.Detail()
	}
	return ""
}

func (s *Service) findDeclarations(name string) []Location {
	var out []Location
	for _, entry := range s.idx.Snapshot() {
		if entry == nil || entry.File == nil {
			continue
		}
		psi.Walk(entry.File, psi.VisitorFunc(func(el psi.Element) {
			if el == entry.File {
				return
			}
			if el.Name() != name {
				return
			}
			r := el.Range()
			if nr, ok := el.(nameRanged); ok {
				if got := nr.NameRange(); got != (psi.Range{}) {
					r = got
				}
			}
			out = append(out, Location{Path: entry.Path, Start: r.Start, End: r.End})
		}))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Start < out[j].Start
	})
	return out
}

// scanWorkspaceFor walks the source of every indexed file and returns each
// identifier-aligned occurrence of name. We cannot rely on PSI alone here
// because most call sites are inside method bodies the M2 backends record
// only as opaque ranges.
func (s *Service) scanWorkspaceFor(name string) []Location {
	var out []Location
	if name == "" {
		return nil
	}
	for _, entry := range s.idx.Snapshot() {
		if entry == nil || entry.Source == nil {
			continue
		}
		out = append(out, scanOccurrencesIn(entry.Path, entry.Source, name)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Start < out[j].Start
	})
	return out
}

func scanOccurrencesIn(path string, source []byte, name string) []Location {
	var out []Location
	n := len(name)
	trivia := triviaMask(source)
	for i := 0; i <= len(source)-n; i++ {
		if source[i] != name[0] {
			continue
		}
		if string(source[i:i+n]) != name {
			continue
		}
		if i > 0 && isIdentRune(source[i-1]) {
			continue
		}
		if i+n < len(source) && isIdentRune(source[i+n]) {
			continue
		}
		if trivia[i] {
			continue
		}
		out = append(out, Location{Path: path, Start: i, End: i + n})
	}
	return out
}

// triviaMask returns a bitmap the same length as source where each byte that
// belongs to a comment, string literal or template literal is marked true. The
// rules we honour are common to Go, TypeScript, JavaScript, Rust, Java, Swift
// and C/C++: //-line comments, /* */-block comments, single/double-quoted
// strings, and back-tick template literals. That covers every M2 backend; the
// only false positive on languages without templates is a stray back-tick,
// which would have to be balanced inside source to mislead us anyway.
func triviaMask(source []byte) []bool {
	mask := make([]bool, len(source))
	i := 0
	for i < len(source) {
		c := source[i]
		switch {
		case c == '/' && i+1 < len(source) && source[i+1] == '/':
			start := i
			i += 2
			for i < len(source) && source[i] != '\n' {
				i++
			}
			markRange(mask, start, i)
		case c == '/' && i+1 < len(source) && source[i+1] == '*':
			start := i
			i += 2
			for i+1 < len(source) && !(source[i] == '*' && source[i+1] == '/') {
				i++
			}
			if i+1 < len(source) {
				i += 2
			} else {
				i = len(source)
			}
			markRange(mask, start, i)
		case c == '"' || c == '\'':
			start := i
			quote := c
			i++
			for i < len(source) && source[i] != quote {
				if source[i] == '\\' && i+1 < len(source) {
					i += 2
					continue
				}
				if source[i] == '\n' {
					// Unterminated string: stop the literal at the line
					// break so we do not mask the whole rest of the file.
					break
				}
				i++
			}
			if i < len(source) && source[i] == quote {
				i++
			}
			markRange(mask, start, i)
		case c == '`':
			start := i
			i++
			for i < len(source) && source[i] != '`' {
				if source[i] == '\\' && i+1 < len(source) {
					i += 2
					continue
				}
				if source[i] == '$' && i+1 < len(source) && source[i+1] == '{' {
					// Template substitutions are real code; mask only the
					// literal portion preceding ${, then resume scanning.
					markRange(mask, start, i)
					i += 2
					depth := 1
					for i < len(source) && depth > 0 {
						switch source[i] {
						case '{':
							depth++
						case '}':
							depth--
						}
						i++
					}
					start = i
					continue
				}
				i++
			}
			if i < len(source) && source[i] == '`' {
				i++
			}
			markRange(mask, start, i)
		default:
			i++
		}
	}
	return mask
}

func markRange(mask []bool, start, end int) {
	if start < 0 {
		start = 0
	}
	if end > len(mask) {
		end = len(mask)
	}
	for i := start; i < end; i++ {
		mask[i] = true
	}
}

func locationKey(l Location) string {
	return l.Path + ":" + itoa(l.Start) + "-" + itoa(l.End)
}

func itoa(v int) string {
	// Avoid bringing in fmt for a cold hot-path key; the indexer can call
	// rename across thousands of locations and fmt.Sprintf("%d") is the
	// slowest piece of that loop in practice.
	if v == 0 {
		return "0"
	}
	var (
		buf [20]byte
		i   = len(buf)
		n   = v
		neg bool
	)
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func isIdentRune(b byte) bool {
	if b >= '0' && b <= '9' {
		return true
	}
	if b >= 'A' && b <= 'Z' {
		return true
	}
	if b >= 'a' && b <= 'z' {
		return true
	}
	return b == '_' || b == '$' || b >= 0x80
}

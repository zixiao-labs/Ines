// rustBackend is the M2 Rust adapter for Ines. It replaces the
// line-oriented regex bootstrap with a hand-written, bracket- and
// comment-aware scanner. The scanner mirrors the structure rust-analyzer's
// grammar walks at the item level: outer attributes, visibility/qualifier
// modifiers (pub, unsafe, async, default, extern, auto, const), then the
// item keyword (use, fn, struct, union, enum, trait, impl, mod, const,
// static, type, macro_rules!, macro, extern).
//
// The implementation is intentionally hand-written rather than wrapping a
// real tree-sitter grammar: tree-sitter requires CGO and would force us to
// ship per-OS shared libraries, which conflicts with Ines's
// "single static binary" rule. The Backend interface is shaped after
// tree-sitter's vocabulary so a future swap to a real grammar is a
// drop-in replacement.
//
// Compared to rust-analyzer the scanner is dramatically smaller because it
// only needs to surface what the editor's outline view, navigation, and
// rename refactorings consume — top-level item shape, not full HIR /
// type-check information. Block bodies are skipped; we rely on token
// classification (string/char/comment/block depth) to find the matching
// `}` even in the presence of `'a'`-style char literals, raw string
// literals and nested `/* /* */ */` block comments — all the edge cases
// where a naive brace-counting scanner trips.
package rust

import (
	"strings"
	"unicode"

	"github.com/zixiao-labs/ines/internal/lang/treesitter"
	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

type rustBackend struct{}

func newRustBackend() treesitter.Backend { return &rustBackend{} }

func (r *rustBackend) Language() string { return "rust" }

func (r *rustBackend) Parse(src parser.Source) (*treesitter.Tree, error) {
	tree := &treesitter.Tree{
		Path:     src.Path,
		Language: "rust",
		Source:   src.Content,
	}
	if len(src.Content) == 0 {
		return tree, nil
	}
	s := newRustScanner(src.Content, tree)
	s.scanItems(&tree.Symbols)
	return tree, nil
}

// rustScanner walks the source byte-by-byte. The cursor `i` always points
// at the next byte to inspect; helpers advance it past the token they
// consume.
type rustScanner struct {
	src  []byte
	i    int
	tree *treesitter.Tree
}

func newRustScanner(src []byte, tree *treesitter.Tree) *rustScanner {
	return &rustScanner{src: src, tree: tree}
}

// scanItems consumes a sequence of items terminated by EOF or `}`. The
// closing brace is consumed before return so callers parsing nested
// bodies can resume immediately after the body.
func (s *rustScanner) scanItems(out *[]*treesitter.Symbol) {
	for s.i < len(s.src) {
		s.skipTrivia()
		if s.i >= len(s.src) {
			return
		}
		c := s.src[s.i]
		if c == '}' {
			s.i++
			return
		}
		if c == '#' {
			// Outer or inner attribute: #[...] or #![...]
			s.skipAttribute()
			continue
		}
		start := s.i
		s.skipModifiers()
		s.skipTrivia()
		kw := s.peekIdent()
		switch kw {
		case "use":
			if sym := s.parseUse(start); sym != nil {
				*out = append(*out, sym)
			}
		case "fn":
			if sym := s.parseFn(start); sym != nil {
				*out = append(*out, sym)
			}
		case "struct", "union":
			if sym := s.parseStruct(start); sym != nil {
				*out = append(*out, sym)
			}
		case "enum":
			if sym := s.parseEnum(start); sym != nil {
				*out = append(*out, sym)
			}
		case "trait":
			if sym := s.parseTrait(start); sym != nil {
				*out = append(*out, sym)
			}
		case "impl":
			if sym := s.parseImpl(start); sym != nil {
				*out = append(*out, sym)
			}
		case "mod":
			if sym := s.parseMod(start); sym != nil {
				*out = append(*out, sym)
			}
		case "const":
			if sym := s.parseConstStatic(start, "const"); sym != nil {
				*out = append(*out, sym)
			}
		case "static":
			if sym := s.parseConstStatic(start, "static"); sym != nil {
				*out = append(*out, sym)
			}
		case "type":
			if sym := s.parseTypeAlias(start); sym != nil {
				*out = append(*out, sym)
			}
		case "macro_rules":
			if sym := s.parseMacroRules(start); sym != nil {
				*out = append(*out, sym)
			}
		case "macro":
			if sym := s.parseMacro2(start); sym != nil {
				*out = append(*out, sym)
			}
		case "extern":
			if sym := s.parseExtern(start); sym != nil {
				*out = append(*out, sym)
			}
		default:
			// Recover by skipping to the next item boundary. This keeps
			// the outline useful even on partial input.
			if !s.skipToNextItem() {
				return
			}
		}
	}
}

// skipModifiers consumes pre-keyword modifiers in any order. The set
// matches rust-analyzer's ITEM modifier set, with one wrinkle: `const`
// and `extern` are also item keywords, so we only treat them as
// modifiers when the *following* token is itself an item keyword
// indicating a function or another modifier.
func (s *rustScanner) skipModifiers() {
	for {
		s.skipTrivia()
		saved := s.i
		kw := s.peekIdent()
		switch kw {
		case "pub":
			s.readIdent()
			s.skipTrivia()
			if s.i < len(s.src) && s.src[s.i] == '(' {
				s.skipMatched('(', ')')
			}
			continue
		case "unsafe", "async", "default", "auto":
			s.readIdent()
			continue
		case "const":
			// `const fn`, `const unsafe fn`, `const async fn`.
			s.readIdent()
			s.skipTrivia()
			if next := s.peekIdent(); next == "fn" || next == "unsafe" ||
				next == "async" || next == "extern" || next == "default" {
				continue
			}
			s.i = saved
			return
		case "extern":
			// `extern "C" fn ...` or `extern fn ...` (modifier form).
			s.readIdent()
			s.skipTrivia()
			if s.i < len(s.src) && s.src[s.i] == '"' {
				s.skipString('"')
				s.skipTrivia()
			}
			if next := s.peekIdent(); next == "fn" || next == "unsafe" ||
				next == "async" || next == "type" {
				continue
			}
			// Not a modifier — restore the cursor so the parseExtern
			// branch sees the original `extern`.
			s.i = saved
			return
		}
		return
	}
}

func (s *rustScanner) parseUse(start int) *treesitter.Symbol {
	s.readIdent() // consume 'use'
	pathStart := s.skipTriviaReturnIndex()
	end := s.skipUntilStatementEnd()
	chunk := s.src[pathStart:end]
	path := strings.TrimSpace(string(chunk))
	// Strip a trailing `as Foo` suffix from the displayed name so the
	// outline reads as just the imported path.
	displayName := usePathDisplayName(path)
	return &treesitter.Symbol{
		Kind:   psi.KindImport,
		Name:   displayName,
		Detail: strings.TrimSpace(string(s.src[start:end])),
		Range:  psi.Range{Start: start, End: end},
	}
}

func usePathDisplayName(path string) string {
	// Trim a trailing semicolon if it slipped through, then drop the
	// inline rename clause that `use` allows.
	path = strings.TrimSuffix(path, ";")
	if idx := strings.Index(path, " as "); idx >= 0 {
		path = path[:idx]
	}
	return strings.TrimSpace(path)
}

func (s *rustScanner) parseFn(start int) *treesitter.Symbol {
	s.readIdent() // consume 'fn'
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	// Optional generic params <T, U: Trait>
	s.skipTrivia()
	if s.i < len(s.src) && s.src[s.i] == '<' {
		s.skipMatched('<', '>')
	}
	params := s.parseFnParams()
	// Walk past return type / where clause until either `{` (body), `;`
	// (declaration in trait), or EOF.
	end := s.skipFnTail()
	signature := strings.TrimSpace(string(s.src[start:end]))
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.skipBraceBody()
		end = s.i
	} else if s.i < len(s.src) && s.src[s.i] == ';' {
		s.i++
		end = s.i
	}
	return &treesitter.Symbol{
		Kind:      psi.KindFunction,
		Name:      name,
		Range:     psi.Range{Start: start, End: end},
		NameRange: nameRange,
		Signature: trimSig(signature),
		Children:  params,
	}
}

// skipFnTail walks forward until it lands on the function body delimiter
// (`{`) or declaration end (`;`). It honours parens for return types
// (`-> impl Iterator<Item = T>`) and respects strings/comments.
func (s *rustScanner) skipFnTail() int {
	depth := 0
	for s.i < len(s.src) {
		c := s.src[s.i]
		if depth == 0 && (c == '{' || c == ';') {
			return s.i
		}
		switch c {
		case '<':
			s.skipMatched('<', '>')
			continue
		case '(':
			s.skipMatched('(', ')')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '"':
			s.skipString('"')
			continue
		case '\'':
			s.skipCharOrLifetime()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		s.i++
		_ = depth
	}
	return s.i
}

// parseFnParams consumes `(...)` and returns one Symbol per parameter.
// Parameters that begin with a destructuring pattern (`{a, b}: Foo`,
// `(x, y): (i32, i32)`, `&self`, `mut self`) are recorded with their
// rendered text as Name so they still surface in the outline.
func (s *rustScanner) parseFnParams() []*treesitter.Symbol {
	if s.i >= len(s.src) || s.src[s.i] != '(' {
		return nil
	}
	open := s.i
	s.i++
	var params []*treesitter.Symbol
	for s.i < len(s.src) {
		s.skipTrivia()
		if s.i >= len(s.src) || s.src[s.i] == ')' {
			break
		}
		paramStart := s.i
		// Consume optional outer attribute on the parameter.
		if s.src[s.i] == '#' {
			s.skipAttribute()
			s.skipTrivia()
		}
		// Skip leading `&`, `&'a`, `mut`, `ref`.
		consumed := false
		for {
			s.skipTrivia()
			if s.i < len(s.src) && s.src[s.i] == '&' {
				s.i++
				s.skipTrivia()
				if s.i < len(s.src) && s.src[s.i] == '\'' {
					s.skipCharOrLifetime()
					s.skipTrivia()
				}
				consumed = true
				continue
			}
			next := s.peekIdent()
			if next == "mut" || next == "ref" {
				s.readIdent()
				consumed = true
				continue
			}
			break
		}
		_ = consumed
		// Now we're at the binding. It can be:
		//   - `self`
		//   - identifier (followed by `:` and a type)
		//   - destructuring pattern `(...)`, `[...]`, `{...}`, `_`
		var nameStart int
		var name string
		switch {
		case s.i < len(s.src) && (s.src[s.i] == '(' || s.src[s.i] == '[' || s.src[s.i] == '{'):
			// Destructuring pattern. Capture the rendered text up to ':'
			// or ',' or ')'.
			startPat := s.i
			open := s.src[s.i]
			closeCh := byte(')')
			switch open {
			case '[':
				closeCh = ']'
			case '{':
				closeCh = '}'
			}
			s.skipMatched(open, closeCh)
			name = strings.TrimSpace(string(s.src[startPat:s.i]))
			nameStart = startPat
		case s.i < len(s.src) && s.src[s.i] == '_':
			nameStart = s.i
			s.i++
			name = "_"
		default:
			nameStart = s.i
			name = s.readIdent()
		}
		// Walk past the rest of the param (`: Type = default`) honouring
		// brackets so `Vec<(A, B)>` does not confuse us.
		s.skipUntilParamSeparator()
		if name == "" {
			// Defensive: avoid zero-progress loops on garbage input.
			if s.i == paramStart {
				s.i++
			}
		} else {
			params = append(params, &treesitter.Symbol{
				Kind:      psi.KindParameter,
				Name:      name,
				Range:     psi.Range{Start: nameStart, End: nameStart + len(name)},
				NameRange: psi.Range{Start: nameStart, End: nameStart + len(name)},
				Detail:    strings.TrimSpace(string(s.src[paramStart:s.i])),
			})
		}
		if s.i < len(s.src) && s.src[s.i] == ',' {
			s.i++
		}
	}
	if s.i < len(s.src) && s.src[s.i] == ')' {
		s.i++
	}
	_ = open
	return params
}

func (s *rustScanner) skipUntilParamSeparator() {
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == ',' || c == ')' {
			return
		}
		switch c {
		case '<':
			s.skipMatched('<', '>')
			continue
		case '(':
			s.skipMatched('(', ')')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '{':
			s.skipMatched('{', '}')
			continue
		case '"':
			s.skipString('"')
			continue
		case '\'':
			s.skipCharOrLifetime()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		s.i++
	}
}

func (s *rustScanner) parseStruct(start int) *treesitter.Symbol {
	kw := s.readIdent() // 'struct' or 'union'
	_ = kw
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	if s.i < len(s.src) && s.src[s.i] == '<' {
		s.skipMatched('<', '>')
	}
	// Walk to the body delimiter. Unlike fn / enum / trait the struct
	// header may end on `(` (tuple struct) so we cannot reuse skipFnTail.
	headerEnd := s.skipUntilStructBody()
	header := strings.TrimSpace(string(s.src[start:headerEnd]))
	sym := &treesitter.Symbol{
		Kind:      psi.KindStruct,
		Name:      name,
		Range:     psi.Range{Start: start, End: headerEnd},
		NameRange: nameRange,
		Signature: trimSig(header),
	}
	if s.i >= len(s.src) {
		sym.Range.End = s.i
		return sym
	}
	switch s.src[s.i] {
	case '{':
		s.i++ // step past '{'
		s.parseRecordFields(sym)
	case '(':
		s.parseTupleFields(sym)
		// Tuple structs end with `;`; an optional where-clause may sit
		// between the `)` and the `;`.
		s.skipUntilStatementEnd()
		if s.i < len(s.src) && s.src[s.i] == ';' {
			s.i++
		}
	case ';':
		// Unit struct.
		s.i++
	}
	sym.Range.End = s.i
	return sym
}

// skipUntilStructBody walks forward until the cursor lands on a struct
// body delimiter: `{` (record), `(` (tuple) or `;` (unit). Generics,
// where-clauses, return types, strings and comments are honoured along
// the way.
func (s *rustScanner) skipUntilStructBody() int {
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == '{' || c == '(' || c == ';' {
			return s.i
		}
		switch c {
		case '<':
			s.skipMatched('<', '>')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '"':
			s.skipString('"')
			continue
		case '\'':
			s.skipCharOrLifetime()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		s.i++
	}
	return s.i
}

func (s *rustScanner) parseRecordFields(parent *treesitter.Symbol) {
	for s.i < len(s.src) {
		s.skipTrivia()
		if s.i >= len(s.src) {
			return
		}
		c := s.src[s.i]
		if c == '}' {
			s.i++
			return
		}
		if c == '#' {
			s.skipAttribute()
			continue
		}
		// Skip visibility prefix.
		if s.peekIdent() == "pub" {
			s.readIdent()
			s.skipTrivia()
			if s.i < len(s.src) && s.src[s.i] == '(' {
				s.skipMatched('(', ')')
			}
		}
		s.skipTrivia()
		fieldStart := s.i
		name := s.readIdent()
		if name == "" {
			// Defensive recovery: jump to the next ',' or '}'.
			for s.i < len(s.src) && s.src[s.i] != ',' && s.src[s.i] != '}' {
				s.i++
			}
			if s.i < len(s.src) && s.src[s.i] == ',' {
				s.i++
			}
			continue
		}
		nameRange := psi.Range{Start: fieldStart, End: s.i}
		// Walk the type until ',' or '}' at brace depth 0.
		typeStart := s.i
		s.skipUntilFieldSeparator()
		typeText := strings.TrimSpace(string(s.src[typeStart:s.i]))
		end := s.i
		parent.Children = append(parent.Children, &treesitter.Symbol{
			Kind:      psi.KindField,
			Name:      name,
			Range:     psi.Range{Start: fieldStart, End: end},
			NameRange: nameRange,
			Detail:    typeText,
		})
		if s.i < len(s.src) && s.src[s.i] == ',' {
			s.i++
		}
	}
}

func (s *rustScanner) skipUntilFieldSeparator() {
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == ',' || c == '}' {
			return
		}
		switch c {
		case '<':
			s.skipMatched('<', '>')
			continue
		case '(':
			s.skipMatched('(', ')')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '{':
			s.skipMatched('{', '}')
			continue
		case '"':
			s.skipString('"')
			continue
		case '\'':
			s.skipCharOrLifetime()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		s.i++
	}
}

func (s *rustScanner) parseTupleFields(parent *treesitter.Symbol) {
	if s.i >= len(s.src) || s.src[s.i] != '(' {
		return
	}
	s.i++
	idx := 0
	for s.i < len(s.src) {
		s.skipTrivia()
		if s.i >= len(s.src) || s.src[s.i] == ')' {
			break
		}
		if s.src[s.i] == '#' {
			s.skipAttribute()
			continue
		}
		if s.peekIdent() == "pub" {
			s.readIdent()
			s.skipTrivia()
			if s.i < len(s.src) && s.src[s.i] == '(' {
				s.skipMatched('(', ')')
			}
		}
		fieldStart := s.i
		s.skipUntilParamSeparator()
		typeText := strings.TrimSpace(string(s.src[fieldStart:s.i]))
		if typeText != "" {
			parent.Children = append(parent.Children, &treesitter.Symbol{
				Kind:   psi.KindField,
				Name:   numericIndexName(idx),
				Detail: typeText,
				Range:  psi.Range{Start: fieldStart, End: s.i},
			})
			idx++
		}
		if s.i < len(s.src) && s.src[s.i] == ',' {
			s.i++
		}
	}
	if s.i < len(s.src) && s.src[s.i] == ')' {
		s.i++
	}
}

func numericIndexName(i int) string {
	// Render small ints without pulling in strconv (cold path elsewhere
	// uses the compiler's mass-strconv but we keep this allocation-light
	// because a tuple struct rarely has >10 fields).
	if i < 10 {
		return string(rune('0' + i))
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func (s *rustScanner) parseEnum(start int) *treesitter.Symbol {
	s.readIdent() // 'enum'
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	if s.i < len(s.src) && s.src[s.i] == '<' {
		s.skipMatched('<', '>')
	}
	headerEnd := s.skipFnTail()
	header := strings.TrimSpace(string(s.src[start:headerEnd]))
	sym := &treesitter.Symbol{
		Kind:      psi.KindEnum,
		Name:      name,
		Range:     psi.Range{Start: start, End: headerEnd},
		NameRange: nameRange,
		Signature: trimSig(header),
	}
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.i++
		s.parseEnumVariants(sym)
	}
	sym.Range.End = s.i
	return sym
}

func (s *rustScanner) parseEnumVariants(parent *treesitter.Symbol) {
	for s.i < len(s.src) {
		s.skipTrivia()
		if s.i >= len(s.src) {
			return
		}
		c := s.src[s.i]
		if c == '}' {
			s.i++
			return
		}
		if c == '#' {
			s.skipAttribute()
			continue
		}
		variantStart := s.i
		nameStart := s.i
		name := s.readIdent()
		if name == "" {
			s.i++
			continue
		}
		nameRange := psi.Range{Start: nameStart, End: s.i}
		s.skipTrivia()
		if s.i < len(s.src) {
			switch s.src[s.i] {
			case '{':
				s.skipMatched('{', '}')
			case '(':
				s.skipMatched('(', ')')
			case '=':
				s.i++
				s.skipUntilParamSeparator()
			}
		}
		end := s.i
		parent.Children = append(parent.Children, &treesitter.Symbol{
			Kind:      psi.KindField,
			Name:      name,
			Range:     psi.Range{Start: variantStart, End: end},
			NameRange: nameRange,
			Detail:    strings.TrimSpace(string(s.src[variantStart:end])),
		})
		s.skipTrivia()
		if s.i < len(s.src) && s.src[s.i] == ',' {
			s.i++
		}
	}
}

func (s *rustScanner) parseTrait(start int) *treesitter.Symbol {
	s.readIdent()
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	if s.i < len(s.src) && s.src[s.i] == '<' {
		s.skipMatched('<', '>')
	}
	headerEnd := s.skipFnTail()
	header := strings.TrimSpace(string(s.src[start:headerEnd]))
	sym := &treesitter.Symbol{
		Kind:      psi.KindInterface,
		Name:      name,
		Range:     psi.Range{Start: start, End: headerEnd},
		NameRange: nameRange,
		Signature: trimSig(header),
	}
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.i++
		s.scanItems(&sym.Children)
	}
	sym.Range.End = s.i
	return sym
}

func (s *rustScanner) parseImpl(start int) *treesitter.Symbol {
	s.readIdent() // 'impl'
	s.skipTrivia()
	if s.i < len(s.src) && s.src[s.i] == '<' {
		s.skipMatched('<', '>')
	}
	// Capture the header up to the body brace so we can derive a useful
	// display name. The header looks like:
	//   <Trait> for <Type>      // trait impl
	//   <Type>                  // inherent impl
	headerStart := s.skipTriviaReturnIndex()
	headerEnd := s.skipFnTail()
	header := strings.TrimSpace(string(s.src[headerStart:headerEnd]))
	name := implDisplayName(header)
	// NameRange is the span of the implementing type within the header
	// — that is, what users typically click to go to "impl X" — so we
	// keep it honest even when the display name keeps the `Trait for `
	// prefix.
	nameRange := implementingTypeRange(s.src, headerStart, headerEnd)
	sym := &treesitter.Symbol{
		Kind:      psi.KindClass,
		Name:      name,
		Range:     psi.Range{Start: start, End: headerEnd},
		NameRange: nameRange,
		Signature: trimSig(strings.TrimSpace(string(s.src[start:headerEnd]))),
	}
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.i++
		s.scanItems(&sym.Children)
		// Mark methods inside the impl as KindMethod for outline parity
		// with class-style languages.
		for _, child := range sym.Children {
			if child.Kind == psi.KindFunction {
				child.Kind = psi.KindMethod
			}
		}
	}
	sym.Range.End = s.i
	return sym
}

// implementingTypeRange returns the byte range of the type being
// implemented inside an impl header. For `Display for Foo<T>` it points
// at `Foo<T>`; for `Foo<T>` (inherent impl) it points at the whole header.
// The scanner is byte-driven so the test suite can spot-check the range
// against the source bytes.
func implementingTypeRange(src []byte, start, end int) psi.Range {
	if start >= end {
		return psi.Range{Start: start, End: end}
	}
	chunk := src[start:end]
	// Locate ` for ` outside any bracket nesting.
	depth := 0
	for i := 0; i+5 <= len(chunk); i++ {
		c := chunk[i]
		switch c {
		case '<', '(', '[':
			depth++
			continue
		case '>', ')', ']':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		if string(chunk[i:i+5]) == " for " {
			absStart := start + i + 5
			return psi.Range{Start: absStart, End: end}
		}
	}
	return psi.Range{Start: start, End: end}
}

// implDisplayName extracts a reasonable outline label from an impl header.
//
// The convention mirrors rust-analyzer's outline:
//
//   - inherent impl `impl<T> Foo<T>`        →  `Foo<T>`
//   - trait impl     `impl Display for Foo` →  `Display for Foo`
//
// Keeping the trait name in trait impls makes the outline unambiguous
// when a single type implements multiple traits, and ensures the impl
// symbol name does not collide with the underlying struct's symbol —
// which matters because Ines's identifier-based go-to-definition
// surfaces every Symbol whose Name matches the clicked-on identifier.
func implDisplayName(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return "impl"
	}
	if idx := strings.Index(header, "where"); idx >= 0 {
		header = strings.TrimSpace(header[:idx])
	}
	if idx := strings.IndexByte(header, '{'); idx >= 0 {
		header = strings.TrimSpace(header[:idx])
	}
	if header == "" {
		return "impl"
	}
	return header
}

func (s *rustScanner) parseMod(start int) *treesitter.Symbol {
	s.readIdent()
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	s.skipTrivia()
	end := s.i
	sym := &treesitter.Symbol{
		Kind:      psi.KindNamespace,
		Name:      name,
		Range:     psi.Range{Start: start, End: end},
		NameRange: nameRange,
	}
	if s.i < len(s.src) {
		switch s.src[s.i] {
		case ';':
			s.i++
			sym.Range.End = s.i
		case '{':
			s.i++
			s.scanItems(&sym.Children)
			sym.Range.End = s.i
		}
	}
	return sym
}

func (s *rustScanner) parseConstStatic(start int, kw string) *treesitter.Symbol {
	s.readIdent() // 'const' / 'static'
	s.skipTrivia()
	if s.peekIdent() == "mut" {
		s.readIdent()
		s.skipTrivia()
	}
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	end := s.skipUntilStatementEnd()
	if s.i < len(s.src) && s.src[s.i] == ';' {
		s.i++
		end = s.i
	}
	return &treesitter.Symbol{
		Kind:      psi.KindVariable,
		Name:      name,
		Detail:    strings.TrimSpace(string(s.src[start:end])),
		Range:     psi.Range{Start: start, End: end},
		NameRange: nameRange,
		Signature: kw + " " + name,
	}
}

func (s *rustScanner) parseTypeAlias(start int) *treesitter.Symbol {
	s.readIdent()
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	end := s.skipUntilStatementEnd()
	if s.i < len(s.src) && s.src[s.i] == ';' {
		s.i++
		end = s.i
	}
	return &treesitter.Symbol{
		Kind:      psi.KindTypeAlias,
		Name:      name,
		Range:     psi.Range{Start: start, End: end},
		NameRange: nameRange,
		Detail:    strings.TrimSpace(string(s.src[start:end])),
	}
}

func (s *rustScanner) parseMacroRules(start int) *treesitter.Symbol {
	s.readIdent() // 'macro_rules'
	s.skipTrivia()
	if s.i < len(s.src) && s.src[s.i] == '!' {
		s.i++
	}
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	s.skipTrivia()
	if s.i < len(s.src) {
		switch s.src[s.i] {
		case '{':
			s.skipMatched('{', '}')
		case '(':
			s.skipMatched('(', ')')
		case '[':
			s.skipMatched('[', ']')
		}
	}
	return &treesitter.Symbol{
		Kind:      psi.KindFunction,
		Name:      name,
		Range:     psi.Range{Start: start, End: s.i},
		NameRange: nameRange,
		Detail:    "macro_rules!",
	}
}

func (s *rustScanner) parseMacro2(start int) *treesitter.Symbol {
	s.readIdent() // 'macro'
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	s.skipTrivia()
	// macro foo(...) { ... }   — both delimiter sets are acceptable.
	if s.i < len(s.src) && s.src[s.i] == '(' {
		s.skipMatched('(', ')')
		s.skipTrivia()
	}
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.skipMatched('{', '}')
	}
	return &treesitter.Symbol{
		Kind:      psi.KindFunction,
		Name:      name,
		Range:     psi.Range{Start: start, End: s.i},
		NameRange: nameRange,
		Detail:    "macro",
	}
}

func (s *rustScanner) parseExtern(start int) *treesitter.Symbol {
	s.readIdent() // 'extern'
	s.skipTrivia()
	// `extern crate foo;` or `extern crate foo as bar;`
	if s.peekIdent() == "crate" {
		s.readIdent()
		s.skipTrivia()
		nameStart := s.i
		name := s.readIdent()
		if name == "" {
			s.skipUntilStatementEnd()
			if s.i < len(s.src) && s.src[s.i] == ';' {
				s.i++
			}
			return nil
		}
		nameRange := psi.Range{Start: nameStart, End: s.i}
		end := s.skipUntilStatementEnd()
		if s.i < len(s.src) && s.src[s.i] == ';' {
			s.i++
			end = s.i
		}
		return &treesitter.Symbol{
			Kind:      psi.KindImport,
			Name:      name,
			Detail:    strings.TrimSpace(string(s.src[start:end])),
			Range:     psi.Range{Start: start, End: end},
			NameRange: nameRange,
		}
	}
	// `extern "ABI" { ... }` block.
	abi := ""
	if s.i < len(s.src) && s.src[s.i] == '"' {
		strStart := s.i
		s.skipString('"')
		abi = strings.Trim(string(s.src[strStart:s.i]), "\"")
		s.skipTrivia()
	}
	if s.i >= len(s.src) || s.src[s.i] != '{' {
		s.skipUntilStatementEnd()
		return nil
	}
	s.i++ // step into the block
	sym := &treesitter.Symbol{
		Kind:   psi.KindNamespace,
		Name:   "extern",
		Detail: "extern \"" + abi + "\"",
		Range:  psi.Range{Start: start, End: s.i},
	}
	if abi != "" {
		sym.Name = "extern \"" + abi + "\""
	}
	s.scanItems(&sym.Children)
	sym.Range.End = s.i
	return sym
}

// skipToNextItem is the recovery routine for the main scan loop. It
// advances past the next `;` or balanced `{...}` so the loop can resume
// at a fresh statement boundary. Returns false when EOF is reached
// without finding a recovery point — the caller then halts.
func (s *rustScanner) skipToNextItem() bool {
	for s.i < len(s.src) {
		c := s.src[s.i]
		switch c {
		case ';':
			s.i++
			return true
		case '{':
			s.skipMatched('{', '}')
			return true
		case '"':
			s.skipString('"')
			continue
		case '\'':
			s.skipCharOrLifetime()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		s.i++
	}
	return false
}

// skipBraceBody assumes the cursor is parked on `{`. It steps past the
// matching `}` while honouring nested strings/comments/brackets.
func (s *rustScanner) skipBraceBody() {
	if s.i >= len(s.src) || s.src[s.i] != '{' {
		return
	}
	s.skipMatched('{', '}')
}

// skipUntilStatementEnd walks until the next `;` at the current bracket
// depth, returning the byte index of that semicolon (or EOF).
func (s *rustScanner) skipUntilStatementEnd() int {
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == ';' {
			return s.i
		}
		switch c {
		case '<':
			s.skipMatched('<', '>')
			continue
		case '(':
			s.skipMatched('(', ')')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '{':
			s.skipMatched('{', '}')
			continue
		case '"':
			s.skipString('"')
			continue
		case '\'':
			s.skipCharOrLifetime()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		s.i++
	}
	return s.i
}

// skipMatched walks past a balanced `<open><close>` block. The cursor
// must be parked on `open`. Strings, char/lifetime literals and comments
// are honoured so braces inside them do not unbalance the count.
//
// `<` and `>` are special-cased: Rust uses them for both generics
// (balanced) and comparisons (`x > 0`). We treat the angle-bracket pair
// the same way other languages treat a generic argument list — starting
// on a literal `<` we walk forward until we see a matching `>` at depth
// 0. If a comment / string would have started in between we handle it
// normally. This is a heuristic, but it's the same one tree-sitter
// employs at the syntactic level and it suffices for outline-grade
// information.
func (s *rustScanner) skipMatched(open, close byte) {
	if s.i >= len(s.src) || s.src[s.i] != open {
		return
	}
	s.i++
	depth := 1
	for s.i < len(s.src) && depth > 0 {
		c := s.src[s.i]
		switch c {
		case open:
			if open != '<' || s.canTreatAsGenericOpen() {
				depth++
				s.i++
				continue
			}
		case close:
			if open == '<' && !s.canTreatAsGenericClose() {
				s.i++
				continue
			}
			depth--
			s.i++
			if depth == 0 {
				return
			}
			continue
		case '"':
			s.skipString('"')
			continue
		case '\'':
			s.skipCharOrLifetime()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		case 'r':
			if s.skipRawStringMaybe() {
				continue
			}
		case 'b':
			if s.skipByteStringMaybe() {
				continue
			}
		case 'c':
			if s.skipCStringMaybe() {
				continue
			}
		}
		// Skip any other matched delimiter we encounter so the body
		// brace counter only grows for `open` itself.
		if open != '{' && (c == '{') {
			s.skipMatched('{', '}')
			continue
		}
		if open != '(' && c == '(' {
			s.skipMatched('(', ')')
			continue
		}
		if open != '[' && c == '[' {
			s.skipMatched('[', ']')
			continue
		}
		s.i++
	}
}

// canTreatAsGenericOpen reports whether a `<` at the current position is
// likely an opening generic bracket vs. a less-than operator. This is the
// classic ambiguity in Rust source. The scanner uses it only inside a
// `skipMatched('<', '>')` call — so the caller has *already decided* it's
// a generic — and the helper just keeps balanced nesting honest.
func (s *rustScanner) canTreatAsGenericOpen() bool { return true }
func (s *rustScanner) canTreatAsGenericClose() bool {
	// `>>` could be either two close-brackets (Rust nested generics like
	// `Vec<Vec<T>>`) or a shift operator. Inside a generic walk the
	// scanner accepts `>>` as two closes, which is the right call for
	// outline accuracy.
	return true
}

func (s *rustScanner) skipString(q byte) {
	if s.i >= len(s.src) || s.src[s.i] != q {
		return
	}
	s.i++
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == '\\' && s.i+1 < len(s.src) {
			s.i += 2
			continue
		}
		if c == q {
			s.i++
			return
		}
		s.i++
	}
	s.tree.Diagnostics = append(s.tree.Diagnostics, treesitter.Diagnostic{
		Severity: parser.SeverityError,
		Message:  "unterminated string literal",
		Source:   "rust",
		Range:    psi.Range{Start: s.i, End: s.i},
	})
}

// skipCharOrLifetime handles the `'...'`/`'a` ambiguity. A single quote
// followed by an identifier-start character that is NOT terminated by a
// matching `'` within a few bytes is treated as a lifetime / label.
// Otherwise the standard char-literal escape walk applies.
func (s *rustScanner) skipCharOrLifetime() {
	if s.i >= len(s.src) || s.src[s.i] != '\'' {
		return
	}
	// Lifetime heuristic: `'` + ident-start that does not start an escape
	// or a multi-character pattern. We peek forward up to the maximum
	// char literal length (an escape sequence like `'\u{1F600}'` is at
	// most 10 bytes); anything longer than that is definitely a lifetime
	// or a malformed literal.
	if s.i+1 < len(s.src) && (isIdentStart(rune(s.src[s.i+1])) || s.src[s.i+1] == '_') {
		// `\u{...}` and `\x..` etc. start with `\`, never an ident byte,
		// so we can safely look at byte+1 only.
		// Walk a possible lifetime: 'a, 'a_b, 'static
		j := s.i + 1
		for j < len(s.src) && isIdentPart(rune(s.src[j])) {
			j++
		}
		if j < len(s.src) && s.src[j] == '\'' {
			// Single-character char literal: 'a'
			s.i = j + 1
			return
		}
		// Lifetime / label.
		s.i = j
		return
	}
	// Char literal with escape or non-ident character.
	s.i++
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == '\\' && s.i+1 < len(s.src) {
			s.i += 2
			continue
		}
		if c == '\'' {
			s.i++
			return
		}
		if c == '\n' {
			// Unterminated char literal; stop at newline so the rest of
			// the file is still scannable.
			s.tree.Diagnostics = append(s.tree.Diagnostics, treesitter.Diagnostic{
				Severity: parser.SeverityError,
				Message:  "unterminated char literal",
				Source:   "rust",
				Range:    psi.Range{Start: s.i, End: s.i},
			})
			return
		}
		s.i++
	}
}

// skipRawStringMaybe consumes `r"..."`, `r#"..."#`, `r##"..."##`, … when
// the cursor is at `r`. Returns true on success.
func (s *rustScanner) skipRawStringMaybe() bool {
	if s.i >= len(s.src) || s.src[s.i] != 'r' {
		return false
	}
	j := s.i + 1
	hashes := 0
	for j < len(s.src) && s.src[j] == '#' {
		hashes++
		j++
	}
	if j >= len(s.src) || s.src[j] != '"' {
		return false
	}
	// Ensure `r` is not preceded by an ident byte (otherwise it's just a
	// suffix on something like `for`, `bar`, …).
	if s.i > 0 && isIdentPart(rune(s.src[s.i-1])) {
		return false
	}
	s.i = j + 1
	closingNeeded := append([]byte{'"'}, bytes(hashes, '#')...)
	for s.i < len(s.src) {
		if s.src[s.i] == '"' && s.matchAhead(closingNeeded) {
			s.i += len(closingNeeded)
			return true
		}
		s.i++
	}
	s.tree.Diagnostics = append(s.tree.Diagnostics, treesitter.Diagnostic{
		Severity: parser.SeverityError,
		Message:  "unterminated raw string literal",
		Source:   "rust",
		Range:    psi.Range{Start: s.i, End: s.i},
	})
	return true
}

// skipByteStringMaybe handles `b"..."` and `br"..."`/`br#"..."#`.
func (s *rustScanner) skipByteStringMaybe() bool {
	if s.i >= len(s.src) || s.src[s.i] != 'b' {
		return false
	}
	if s.i+1 >= len(s.src) {
		return false
	}
	if s.i > 0 && isIdentPart(rune(s.src[s.i-1])) {
		return false
	}
	switch s.src[s.i+1] {
	case '"':
		s.i++
		s.skipString('"')
		return true
	case 'r':
		// br"..." / br#"..."#
		s.i++
		return s.skipRawStringMaybe()
	case '\'':
		// b'a'
		s.i++
		s.skipCharOrLifetime()
		return true
	}
	return false
}

// skipCStringMaybe handles `c"..."` and `cr"..."`/`cr#"..."#` (Rust 1.77+).
func (s *rustScanner) skipCStringMaybe() bool {
	if s.i >= len(s.src) || s.src[s.i] != 'c' {
		return false
	}
	if s.i+1 >= len(s.src) {
		return false
	}
	if s.i > 0 && isIdentPart(rune(s.src[s.i-1])) {
		return false
	}
	switch s.src[s.i+1] {
	case '"':
		s.i++
		s.skipString('"')
		return true
	case 'r':
		s.i++
		return s.skipRawStringMaybe()
	}
	return false
}

func bytes(n int, c byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = c
	}
	return out
}

func (s *rustScanner) matchAhead(needle []byte) bool {
	if s.i+len(needle) > len(s.src) {
		return false
	}
	for k, b := range needle {
		if s.src[s.i+k] != b {
			return false
		}
	}
	return true
}

func (s *rustScanner) skipCommentMaybe() bool {
	if s.i+1 >= len(s.src) || s.src[s.i] != '/' {
		return false
	}
	switch s.src[s.i+1] {
	case '/':
		s.i += 2
		for s.i < len(s.src) && s.src[s.i] != '\n' {
			s.i++
		}
		return true
	case '*':
		// Rust block comments nest.
		s.i += 2
		depth := 1
		for s.i+1 < len(s.src) && depth > 0 {
			switch {
			case s.src[s.i] == '/' && s.src[s.i+1] == '*':
				depth++
				s.i += 2
			case s.src[s.i] == '*' && s.src[s.i+1] == '/':
				depth--
				s.i += 2
			default:
				s.i++
			}
		}
		if depth > 0 {
			s.tree.Diagnostics = append(s.tree.Diagnostics, treesitter.Diagnostic{
				Severity: parser.SeverityError,
				Message:  "unterminated block comment",
				Source:   "rust",
				Range:    psi.Range{Start: s.i, End: s.i},
			})
			s.i = len(s.src)
		}
		return true
	}
	return false
}

func (s *rustScanner) skipAttribute() {
	// At entry we're at `#`. We may also see `#!` for inner attrs.
	if s.i >= len(s.src) || s.src[s.i] != '#' {
		return
	}
	s.i++
	if s.i < len(s.src) && s.src[s.i] == '!' {
		s.i++
	}
	if s.i < len(s.src) && s.src[s.i] == '[' {
		s.skipMatched('[', ']')
	}
}

func (s *rustScanner) skipTrivia() {
	for s.i < len(s.src) {
		c := s.src[s.i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			s.i++
			continue
		}
		if !s.skipCommentMaybe() {
			return
		}
	}
}

func (s *rustScanner) skipTriviaReturnIndex() int {
	s.skipTrivia()
	return s.i
}

func (s *rustScanner) readIdent() string {
	start := s.i
	if s.i >= len(s.src) {
		return ""
	}
	c := rune(s.src[s.i])
	if !isIdentStart(c) {
		return ""
	}
	s.i++
	for s.i < len(s.src) && isIdentPart(rune(s.src[s.i])) {
		s.i++
	}
	return string(s.src[start:s.i])
}

func (s *rustScanner) peekIdent() string {
	saved := s.i
	defer func() { s.i = saved }()
	return s.readIdent()
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}

// trimSig collapses interior whitespace runs in a signature so the
// rendered outline does not carry stray newlines from where-clauses or
// generic bounds. The body is intentionally short — this is best-effort
// formatting, not a pretty-printer.
func trimSig(sig string) string {
	sig = strings.TrimSpace(sig)
	if sig == "" {
		return sig
	}
	var b strings.Builder
	prevSpace := false
	for _, r := range sig {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if r == ' ' {
			if prevSpace {
				continue
			}
			prevSpace = true
			b.WriteRune(' ')
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

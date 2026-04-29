// tsBackend is the M2 TypeScript / JavaScript adapter. It scans the source
// with a small bracket-aware lexer that recognises classes, interfaces,
// enums, functions, methods, parameters and module-level variables —
// considerably more structure than the line-oriented regex parser surfaces.
//
// The implementation is hand-written rather than wrapping a real
// tree-sitter grammar: tree-sitter requires CGO and would force us to ship
// per-OS shared libraries, which conflicts with Ines's "single static binary"
// rule. The Backend interface is shaped after tree-sitter's vocabulary so a
// future swap to a real grammar is a drop-in replacement.
package typescript

import (
	"strings"
	"unicode"

	"github.com/zixiao-labs/ines/internal/lang/treesitter"
	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

type tsBackend struct{}

func newTSBackend() treesitter.Backend { return &tsBackend{} }

func (t *tsBackend) Language() string { return "typescript" }

func (t *tsBackend) Parse(src parser.Source) (*treesitter.Tree, error) {
	tree := &treesitter.Tree{
		Path:     src.Path,
		Language: "typescript",
		Source:   src.Content,
	}
	s := newScanner(src.Content)
	s.scanModule(tree, &tree.Symbols, 0)
	return tree, nil
}

// scanner walks the source while tracking string / template / comment state
// and brace depth so that we never recurse into a function body or template
// literal.
type scanner struct {
	src []byte
	i   int
}

func newScanner(src []byte) *scanner { return &scanner{src: src} }

func (s *scanner) scanModule(tree *treesitter.Tree, out *[]*treesitter.Symbol, depth int) {
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
		if c == '{' {
			// Anonymous block — recurse so nested declarations still get
			// indexed at the right depth (e.g. namespace bodies).
			s.i++
			s.scanModule(tree, out, depth+1)
			continue
		}
		if !isIdentStart(rune(c)) {
			s.i++
			continue
		}
		start := s.i
		word := s.readIdent()
		switch word {
		case "import":
			if sym := s.parseImport(start); sym != nil {
				*out = append(*out, sym)
			}
		case "export":
			// fall through; the next iteration sees the actual decl keyword
			s.skipTrivia()
			if s.lookAhead("default") {
				s.consume("default")
				s.skipTrivia()
			}
		case "abstract":
			// Pass-through; will combine with class on next loop.
		case "class":
			if sym := s.parseClassLike(start, psi.KindClass); sym != nil {
				*out = append(*out, sym)
			}
		case "interface":
			if sym := s.parseClassLike(start, psi.KindInterface); sym != nil {
				*out = append(*out, sym)
			}
		case "enum":
			if sym := s.parseEnum(start); sym != nil {
				*out = append(*out, sym)
			}
		case "function":
			if sym := s.parseFunction(start); sym != nil {
				*out = append(*out, sym)
			}
		case "type":
			if sym := s.parseTypeAlias(start); sym != nil {
				*out = append(*out, sym)
			}
		case "const", "let", "var":
			if syms := s.parseVarStatement(start); len(syms) > 0 {
				*out = append(*out, syms...)
			}
		case "namespace", "module":
			if sym := s.parseNamespace(start); sym != nil {
				*out = append(*out, sym)
			}
		default:
			// Unknown identifier at module level — skip until the next
			// statement boundary.
			s.skipUntilStatementEnd()
		}
	}
}

func (s *scanner) parseImport(start int) *treesitter.Symbol {
	// Walk to the from-clause or end-of-statement and capture the path.
	end := s.skipUntilOneOfTopLevel(';', '\n', 0)
	chunk := s.src[start:end]
	path := importPath(chunk)
	if path == "" {
		return nil
	}
	return &treesitter.Symbol{
		Kind:   psi.KindImport,
		Name:   path,
		Detail: strings.TrimSpace(string(chunk)),
		Range:  psi.Range{Start: start, End: end},
	}
}

func (s *scanner) parseClassLike(start int, kind psi.Kind) *treesitter.Symbol {
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	nameRange := psi.Range{Start: nameStart, End: s.i}
	// Consume generics, extends, implements, … up to the body brace.
	for s.i < len(s.src) && s.src[s.i] != '{' && s.src[s.i] != ';' {
		s.i++
	}
	sym := &treesitter.Symbol{
		Kind:      kind,
		Name:      name,
		Range:     psi.Range{Start: start, End: s.i},
		NameRange: nameRange,
	}
	if s.i >= len(s.src) || s.src[s.i] != '{' {
		return sym
	}
	s.i++ // step past '{'
	s.parseClassBody(sym)
	sym.Range.End = s.i
	return sym
}

func (s *scanner) parseClassBody(parent *treesitter.Symbol) {
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
		if c == ';' || c == ',' {
			s.i++
			continue
		}
		if !isIdentStart(rune(c)) && c != '#' && c != '_' && c != '$' && c != '[' && c != '"' && c != '\'' {
			s.i++
			continue
		}
		// Skip access / decorator modifiers.
		for {
			s.skipTrivia()
			next := s.peekIdent()
			if next == "public" || next == "private" || next == "protected" ||
				next == "readonly" || next == "static" || next == "abstract" ||
				next == "async" || next == "get" || next == "set" || next == "override" {
				s.readIdent()
				continue
			}
			break
		}
		memberStart := s.i
		name := s.readMemberName()
		if name == "" {
			// Defensive: avoid an infinite loop on unexpected input.
			s.i++
			continue
		}
		s.skipTrivia()
		// Skip type-parameter list.
		if s.i < len(s.src) && s.src[s.i] == '<' {
			s.skipMatched('<', '>')
			s.skipTrivia()
		}
		if s.i < len(s.src) && s.src[s.i] == '(' {
			parent.Children = append(parent.Children, s.parseMethod(memberStart, name))
		} else {
			parent.Children = append(parent.Children, s.parseField(memberStart, name))
		}
	}
}

func (s *scanner) parseMethod(start int, name string) *treesitter.Symbol {
	paramStart := s.i
	params := s.collectParams()
	sigEnd := s.skipUntilOneOfTopLevel('{', ';', 0)
	signature := strings.TrimSpace(string(s.src[start:sigEnd]))
	end := sigEnd
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.i++
		s.skipBalanced()
		end = s.i
	} else if s.i < len(s.src) && s.src[s.i] == ';' {
		s.i++
		end = s.i
	}
	return &treesitter.Symbol{
		Kind:      psi.KindMethod,
		Name:      name,
		Range:     psi.Range{Start: start, End: end},
		NameRange: psi.Range{Start: start, End: start + len(name)},
		Signature: signature,
		Children:  params,
		Detail:    string(s.src[paramStart:sigEnd]),
	}
}

func (s *scanner) parseField(start int, name string) *treesitter.Symbol {
	end := s.skipUntilOneOfTopLevel(';', '\n', 0)
	if s.i < len(s.src) && s.src[s.i] == ';' {
		s.i++
		end = s.i
	}
	return &treesitter.Symbol{
		Kind:   psi.KindField,
		Name:   name,
		Detail: strings.TrimSpace(string(s.src[start:end])),
		Range:  psi.Range{Start: start, End: end},
	}
}

func (s *scanner) parseEnum(start int) *treesitter.Symbol {
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	for s.i < len(s.src) && s.src[s.i] != '{' && s.src[s.i] != ';' {
		s.i++
	}
	sym := &treesitter.Symbol{
		Kind:      psi.KindEnum,
		Name:      name,
		Range:     psi.Range{Start: start, End: s.i},
		NameRange: psi.Range{Start: nameStart, End: nameStart + len(name)},
	}
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.i++
		s.skipBalanced()
	}
	sym.Range.End = s.i
	return sym
}

func (s *scanner) parseFunction(start int) *treesitter.Symbol {
	s.skipTrivia()
	// Anonymous expression form (`function () {}`) — skip without recording.
	if s.i < len(s.src) && (s.src[s.i] == '*' || s.src[s.i] == '(') {
		s.skipUntilStatementEnd()
		return nil
	}
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	if s.i < len(s.src) && s.src[s.i] == '<' {
		s.skipMatched('<', '>')
	}
	params := s.collectParams()
	sigEnd := s.skipUntilOneOfTopLevel('{', ';', 0)
	signature := strings.TrimSpace(string(s.src[start:sigEnd]))
	end := sigEnd
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.i++
		s.skipBalanced()
		end = s.i
	} else if s.i < len(s.src) && s.src[s.i] == ';' {
		s.i++
		end = s.i
	}
	return &treesitter.Symbol{
		Kind:      psi.KindFunction,
		Name:      name,
		Range:     psi.Range{Start: start, End: end},
		NameRange: psi.Range{Start: nameStart, End: nameStart + len(name)},
		Signature: signature,
		Children:  params,
	}
}

func (s *scanner) parseTypeAlias(start int) *treesitter.Symbol {
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	end := s.skipUntilOneOfTopLevel(';', '\n', 0)
	if s.i < len(s.src) && s.src[s.i] == ';' {
		s.i++
		end = s.i
	}
	return &treesitter.Symbol{
		Kind:      psi.KindTypeAlias,
		Name:      name,
		Range:     psi.Range{Start: start, End: end},
		NameRange: psi.Range{Start: nameStart, End: nameStart + len(name)},
		Detail:    "type alias",
	}
}

func (s *scanner) parseVarStatement(start int) []*treesitter.Symbol {
	var out []*treesitter.Symbol
	for s.i < len(s.src) {
		s.skipTrivia()
		if s.i >= len(s.src) {
			break
		}
		nameStart := s.i
		name := s.readIdent()
		if name == "" {
			break
		}
		// If the next thing is "= () =>" or "= function" we promote the
		// declaration to a function symbol so call-graph queries find it.
		s.skipTrivia()
		isFunction := false
		// Skip type annotation.
		if s.i < len(s.src) && s.src[s.i] == ':' {
			s.i++
			for s.i < len(s.src) && s.src[s.i] != '=' && s.src[s.i] != ',' && s.src[s.i] != ';' && s.src[s.i] != '\n' {
				if s.src[s.i] == '<' {
					s.skipMatched('<', '>')
					continue
				}
				if s.src[s.i] == '(' {
					s.skipMatched('(', ')')
					continue
				}
				s.i++
			}
		}
		if s.i < len(s.src) && s.src[s.i] == '=' {
			s.i++
			s.skipTrivia()
			if s.lookAhead("async") {
				s.consume("async")
				s.skipTrivia()
			}
			if s.lookAhead("function") {
				isFunction = true
			} else if s.i < len(s.src) && s.src[s.i] == '(' {
				// Could be arrow `() =>` or a parenthesised expression.
				peek := s.scanArrowAhead()
				if peek {
					isFunction = true
				}
			} else if s.scanSingleParamArrowAhead() {
				// Single-parameter arrow form `x => …` — the parser used to
				// only recognise the parenthesised variant.
				isFunction = true
			}
		}
		// Read until next , or ; to mark the declaration end.
		end := s.skipUntilOneOfTopLevel(',', ';', '\n')
		kind := psi.KindVariable
		if isFunction {
			kind = psi.KindFunction
		}
		out = append(out, &treesitter.Symbol{
			Kind:      kind,
			Name:      name,
			Range:     psi.Range{Start: start, End: end},
			NameRange: psi.Range{Start: nameStart, End: nameStart + len(name)},
			Detail:    strings.TrimSpace(string(s.src[start:end])),
		})
		if s.i < len(s.src) && s.src[s.i] == ',' {
			s.i++
			start = s.i
			continue
		}
		break
	}
	if s.i < len(s.src) && s.src[s.i] == ';' {
		s.i++
	}
	return out
}

func (s *scanner) parseNamespace(start int) *treesitter.Symbol {
	s.skipTrivia()
	nameStart := s.i
	name := s.readIdent()
	if name == "" {
		return nil
	}
	for s.i < len(s.src) && s.src[s.i] != '{' && s.src[s.i] != ';' {
		s.i++
	}
	sym := &treesitter.Symbol{
		Kind:      psi.KindNamespace,
		Name:      name,
		Range:     psi.Range{Start: start, End: s.i},
		NameRange: psi.Range{Start: nameStart, End: nameStart + len(name)},
	}
	if s.i < len(s.src) && s.src[s.i] == '{' {
		s.i++
		nestedTree := &treesitter.Tree{Source: s.src}
		s.scanModule(nestedTree, &sym.Children, 1)
	}
	sym.Range.End = s.i
	return sym
}

// collectParams parses a parameter list starting at the open paren and
// returns one Symbol per parameter. The scanner stops just after the closing
// paren.
func (s *scanner) collectParams() []*treesitter.Symbol {
	if s.i >= len(s.src) || s.src[s.i] != '(' {
		return nil
	}
	s.i++ // step past '('
	var params []*treesitter.Symbol
	for s.i < len(s.src) {
		s.skipTrivia()
		if s.i >= len(s.src) || s.src[s.i] == ')' {
			break
		}
		// Skip access / decorator modifiers in constructor parameters.
		for {
			s.skipTrivia()
			next := s.peekIdent()
			if next == "public" || next == "private" || next == "protected" || next == "readonly" {
				s.readIdent()
				continue
			}
			break
		}
		// Skip rest / object destructuring patterns.
		if s.i < len(s.src) && s.src[s.i] == '.' {
			for s.i < len(s.src) && s.src[s.i] == '.' {
				s.i++
			}
		}
		if s.i < len(s.src) && (s.src[s.i] == '{' || s.src[s.i] == '[') {
			// Destructuring pattern — record the slice as one param without a clean name.
			openStart := s.i
			open, close := s.src[s.i], byte('}')
			if open == '[' {
				close = ']'
			}
			s.skipMatched(open, close)
			detail := strings.TrimSpace(string(s.src[openStart:s.i]))
			params = append(params, &treesitter.Symbol{
				Kind:   psi.KindParameter,
				Name:   detail,
				Detail: detail,
				Range:  psi.Range{Start: openStart, End: s.i},
			})
		} else {
			nameStart := s.i
			name := s.readIdent()
			if name != "" {
				params = append(params, &treesitter.Symbol{
					Kind:      psi.KindParameter,
					Name:      name,
					Range:     psi.Range{Start: nameStart, End: nameStart + len(name)},
					NameRange: psi.Range{Start: nameStart, End: nameStart + len(name)},
				})
			}
		}
		// Skip ?, type annotation, default value until comma or ).
		s.skipUntilParamSeparator()
		if s.i < len(s.src) && s.src[s.i] == ',' {
			s.i++
			continue
		}
	}
	if s.i < len(s.src) && s.src[s.i] == ')' {
		s.i++
	}
	return params
}

func (s *scanner) skipUntilParamSeparator() {
	depth := 0
	for s.i < len(s.src) {
		c := s.src[s.i]
		if depth == 0 && (c == ',' || c == ')') {
			return
		}
		switch c {
		case '(':
			s.skipMatched('(', ')')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '{':
			s.skipMatched('{', '}')
			continue
		case '<':
			s.skipMatched('<', '>')
			continue
		case '"', '\'':
			s.skipString(c)
			continue
		case '`':
			s.skipTemplate()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		s.i++
		_ = depth
	}
}

// scanSingleParamArrowAhead reports whether the current position starts an
// identifier immediately followed by `=>` (optionally with whitespace between),
// which is the bare single-parameter arrow form `x => …`. The scanner does NOT
// consume input.
func (s *scanner) scanSingleParamArrowAhead() bool {
	if s.i >= len(s.src) || !isIdentStart(rune(s.src[s.i])) {
		return false
	}
	j := s.i + 1
	for j < len(s.src) && isIdentPart(rune(s.src[j])) {
		j++
	}
	for j < len(s.src) && unicode.IsSpace(rune(s.src[j])) {
		j++
	}
	return j+1 < len(s.src) && s.src[j] == '=' && s.src[j+1] == '>'
}

// scanArrowAhead checks whether the current position starts a parenthesised
// param list followed by `=>` (optionally past a return-type annotation).
// The scanner does NOT consume input.
func (s *scanner) scanArrowAhead() bool {
	depth := 0
	j := s.i
	for ; j < len(s.src); j++ {
		switch s.src[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				j++
				goto afterParen
			}
		}
	}
	return false
afterParen:
	// After `)` there may be `: ReturnType` before the `=>` arrow.
	for j < len(s.src) {
		ch := s.src[j]
		if unicode.IsSpace(rune(ch)) {
			j++
			continue
		}
		if ch == '=' && j+1 < len(s.src) && s.src[j+1] == '>' {
			return true
		}
		if ch == ':' || ch == '<' || ch == '|' || ch == '&' || isIdentPart(rune(ch)) || ch == '.' || ch == '[' || ch == ']' || ch == '"' || ch == '\'' || ch == ',' {
			j++
			continue
		}
		return false
	}
	return false
}

// skipUntilOneOfTopLevel walks forward respecting strings, templates, comments
// and brace nesting. It stops *before* consuming the matched character.
// Pass 0 to omit a stop character.
func (s *scanner) skipUntilOneOfTopLevel(a, b, c byte) int {
	depth := 0
	for s.i < len(s.src) {
		ch := s.src[s.i]
		if depth == 0 && (ch == a || ch == b || ch == c) {
			return s.i
		}
		switch ch {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				return s.i
			}
			depth--
		case '(':
			s.skipMatched('(', ')')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '"', '\'':
			s.skipString(ch)
			continue
		case '`':
			s.skipTemplate()
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

func (s *scanner) skipUntilStatementEnd() {
	for s.i < len(s.src) {
		ch := s.src[s.i]
		if ch == ';' || ch == '\n' {
			s.i++
			return
		}
		switch ch {
		case '{':
			s.i++
			s.skipBalanced()
			continue
		case '(':
			s.skipMatched('(', ')')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '"', '\'':
			s.skipString(ch)
			continue
		case '`':
			s.skipTemplate()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		s.i++
	}
}

func (s *scanner) skipMatched(open, close byte) {
	if s.i >= len(s.src) || s.src[s.i] != open {
		return
	}
	s.i++
	depth := 1
	for s.i < len(s.src) && depth > 0 {
		ch := s.src[s.i]
		switch ch {
		case open:
			depth++
		case close:
			depth--
		case '"', '\'':
			s.skipString(ch)
			continue
		case '`':
			s.skipTemplate()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		if depth == 0 {
			s.i++
			return
		}
		s.i++
	}
}

func (s *scanner) skipBalanced() {
	depth := 1
	for s.i < len(s.src) && depth > 0 {
		ch := s.src[s.i]
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
		case '(':
			s.skipMatched('(', ')')
			continue
		case '[':
			s.skipMatched('[', ']')
			continue
		case '"', '\'':
			s.skipString(ch)
			continue
		case '`':
			s.skipTemplate()
			continue
		case '/':
			if s.skipCommentMaybe() {
				continue
			}
		}
		if depth == 0 {
			s.i++
			return
		}
		s.i++
	}
}

func (s *scanner) skipString(q byte) {
	s.i++ // opening quote
	for s.i < len(s.src) {
		ch := s.src[s.i]
		if ch == '\\' && s.i+1 < len(s.src) {
			s.i += 2
			continue
		}
		if ch == q {
			s.i++
			return
		}
		s.i++
	}
}

func (s *scanner) skipTemplate() {
	s.i++ // opening backtick
	for s.i < len(s.src) {
		ch := s.src[s.i]
		if ch == '\\' && s.i+1 < len(s.src) {
			s.i += 2
			continue
		}
		if ch == '`' {
			s.i++
			return
		}
		if ch == '$' && s.i+1 < len(s.src) && s.src[s.i+1] == '{' {
			s.i += 2
			s.skipBalanced()
			continue
		}
		s.i++
	}
}

// skipCommentMaybe consumes a // or /* … */ comment at the cursor and reports
// whether it advanced the cursor.
func (s *scanner) skipCommentMaybe() bool {
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
		s.i += 2
		for s.i+1 < len(s.src) {
			if s.src[s.i] == '*' && s.src[s.i+1] == '/' {
				s.i += 2
				return true
			}
			s.i++
		}
		s.i = len(s.src)
		return true
	}
	return false
}

func (s *scanner) skipTrivia() {
	for s.i < len(s.src) {
		ch := s.src[s.i]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			s.i++
			continue
		}
		if !s.skipCommentMaybe() {
			return
		}
	}
}

func (s *scanner) readIdent() string {
	start := s.i
	if s.i >= len(s.src) {
		return ""
	}
	if s.src[s.i] == '#' || s.src[s.i] == '_' || s.src[s.i] == '$' {
		s.i++
	} else if !isIdentStart(rune(s.src[s.i])) {
		return ""
	} else {
		s.i++
	}
	for s.i < len(s.src) && isIdentPart(rune(s.src[s.i])) {
		s.i++
	}
	return string(s.src[start:s.i])
}

func (s *scanner) readMemberName() string {
	if s.i >= len(s.src) {
		return ""
	}
	switch s.src[s.i] {
	case '"', '\'':
		start := s.i
		s.skipString(s.src[s.i])
		return strings.Trim(string(s.src[start:s.i]), "'\"")
	case '[':
		start := s.i
		s.skipMatched('[', ']')
		return strings.TrimSpace(string(s.src[start:s.i]))
	}
	return s.readIdent()
}

func (s *scanner) peekIdent() string {
	saved := s.i
	defer func() { s.i = saved }()
	return s.readIdent()
}

func (s *scanner) lookAhead(word string) bool {
	if s.i+len(word) > len(s.src) {
		return false
	}
	if string(s.src[s.i:s.i+len(word)]) != word {
		return false
	}
	if s.i+len(word) < len(s.src) {
		return !isIdentPart(rune(s.src[s.i+len(word)]))
	}
	return true
}

func (s *scanner) consume(word string) {
	if s.lookAhead(word) {
		s.i += len(word)
	}
}

func isIdentStart(r rune) bool {
	return r == '_' || r == '$' || r == '#' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || unicode.IsDigit(r)
}

func importPath(chunk []byte) string {
	for i := 0; i < len(chunk); i++ {
		c := chunk[i]
		if c == '"' || c == '\'' {
			j := i + 1
			for j < len(chunk) && chunk[j] != c {
				j++
			}
			if j < len(chunk) {
				return string(chunk[i+1 : j])
			}
		}
	}
	return ""
}

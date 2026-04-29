// Package treesitter is the abstraction layer the M2 milestone introduces to
// move Ines off the bootstrap line-oriented regex parsers. Real tree-sitter
// grammars require CGO and would force every download channel to ship per-OS
// shared libraries, which conflicts with the "single static binary" rule in
// README.md. Instead, the abstraction defined here mirrors tree-sitter's
// vocabulary — Tree, Node, Symbol, Backend — so that adapters can be written
// against a stable surface that an actual tree-sitter wrapper will satisfy
// the day we are willing to take on the CGO dependency.
//
// In the meantime each adapter ships its own Backend that surfaces full
// syntactic structure (parameters, methods inside classes, signatures)
// instead of the line-by-line view the regex parsers offered.
package treesitter

import (
	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

// Backend is the unit of pluggability inside this package. A Backend turns a
// parser.Source into a Tree of Symbols. Implementations are expected to be
// stateless and safe to share between goroutines.
type Backend interface {
	// Language returns the canonical language identifier ("go", "typescript", ...).
	Language() string
	// Parse builds a Tree from the given source. Implementations must always
	// return a non-nil Tree, even when the source is malformed; recovery is
	// the backend's responsibility and surfaces as Diagnostics on the Tree.
	Parse(src parser.Source) (*Tree, error)
}

// Tree is the high-level result of a parse: a flat list of root symbols (the
// outline a tree-sitter query for "symbols" would yield), plus any diagnostics
// the backend recovered while parsing.
type Tree struct {
	Path        string
	Language    string
	Source      []byte
	Symbols     []*Symbol
	Diagnostics []Diagnostic
}

// Symbol is the structural node every backend emits. It carries the parent
// chain implicitly through Children. Range covers the whole declaration
// including its body; NameRange is the sub-range of the identifier so that
// rename refactorings can produce minimal text edits without re-scanning.
type Symbol struct {
	Kind      psi.Kind
	Name      string
	Detail    string
	Signature string
	Range     psi.Range
	NameRange psi.Range
	Children  []*Symbol
}

// Diagnostic is the unified shape Ines emits for parse errors and lint hits.
// Severity matches the LSP convention (1=Error, 2=Warning, 3=Info, 4=Hint)
// so renderer-side translation is a no-op.
type Diagnostic struct {
	Severity int
	Message  string
	Range    psi.Range
	Source   string
}

// LiftToPSI walks the Tree and produces a PSI File whose Children are the
// flattened Symbols. The hierarchy is preserved through psi.Element parent
// pointers; consumers that only care about top-level outlines can keep
// reading file.Children() exactly as they did under the regex parsers.
func LiftToPSI(tree *Tree) psi.File {
	if tree == nil {
		return psi.NewFile("", "", nil)
	}
	file := psi.NewFile(tree.Path, tree.Language, tree.Source)
	for _, sym := range tree.Symbols {
		file.AddChild(liftSymbol(sym, tree.Source, tree.Language))
	}
	return file
}

func liftSymbol(sym *Symbol, source []byte, language string) psi.Element {
	if sym == nil {
		return nil
	}
	el := psi.NewElement(sym.Kind, sym.Name, sym.Range, source, language)
	for _, child := range sym.Children {
		c := liftSymbol(child, source, language)
		if c != nil {
			el.AddChild(c)
		}
	}
	return el
}

// adapterParser is the parser.Parser shim that connects a Backend to the rest
// of Ines. Adapters wire it via lang.Register; everything downstream still
// sees a parser.Parser.
type adapterParser struct {
	backend Backend
}

// NewParser returns a parser.Parser that delegates to backend. When backend
// is nil it returns nil, which lets callers fall back to the regex parser
// during gradual rollout.
func NewParser(backend Backend) parser.Parser {
	if backend == nil {
		return nil
	}
	return &adapterParser{backend: backend}
}

func (p *adapterParser) Language() string { return p.backend.Language() }

func (p *adapterParser) Parse(src parser.Source) (psi.File, error) {
	file, _, err := p.ParseWithDiagnostics(src)
	return file, err
}

// ParseWithDiagnostics runs the backend and exposes both the PSI tree and the
// diagnostics it recovered, satisfying parser.DiagnosingParser.
func (p *adapterParser) ParseWithDiagnostics(src parser.Source) (psi.File, []parser.Diagnostic, error) {
	tree, err := p.backend.Parse(src)
	if err != nil {
		return psi.NewFile(src.Path, p.backend.Language(), src.Content), nil, err
	}
	if tree == nil {
		return psi.NewFile(src.Path, p.backend.Language(), src.Content), nil, nil
	}
	if tree.Path == "" {
		tree.Path = src.Path
	}
	if tree.Language == "" {
		tree.Language = p.backend.Language()
	}
	if tree.Source == nil {
		tree.Source = src.Content
	}
	diagnostics := make([]parser.Diagnostic, 0, len(tree.Diagnostics))
	for _, d := range tree.Diagnostics {
		diagnostics = append(diagnostics, parser.Diagnostic{
			Severity: d.Severity,
			Message:  d.Message,
			Source:   d.Source,
			Start:    d.Range.Start,
			End:      d.Range.End,
		})
	}
	return LiftToPSI(tree), diagnostics, nil
}

// FlattenSymbols returns every Symbol in the tree in pre-order. Useful for
// completion and reference resolution without recursing on the caller side.
func FlattenSymbols(tree *Tree) []*Symbol {
	if tree == nil {
		return nil
	}
	var out []*Symbol
	var walk func(syms []*Symbol)
	walk = func(syms []*Symbol) {
		for _, s := range syms {
			out = append(out, s)
			walk(s.Children)
		}
	}
	walk(tree.Symbols)
	return out
}

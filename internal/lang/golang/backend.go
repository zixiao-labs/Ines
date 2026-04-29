// goBackend is the M2 replacement for the regex-based Go parser. It uses the
// standard library's go/parser so the resulting outline is fully aware of
// nested types, methods, parameters and signatures rather than the line-by
// -line approximation the bootstrap parser produced. Its surface is the
// treesitter.Backend interface so the eventual real-tree-sitter rollout is a
// drop-in replacement.
package golang

import (
	"fmt"
	"go/ast"
	gp "go/parser"
	"go/scanner"
	"go/token"
	"strings"

	"github.com/zixiao-labs/ines/internal/lang/treesitter"
	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

type goBackend struct{}

func newGoBackend() treesitter.Backend { return &goBackend{} }

func (g *goBackend) Language() string { return "go" }

func (g *goBackend) Parse(src parser.Source) (*treesitter.Tree, error) {
	fset := token.NewFileSet()
	tree := &treesitter.Tree{
		Path:     src.Path,
		Language: "go",
		Source:   src.Content,
	}
	if len(src.Content) == 0 {
		return tree, nil
	}
	file, err := gp.ParseFile(fset, src.Path, src.Content, gp.ParseComments|gp.AllErrors)
	if file == nil {
		return tree, err
	}
	if err != nil {
		// go/parser collects every error it could recover from in its err
		// chain. We surface them as diagnostics but keep walking the partial
		// AST so the outline still renders.
		appendDiagnostics(tree, fset, err)
	}

	tf := fset.File(file.Pos())
	rangeOf := func(p, q token.Pos) psi.Range {
		return psi.Range{
			Start: posOffset(tf, p),
			End:   posOffset(tf, q),
		}
	}

	if file.Name != nil && file.Name.Name != "" {
		tree.Symbols = append(tree.Symbols, &treesitter.Symbol{
			Kind:      psi.KindPackage,
			Name:      file.Name.Name,
			Range:     rangeOf(file.Package, file.Name.End()),
			NameRange: rangeOf(file.Name.Pos(), file.Name.End()),
		})
	}

	for _, imp := range file.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		path := strings.Trim(imp.Path.Value, `"`)
		name := path
		if imp.Name != nil && imp.Name.Name != "" {
			name = imp.Name.Name
		}
		tree.Symbols = append(tree.Symbols, &treesitter.Symbol{
			Kind:   psi.KindImport,
			Name:   name,
			Detail: path,
			Range:  rangeOf(imp.Pos(), imp.End()),
		})
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			tree.Symbols = append(tree.Symbols, funcSymbol(d, src.Content, tf))
		case *ast.GenDecl:
			tree.Symbols = append(tree.Symbols, genDeclSymbols(d, src.Content, tf)...)
		}
	}
	return tree, nil
}

func funcSymbol(decl *ast.FuncDecl, source []byte, tf *token.File) *treesitter.Symbol {
	kind := psi.KindFunction
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		kind = psi.KindMethod
	}
	rng := psi.Range{
		Start: posOffset(tf, decl.Pos()),
		End:   posOffset(tf, decl.End()),
	}
	nameRange := psi.Range{
		Start: posOffset(tf, decl.Name.Pos()),
		End:   posOffset(tf, decl.Name.End()),
	}
	sym := &treesitter.Symbol{
		Kind:      kind,
		Name:      decl.Name.Name,
		Range:     rng,
		NameRange: nameRange,
		Signature: signatureSlice(source, rng.Start, sigEnd(decl, tf, rng.End)),
	}
	if decl.Type != nil && decl.Type.Params != nil {
		for _, field := range decl.Type.Params.List {
			for _, n := range field.Names {
				sym.Children = append(sym.Children, &treesitter.Symbol{
					Kind:      psi.KindParameter,
					Name:      n.Name,
					Detail:    typeText(source, tf, field.Type),
					Range:     psi.Range{Start: posOffset(tf, n.Pos()), End: posOffset(tf, n.End())},
					NameRange: psi.Range{Start: posOffset(tf, n.Pos()), End: posOffset(tf, n.End())},
				})
			}
			if len(field.Names) == 0 && field.Type != nil {
				// Unnamed return / parameter (rare — only allowed in interfaces).
				sym.Children = append(sym.Children, &treesitter.Symbol{
					Kind:   psi.KindParameter,
					Name:   "_",
					Detail: typeText(source, tf, field.Type),
					Range:  psi.Range{Start: posOffset(tf, field.Pos()), End: posOffset(tf, field.End())},
				})
			}
		}
	}
	return sym
}

func genDeclSymbols(decl *ast.GenDecl, source []byte, tf *token.File) []*treesitter.Symbol {
	var out []*treesitter.Symbol
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			out = append(out, typeSymbol(s, source, tf))
		case *ast.ValueSpec:
			kind := psi.KindVariable
			if decl.Tok == token.CONST {
				kind = psi.KindVariable // PSI has no separate const kind today.
			}
			for _, name := range s.Names {
				out = append(out, &treesitter.Symbol{
					Kind:   kind,
					Name:   name.Name,
					Detail: typeText(source, tf, s.Type),
					Range:  psi.Range{Start: posOffset(tf, name.Pos()), End: posOffset(tf, name.End())},
				})
			}
		}
	}
	return out
}

func typeSymbol(spec *ast.TypeSpec, source []byte, tf *token.File) *treesitter.Symbol {
	kind := psi.KindClass
	switch spec.Type.(type) {
	case *ast.StructType:
		kind = psi.KindStruct
	case *ast.InterfaceType:
		kind = psi.KindInterface
	case *ast.Ident, *ast.SelectorExpr, *ast.MapType, *ast.ArrayType, *ast.ChanType:
		kind = psi.KindTypeAlias
	}
	sym := &treesitter.Symbol{
		Kind:      kind,
		Name:      spec.Name.Name,
		Range:     psi.Range{Start: posOffset(tf, spec.Pos()), End: posOffset(tf, spec.End())},
		NameRange: psi.Range{Start: posOffset(tf, spec.Name.Pos()), End: posOffset(tf, spec.Name.End())},
		Detail:    typeText(source, tf, spec.Type),
	}
	if st, ok := spec.Type.(*ast.StructType); ok && st.Fields != nil {
		for _, field := range st.Fields.List {
			for _, name := range field.Names {
				sym.Children = append(sym.Children, &treesitter.Symbol{
					Kind:   psi.KindField,
					Name:   name.Name,
					Detail: typeText(source, tf, field.Type),
					Range:  psi.Range{Start: posOffset(tf, name.Pos()), End: posOffset(tf, name.End())},
				})
			}
		}
	}
	if it, ok := spec.Type.(*ast.InterfaceType); ok && it.Methods != nil {
		for _, m := range it.Methods.List {
			for _, name := range m.Names {
				sym.Children = append(sym.Children, &treesitter.Symbol{
					Kind:   psi.KindMethod,
					Name:   name.Name,
					Detail: typeText(source, tf, m.Type),
					Range:  psi.Range{Start: posOffset(tf, name.Pos()), End: posOffset(tf, name.End())},
				})
			}
		}
	}
	return sym
}

func appendDiagnostics(tree *treesitter.Tree, fset *token.FileSet, err error) {
	if list, ok := err.(scanner.ErrorList); ok {
		for _, se := range list {
			tree.Diagnostics = append(tree.Diagnostics, scannerDiagnostic(fset, se))
		}
		return
	}
	if se, ok := err.(*scanner.Error); ok {
		tree.Diagnostics = append(tree.Diagnostics, scannerDiagnostic(fset, se))
		return
	}
	tree.Diagnostics = append(tree.Diagnostics, treesitter.Diagnostic{
		Severity: parser.SeverityError,
		Message:  err.Error(),
		Source:   "go-parser",
	})
}

func scannerDiagnostic(_ *token.FileSet, se *scanner.Error) treesitter.Diagnostic {
	off := se.Pos.Offset
	if off < 0 {
		off = 0
	}
	return treesitter.Diagnostic{
		Severity: parser.SeverityError,
		Message:  se.Msg,
		Source:   "go-parser",
		Range:    psi.Range{Start: off, End: off},
	}
}

func posOffset(tf *token.File, pos token.Pos) int {
	if tf == nil || pos == token.NoPos {
		return 0
	}
	off := tf.Offset(pos)
	if off < 0 {
		return 0
	}
	return off
}

func sigEnd(decl *ast.FuncDecl, tf *token.File, fallback int) int {
	if decl.Body != nil {
		return posOffset(tf, decl.Body.Pos())
	}
	return fallback
}

func signatureSlice(source []byte, start, end int) string {
	if start < 0 || end <= start || end > len(source) {
		return ""
	}
	return strings.TrimSpace(string(source[start:end]))
}

func typeText(source []byte, tf *token.File, expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	start := posOffset(tf, expr.Pos())
	end := posOffset(tf, expr.End())
	if start < 0 || end <= start || end > len(source) {
		return fmt.Sprintf("%T", expr)
	}
	return strings.TrimSpace(string(source[start:end]))
}

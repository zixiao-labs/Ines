package regexparser

import (
	"regexp"
	"testing"

	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func TestParserExtractsTopLevelDeclarations(t *testing.T) {
	rules := []Rule{
		MustRule(psi.KindPackage, `^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)`),
		MustRule(psi.KindFunction, `^\s*func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`),
	}
	p := New("go", rules, regexp.MustCompile(`^\s*//`))

	source := []byte("package demo\n// ignored func Skip()\nfunc Hello() {}\n")
	file, err := p.Parse(parser.Source{Path: "demo.go", Content: source, Language: "go"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	children := file.Children()
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
	if children[0].Kind() != psi.KindPackage || children[0].Name() != "demo" {
		t.Fatalf("first child mismatch: %+v", children[0])
	}
	if children[1].Kind() != psi.KindFunction || children[1].Name() != "Hello" {
		t.Fatalf("second child mismatch: %+v", children[1])
	}
}

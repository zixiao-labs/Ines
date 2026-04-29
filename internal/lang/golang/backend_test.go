package golang

import (
	"testing"

	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func TestGoBackendExtractsFunctionsTypesAndMethods(t *testing.T) {
	src := []byte(`package demo

import "fmt"

type Point struct {
	X int
	Y int
}

type Stringer interface {
	String() string
}

func Hello(name string) string {
	return fmt.Sprintf("hi %s", name)
}

func (p *Point) Move(dx, dy int) {
	p.X += dx
	p.Y += dy
}

const Answer = 42
`)
	backend := newGoBackend()
	tree, err := backend.Parse(parser.Source{Path: "demo.go", Content: src, Language: "go"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := map[string]psi.Kind{
		"demo":     psi.KindPackage,
		"fmt":      psi.KindImport,
		"Point":    psi.KindStruct,
		"Stringer": psi.KindInterface,
		"Hello":    psi.KindFunction,
		"Move":     psi.KindMethod,
		"Answer":   psi.KindVariable,
	}
	got := map[string]psi.Kind{}
	for _, s := range tree.Symbols {
		got[s.Name] = s.Kind
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("symbol %q: got %q want %q", name, got[name], kind)
		}
	}

	// Ensure parameters were attached to the function.
	for _, s := range tree.Symbols {
		if s.Name == "Hello" {
			if len(s.Children) != 1 || s.Children[0].Name != "name" {
				t.Errorf("Hello params: %+v", s.Children)
			}
		}
		if s.Name == "Move" {
			if len(s.Children) != 2 {
				t.Errorf("Move expected 2 params, got %d", len(s.Children))
			}
		}
		if s.Name == "Point" {
			fields := map[string]bool{}
			for _, c := range s.Children {
				if c.Kind == psi.KindField {
					fields[c.Name] = true
				}
			}
			if !fields["X"] || !fields["Y"] {
				t.Errorf("Point fields missing: %+v", s.Children)
			}
		}
	}
}

func TestGoBackendCapturesParseErrors(t *testing.T) {
	src := []byte("package demo\nfunc Hello( {}\n")
	backend := newGoBackend()
	tree, _ := backend.Parse(parser.Source{Path: "demo.go", Content: src, Language: "go"})
	if len(tree.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics for malformed source")
	}
}

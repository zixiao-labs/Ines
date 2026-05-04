package rust

import (
	"strings"
	"testing"

	"github.com/zixiao-labs/ines/internal/lang/treesitter"
	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

// findSymbol returns the first descendant whose Name matches; depth-first.
func findSymbol(syms []*treesitter.Symbol, name string) *treesitter.Symbol {
	for _, s := range syms {
		if s.Name == name {
			return s
		}
		if got := findSymbol(s.Children, name); got != nil {
			return got
		}
	}
	return nil
}

func findSymbolByKind(syms []*treesitter.Symbol, name string, kind psi.Kind) *treesitter.Symbol {
	for _, s := range syms {
		if s.Name == name && s.Kind == kind {
			return s
		}
	}
	return nil
}

func parseRust(t *testing.T, body string) *treesitter.Tree {
	t.Helper()
	backend := newRustBackend()
	tree, err := backend.Parse(parser.Source{Path: "x.rs", Content: []byte(body), Language: "rust"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree
}

// TestRustBackendExtractsTopLevelItems is the headline coverage test:
// every kind of item we expect to see in real Rust code, in one fixture.
func TestRustBackendExtractsTopLevelItems(t *testing.T) {
	src := `use std::collections::HashMap;
use std::sync::Arc;
pub use crate::foo::bar as renamed;

mod inner {
    pub fn inner_fn() {}
}

pub struct Counter {
    pub value: i32,
    name: String,
}

pub struct TupleStruct(pub i32, String);
pub struct UnitStruct;

pub union Either {
    a: i32,
    b: f32,
}

pub enum Status {
    Open,
    Closed(String),
    Pending { until: u64 },
}

pub trait Repository<T> {
    fn get(&self, id: &str) -> Option<T>;
    fn put(&mut self, id: String, value: T);
}

impl<T> Repository<T> for Counter {
    fn get(&self, id: &str) -> Option<T> { None }
    fn put(&mut self, id: String, value: T) {}
}

impl Counter {
    pub fn new() -> Self {
        Self { value: 0, name: String::new() }
    }
    pub async fn increment(&mut self, by: i32) -> i32 {
        self.value += by;
        self.value
    }
}

pub const MAX_SIZE: usize = 1024;
pub static GLOBAL: u32 = 42;
pub type Id = u64;

extern "C" {
    fn external_fn(x: i32) -> i32;
}

extern crate libc;

macro_rules! my_macro {
    ($x:expr) => { $x };
}

pub fn add(a: i32, b: i32) -> i32 { a + b }
`
	tree := parseRust(t, src)
	syms := tree.Symbols

	wantTop := map[string]psi.Kind{
		"std::collections::HashMap": psi.KindImport,
		"std::sync::Arc":            psi.KindImport,
		"crate::foo::bar":           psi.KindImport,
		"inner":                     psi.KindNamespace,
		"TupleStruct":               psi.KindStruct,
		"UnitStruct":                psi.KindStruct,
		"Either":                    psi.KindStruct,
		"Status":                    psi.KindEnum,
		"Repository":                psi.KindInterface,
		"MAX_SIZE":                  psi.KindVariable,
		"GLOBAL":                    psi.KindVariable,
		"Id":                        psi.KindTypeAlias,
		"libc":                      psi.KindImport,
		"my_macro":                  psi.KindFunction,
		"add":                       psi.KindFunction,
	}
	// Assert each (name, kind) appears at least once at the top level.
	// We use a multimap shape because `Counter` appears both as a
	// struct and as one or more impl blocks.
	have := map[string]map[psi.Kind]bool{}
	for _, s := range syms {
		if _, ok := have[s.Name]; !ok {
			have[s.Name] = map[psi.Kind]bool{}
		}
		have[s.Name][s.Kind] = true
	}
	for name, kind := range wantTop {
		if !have[name][kind] {
			t.Errorf("top-level %q: missing kind %q (have=%v)", name, kind, have[name])
		}
	}
	if !have["Counter"][psi.KindStruct] {
		t.Errorf("Counter struct missing (have=%v)", have["Counter"])
	}
	if !have["Counter"][psi.KindClass] {
		t.Errorf("inherent impl Counter missing (have=%v)", have["Counter"])
	}
	if !have["Repository<T> for Counter"][psi.KindClass] {
		t.Errorf("trait impl `Repository<T> for Counter` missing (have keys=%v)", topLevelNames(syms))
	}

	// Counter struct has two named fields.
	if c := findStruct(syms, "Counter"); c != nil {
		fields := map[string]psi.Kind{}
		for _, child := range c.Children {
			fields[child.Name] = child.Kind
		}
		for _, f := range []string{"value", "name"} {
			if fields[f] != psi.KindField {
				t.Errorf("Counter.%s: got %q want field", f, fields[f])
			}
		}
	} else {
		t.Errorf("Counter struct not found")
	}

	// TupleStruct has 2 numeric fields (0, 1).
	if ts := findSymbol(syms, "TupleStruct"); ts != nil {
		if len(ts.Children) != 2 {
			t.Errorf("TupleStruct child count: got %d want 2", len(ts.Children))
		}
	}

	// Status enum has three variants.
	if e := findSymbol(syms, "Status"); e != nil {
		variants := map[string]bool{}
		for _, c := range e.Children {
			variants[c.Name] = true
		}
		for _, v := range []string{"Open", "Closed", "Pending"} {
			if !variants[v] {
				t.Errorf("Status missing variant %q", v)
			}
		}
	}

	// Repository trait has two method signatures.
	if r := findSymbol(syms, "Repository"); r != nil {
		methods := []string{}
		for _, c := range r.Children {
			if c.Kind == psi.KindFunction || c.Kind == psi.KindMethod {
				methods = append(methods, c.Name)
			}
		}
		if !containsAll(methods, []string{"get", "put"}) {
			t.Errorf("Repository methods: got %v want get/put", methods)
		}
	}

	// inherent impl Counter has new + increment as methods.
	implCounter := findInherentImpl(syms, "Counter")
	if implCounter == nil {
		t.Fatalf("inherent impl Counter not found, got top-level: %v", topLevelNames(syms))
	}
	if findSymbolByKind(implCounter.Children, "new", psi.KindMethod) == nil {
		t.Errorf("impl Counter::new should be method")
	}
	if findSymbolByKind(implCounter.Children, "increment", psi.KindMethod) == nil {
		t.Errorf("impl Counter::increment should be method")
	}

	// `add` parameter list survives the scan.
	if a := findSymbol(syms, "add"); a != nil {
		names := []string{}
		for _, p := range a.Children {
			names = append(names, p.Name)
		}
		if len(names) != 2 || names[0] != "a" || names[1] != "b" {
			t.Errorf("add params: got %v", names)
		}
	}
}

func TestRustBackendHandlesTrickyLexicalCases(t *testing.T) {
	// Fixture exercises:
	//   - nested block comments (`/* /* */ */`) — must not unbalance braces
	//   - raw strings with hashes
	//   - char vs lifetime ambiguity
	//   - generics with `>>` close
	src := `/* outer /* inner */ still in comment */
pub fn foo<'a, T: Clone + 'a>(x: &'a T) -> Vec<Vec<T>> {
    let s = r#"raw "with quotes" escaping"#;
    let c = '"';
    let life: &'static str = "literal";
    let _ = '\'';
    Vec::new()
}
`
	tree := parseRust(t, src)
	if findSymbol(tree.Symbols, "foo") == nil {
		t.Fatalf("foo missing — scanner likely tripped on tricky tokens, got=%v", topLevelNames(tree.Symbols))
	}
	if len(tree.Diagnostics) != 0 {
		t.Errorf("did not expect diagnostics, got=%v", tree.Diagnostics)
	}
}

func TestRustBackendReportsUnterminatedTokens(t *testing.T) {
	cases := map[string]string{
		"unterminated string":        `pub fn x() { let s = "broken; }`,
		"unterminated raw string":    `pub fn x() { let s = r#"broken; }`,
		"unterminated block comment": `pub fn x() {} /* not closed`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			tree := parseRust(t, body)
			if len(tree.Diagnostics) == 0 {
				t.Errorf("expected at least one diagnostic for %q, got none", body)
			}
		})
	}
}

func TestRustBackendRecognisesAttributes(t *testing.T) {
	src := `#![allow(dead_code)]
#[derive(Debug, Clone)]
pub struct Tagged {
    #[serde(rename = "id")]
    pub id: String,
}

#[cfg(test)]
mod tests {
    #[test]
    fn it_works() {}
}
`
	tree := parseRust(t, src)
	if findSymbol(tree.Symbols, "Tagged") == nil {
		t.Errorf("Tagged struct not found, got=%v", topLevelNames(tree.Symbols))
	}
	tests := findSymbol(tree.Symbols, "tests")
	if tests == nil {
		t.Fatalf("tests module not found, got=%v", topLevelNames(tree.Symbols))
	}
	if findSymbol(tests.Children, "it_works") == nil {
		t.Errorf("it_works fn not found inside tests module")
	}
}

func TestRustBackendImplDisplayName(t *testing.T) {
	src := `impl Display for Foo<T> where T: Debug {
    fn fmt(&self, f: &mut Formatter) -> Result {}
}

impl<T> Foo<T> {
    fn helper(&self) {}
}
`
	tree := parseRust(t, src)
	// Trait impl keeps `Trait for Type` so the symbol name is unique
	// against the bare-Type inherent impl. Search by name rather than
	// by position so the test does not depend on backend ordering.
	var traitImpl, inherentImpl *treesitter.Symbol
	for _, s := range tree.Symbols {
		if strings.Contains(s.Name, "Display for Foo") {
			traitImpl = s
		}
		if s.Name == "Foo<T>" {
			inherentImpl = s
		}
	}
	if traitImpl == nil {
		t.Errorf("trait impl symbol `Display for Foo<T>` not found, got=%v", topLevelNames(tree.Symbols))
	}
	if inherentImpl == nil {
		t.Errorf("inherent impl symbol `Foo<T>` not found, got=%v", topLevelNames(tree.Symbols))
	}
}

func TestRustBackendCarriesSignatureRange(t *testing.T) {
	tree := parseRust(t, `pub fn add(a: i32, b: i32) -> i32 { a + b }`)
	add := findSymbol(tree.Symbols, "add")
	if add == nil {
		t.Fatal("add missing")
	}
	if add.Signature == "" {
		t.Errorf("expected signature to be captured, got empty")
	}
	if !strings.Contains(add.Signature, "fn add(a: i32, b: i32) -> i32") {
		t.Errorf("signature: %q", add.Signature)
	}
	if add.NameRange == (psi.Range{}) {
		t.Errorf("expected NameRange to be set")
	}
	// NameRange must point exactly at the identifier "add".
	got := tree.Source[add.NameRange.Start:add.NameRange.End]
	if string(got) != "add" {
		t.Errorf("NameRange covers %q want %q", got, "add")
	}
}

// ===== helpers =====

func findInherentImpl(syms []*treesitter.Symbol, typeName string) *treesitter.Symbol {
	for _, s := range syms {
		if s.Kind != psi.KindClass {
			continue
		}
		// Inherent impls keep just the type name (or `Type<…>`); trait
		// impls keep the `Trait for Type` prefix.
		if (s.Name == typeName || strings.HasPrefix(s.Name, typeName+"<")) &&
			!strings.Contains(s.Signature, " for ") {
			return s
		}
	}
	return nil
}

func findStruct(syms []*treesitter.Symbol, name string) *treesitter.Symbol {
	for _, s := range syms {
		if s.Kind == psi.KindStruct && s.Name == name {
			return s
		}
	}
	return nil
}

func containsAll(haystack, needles []string) bool {
	have := map[string]bool{}
	for _, h := range haystack {
		have[h] = true
	}
	for _, n := range needles {
		if !have[n] {
			return false
		}
	}
	return true
}

func topLevelNames(syms []*treesitter.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Name
	}
	return out
}

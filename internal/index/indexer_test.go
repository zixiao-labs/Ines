package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zixiao-labs/ines/internal/parser"

	_ "github.com/zixiao-labs/ines/internal/lang/golang"
	_ "github.com/zixiao-labs/ines/internal/lang/rust"
	_ "github.com/zixiao-labs/ines/internal/lang/typescript"
)

func TestIndexerEmitsProgressAndPopulatesEntries(t *testing.T) {
	dir := t.TempDir()
	must := func(rel, body string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("a/main.go", "package a\nfunc Hello() {}\n")
	must("b/util.ts", "export function go() {}\n")
	must("node_modules/ignored.go", "package x\n")

	idx := NewIndexer(nil)
	ch, err := idx.Index(context.Background(), dir)
	if err != nil {
		t.Fatalf("index: %v", err)
	}

	var sawScanning, sawParsing, sawDone bool
	var lastTotal int
	for p := range ch {
		switch p.Phase {
		case "scanning":
			sawScanning = true
		case "parsing":
			sawParsing = true
			if p.Total > lastTotal {
				lastTotal = p.Total
			}
		case "done":
			sawDone = true
		}
	}
	if !sawScanning || !sawParsing || !sawDone {
		t.Fatalf("phases: scan=%v parse=%v done=%v", sawScanning, sawParsing, sawDone)
	}
	if lastTotal != 2 {
		t.Fatalf("total: got %d want 2 (node_modules must be skipped)", lastTotal)
	}
	stats := idx.Stats()
	if stats.Files != 2 {
		t.Fatalf("indexed: got %d want 2", stats.Files)
	}
}

// TestIndexerHandlesRust covers end-to-end indexing of `.rs` files: the
// new tree-sitter-style backend produces a full PSI tree with nested
// items, the indexer caches it on the entry, and `feature` consumers can
// pull it back out.
func TestIndexerHandlesRust(t *testing.T) {
	dir := t.TempDir()
	src := `pub struct Counter {
    value: i32,
}

impl Counter {
    pub fn new() -> Self { Self { value: 0 } }
    pub fn get(&self) -> i32 { self.value }
}
`
	must := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("src/lib.rs", src)

	idx := NewIndexer(nil)
	ch, err := idx.Index(context.Background(), dir)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	for range ch {
	}
	entry := idx.Lookup(filepath.Join(dir, "src", "lib.rs"))
	if entry == nil {
		t.Fatalf("lib.rs was not indexed")
	}
	if entry.Language != "rust" {
		t.Errorf("language: got %q want rust", entry.Language)
	}
	names := []string{}
	for _, child := range entry.File.Children() {
		names = append(names, child.Name())
	}
	if !sliceContains(names, "Counter") {
		t.Errorf("Counter struct missing from outline: %v", names)
	}
}

func sliceContains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestIndexerRunsSemanticAugmenter is the end-to-end check that the
// indexer hands its workspace root and parsed PSI to the language-specific
// augmenter, and that the augmenter's diagnostics land on the entry the
// IPC layer surfaces over `ide/diagnostics`. The TypeScript adapter is the
// canonical augmenter today (Issue #5: module resolution).
func TestIndexerRunsSemanticAugmenter(t *testing.T) {
	dir := t.TempDir()
	must := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("tsconfig.json", `{"compilerOptions":{"baseUrl":"./","paths":{"@/*":["src/*"]}}}`)
	must("src/main.ts", `import { good } from "@/util";
import { bad } from "@/missing";
import "node:fs";
`)
	must("src/util.ts", "export const good = 1;\n")

	idx := NewIndexer(nil)
	ch, err := idx.Index(context.Background(), dir)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	for range ch {
	}
	entry := idx.Lookup(filepath.Join(dir, "src", "main.ts"))
	if entry == nil {
		t.Fatalf("main.ts not indexed")
	}
	var seen []parser.Diagnostic
	for _, d := range entry.Diagnostics {
		if strings.Contains(d.Message, "Cannot find module") {
			seen = append(seen, d)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("expected one Cannot-find-module diagnostic, got %d (%v)", len(seen), entry.Diagnostics)
	}
	if !strings.Contains(seen[0].Message, "@/missing") {
		t.Errorf("wrong specifier: %q", seen[0].Message)
	}
}

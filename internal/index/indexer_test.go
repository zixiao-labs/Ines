package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/zixiao-labs/ines/internal/lang/golang"
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

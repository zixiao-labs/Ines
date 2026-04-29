package feature_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zixiao-labs/ines/internal/feature"
	"github.com/zixiao-labs/ines/internal/index"

	_ "github.com/zixiao-labs/ines/internal/lang/golang"
	_ "github.com/zixiao-labs/ines/internal/lang/typescript"
)

func setupIndexedWorkspace(t *testing.T) (*index.Indexer, string, string) {
	t.Helper()
	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	utilGo := filepath.Join(dir, "util.go")
	if err := os.WriteFile(mainGo, []byte(`package demo

func Greet(name string) string {
	return "hi " + name
}

func Run() string {
	return Greet("world")
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(utilGo, []byte(`package demo

func Helper() string {
	return Greet("there")
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := index.NewIndexer(nil)
	ch, err := idx.Index(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	return idx, mainGo, utilGo
}

func TestCompletionFiltersByPrefix(t *testing.T) {
	idx, mainGo, _ := setupIndexedWorkspace(t)
	svc := feature.New(idx)
	items := svc.Completion(mainGo, "Gr", 50)
	if len(items) == 0 {
		t.Fatalf("expected completion items")
	}
	found := false
	for _, item := range items {
		if item.Label == "Greet" {
			found = true
		}
	}
	if !found {
		t.Errorf("Greet not in completions: %v", items)
	}
}

func TestDefinitionFindsCrossFileSymbol(t *testing.T) {
	idx, mainGo, utilGo := setupIndexedWorkspace(t)
	svc := feature.New(idx)
	// "Greet" appears in util.go inside `Greet("there")`. Pick the offset
	// inside the call.
	src, _ := os.ReadFile(utilGo)
	offset := strings.Index(string(src), "Greet(\"there\")") + 1
	locs := svc.Definition(utilGo, offset)
	if len(locs) == 0 {
		t.Fatalf("expected definition locations")
	}
	foundMain := false
	for _, l := range locs {
		if l.Path == mainGo {
			foundMain = true
		}
	}
	if !foundMain {
		t.Errorf("definition not in main.go: %+v", locs)
	}
}

func TestReferencesFindsAllOccurrences(t *testing.T) {
	idx, mainGo, _ := setupIndexedWorkspace(t)
	svc := feature.New(idx)
	src, _ := os.ReadFile(mainGo)
	offset := strings.Index(string(src), "func Greet") + len("func ")
	refs := svc.References(mainGo, offset, true)
	if len(refs) < 2 {
		t.Fatalf("expected at least two references, got %d", len(refs))
	}
}

func TestRenameProducesEditsForEveryOccurrence(t *testing.T) {
	idx, mainGo, _ := setupIndexedWorkspace(t)
	svc := feature.New(idx)
	src, _ := os.ReadFile(mainGo)
	offset := strings.Index(string(src), "func Greet") + len("func ")
	oldName, edits := svc.Rename(mainGo, offset, "Hello")
	if oldName != "Greet" {
		t.Fatalf("oldName: got %q", oldName)
	}
	if len(edits) < 3 {
		t.Fatalf("expected >= 3 edits, got %d", len(edits))
	}
	for _, e := range edits {
		if e.NewText != "Hello" {
			t.Errorf("unexpected newText: %q", e.NewText)
		}
	}
}

func TestDiagnosticsSurfacesParseErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.go")
	if err := os.WriteFile(bad, []byte("package x\nfunc oops( {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := index.NewIndexer(nil)
	ch, err := idx.Index(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	svc := feature.New(idx)
	diags := svc.Diagnostics(bad)
	if len(diags[bad]) == 0 {
		t.Fatalf("expected diagnostics for broken file")
	}
}

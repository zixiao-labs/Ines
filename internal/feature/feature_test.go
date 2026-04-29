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

// Greet says hello — the word Greet appears in this comment too.
func Greet(name string) string {
	return "hi Greet " + name
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

// findOffset wraps strings.Index with a hard-fail when the needle is missing,
// preventing silent 0/4 offsets if the fixture changes underneath the tests.
func findOffset(t *testing.T, src []byte, needle string, plus int) int {
	t.Helper()
	idx := strings.Index(string(src), needle)
	if idx == -1 {
		t.Fatalf("needle %q not found in source", needle)
	}
	return idx + plus
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
	offset := findOffset(t, src, `Greet("there")`, 1)
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
	offset := findOffset(t, src, "func Greet", len("func "))
	refs := svc.References(mainGo, offset, true)
	if len(refs) < 2 {
		t.Fatalf("expected at least two references, got %d", len(refs))
	}
}

func TestReferencesExcludesDeclaration(t *testing.T) {
	idx, mainGo, _ := setupIndexedWorkspace(t)
	svc := feature.New(idx)
	src, _ := os.ReadFile(mainGo)
	declOffset := findOffset(t, src, "func Greet", len("func "))
	declStart := findOffset(t, src, "func Greet", len("func "))
	declEnd := declStart + len("Greet")

	withDecl := svc.References(mainGo, declOffset, true)
	withoutDecl := svc.References(mainGo, declOffset, false)
	if len(withoutDecl) >= len(withDecl) {
		t.Fatalf("expected fewer refs without declaration: with=%d without=%d",
			len(withDecl), len(withoutDecl))
	}
	for _, ref := range withoutDecl {
		if ref.Path == mainGo && ref.Start == declStart && ref.End == declEnd {
			t.Fatalf("declaration leaked into includeDeclaration=false result: %+v", ref)
		}
	}
}

func TestRenameProducesEditsForEveryOccurrence(t *testing.T) {
	idx, mainGo, _ := setupIndexedWorkspace(t)
	svc := feature.New(idx)
	src, _ := os.ReadFile(mainGo)
	offset := findOffset(t, src, "func Greet", len("func "))
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

func TestRenameSkipsStringsAndComments(t *testing.T) {
	idx, mainGo, _ := setupIndexedWorkspace(t)
	svc := feature.New(idx)
	src, _ := os.ReadFile(mainGo)
	offset := findOffset(t, src, "func Greet", len("func "))
	commentStart := findOffset(t, src, "// Greet says hello", 0)
	commentEnd := commentStart + len("// Greet says hello — the word Greet appears in this comment too.")
	stringStart := findOffset(t, src, `"hi Greet "`, 0)
	stringEnd := stringStart + len(`"hi Greet "`)

	_, edits := svc.Rename(mainGo, offset, "Hello")
	if len(edits) == 0 {
		t.Fatalf("rename produced no edits")
	}
	for _, e := range edits {
		if e.Path != mainGo {
			continue
		}
		if e.Start >= commentStart && e.End <= commentEnd {
			t.Errorf("rename touched the comment range [%d,%d]: %+v",
				commentStart, commentEnd, e)
		}
		if e.Start >= stringStart && e.End <= stringEnd {
			t.Errorf("rename touched the string literal range [%d,%d]: %+v",
				stringStart, stringEnd, e)
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

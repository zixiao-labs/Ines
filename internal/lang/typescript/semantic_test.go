package typescript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zixiao-labs/ines/internal/lang/treesitter"
	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

// parseFile reproduces the production pipeline: backend.Parse → LiftToPSI.
// Tests use it to feed the augmenter realistic input without leaking the
// scanner's internal Tree / Symbol types into assertions.
func parseFile(t *testing.T, path string, src []byte) psi.File {
	t.Helper()
	backend := newTSBackend()
	tree, err := backend.Parse(parser.Source{Path: path, Content: src, Language: "typescript"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return treesitter.LiftToPSI(tree)
}

func TestAugmenterEmitsCannotFindModule(t *testing.T) {
	dir := t.TempDir()
	src := []byte(`import { foo } from "./util";
import { bar } from "./missing";
import React from "react";
import { ghost } from "ghost-pkg";
import "node:fs";
`)
	path := filepath.Join(dir, "src", "main.ts")
	writeFile(t, path, string(src))
	writeFile(t, filepath.Join(dir, "src", "util.ts"), "export const foo = 1;")
	pkgDir := filepath.Join(dir, "node_modules", "react")
	writeFile(t, filepath.Join(pkgDir, "package.json"), `{"types":"index.d.ts"}`)
	writeFile(t, filepath.Join(pkgDir, "index.d.ts"), "")

	file := parseFile(t, path, src)
	a := newAugmenter()
	got := a.AugmentDiagnostics(parser.SemanticContext{
		Workspace: dir,
		Path:      path,
		Source:    src,
		File:      file,
	})

	wantMessages := map[string]bool{
		"./missing": true,
		"ghost-pkg": true,
	}
	gotMessages := map[string]bool{}
	for _, d := range got {
		if d.Severity != parser.SeverityError {
			t.Errorf("severity: got %v want Error for %q", d.Severity, d.Message)
		}
		if !strings.Contains(d.Message, "Cannot find module") {
			t.Errorf("message shape: got %q", d.Message)
			continue
		}
		start := strings.Index(d.Message, "'")
		end := strings.LastIndex(d.Message, "'")
		if start < 0 || end <= start {
			continue
		}
		gotMessages[d.Message[start+1:end]] = true
	}
	for spec := range wantMessages {
		if !gotMessages[spec] {
			t.Errorf("expected diagnostic for %q, got=%v", spec, gotMessages)
		}
	}
	for spec := range gotMessages {
		if !wantMessages[spec] {
			t.Errorf("unexpected diagnostic for %q", spec)
		}
	}
	// Spot-check that the squiggle range covers the quoted specifier
	// (including the quotes), not the whole import statement.
	for _, d := range got {
		span := string(src[d.Start:d.End])
		if !strings.HasPrefix(span, `"`) || !strings.HasSuffix(span, `"`) {
			t.Errorf("diagnostic range should cover the quoted literal, got %q", span)
		}
	}
}

func TestAugmenterRespectsTSConfigPaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{
		"compilerOptions": {
			"baseUrl": "./",
			"paths": { "@/*": ["src/*"] }
		}
	}`)
	src := []byte(`import { settings } from "@/stores/settings";
import { gone } from "@/stores/missing";
`)
	path := filepath.Join(dir, "src", "main.ts")
	writeFile(t, path, string(src))
	writeFile(t, filepath.Join(dir, "src", "stores", "settings.ts"), "export const settings = {};")

	file := parseFile(t, path, src)
	a := newAugmenter()
	got := a.AugmentDiagnostics(parser.SemanticContext{
		Workspace: dir,
		Path:      path,
		Source:    src,
		File:      file,
	})
	if len(got) != 1 {
		t.Fatalf("expected one diagnostic, got=%d (%v)", len(got), got)
	}
	if !strings.Contains(got[0].Message, "@/stores/missing") {
		t.Errorf("wrong specifier in diagnostic: %q", got[0].Message)
	}
}

func TestAugmenterRecognisesReExports(t *testing.T) {
	dir := t.TempDir()
	src := []byte(`export { foo } from "./missing";
export * from "./also-missing";
`)
	path := filepath.Join(dir, "src", "barrel.ts")
	writeFile(t, path, string(src))
	file := parseFile(t, path, src)
	a := newAugmenter()
	got := a.AugmentDiagnostics(parser.SemanticContext{
		Workspace: dir,
		Path:      path,
		Source:    src,
		File:      file,
	})
	if len(got) != 2 {
		t.Fatalf("expected two re-export diagnostics, got=%d (%v)", len(got), got)
	}
}

// stubBridge lets us assert that augmenter merges Bridge output without
// shelling out to a Node child.
type stubBridge struct {
	out map[string][]parser.Diagnostic
	err error
}

func (s *stubBridge) WorkspaceDiagnostics(workspace string) (map[string][]parser.Diagnostic, error) {
	return s.out, s.err
}

func TestAugmenterMergesBridgeDiagnostics(t *testing.T) {
	dir := t.TempDir()
	src := []byte(`export const x = 1;\n`)
	path := filepath.Join(dir, "src", "x.ts")
	writeFile(t, path, string(src))
	file := parseFile(t, path, src)
	SetBridge(&stubBridge{out: map[string][]parser.Diagnostic{
		path: {{
			Severity: parser.SeverityError,
			Message:  "Type 'string' is not assignable to type 'number'.",
			Source:   "ts",
			Start:    0,
			End:      1,
		}},
	}})
	t.Cleanup(func() { SetBridge(nil) })

	a := newAugmenter()
	got := a.AugmentDiagnostics(parser.SemanticContext{
		Workspace: dir,
		Path:      path,
		Source:    src,
		File:      file,
	})
	found := false
	for _, d := range got {
		if strings.Contains(d.Message, "is not assignable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected bridge diagnostic to be merged, got=%v", got)
	}
}

func TestAugmenterIsNoOpWithoutFile(t *testing.T) {
	a := newAugmenter()
	got := a.AugmentDiagnostics(parser.SemanticContext{Workspace: t.TempDir()})
	if len(got) != 0 {
		t.Fatalf("expected no diagnostics for nil file, got=%v", got)
	}
}

// Smoke test that the resolver caches across separate workspace switches —
// the augmenter must invalidate the resolver when ctx.Workspace changes so
// switching projects does not leak node_modules from the previous one.
func TestAugmenterRebuildsResolverOnWorkspaceSwitch(t *testing.T) {
	a := newAugmenter()
	first := t.TempDir()
	second := t.TempDir()
	r1 := a.resolverFor(first)
	r2 := a.resolverFor(second)
	if r1 == r2 {
		t.Fatalf("expected distinct resolvers per workspace")
	}
	r3 := a.resolverFor(second)
	if r2 != r3 {
		t.Fatalf("expected resolver reuse for the same workspace")
	}
}

func init() {
	// Touching os keeps go vet from flagging an unused import when we
	// shrink the helper file in the future.
	_ = os.TempDir
}

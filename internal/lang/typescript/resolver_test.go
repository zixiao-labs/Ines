package typescript

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a convenience helper that creates parent directories on
// demand so test fixtures stay legible.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolverHandlesRelativeSpecifier(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "index.ts"), `import { foo } from "./util";`)
	writeFile(t, filepath.Join(dir, "src", "util.ts"), `export const foo = 1;`)
	r := newResolver(dir)
	if !r.Resolve(filepath.Join(dir, "src"), "./util") {
		t.Fatalf("expected relative import to resolve")
	}
	if r.Resolve(filepath.Join(dir, "src"), "./missing") {
		t.Fatalf("expected missing relative import to fail")
	}
}

func TestResolverHonoursTSConfigPaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{
		// the resolver tolerates JSONC
		"compilerOptions": {
			"baseUrl": "./",
			"paths": {
				"@/*": ["src/*"],
				"~lib/*": ["src/lib/*"],
			}
		}
	}`)
	writeFile(t, filepath.Join(dir, "src", "stores", "settings.ts"), `export {};`)
	writeFile(t, filepath.Join(dir, "src", "lib", "fs.ts"), `export {};`)
	r := newResolver(dir)
	importerDir := filepath.Join(dir, "src", "components")
	if !r.Resolve(importerDir, "@/stores/settings") {
		t.Fatalf("expected @/stores/settings to resolve via paths")
	}
	if !r.Resolve(importerDir, "~lib/fs") {
		t.Fatalf("expected ~lib/fs to resolve via paths")
	}
	if r.Resolve(importerDir, "@/stores/missing") {
		t.Fatalf("missing path-mapped specifier should fail")
	}
}

func TestResolverWalksNodeModules(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "node_modules", "react")
	writeFile(t, filepath.Join(pkgDir, "package.json"), `{"main":"index.js","types":"index.d.ts"}`)
	writeFile(t, filepath.Join(pkgDir, "index.d.ts"), `export {};`)
	scopedDir := filepath.Join(dir, "node_modules", "@heroui", "react")
	writeFile(t, filepath.Join(scopedDir, "package.json"), `{"main":"index.js"}`)
	writeFile(t, filepath.Join(scopedDir, "index.d.ts"), `export {};`)
	typesDir := filepath.Join(dir, "node_modules", "@types", "node")
	writeFile(t, filepath.Join(typesDir, "package.json"), `{"types":"index.d.ts"}`)
	writeFile(t, filepath.Join(typesDir, "index.d.ts"), `export {};`)

	r := newResolver(dir)
	importerDir := filepath.Join(dir, "src")
	if !r.Resolve(importerDir, "react") {
		t.Fatalf("expected react to resolve through node_modules")
	}
	if !r.Resolve(importerDir, "@heroui/react") {
		t.Fatalf("expected scoped package to resolve")
	}
	if !r.Resolve(importerDir, "node:fs") {
		t.Fatalf("node: specifiers should always resolve")
	}
	// "node" as a bare specifier should NOT auto-resolve; it must come
	// from node_modules. We installed @types/node but not the real
	// "node" module, and the resolver's @types/<spec> fallback should
	// kick in here.
	if !r.Resolve(importerDir, "node") {
		t.Fatalf("expected @types/node fallback")
	}
	if r.Resolve(importerDir, "this-package-does-not-exist") {
		t.Fatalf("missing package should fail")
	}
}

func TestResolverPathsExtendsChain(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.base.json"), `{
		"compilerOptions": {
			"baseUrl": "./",
			"paths": { "@/*": ["src/*"] }
		}
	}`)
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{
		"extends": "./tsconfig.base.json"
	}`)
	writeFile(t, filepath.Join(dir, "src", "x.ts"), `export const x = 1;`)
	r := newResolver(dir)
	if !r.Resolve(filepath.Join(dir, "tests"), "@/x") {
		t.Fatalf("expected @/x to resolve via extends chain")
	}
}

func TestResolverCachesResults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "a.ts"), "")
	r := newResolver(dir)
	importerDir := filepath.Join(dir, "src")
	if !r.Resolve(importerDir, "./a") {
		t.Fatalf("first lookup")
	}
	// Mutate the underlying filesystem; the cache must still report the
	// previous answer because it is keyed on (importerDir, specifier).
	if err := os.Remove(filepath.Join(dir, "src", "a.ts")); err != nil {
		t.Fatal(err)
	}
	if !r.Resolve(importerDir, "./a") {
		t.Fatalf("expected cached resolve to survive filesystem mutation")
	}
}

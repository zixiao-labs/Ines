// resolver.go implements TypeScript's module resolution algorithm against
// the workspace filesystem. It is the in-process counterpart of `tsc
// --noEmit` for the single, narrow purpose of detecting `Cannot find
// module` false positives that have to-date been the dominant noise source
// in Logos's editor — see Issue #5.
//
// The algorithm follows the upstream classic + bundler hybrid that
// real-world projects need:
//
//  1. Bare `node:` specifiers are always considered resolved (they are
//     Node built-ins).
//  2. Relative specifiers (`./`, `../`) are resolved against the importer's
//     directory with the standard TypeScript extension probe.
//  3. tsconfig.json `compilerOptions.paths` mappings are applied; both
//     literal entries and `*` wildcards are honoured.
//  4. tsconfig.json `compilerOptions.baseUrl` (when set) is treated as a
//     classic-style root: bare specifiers may resolve relative to it.
//  5. Bare specifiers fall back to a node_modules walk that climbs from
//     the importer's directory up to (and including) the workspace root.
//     Both the package itself and `@types/<package>` declaration packages
//     are honoured.
//
// All filesystem checks go through a single `exists`/`stat` helper so the
// resolver can be unit-tested against an in-memory FS later. For now it
// uses the real os package because indexing already touches disk.
package typescript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// resolver caches workspace-level state (tsconfig.json) and per-specifier
// resolution results. It is created lazily by the augmenter the first time
// a given workspace asks for diagnostics; one resolver per workspace.
type resolver struct {
	workspace string

	cfgOnce sync.Once
	cfg     *tsConfig

	cache sync.Map // key = importerDir + "\x00" + specifier, value = bool
}

func newResolver(workspace string) *resolver {
	return &resolver{workspace: workspace}
}

// loadConfig reads the workspace tsconfig at most once and caches the
// result. Errors are intentionally silent: a missing or malformed tsconfig
// must not break diagnostics — we just fall back to relative + node_modules
// resolution.
func (r *resolver) loadConfig() *tsConfig {
	r.cfgOnce.Do(func() {
		if r.workspace == "" {
			return
		}
		cfg, err := loadTSConfig(r.workspace, r.workspace)
		if err == nil {
			r.cfg = cfg
		}
	})
	return r.cfg
}

// Resolve reports whether specifier is a real on-disk module from the
// importer's perspective. Resolution failures are cached to keep the hot
// path cheap.
func (r *resolver) Resolve(importerDir, specifier string) bool {
	if specifier == "" {
		return true
	}
	if isAlwaysResolved(specifier) {
		return true
	}
	key := importerDir + "\x00" + specifier
	if v, ok := r.cache.Load(key); ok {
		return v.(bool)
	}
	ok := r.resolveUncached(importerDir, specifier)
	r.cache.Store(key, ok)
	return ok
}

func (r *resolver) resolveUncached(importerDir, specifier string) bool {
	// 1. Relative specifier
	if isRelativeSpecifier(specifier) {
		base := filepath.Join(importerDir, specifier)
		return tryFileOrIndex(base)
	}

	cfg := r.loadConfig()

	// 2. tsconfig.paths mappings. Wildcard matching follows TypeScript:
	// the longest matching pattern wins; a `*` placeholder matches any
	// suffix and is substituted into each candidate template.
	if cfg != nil && len(cfg.Paths) > 0 {
		if matched := matchPathsMapping(cfg, specifier); matched {
			return true
		}
	}

	// 3. baseUrl-relative classic resolution
	if cfg != nil && cfg.BaseURL != "" {
		if tryFileOrIndex(filepath.Join(cfg.BaseURL, specifier)) {
			return true
		}
	}

	// 4. node_modules walk
	if tryNodeModules(importerDir, r.workspace, specifier) {
		return true
	}
	return false
}

// matchPathsMapping resolves specifier through cfg.Paths. Every matched
// template is probed against the workspace filesystem; the function
// returns true on the first hit. For wildcard patterns the longer,
// more-specific prefix wins, mirroring TypeScript.
func matchPathsMapping(cfg *tsConfig, specifier string) bool {
	type pathHit struct {
		key   string
		subst string
	}
	var hits []pathHit
	for k := range cfg.Paths {
		if strings.Contains(k, "*") {
			star := strings.Index(k, "*")
			prefix := k[:star]
			suffix := k[star+1:]
			if strings.HasPrefix(specifier, prefix) && strings.HasSuffix(specifier, suffix) {
				subst := specifier[len(prefix) : len(specifier)-len(suffix)]
				hits = append(hits, pathHit{key: k, subst: subst})
			}
		} else if k == specifier {
			hits = append(hits, pathHit{key: k, subst: ""})
		}
	}
	// Longest-prefix-wins ordering for wildcard hits.
	if len(hits) > 1 {
		// Insertion sort — len(hits) is tiny in practice.
		for i := 1; i < len(hits); i++ {
			for j := i; j > 0 && len(hits[j].key) > len(hits[j-1].key); j-- {
				hits[j], hits[j-1] = hits[j-1], hits[j]
			}
		}
	}
	base := cfg.BaseURL
	if base == "" {
		base = filepath.Dir(cfg.File)
	}
	for _, hit := range hits {
		for _, tmpl := range cfg.Paths[hit.key] {
			candidatePath := strings.ReplaceAll(tmpl, "*", hit.subst)
			if !filepath.IsAbs(candidatePath) {
				candidatePath = filepath.Join(base, candidatePath)
			}
			if tryFileOrIndex(candidatePath) {
				return true
			}
		}
	}
	return false
}

// tryNodeModules walks dir up to (and including) workspace, probing
// node_modules at each level. Both `<spec>` and `@types/<spec>` are tried.
func tryNodeModules(dir, workspace, specifier string) bool {
	if dir == "" {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	wsAbs := workspace
	if workspace != "" {
		if a, err := filepath.Abs(workspace); err == nil {
			wsAbs = a
		}
	}
	for {
		nm := filepath.Join(abs, "node_modules")
		if info, err := os.Stat(nm); err == nil && info.IsDir() {
			if tryNodeModulesPackage(nm, specifier) {
				return true
			}
			if tryNodeModulesPackage(nm, "@types/"+typesPackageName(specifier)) {
				return true
			}
		}
		if wsAbs != "" && abs == wsAbs {
			return false
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false
		}
		abs = parent
		if wsAbs != "" && !strings.HasPrefix(abs, filepath.Dir(wsAbs)) && abs != wsAbs {
			// Stop once we've climbed strictly above the workspace.
			// (Without this guard a project that opens a sub-folder of a
			// monorepo could accidentally reach the user's home.)
			if parent != wsAbs {
				return false
			}
		}
	}
}

// tryNodeModulesPackage resolves specifier inside nodeModulesDir. It
// honours the package's `main` / `types` entry, the bare directory entry
// (index.{ts,...}), and a sub-path inside the package.
func tryNodeModulesPackage(nodeModulesDir, specifier string) bool {
	pkgRoot, subPath := splitPackageSpecifier(specifier)
	pkgDir := filepath.Join(nodeModulesDir, pkgRoot)
	info, err := os.Stat(pkgDir)
	if err != nil || !info.IsDir() {
		return false
	}
	if subPath == "" {
		// Resolve the package entrypoint via package.json's `types`
		// (preferred) or `main`. If neither is present we still treat the
		// presence of the directory as a hit because a JS-only package
		// without types is still a real module.
		if entry := readPackageEntrypoint(pkgDir); entry != "" {
			if tryFileOrIndex(filepath.Join(pkgDir, entry)) {
				return true
			}
		}
		return tryFileOrIndex(filepath.Join(pkgDir, "index"))
	}
	return tryFileOrIndex(filepath.Join(pkgDir, subPath))
}

// splitPackageSpecifier splits "@scope/name/sub/path" into ("@scope/name",
// "sub/path") and "name/sub/path" into ("name", "sub/path").
func splitPackageSpecifier(spec string) (string, string) {
	if strings.HasPrefix(spec, "@") {
		// scoped package keeps the first two segments together
		idx := strings.Index(spec, "/")
		if idx == -1 {
			return spec, ""
		}
		rest := spec[idx+1:]
		idx2 := strings.Index(rest, "/")
		if idx2 == -1 {
			return spec, ""
		}
		return spec[:idx+1+idx2], rest[idx2+1:]
	}
	idx := strings.Index(spec, "/")
	if idx == -1 {
		return spec, ""
	}
	return spec[:idx], spec[idx+1:]
}

// typesPackageName converts "@scope/foo" → "scope__foo" (the @types
// convention) and leaves bare names untouched.
func typesPackageName(spec string) string {
	pkg, _ := splitPackageSpecifier(spec)
	if strings.HasPrefix(pkg, "@") {
		rest := strings.TrimPrefix(pkg, "@")
		return strings.Replace(rest, "/", "__", 1)
	}
	return pkg
}

func readPackageEntrypoint(pkgDir string) string {
	raw, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		Types   string `json:"types"`
		Typings string `json:"typings"`
		Main    string `json:"main"`
		Module  string `json:"module"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}
	for _, candidate := range []string{meta.Types, meta.Typings, meta.Main, meta.Module} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// tryFileOrIndex probes base with the standard TypeScript extension order,
// then falls back to base/index.* when base is a directory.
func tryFileOrIndex(base string) bool {
	if fileExists(base) {
		return true
	}
	for _, ext := range fileExtensions {
		if fileExists(base + ext) {
			return true
		}
	}
	if dirExists(base) {
		for _, ext := range indexExtensions {
			if fileExists(filepath.Join(base, "index"+ext)) {
				return true
			}
		}
		// Last resort: a package.json inside the directory.
		if fileExists(filepath.Join(base, "package.json")) {
			return true
		}
	}
	return false
}

// fileExtensions is the TypeScript extension probe order. .d.ts beats .ts
// in some configurations but for a cheap "does it exist" probe ordering
// barely matters; we keep the canonical TS order for readability.
var fileExtensions = []string{
	".ts", ".tsx", ".d.ts", ".js", ".jsx", ".mjs", ".cjs", ".json",
	".css", ".scss", ".less", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp",
}

// indexExtensions is the subset that may appear as `index.<ext>`.
var indexExtensions = []string{
	".ts", ".tsx", ".d.ts", ".js", ".jsx", ".mjs", ".cjs", ".json",
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isRelativeSpecifier reports whether specifier is one of `./`, `../`, or
// `/`. TypeScript also treats Windows-style paths as relative; we keep it
// simple for now because the daemon normalises paths to forward slashes
// before they reach the resolver.
func isRelativeSpecifier(spec string) bool {
	return strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") ||
		spec == "." || spec == ".." || strings.HasPrefix(spec, "/")
}

// isAlwaysResolved is the small allowlist of specifiers we never report
// errors for. `node:`-prefixed specifiers are core modules that ship with
// the runtime; we do not have a canonical list of every Node built-in
// because @types/node accretes them, but the prefix form suffices to
// silence the most common false positive (`import fs from "node:fs"`).
//
// Empty-import declarations like `import "./style.css"` are handled by the
// relative branch in resolveUncached.
func isAlwaysResolved(spec string) bool {
	return strings.HasPrefix(spec, "node:")
}

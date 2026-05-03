// tsconfig.go reads tsconfig.json (and the tsconfig.json files referenced
// through `extends`) and exposes the subset of options the resolver needs:
// `compilerOptions.baseUrl`, `compilerOptions.paths`, and `extends`.
//
// The TypeScript reference implementation tolerates a JSONC dialect on disk
// — line comments, block comments, and trailing commas are all legal in a
// real-world tsconfig. We strip those before handing the bytes to
// encoding/json so we can keep using the standard library.
package typescript

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// tsConfig captures the options the resolver needs. baseURL is resolved to
// an absolute path; paths' values are kept as-is and are interpreted
// relative to baseURL at resolution time.
type tsConfig struct {
	// File is the absolute path of the tsconfig.json this struct was loaded
	// from. Used for error messages and for joining `extends`.
	File string
	// BaseURL is the absolute directory `compilerOptions.baseUrl` resolves
	// to. Empty when not set.
	BaseURL string
	// Paths mirrors `compilerOptions.paths`. Wildcards are kept literal:
	// the resolver substitutes `*` at lookup time.
	Paths map[string][]string
}

// loadTSConfig walks up from start looking for the nearest tsconfig.json,
// loading and resolving `extends` chains. Returns (nil, nil) when no
// tsconfig.json is found within ceiling — the resolver treats that as
// "no config" rather than an error.
func loadTSConfig(start, ceiling string) (*tsConfig, error) {
	path := findTSConfigUp(start, ceiling)
	if path == "" {
		return nil, nil
	}
	return readTSConfigChain(path, map[string]struct{}{})
}

func findTSConfigUp(start, ceiling string) string {
	dir := start
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	dir = abs
	ceilingAbs := ceiling
	if ceiling != "" {
		if a, err := filepath.Abs(ceiling); err == nil {
			ceilingAbs = a
		}
	}
	for {
		candidate := filepath.Join(dir, "tsconfig.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		// jsconfig.json is the JS counterpart and ships the same `paths`
		// shape; honouring it lets a project-with-no-tsconfig still get
		// resolver-driven diagnostics.
		jcandidate := filepath.Join(dir, "jsconfig.json")
		if info, err := os.Stat(jcandidate); err == nil && !info.IsDir() {
			return jcandidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		if ceilingAbs != "" && (dir == ceilingAbs || !strings.HasPrefix(parent, ceilingAbs)) {
			// We've climbed past the workspace root; stop.
			if dir == ceilingAbs {
				return ""
			}
		}
		dir = parent
	}
}

// readTSConfigChain reads the tsconfig at path and merges any `extends`
// ancestors. visited prevents infinite loops on circular extends chains.
func readTSConfigChain(path string, visited map[string]struct{}) (*tsConfig, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, dup := visited[abs]; dup {
		return nil, errors.New("typescript: tsconfig extends cycle at " + abs)
	}
	visited[abs] = struct{}{}

	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	cleaned, err := stripJSONC(raw)
	if err != nil {
		return nil, err
	}
	type compilerOptions struct {
		BaseURL string              `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	}
	var raw2 struct {
		Extends         string          `json:"extends"`
		CompilerOptions compilerOptions `json:"compilerOptions"`
	}
	if err := json.Unmarshal(cleaned, &raw2); err != nil {
		return nil, err
	}
	cfg := &tsConfig{File: abs}
	dir := filepath.Dir(abs)
	if raw2.Extends != "" {
		extPath := raw2.Extends
		if !filepath.IsAbs(extPath) {
			// `extends` may be a relative path, a node_modules package, or
			// a "@scope/pkg/preset" specifier. We try a relative resolve
			// first and fall back to a node_modules walk so workshop
			// configs (`extends: "@tsconfig/node20"`) work too.
			if !strings.HasPrefix(extPath, ".") && !strings.HasPrefix(extPath, "..") {
				if hit := findExtendsInNodeModules(dir, extPath); hit != "" {
					extPath = hit
				} else {
					extPath = filepath.Join(dir, extPath)
				}
			} else {
				extPath = filepath.Join(dir, extPath)
			}
		}
		// `extends` allows omitting the `.json` suffix.
		if filepath.Ext(extPath) == "" {
			extPath += ".json"
		}
		parent, err := readTSConfigChain(extPath, visited)
		if err == nil && parent != nil {
			cfg.BaseURL = parent.BaseURL
			if len(parent.Paths) > 0 {
				cfg.Paths = make(map[string][]string, len(parent.Paths))
				for k, v := range parent.Paths {
					cfg.Paths[k] = append([]string(nil), v...)
				}
			}
		}
	}
	if raw2.CompilerOptions.BaseURL != "" {
		base := raw2.CompilerOptions.BaseURL
		if !filepath.IsAbs(base) {
			base = filepath.Join(dir, base)
		}
		cfg.BaseURL = filepath.Clean(base)
	}
	if len(raw2.CompilerOptions.Paths) > 0 {
		if cfg.Paths == nil {
			cfg.Paths = map[string][]string{}
		}
		for k, v := range raw2.CompilerOptions.Paths {
			cfg.Paths[k] = append([]string(nil), v...)
		}
	}
	return cfg, nil
}

func findExtendsInNodeModules(start, spec string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, "node_modules", spec)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		// Allow extends targeting a directory's package.json/tsconfig.json
		if info, err := os.Stat(candidate + ".json"); err == nil && !info.IsDir() {
			return candidate + ".json"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// stripJSONC removes // line comments, /* */ block comments, and trailing
// commas from src so that encoding/json can parse it. The function honours
// string literals so it does not corrupt strings containing `//` or `,]`.
func stripJSONC(src []byte) ([]byte, error) {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); {
		c := src[i]
		switch c {
		case '"':
			// String literal: copy verbatim including escapes.
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				if src[j] == '\n' {
					return nil, errors.New("typescript: unterminated string literal in tsconfig")
				}
				j++
			}
			if j >= len(src) {
				return nil, errors.New("typescript: unterminated string literal in tsconfig")
			}
			out = append(out, src[i:j+1]...)
			i = j + 1
			continue
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				j := i + 2
				for j < len(src) && src[j] != '\n' {
					j++
				}
				i = j
				continue
			}
			if i+1 < len(src) && src[i+1] == '*' {
				j := i + 2
				for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
					j++
				}
				if j+1 < len(src) {
					j += 2
				} else {
					j = len(src)
				}
				i = j
				continue
			}
		case ',':
			// Trailing-comma elision: peek past whitespace/comments to the
			// next significant byte. If it is `]` or `}`, drop the comma.
			j := i + 1
			for j < len(src) {
				if src[j] == ' ' || src[j] == '\t' || src[j] == '\r' || src[j] == '\n' {
					j++
					continue
				}
				if src[j] == '/' && j+1 < len(src) && (src[j+1] == '/' || src[j+1] == '*') {
					// Defer comment handling to the outer loop after we
					// commit (or skip) the comma.
					break
				}
				break
			}
			if j < len(src) && (src[j] == ']' || src[j] == '}') {
				i++ // drop trailing comma
				continue
			}
		}
		out = append(out, c)
		i++
	}
	return out, nil
}

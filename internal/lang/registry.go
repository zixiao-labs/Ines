// Package lang owns the registry of language adapters. Adapters register
// themselves at init time via Register, and downstream components (the
// indexer, the IPC server) look them up by language id or by file extension.
package lang

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/zixiao-labs/ines/internal/parser"
)

// Adapter ties a parser to the file-extension set it claims. Adapters are the
// public unit of pluggability inside Ines.
type Adapter struct {
	Language   string
	Extensions []string
	Parser     parser.Parser
}

var (
	mu         sync.RWMutex
	adapters   = map[string]*Adapter{}
	byExt      = map[string]*Adapter{}
	registered []*Adapter
)

// Register adds adapter to the global registry. It is safe to call from init
// functions inside language packages. Calling Register twice for the same
// language overwrites the previous entry — useful in tests.
func Register(adapter *Adapter) {
	if adapter == nil || adapter.Language == "" || adapter.Parser == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if existing, ok := adapters[adapter.Language]; ok {
		for _, ext := range existing.Extensions {
			delete(byExt, normaliseExt(ext))
		}
		registered = removeAdapter(registered, existing)
	}
	adapters[adapter.Language] = adapter
	registered = append(registered, adapter)
	for _, ext := range adapter.Extensions {
		byExt[normaliseExt(ext)] = adapter
	}
}

// All returns a snapshot of every registered adapter in registration order.
func All() []*Adapter {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]*Adapter, len(registered))
	copy(out, registered)
	return out
}

// ByLanguage returns the adapter registered for language, or nil when no
// adapter claimed that id.
func ByLanguage(language string) *Adapter {
	mu.RLock()
	defer mu.RUnlock()
	return adapters[language]
}

// ByPath inspects the file extension of path and returns the matching
// adapter. Hidden files without an extension are treated as language-agnostic
// and yield nil.
func ByPath(path string) *Adapter {
	mu.RLock()
	defer mu.RUnlock()
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return nil
	}
	return byExt[ext]
}

// SupportedExtensions returns every extension claimed by any adapter. Useful
// when the indexer wants to skip files that no adapter can understand.
func SupportedExtensions() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(byExt))
	for ext := range byExt {
		out = append(out, ext)
	}
	return out
}

func normaliseExt(ext string) string {
	if ext == "" {
		return ext
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return strings.ToLower(ext)
}

func removeAdapter(list []*Adapter, target *Adapter) []*Adapter {
	out := list[:0]
	for _, a := range list {
		if a != target {
			out = append(out, a)
		}
	}
	return out
}

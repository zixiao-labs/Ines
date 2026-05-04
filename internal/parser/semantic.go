package parser

import (
	"sync"

	"github.com/zixiao-labs/ines/internal/psi"
)

// SemanticContext bundles the workspace-aware information a SemanticAugmenter
// needs to emit cross-file diagnostics. It is built by the indexer for every
// file it parses; augmenters are expected to be cheap on cache hits because
// they run on every parse pass.
type SemanticContext struct {
	// Workspace is the absolute path of the indexer's root. Augmenters use
	// it to anchor relative lookups (e.g. tsconfig.json discovery and a
	// node_modules walk that must stop at the workspace boundary instead of
	// climbing into the user's home directory).
	Workspace string
	// Path is the absolute path of the file being augmented.
	Path string
	// Source is the byte content the parser saw.
	Source []byte
	// File is the parsed PSI tree.
	File psi.File
}

// SemanticAugmenter is implemented by language adapters that can compute
// extra diagnostics with workspace context — the canonical example is the
// TypeScript adapter resolving `import "..."` against tsconfig.json paths
// and node_modules, but the contract is intentionally generic so other
// adapters (Go module resolution, Rust workspace crates, ...) can plug in.
//
// Implementations must be safe for concurrent use because the indexer fans
// parse work out across goroutines.
type SemanticAugmenter interface {
	AugmentDiagnostics(ctx SemanticContext) []Diagnostic
}

var (
	augMu      sync.RWMutex
	augmenters = map[string]SemanticAugmenter{}
)

// RegisterSemanticAugmenter wires an augmenter for language. Calling it
// twice for the same language overwrites the previous entry — useful in
// tests where a fresh resolver is installed per t.TempDir().
func RegisterSemanticAugmenter(language string, a SemanticAugmenter) {
	if language == "" || a == nil {
		return
	}
	augMu.Lock()
	defer augMu.Unlock()
	augmenters[language] = a
}

// UnregisterSemanticAugmenter removes the augmenter for language. Useful in
// tests that want to restore the default state in t.Cleanup.
func UnregisterSemanticAugmenter(language string) {
	augMu.Lock()
	defer augMu.Unlock()
	delete(augmenters, language)
}

// SemanticAugmenterFor returns the augmenter registered for language, or
// nil. The indexer probes every adapter through this lookup, so a nil
// return is the common case.
func SemanticAugmenterFor(language string) SemanticAugmenter {
	augMu.RLock()
	defer augMu.RUnlock()
	return augmenters[language]
}

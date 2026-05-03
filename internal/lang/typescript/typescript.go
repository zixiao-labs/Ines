// Package typescript registers the TypeScript / JavaScript adapter.
//
// Since M2 the parser is backed by a hand-written, bracket-aware scanner
// surfaced through the treesitter abstraction. The new pipeline records
// nested classes and methods, parameter lists, namespaces and type aliases
// — replacing the line-oriented regex bootstrap. The Backend interface
// shape matches tree-sitter's vocabulary so a future swap to a real grammar
// is a drop-in replacement.
//
// The package also wires a workspace-aware semantic augmenter that
// resolves `import "..."` specifiers against tsconfig.json and
// node_modules and emits `Cannot find module` diagnostics when a specifier
// fails to resolve. See Issue #5 for the motivating context: Logos's
// editor used to flood the Problems panel with bogus "Cannot find module"
// findings whenever Monaco's bundled TS worker met a path-mapped or
// node_modules-resolved import; the augmenter replaces those with reality.
package typescript

import (
	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/treesitter"
	"github.com/zixiao-labs/ines/internal/parser"
)

func init() {
	lang.Register(&lang.Adapter{
		Language:   "typescript",
		Extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		Parser:     treesitter.NewParser(newTSBackend()),
	})
	parser.RegisterSemanticAugmenter("typescript", newAugmenter())
}

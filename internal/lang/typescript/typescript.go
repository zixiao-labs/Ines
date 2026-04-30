// Package typescript registers the TypeScript / JavaScript adapter.
//
// Since M2 the parser is backed by a hand-written, bracket-aware scanner
// surfaced through the treesitter abstraction. The new pipeline records
// nested classes and methods, parameter lists, namespaces and type aliases
// — replacing the line-oriented regex bootstrap. The Backend interface
// shape matches tree-sitter's vocabulary so a future swap to a real grammar
// is a drop-in replacement.
package typescript

import (
	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/treesitter"
)

func init() {
	lang.Register(&lang.Adapter{
		Language:   "typescript",
		Extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		Parser:     treesitter.NewParser(newTSBackend()),
	})
}

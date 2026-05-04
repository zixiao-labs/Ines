// Package rust registers the Rust language adapter.
//
// Since the upgrade to M2 the parser is a hand-written, bracket- and
// comment-aware scanner shaped after rust-analyzer's grammar. The new
// pipeline records nested items (mod / impl / trait bodies), struct
// fields, enum variants, and full function signatures — replacing the
// line-oriented regex bootstrap that only saw declaration headers.
//
// The Backend interface shape mirrors tree-sitter's vocabulary so a
// future swap to a real grammar (the day we are willing to take on the
// CGO dependency) is a drop-in replacement.
package rust

import (
	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/treesitter"
)

func init() {
	lang.Register(&lang.Adapter{
		Language:   "rust",
		Extensions: []string{".rs"},
		Parser:     treesitter.NewParser(newRustBackend()),
	})
}

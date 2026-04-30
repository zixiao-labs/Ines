// Package golang registers the Go language adapter.
//
// Since M2 the parser is backed by go/parser via the treesitter abstraction
// in internal/lang/treesitter, replacing the line-oriented regex bootstrap.
// The new pipeline surfaces nested type members, methods, parameters and
// signatures rather than just the top-level declarations.
package golang

import (
	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/treesitter"
)

func init() {
	lang.Register(&lang.Adapter{
		Language:   "go",
		Extensions: []string{".go"},
		Parser:     treesitter.NewParser(newGoBackend()),
	})
}

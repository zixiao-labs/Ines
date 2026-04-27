// Package parser defines the contract that every language adapter must
// satisfy. The PSI layer in package psi never talks to a concrete parser
// directly — it consumes the AST surfaced through the Parser interface so
// that the underlying machinery (a hand-written scanner today, a
// tree-sitter wrapper tomorrow) can be swapped without touching call sites.
package parser

import "github.com/zixiao-labs/ines/internal/psi"

// Source bundles the input handed to a parser: a logical path used to anchor
// the resulting PSI file plus the raw bytes that make up the source. Path may
// be empty for in-memory snippets.
type Source struct {
	Path     string
	Content  []byte
	Language string
}

// Parser turns a Source into a PSI file. Implementations are expected to be
// stateless and safe to share between goroutines.
//
// The first iteration of Ines ships hand-written scanners that recognise the
// top-level declarations relevant to navigation and outline rendering. The
// next milestone will replace them with tree-sitter-backed parsers that
// surface the full grammar; switching is a matter of registering a new
// implementation through lang.Registry.
type Parser interface {
	// Language returns the canonical language identifier ("go", "ts", ...).
	Language() string
	// Parse builds a PSI tree for the given source. Implementations must
	// return a non-nil File even when the input is malformed; recovery is the
	// adapter's responsibility.
	Parse(src Source) (psi.File, error)
}

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

// DiagnosticSeverity classifies how serious a parse-time finding is. Values
// match the LSP convention so a bare int round-trips through the wire codec
// without translation, but the named type stops adapters from drifting into
// ad-hoc severity numbers and gives the renderer stable colour/icon/filter
// semantics on the other end of the IPC channel.
type DiagnosticSeverity int

const (
	SeverityError   DiagnosticSeverity = 1
	SeverityWarning DiagnosticSeverity = 2
	SeverityInfo    DiagnosticSeverity = 3
	SeverityHint    DiagnosticSeverity = 4
)

// NormalizeSeverity clamps an arbitrary integer (e.g. one decoded from JSON)
// into the supported range, defaulting to SeverityError so an unknown value
// never silently disappears from the renderer.
func NormalizeSeverity(v int) DiagnosticSeverity {
	switch DiagnosticSeverity(v) {
	case SeverityError, SeverityWarning, SeverityInfo, SeverityHint:
		return DiagnosticSeverity(v)
	}
	return SeverityError
}

// Diagnostic is the public shape of a parse-time warning or error. The
// concrete tree-sitter backend produces these alongside its PSI tree; the
// indexer copies them into Entry so IDE features can serve them.
type Diagnostic struct {
	Severity DiagnosticSeverity
	Message  string
	Source   string
	Start    int
	End      int
}

// DiagnosingParser is implemented by parsers that surface structured
// diagnostics. The indexer probes for it via type assertion after every
// parse. Parsers that satisfy this interface return the same PSI tree
// Parse() would produce, plus any diagnostics the backend recovered.
type DiagnosingParser interface {
	Parser
	ParseWithDiagnostics(Source) (psi.File, []Diagnostic, error)
}

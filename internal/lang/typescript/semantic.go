// semantic.go wires the TypeScript module resolver into Ines's
// SemanticAugmenter contract. The indexer hands every parsed TypeScript /
// JavaScript file through Augment, the augmenter walks the PSI tree
// looking for KindImport nodes, and each unresolved specifier turns into a
// `Cannot find module` diagnostic on the wire.
//
// The augmenter intentionally only emits the `Cannot find module` family
// of diagnostics. Real type-aware diagnostics (assignment-not-assignable,
// missing properties, …) require hosting a TypeScript compiler / tsserver
// process; that work is gated on a separate Bridge interface declared
// here that future PRs can fill in without touching the renderer.
package typescript

import (
	"path/filepath"
	"sync"

	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

// DiagnosticSource is the canonical source label that wire diagnostics
// carry. It mirrors the TS compiler's "ts" tag while also marking the
// finding as Ines-emitted so the renderer can pick its own colour/icon.
const DiagnosticSource = "ines:ts"

// Bridge is the optional surface that hosts a real TypeScript compiler /
// tsserver outside the Go process. Implementations spawn `node` against a
// bundled helper script and translate its output into parser.Diagnostic.
//
// The augmenter has no Bridge by default — module-resolution diagnostics
// alone already eliminate the dominant false-positive class that motivates
// Issue #5. A future PR can register a real bridge through SetBridge so
// the wire shape stays stable.
type Bridge interface {
	WorkspaceDiagnostics(workspace string) (map[string][]parser.Diagnostic, error)
}

var (
	bridgeMu sync.RWMutex
	bridge   Bridge
)

// SetBridge installs an optional Bridge. Pass nil to remove the bridge.
// Tests can use this to stub the bridge without spawning a Node child.
func SetBridge(b Bridge) {
	bridgeMu.Lock()
	defer bridgeMu.Unlock()
	bridge = b
}

func currentBridge() Bridge {
	bridgeMu.RLock()
	defer bridgeMu.RUnlock()
	return bridge
}

// augmenter is the SemanticAugmenter implementation registered for the
// TypeScript adapter. It owns one resolver per workspace; switching
// workspaces clears the stale entry so an old node_modules tree does not
// taint a new project.
type augmenter struct {
	mu        sync.Mutex
	workspace string
	resolver  *resolver
}

func newAugmenter() *augmenter { return &augmenter{} }

func (a *augmenter) resolverFor(workspace string) *resolver {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.resolver == nil || a.workspace != workspace {
		a.workspace = workspace
		a.resolver = newResolver(workspace)
	}
	return a.resolver
}

// AugmentDiagnostics implements parser.SemanticAugmenter.
func (a *augmenter) AugmentDiagnostics(ctx parser.SemanticContext) []parser.Diagnostic {
	if ctx.File == nil {
		return nil
	}
	r := a.resolverFor(ctx.Workspace)
	importerDir := filepath.Dir(ctx.Path)
	var out []parser.Diagnostic
	walk(ctx.File, func(el psi.Element) {
		if el.Kind() != psi.KindImport {
			return
		}
		spec := el.Name()
		if spec == "" {
			return
		}
		if r.Resolve(importerDir, spec) {
			return
		}
		nameRange := importSpecifierRange(el, ctx.Source)
		out = append(out, parser.Diagnostic{
			Severity: parser.SeverityError,
			Message:  "Cannot find module '" + spec + "' or its corresponding type declarations.",
			Source:   DiagnosticSource,
			Start:    nameRange.Start,
			End:      nameRange.End,
		})
	})
	// If a Bridge is installed, merge its findings. Bridge errors are
	// silenced — diagnostics are an enhancement, not a hard requirement,
	// and a missing `node` binary must not surface as a per-file error.
	if b := currentBridge(); b != nil {
		extra, err := b.WorkspaceDiagnostics(ctx.Workspace)
		if err == nil {
			if list, ok := extra[ctx.Path]; ok {
				out = append(out, list...)
			}
		}
	}
	return out
}

// importSpecifierRange picks the tightest range we can squiggle for the
// import. The TypeScript backend records the quoted specifier in
// NameRange when it can; falling back to the statement Range keeps us
// resilient if the underlying scanner ever changes.
func importSpecifierRange(el psi.Element, source []byte) psi.Range {
	if nr, ok := el.(interface{ NameRange() psi.Range }); ok {
		if got := nr.NameRange(); got != (psi.Range{}) {
			return got
		}
	}
	r := el.Range()
	// Clamp to source bounds defensively — a buggy parser must not crash
	// the augmenter.
	if r.Start < 0 {
		r.Start = 0
	}
	if r.End > len(source) {
		r.End = len(source)
	}
	if r.End < r.Start {
		r.End = r.Start
	}
	return r
}

// walk visits every PSI node in the file in pre-order. Defined locally so
// the augmenter does not pull in feature/psi-walk helpers.
func walk(root psi.Element, fn func(psi.Element)) {
	if root == nil {
		return
	}
	fn(root)
	for _, child := range root.Children() {
		walk(child, fn)
	}
}

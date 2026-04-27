// Package index drives workspace indexing: walking a project, parsing every
// recognised file via the registered language adapter, and storing the
// resulting PSI trees so downstream features (completion, navigation,
// refactoring, diagnostics) can query them.
//
// Indexing is intentionally eager — the ai-prompt.md mandates that
// dependencies are included during indexing and that lazy loading is
// avoided — so the indexer makes a single pass over the workspace and
// reports its progress through a Progress channel.
package index

// Progress reports the state of an in-flight indexing run. Values are pushed
// onto the Progress channel returned by Indexer.Index and consumed by the
// IPC layer, which forwards them to Logos for the indexing progress bar.
type Progress struct {
	// Phase identifies the current indexing stage so the UI can render an
	// informative label ("scanning", "parsing", "linking", "done").
	Phase string
	// Done is the number of files processed so far.
	Done int
	// Total is the total number of files queued for processing. Zero while
	// the workspace is still being scanned.
	Total int
	// CurrentFile is the file the indexer is currently processing. Empty
	// during the discovery phase and after completion.
	CurrentFile string
}

// Fraction returns the [0, 1] completion ratio derived from Done/Total. It
// returns 0 while Total is unknown to avoid leaking a misleading percentage
// to the UI.
func (p Progress) Fraction() float64 {
	if p.Total <= 0 {
		return 0
	}
	if p.Done >= p.Total {
		return 1
	}
	return float64(p.Done) / float64(p.Total)
}

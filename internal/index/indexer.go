package index

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/metrics"
	"github.com/zixiao-labs/ines/internal/parser"
	"github.com/zixiao-labs/ines/internal/psi"
)

// Entry is the per-file record the indexer maintains. It bundles the parsed
// PSI tree with bookkeeping the IPC layer surfaces over its API.
type Entry struct {
	Path     string
	Language string
	File     psi.File
	Size     int64
	IndexAt  time.Time
}

// Indexer walks a workspace and builds a snapshot of PSI trees for every
// recognised file. Snapshots can be queried while a re-index is running; the
// internal map is guarded by a sync.RWMutex.
type Indexer struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	// SkipDirs lists directory base names that the walker ignores entirely.
	// Defaults are wired in NewIndexer.
	SkipDirs map[string]struct{}
	// MaxFileSize bounds the largest file the indexer will read. 0 disables
	// the limit. Defaults to 4 MiB.
	MaxFileSize int64
	// runMu serializes Index() calls to prevent reentrancy bugs where an old
	// worker goroutine writes into a freshly reset entries map.
	runMu    sync.Mutex
	reporter *metrics.Reporter
}

// NewIndexer constructs an Indexer with the conventional default skip set
// (vendored dependencies, build outputs, VCS metadata).
func NewIndexer(reporter *metrics.Reporter) *Indexer {
	return &Indexer{
		entries: map[string]*Entry{},
		SkipDirs: map[string]struct{}{
			".git":         {},
			".hg":          {},
			".svn":         {},
			".idea":        {},
			".vscode":      {},
			"node_modules": {},
			"vendor":       {},
			"target":       {},
			"build":        {},
			"dist":         {},
			"out":          {},
			".cache":       {},
		},
		MaxFileSize: 4 << 20,
		reporter:    reporter,
	}
}

// Index walks root and parses every recognised file. Progress events are
// pushed on the returned channel, which is closed once indexing completes or
// the context is cancelled. The function blocks until the workspace has been
// scanned in full so callers can either run it in a goroutine or block on it.
func (idx *Indexer) Index(ctx context.Context, root string) (<-chan Progress, error) {
	if root == "" {
		return nil, errors.New("indexer: empty root")
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("indexer: root is not a directory")
	}

	// Serialize Index() calls to prevent reentrancy bugs.
	idx.runMu.Lock()

	progress := make(chan Progress, 64)
	go func() {
		defer close(progress)
		defer idx.runMu.Unlock()
		send := func(p Progress) {
			select {
			case progress <- p:
			case <-ctx.Done():
			}
		}

		send(Progress{Phase: "scanning"})
		queue, err := idx.scan(ctx, root)
		if err != nil {
			send(Progress{Phase: "error"})
			return
		}
		total := len(queue)
		send(Progress{Phase: "parsing", Done: 0, Total: total})

		newEntries := make(map[string]*Entry, total)

		for i, path := range queue {
			if ctx.Err() != nil {
				return
			}
			entry, err := idx.parseOne(path)
			if err == nil && entry != nil {
				newEntries[path] = entry
			}
			send(Progress{
				Phase:       "parsing",
				Done:        i + 1,
				Total:       total,
				CurrentFile: path,
			})
		}
		idx.mu.Lock()
		idx.entries = newEntries
		idx.mu.Unlock()
		send(Progress{Phase: "done", Done: total, Total: total})
	}()
	return progress, nil
}

// scan walks the workspace once to gather every candidate file. The walk is
// performed up front so the Progress reports can carry an accurate Total.
func (idx *Indexer) scan(ctx context.Context, root string) ([]string, error) {
	var queue []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		base := d.Name()
		if d.IsDir() {
			if path == root {
				return nil
			}
			if _, skip := idx.SkipDirs[base]; skip {
				return filepath.SkipDir
			}
			if strings.HasPrefix(base, ".") && base != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if lang.ByPath(path) == nil {
			return nil
		}
		queue = append(queue, path)
		return nil
	})
	return queue, err
}

func (idx *Indexer) parseOne(path string) (*Entry, error) {
	adapter := lang.ByPath(path)
	if adapter == nil {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if idx.MaxFileSize > 0 && info.Size() > idx.MaxFileSize {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	file, err := adapter.Parser.Parse(parser.Source{
		Path:     path,
		Content:  content,
		Language: adapter.Language,
	})
	duration := time.Since(start)
	if idx.reporter != nil {
		idx.reporter.ObserveParse(duration)
	}
	if err != nil {
		return nil, err
	}
	return &Entry{
		Path:     path,
		Language: adapter.Language,
		File:     file,
		Size:     info.Size(),
		IndexAt:  time.Now(),
	}, nil
}

// Lookup returns the Entry for path, or nil when the file has not been
// indexed yet. Safe for concurrent use.
func (idx *Indexer) Lookup(path string) *Entry {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.entries[path]
}

// Snapshot returns a copy of the current indexed entries keyed by absolute
// path. Useful for tests and metrics.
func (idx *Indexer) Snapshot() map[string]*Entry {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make(map[string]*Entry, len(idx.entries))
	for k, v := range idx.entries {
		out[k] = v
	}
	return out
}

// Stats returns the headline numbers reported back to Logos via metrics IPC.
func (idx *Indexer) Stats() Stats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	stats := Stats{Files: len(idx.entries), Languages: map[string]int{}}
	for _, e := range idx.entries {
		stats.Languages[e.Language]++
		stats.Elements += psi.CountElements(e.File)
	}
	return stats
}

// Stats is the value Indexer.Stats returns. Languages maps language id to
// file count; Elements is the total number of PSI nodes across the index.
type Stats struct {
	Files     int
	Elements  int
	Languages map[string]int
}

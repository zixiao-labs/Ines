// Package metrics exposes a process-level snapshot of resource usage. The
// IPC layer publishes these values periodically so Logos can render the CPU
// / memory / generation-time figures the ai-prompt.md mandates.
package metrics

import (
	"runtime"
	"sync"
	"time"
)

// Snapshot bundles the metrics published over IPC.
type Snapshot struct {
	// Uptime since the daemon started.
	Uptime time.Duration
	// HeapAllocBytes is the live heap reported by the Go runtime.
	HeapAllocBytes uint64
	// SysBytes is the total memory obtained from the OS.
	SysBytes uint64
	// NumGoroutine is the number of running goroutines.
	NumGoroutine int
	// NumGC is the cumulative count of finalised GC cycles.
	NumGC uint32
	// CPUSeconds is the cumulative CPU time consumed by the process. The
	// scaffold reports an approximation derived from runtime.MemStats; the
	// follow-up gRPC migration will surface true OS counters.
	CPUSeconds float64
	// AverageParseDuration is the rolling mean of per-file parse durations
	// observed by the indexer.
	AverageParseDuration time.Duration
	// IndexedFiles is the number of files currently held in the index.
	IndexedFiles int
}

// Reporter accumulates per-parse timings and produces Snapshots on demand.
// It is safe for concurrent use.
type Reporter struct {
	mu             sync.Mutex
	startedAt      time.Time
	parseSamples   int
	parseTotalNS   int64
	indexedFiles   int
	cpuSecondsHint float64
}

// NewReporter constructs a Reporter and stamps its start time.
func NewReporter() *Reporter {
	return &Reporter{startedAt: time.Now()}
}

// ObserveParse records one parse latency sample. The Reporter keeps a
// running mean to keep memory bounded.
func (r *Reporter) ObserveParse(d time.Duration) {
	if r == nil || d < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parseSamples++
	r.parseTotalNS += d.Nanoseconds()
}

// SetIndexedFiles records the latest indexed-file count for the snapshot.
func (r *Reporter) SetIndexedFiles(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexedFiles = n
}

// AddCPUSeconds is exposed so future OS-specific samplers can feed the
// Reporter without having to know its internal layout. The scaffold relies
// on the runtime estimate; production builds will plug in real counters.
func (r *Reporter) AddCPUSeconds(seconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cpuSecondsHint += seconds
}

// Snapshot captures the current process metrics.
func (r *Reporter) Snapshot() Snapshot {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	r.mu.Lock()
	defer r.mu.Unlock()
	avg := time.Duration(0)
	if r.parseSamples > 0 {
		avg = time.Duration(r.parseTotalNS / int64(r.parseSamples))
	}
	return Snapshot{
		Uptime:               time.Since(r.startedAt),
		HeapAllocBytes:       ms.HeapAlloc,
		SysBytes:             ms.Sys,
		NumGoroutine:         runtime.NumGoroutine(),
		NumGC:                ms.NumGC,
		CPUSeconds:           r.cpuSecondsHint,
		AverageParseDuration: avg,
		IndexedFiles:         r.indexedFiles,
	}
}

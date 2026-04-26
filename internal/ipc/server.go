package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/zixiao-labs/ines/internal/buildinfo"
	"github.com/zixiao-labs/ines/internal/index"
	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/metrics"
	"github.com/zixiao-labs/ines/internal/psi"
)

// ProtocolVersion is the wire version the server speaks. Bumped when any
// message in messages.go changes shape.
const ProtocolVersion = "1.0"

// Server owns the daemon-side state: the codec, the indexer, the metrics
// reporter, and the negotiated workspace. It is single-threaded on the read
// loop but answers requests via worker goroutines so a long-running index
// run never blocks an incoming metrics request.
type Server struct {
	codec      *Codec
	indexer    *index.Indexer
	metrics    *metrics.Reporter
	mu         sync.Mutex
	wkspc      string
	indexCtx   context.Context
	cancel     context.CancelFunc
	closed     bool
	runCtx     context.Context
	runCancel  context.CancelFunc
}

// NewServer wires the wire codec to the indexer and metrics reporter.
func NewServer(codec *Codec, indexer *index.Indexer, reporter *metrics.Reporter) *Server {
	return &Server{
		codec:   codec,
		indexer: indexer,
		metrics: reporter,
	}
}

// Run blocks reading frames until the underlying reader closes or ctx is
// cancelled. Errors during request handling are surfaced as TypeError
// frames so Logos can render them inline.
func (s *Server) Run(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.runCtx = runCtx
	s.runCancel = runCancel
	s.mu.Unlock()
	defer runCancel()

	heartbeatCtx, stopHeartbeat := context.WithCancel(runCtx)
	defer stopHeartbeat()
	go s.heartbeatLoop(heartbeatCtx)

	for {
		if runCtx.Err() != nil {
			return runCtx.Err()
		}
		frame, err := s.codec.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		go s.dispatch(runCtx, frame)
	}
}

func (s *Server) dispatch(ctx context.Context, frame *Frame) {
	switch frame.Type {
	case TypeRequest:
		s.handleRequest(ctx, frame)
	case TypeNotification:
		// Notifications are fire-and-forget; the bootstrap protocol does
		// not define any client-to-server notifications yet.
	default:
		_ = s.codec.WriteFrame(&Frame{
			ID:    frame.ID,
			Type:  TypeError,
			Error: &FrameError{Code: 400, Message: "unsupported frame type"},
		})
	}
}

func (s *Server) handleRequest(ctx context.Context, frame *Frame) {
	switch frame.Method {
	case MethodInitialize:
		s.handleInitialize(frame)
	case MethodIndexWorkspace:
		s.handleIndexWorkspace(ctx, frame)
	case MethodIndexLookup:
		s.handleIndexLookup(frame)
	case MethodMetricsSnapshot:
		s.handleMetricsSnapshot(frame)
	case MethodShutdown:
		s.handleShutdown(frame)
	default:
		s.respondError(frame, 404, "unknown method "+frame.Method)
	}
}

func (s *Server) handleInitialize(frame *Frame) {
	var params InitializeParams
	if err := unmarshal(frame.Params, &params); err != nil {
		s.respondError(frame, 400, err.Error())
		return
	}
	s.mu.Lock()
	s.wkspc = params.Workspace
	s.mu.Unlock()

	languages := []string{}
	for _, adapter := range lang.All() {
		languages = append(languages, adapter.Language)
	}
	result := InitializeResult{
		ServerVersion:      buildinfo.Version,
		ProtocolVersion:    ProtocolVersion,
		SupportedLanguages: languages,
	}
	s.respond(frame, result)
	// Push a status notification so Logos can render the splash text while
	// the daemon warms its internals.
	s.notify(NotifInitializeStatus, InitializeStatus{
		Stage:   "ready",
		Message: "Activating enhanced language capabilities",
	})
}

func (s *Server) handleIndexWorkspace(ctx context.Context, frame *Frame) {
	var params IndexWorkspaceParams
	if err := unmarshal(frame.Params, &params); err != nil {
		s.respondError(frame, 400, err.Error())
		return
	}
	workspace := params.Workspace
	if workspace == "" {
		s.mu.Lock()
		workspace = s.wkspc
		s.mu.Unlock()
	}
	if workspace == "" {
		s.respondError(frame, 400, "no workspace negotiated")
		return
	}

	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	indexCtx, cancel := context.WithCancel(ctx)
	s.indexCtx = indexCtx
	s.cancel = cancel
	s.mu.Unlock()

	progressCh, err := s.indexer.Index(indexCtx, workspace)
	if err != nil {
		s.respondError(frame, 500, err.Error())
		return
	}

	go func() {
		// Capture the context for this indexing run to detect stale updates.
		runCtx := indexCtx
		for p := range progressCh {
			s.notify(NotifIndexProgress, IndexProgress{
				Phase:       p.Phase,
				Done:        p.Done,
				Total:       p.Total,
				CurrentFile: p.CurrentFile,
				Fraction:    p.Fraction(),
			})
		}
		// Only update metrics if this context hasn't been cancelled (i.e., we're
		// still the active indexing run).
		if runCtx.Err() == nil {
			stats := s.indexer.Stats()
			s.metrics.SetIndexedFiles(stats.Files)
		}
	}()

	s.respond(frame, map[string]any{"accepted": true, "workspace": workspace})
}

func (s *Server) handleIndexLookup(frame *Frame) {
	var params IndexLookupParams
	if err := unmarshal(frame.Params, &params); err != nil {
		s.respondError(frame, 400, err.Error())
		return
	}
	entry := s.indexer.Lookup(params.Path)
	if entry == nil {
		s.respondError(frame, 404, "file not indexed")
		return
	}
	out := IndexLookupResult{
		Path:     entry.Path,
		Language: entry.Language,
	}
	for _, child := range entry.File.Children() {
		r := child.Range()
		out.Symbols = append(out.Symbols, SymbolOutput{
			Kind:  string(child.Kind()),
			Name:  child.Name(),
			Start: r.Start,
			End:   r.End,
		})
	}
	s.respond(frame, out)
}

func (s *Server) handleMetricsSnapshot(frame *Frame) {
	snap := s.metrics.Snapshot()
	stats := s.indexer.Stats()
	out := MetricsSnapshot{
		UptimeSeconds:      snap.Uptime.Seconds(),
		HeapAllocBytes:     snap.HeapAllocBytes,
		SysBytes:           snap.SysBytes,
		NumGoroutine:       snap.NumGoroutine,
		NumGC:              snap.NumGC,
		CPUSeconds:         snap.CPUSeconds,
		AverageParseMillis: float64(snap.AverageParseDuration) / float64(time.Millisecond),
		IndexedFiles:       stats.Files,
		IndexedElements:    stats.Elements,
		LanguageBreakdown:  stats.Languages,
	}
	s.respond(frame, out)
}

func (s *Server) handleShutdown(frame *Frame) {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.closed = true
	if s.runCancel != nil {
		s.runCancel()
	}
	s.mu.Unlock()
	s.respond(frame, map[string]any{"acknowledged": true})
}

func (s *Server) respond(frame *Frame, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		s.respondError(frame, 500, err.Error())
		return
	}
	_ = s.codec.WriteFrame(&Frame{
		ID:     frame.ID,
		Type:   TypeResponse,
		Method: frame.Method,
		Result: raw,
	})
}

func (s *Server) respondError(frame *Frame, code int, msg string) {
	_ = s.codec.WriteFrame(&Frame{
		ID:     frame.ID,
		Type:   TypeError,
		Method: frame.Method,
		Error:  &FrameError{Code: code, Message: msg},
	})
}

func (s *Server) notify(method string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = s.codec.WriteFrame(&Frame{
		Type:   TypeNotification,
		Method: method,
		Params: raw,
	})
}

func (s *Server) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := s.metrics.Snapshot()
			stats := s.indexer.Stats()
			s.notify(NotifMetricsHeartbeat, MetricsSnapshot{
				UptimeSeconds:      snap.Uptime.Seconds(),
				HeapAllocBytes:     snap.HeapAllocBytes,
				SysBytes:           snap.SysBytes,
				NumGoroutine:       snap.NumGoroutine,
				NumGC:              snap.NumGC,
				CPUSeconds:         snap.CPUSeconds,
				AverageParseMillis: float64(snap.AverageParseDuration) / float64(time.Millisecond),
				IndexedFiles:       stats.Files,
				IndexedElements:    stats.Elements,
				LanguageBreakdown:  stats.Languages,
			})
		}
	}
}

func unmarshal(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

// Ensure psi is imported even if no symbol from it is referenced after
// future refactors. The compiler will otherwise complain when handlers move
// out of this file.
var _ = psi.KindFile

// Package ipc carries the wire protocol Logos and Ines exchange over stdio.
//
// The bootstrap encoding is length-prefixed JSON. Every frame is a 4-byte
// big-endian unsigned length followed by exactly that many bytes of payload.
// JSON keeps the protocol legible while we stabilise the message set; once
// the contract is frozen the encoder will be swapped for protobuf without
// changing the framing or the Go-side handlers.
package ipc

import "encoding/json"

// MessageType discriminates the wire union below. Adding a new message kind
// is intentionally a single edit so the protocol stays auditable.
type MessageType string

const (
	TypeRequest      MessageType = "request"
	TypeResponse     MessageType = "response"
	TypeNotification MessageType = "notification"
	TypeError        MessageType = "error"
)

// Method names live in their own constants so the renderer side can import
// them via a generated TS file later. Keep these in sync with the Logos
// preload bridge.
const (
	MethodInitialize       = "initialize"
	MethodIndexWorkspace   = "index/workspace"
	MethodIndexLookup      = "index/lookup"
	MethodMetricsSnapshot  = "metrics/snapshot"
	MethodShutdown         = "shutdown"
	MethodIDECompletion    = "ide/completion"
	MethodIDEDefinition    = "ide/definition"
	MethodIDEReferences    = "ide/references"
	MethodIDERename        = "ide/rename"
	MethodIDEDiagnostics   = "ide/diagnostics"
	NotifIndexProgress     = "index/progress"
	NotifMetricsHeartbeat  = "metrics/heartbeat"
	NotifInitializeStatus  = "initialize/status"
)

// Frame is the envelope used for every message exchanged with Logos.
type Frame struct {
	ID     int64           `json:"id,omitempty"`
	Type   MessageType     `json:"type"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *FrameError     `json:"error,omitempty"`
}

// FrameError mirrors the JSON-RPC error shape so existing tooling can be
// reused if helpful.
type FrameError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// InitializeParams is the payload sent by Logos right after the daemon
// starts. It carries the workspace root and the protocol version Logos
// expects so the daemon can negotiate features.
type InitializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	Workspace       string `json:"workspace"`
	ClientVersion   string `json:"clientVersion,omitempty"`
}

// InitializeResult is returned synchronously to the initialize request. It
// announces the daemon version and the languages the registry can serve.
type InitializeResult struct {
	ServerVersion      string   `json:"serverVersion"`
	ProtocolVersion    string   `json:"protocolVersion"`
	SupportedLanguages []string `json:"supportedLanguages"`
}

// IndexWorkspaceParams kicks off (or restarts) workspace indexing. Workspace
// is optional and falls back to the workspace negotiated during initialize.
type IndexWorkspaceParams struct {
	Workspace string `json:"workspace,omitempty"`
}

// IndexProgress mirrors index.Progress but uses JSON-friendly types.
type IndexProgress struct {
	Phase       string  `json:"phase"`
	Done        int     `json:"done"`
	Total       int     `json:"total"`
	CurrentFile string  `json:"currentFile,omitempty"`
	Fraction    float64 `json:"fraction"`
}

// IndexLookupParams asks the daemon for the PSI tree of a single file.
type IndexLookupParams struct {
	Path string `json:"path"`
}

// IndexLookupResult is a flat outline of the requested file. Full trees
// would explode the JSON payload, so the bootstrap protocol only surfaces
// the top-level declarations the editor needs for the symbols view.
type IndexLookupResult struct {
	Path     string         `json:"path"`
	Language string         `json:"language"`
	Symbols  []SymbolOutput `json:"symbols"`
}

// SymbolOutput is one row in the outline. Range is the byte range inside the
// source file. Children carry the nested members that M2 surfaces (class
// methods, struct fields, function parameters …); the older renderer
// happily ignores it because the JSON tag is omitempty.
type SymbolOutput struct {
	Kind      string         `json:"kind"`
	Name      string         `json:"name"`
	Start     int            `json:"start"`
	End       int            `json:"end"`
	Detail    string         `json:"detail,omitempty"`
	Signature string         `json:"signature,omitempty"`
	Children  []SymbolOutput `json:"children,omitempty"`
}

// CompletionParams asks the daemon for completion candidates at a position.
// Path is the file the user is editing; Offset is the byte offset of the
// caret. Prefix is the partial identifier the renderer wants to match
// against.
type CompletionParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Prefix string `json:"prefix,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// CompletionItem is one row in the completion popup. Kind matches psi.Kind
// so the renderer can use the same icon set as the outline.
type CompletionItem struct {
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Detail     string `json:"detail,omitempty"`
	InsertText string `json:"insertText,omitempty"`
	Path       string `json:"path,omitempty"`
}

// CompletionResult is the IDE-side response to a completion request.
type CompletionResult struct {
	Items []CompletionItem `json:"items"`
}

// DefinitionParams locates a symbol at the given offset.
type DefinitionParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
}

// Location is a path + half-open byte range in the source. Used by
// definition / references / rename.
type Location struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// DefinitionResult is the response to ide/definition.
type DefinitionResult struct {
	Locations []Location `json:"locations"`
}

// ReferencesParams asks for every reference to the symbol identified at the
// given offset. IncludeDeclaration controls whether the declaration site is
// included in the result list.
type ReferencesParams struct {
	Path               string `json:"path"`
	Offset             int    `json:"offset"`
	IncludeDeclaration bool   `json:"includeDeclaration"`
}

// ReferencesResult is the response to ide/references.
type ReferencesResult struct {
	Locations []Location `json:"locations"`
}

// RenameParams asks for the text edits that rename the symbol at offset
// across the workspace.
type RenameParams struct {
	Path    string `json:"path"`
	Offset  int    `json:"offset"`
	NewName string `json:"newName"`
}

// TextEdit replaces the bytes inside Range (relative to Path) with NewText.
type TextEdit struct {
	Path    string `json:"path"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	NewText string `json:"newText"`
}

// RenameResult is the response to ide/rename. Edits is grouped by file in
// source order so the renderer can apply them top-to-bottom without
// recomputing offsets.
type RenameResult struct {
	OldName string     `json:"oldName"`
	NewName string     `json:"newName"`
	Edits   []TextEdit `json:"edits"`
}

// DiagnosticsParams requests the cached diagnostics for a file. When Path is
// empty every indexed file's diagnostics are returned.
type DiagnosticsParams struct {
	Path string `json:"path,omitempty"`
}

// DiagnosticOutput is the wire shape of treesitter.Diagnostic.
type DiagnosticOutput struct {
	Path     string `json:"path"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

// DiagnosticsResult is the response to ide/diagnostics.
type DiagnosticsResult struct {
	Diagnostics []DiagnosticOutput `json:"diagnostics"`
}

// MetricsSnapshot is the JSON shape of metrics.Snapshot.
type MetricsSnapshot struct {
	UptimeSeconds        float64        `json:"uptimeSeconds"`
	HeapAllocBytes       uint64         `json:"heapAllocBytes"`
	SysBytes             uint64         `json:"sysBytes"`
	NumGoroutine         int            `json:"numGoroutine"`
	NumGC                uint32         `json:"numGc"`
	CPUSeconds           float64        `json:"cpuSeconds"`
	AverageParseMillis   float64        `json:"averageParseMillis"`
	IndexedFiles         int            `json:"indexedFiles"`
	IndexedElements      int            `json:"indexedElements"`
	LanguageBreakdown    map[string]int `json:"languageBreakdown,omitempty"`
}

// InitializeStatus is broadcast as a notification while the daemon is
// performing post-initialize bootstrap (loading grammars, warming caches).
// Logos uses the Message field to render the "Activating enhanced language
// capabilities" splash text.
type InitializeStatus struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

package ipc

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCodecRoundTripsFrames(t *testing.T) {
	var buf bytes.Buffer
	codec := NewCodec(&buf, &buf)

	payload, _ := json.Marshal(map[string]any{"workspace": "/tmp/x"})
	out := &Frame{ID: 7, Type: TypeRequest, Method: MethodInitialize, Params: payload}
	if err := codec.WriteFrame(out); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := codec.ReadFrame()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != out.ID || got.Method != out.Method || got.Type != out.Type {
		t.Fatalf("frame mismatch: got %+v want %+v", got, out)
	}
	if !bytes.Equal(got.Params, payload) {
		t.Fatalf("params mismatch: got %s want %s", got.Params, payload)
	}
}

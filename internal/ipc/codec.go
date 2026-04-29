package ipc

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// MaxFrameBytes caps the size of a single decoded frame to keep a
// misbehaving client from forcing arbitrarily large allocations.
const MaxFrameBytes = 32 << 20

// Codec encodes and decodes Frames over an io.ReadWriter. Reads are buffered
// internally; writes are synchronised so multiple goroutines can push frames
// without interleaving bytes on the wire.
type Codec struct {
	reader *bufio.Reader
	writer io.Writer
	wmu    sync.Mutex
	closer io.Closer
}

// NewCodec wires a Codec onto the provided streams. Pass os.Stdin/os.Stdout
// when running as a child process of Logos.
func NewCodec(r io.Reader, w io.Writer) *Codec {
	closer, _ := r.(io.Closer)
	return &Codec{reader: bufio.NewReaderSize(r, 64<<10), writer: w, closer: closer}
}

// ReadFrame blocks until a complete frame is available. It returns io.EOF
// when the underlying reader closes.
func (c *Codec) ReadFrame() (*Frame, error) {
	for {
		var lengthBuf [4]byte
		if _, err := io.ReadFull(c.reader, lengthBuf[:]); err != nil {
			return nil, err
		}
		length := binary.BigEndian.Uint32(lengthBuf[:])
		// Zero-length frames are treated as keepalive/ping markers for future
		// protocol extensions; skip them and continue reading.
		if length == 0 {
			continue
		}
		if length > MaxFrameBytes {
			return nil, fmt.Errorf("ipc: frame %d bytes exceeds limit %d", length, MaxFrameBytes)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.reader, payload); err != nil {
			return nil, err
		}
		frame := &Frame{}
		if err := json.Unmarshal(payload, frame); err != nil {
			return nil, fmt.Errorf("ipc: malformed frame: %w", err)
		}
		return frame, nil
	}
}

// WriteFrame serialises frame and pushes it to the writer atomically.
func (c *Codec) WriteFrame(frame *Frame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(payload) > MaxFrameBytes {
		return fmt.Errorf("ipc: outbound frame %d bytes exceeds limit %d", len(payload), MaxFrameBytes)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	var lengthBuf [4]byte
	binary.BigEndian.PutUint32(lengthBuf[:], uint32(len(payload)))
	if _, err := c.writer.Write(lengthBuf[:]); err != nil {
		return err
	}
	if _, err := c.writer.Write(payload); err != nil {
		return err
	}
	if flusher, ok := c.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

// Close closes the underlying reader if it implements io.Closer. This unblocks
// any pending ReadFrame calls, allowing graceful shutdown.
func (c *Codec) Close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}

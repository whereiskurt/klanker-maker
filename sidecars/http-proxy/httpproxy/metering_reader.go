package httpproxy

import (
	"bytes"
	"io"
	"sync"
)

// meteringReader wraps an io.ReadCloser, capturing all bytes read into a buffer.
// When the stream ends (Read returns EOF) or Close is called, the onComplete
// callback fires exactly once in a goroutine with the captured data.
//
// This enables non-blocking token extraction from streaming responses:
// data flows through to the client immediately while being tee'd into the buffer.
// After the last byte, the callback parses tokens and fires DynamoDB metering.
type meteringReader struct {
	inner      io.ReadCloser
	buf        bytes.Buffer
	onComplete func(captured []byte)
	once       sync.Once
}

// newMeteringReader wraps r so that all Read data is captured. When the stream
// finishes (EOF or Close), onComplete is called once with the full captured bytes.
func newMeteringReader(r io.ReadCloser, onComplete func(captured []byte)) io.ReadCloser {
	return &meteringReader{
		inner:      r,
		onComplete: onComplete,
	}
}

func (m *meteringReader) Read(p []byte) (int, error) {
	n, err := m.inner.Read(p)
	if n > 0 && m.buf.Len() < maxResponseBodySize {
		// Capture up to 10 MB to prevent unbounded growth.
		remaining := maxResponseBodySize - m.buf.Len()
		writeN := n
		if writeN > remaining {
			writeN = remaining
		}
		m.buf.Write(p[:writeN])
	}
	if err == io.EOF {
		m.fireOnce()
	}
	return n, err
}

func (m *meteringReader) Close() error {
	m.fireOnce()
	return m.inner.Close()
}

func (m *meteringReader) fireOnce() {
	m.once.Do(func() {
		captured := m.buf.Bytes()
		if len(captured) > 0 && m.onComplete != nil {
			go m.onComplete(captured)
		}
	})
}

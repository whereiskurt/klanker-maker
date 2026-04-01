//go:build linux

package tls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestConsumerEventParsing(t *testing.T) {
	// Create a TLSEvent with known values.
	original := TLSEvent{
		TimestampNs: 1234567890,
		Pid:         42,
		Tid:         43,
		Fd:          5,
		RemoteIP:    0x0100007f, // 127.0.0.1 in network byte order
		RemotePort:  443,
		Direction:   DirWrite,
		LibraryType: LibOpenSSL,
		PayloadLen:  5,
	}
	copy(original.Payload[:], []byte("hello"))

	// Serialize to bytes (little-endian, matching BPF output).
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, &original); err != nil {
		t.Fatalf("serialize TLSEvent: %v", err)
	}

	// Deserialize back — this is what the consumer does.
	var parsed TLSEvent
	if err := binary.Read(bytes.NewReader(buf.Bytes()), binary.LittleEndian, &parsed); err != nil {
		t.Fatalf("deserialize TLSEvent: %v", err)
	}

	// Verify fields.
	if parsed.TimestampNs != 1234567890 {
		t.Errorf("TimestampNs = %d, want 1234567890", parsed.TimestampNs)
	}
	if parsed.Pid != 42 {
		t.Errorf("Pid = %d, want 42", parsed.Pid)
	}
	if parsed.Tid != 43 {
		t.Errorf("Tid = %d, want 43", parsed.Tid)
	}
	if parsed.Fd != 5 {
		t.Errorf("Fd = %d, want 5", parsed.Fd)
	}
	if parsed.RemotePort != 443 {
		t.Errorf("RemotePort = %d, want 443", parsed.RemotePort)
	}
	if parsed.Direction != DirWrite {
		t.Errorf("Direction = %d, want DirWrite", parsed.Direction)
	}
	if parsed.LibraryType != LibOpenSSL {
		t.Errorf("LibraryType = %d, want LibOpenSSL", parsed.LibraryType)
	}
	if string(parsed.PayloadBytes()) != "hello" {
		t.Errorf("Payload = %q, want %q", string(parsed.PayloadBytes()), "hello")
	}

	// Verify RemoteAddr parsing.
	addr := parsed.RemoteAddr()
	if addr.Addr().String() != "127.0.0.1" {
		t.Errorf("RemoteAddr IP = %s, want 127.0.0.1", addr.Addr().String())
	}
	if addr.Port() != 443 {
		t.Errorf("RemoteAddr Port = %d, want 443", addr.Port())
	}
}

func TestConsumerMultipleHandlers(t *testing.T) {
	event := &TLSEvent{
		Pid:         100,
		Direction:   DirRead,
		LibraryType: LibOpenSSL,
		PayloadLen:  4,
	}
	copy(event.Payload[:], []byte("test"))

	var called1, called2 bool
	h1 := func(e *TLSEvent) error {
		called1 = true
		if e.Pid != 100 {
			t.Errorf("handler1: Pid = %d, want 100", e.Pid)
		}
		return nil
	}
	h2 := func(e *TLSEvent) error {
		called2 = true
		if string(e.PayloadBytes()) != "test" {
			t.Errorf("handler2: payload = %q, want %q", string(e.PayloadBytes()), "test")
		}
		return nil
	}

	// Simulate handler dispatch (same logic as Consumer.dispatchEvent).
	handlers := []EventHandler{h1, h2}
	for _, h := range handlers {
		_ = h(event)
	}

	if !called1 {
		t.Error("handler1 was not called")
	}
	if !called2 {
		t.Error("handler2 was not called")
	}
}

func TestConsumerHandlerErrorsDoNotStop(t *testing.T) {
	event := &TLSEvent{
		Pid:         200,
		LibraryType: LibOpenSSL,
	}

	errHandler := func(e *TLSEvent) error {
		return errors.New("handler failed")
	}

	var okCalled bool
	okHandler := func(e *TLSEvent) error {
		okCalled = true
		return nil
	}

	// Simulate dispatch: error from first handler should not prevent second.
	handlers := []EventHandler{errHandler, okHandler}
	for _, h := range handlers {
		// In the real consumer, errors are logged but don't stop processing.
		_ = h(event)
	}

	if !okCalled {
		t.Error("ok handler was not called after error handler")
	}
}

func TestConsumerEventSize(t *testing.T) {
	// Verify that TLSEvent serializes to the expected size.
	// This ensures the Go struct matches the BPF-side struct layout.
	var buf bytes.Buffer
	event := TLSEvent{}
	if err := binary.Write(&buf, binary.LittleEndian, &event); err != nil {
		t.Fatalf("serialize: %v", err)
	}

	// Expected size:
	// uint64 (8) + uint32 (4) + uint32 (4) + uint32 (4) + uint32 (4) +
	// uint16 (2) + uint8 (1) + uint8 (1) + uint32 (4) + [16384]byte
	// = 8 + 4 + 4 + 4 + 4 + 2 + 1 + 1 + 4 + 16384 = 16416
	expected := 16416
	if buf.Len() != expected {
		t.Errorf("TLSEvent serialized size = %d, want %d", buf.Len(), expected)
	}
}

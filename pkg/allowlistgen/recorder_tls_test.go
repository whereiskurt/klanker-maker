//go:build linux

package allowlistgen_test

import (
	"fmt"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/allowlistgen"
	"github.com/whereiskurt/klankrmkr/pkg/ebpf/tls"
)

func makeTLSEvent(direction uint8, payload string) *tls.TLSEvent {
	e := &tls.TLSEvent{
		Direction: direction,
	}
	n := copy(e.Payload[:], []byte(payload))
	e.PayloadLen = uint32(n)
	return e
}

func TestRecorderTLS(t *testing.T) {
	r := allowlistgen.NewRecorder()
	payload := "GET /repos/octocat/hello-world/issues HTTP/1.1\r\nHost: api.github.com\r\nUser-Agent: test\r\n\r\n"
	ev := makeTLSEvent(tls.DirWrite, payload)
	if err := r.HandleTLSEvent(ev); err != nil {
		t.Fatalf("HandleTLSEvent returned error: %v", err)
	}
	hosts := r.Hosts()
	if len(hosts) != 1 || hosts[0] != "api.github.com" {
		t.Errorf("expected host api.github.com, got %v", hosts)
	}
	repos := r.Repos()
	if len(repos) != 1 || repos[0] != "octocat/hello-world" {
		t.Errorf("expected repo octocat/hello-world, got %v", repos)
	}
}

func TestRecorderTLS_ReadDirection(t *testing.T) {
	r := allowlistgen.NewRecorder()
	payload := "GET /repos/octocat/hello-world/issues HTTP/1.1\r\nHost: api.github.com\r\n\r\n"
	ev := makeTLSEvent(tls.DirRead, payload)
	if err := r.HandleTLSEvent(ev); err != nil {
		t.Fatalf("HandleTLSEvent returned error: %v", err)
	}
	// DirRead should be ignored
	if len(r.Hosts()) != 0 {
		t.Errorf("expected no hosts for DirRead, got %v", r.Hosts())
	}
}

func TestRecorderTLS_NonHTTP(t *testing.T) {
	r := allowlistgen.NewRecorder()
	// binary payload that is not HTTP
	payload := string([]byte{0x16, 0x03, 0x01, 0x00, 0x01, 0x00})
	ev := makeTLSEvent(tls.DirWrite, payload)
	if err := r.HandleTLSEvent(ev); err != nil {
		t.Fatalf("HandleTLSEvent returned error: %v", err)
	}
	// Should silently skip
	if len(r.Hosts()) != 0 {
		t.Errorf("expected no hosts for non-HTTP payload, got %v", r.Hosts())
	}
}

// Verify HandleTLSEvent satisfies the tls.EventHandler type.
func TestHandlerSignature(t *testing.T) {
	r := allowlistgen.NewRecorder()
	var _ tls.EventHandler = r.HandleTLSEvent
	_ = fmt.Sprintf("ok") // suppress unused import
}

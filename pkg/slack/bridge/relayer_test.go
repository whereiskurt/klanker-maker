package bridge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// capturedRequest holds what a test server received for inspection.
type capturedRequest struct {
	body    string
	headers http.Header
}

// newCaptureServer starts an httptest.Server that records the first request
// and returns HTTP 200. Close the server when done.
func newCaptureServer(t *testing.T) (*httptest.Server, *capturedRequest) {
	t.Helper()
	var mu sync.Mutex
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		captured.body = string(body)
		captured.headers = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return srv, captured
}

// TestPeerRelayer_PreservesHeaders verifies that Broadcast POSTs the verbatim
// body, forwards X-Slack-Signature + X-Slack-Request-Timestamp, sets
// X-KM-Relayed: 1, and Content-Type: application/json.
func TestPeerRelayer_PreservesHeaders(t *testing.T) {
	srv, captured := newCaptureServer(t)
	defer srv.Close()

	relayer := &HTTPPeerRelayer{
		PeerURLs:   []string{srv.URL},
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	body := `{"type":"event_callback","event":{"type":"message","text":"hello"}}`
	slackHeaders := map[string]string{
		"x-slack-signature":         "v0=abc123",
		"x-slack-request-timestamp": "1680000000",
		"content-type":              "application/json", // should NOT be forwarded directly — we set it ourselves
	}

	if err := relayer.Broadcast(t.Context(), body, slackHeaders); err != nil {
		t.Fatalf("Broadcast returned unexpected error: %v", err)
	}

	if captured.body != body {
		t.Errorf("body mismatch: got %q, want %q", captured.body, body)
	}
	if got := captured.headers.Get("X-Slack-Signature"); got != "v0=abc123" {
		t.Errorf("X-Slack-Signature: got %q, want %q", got, "v0=abc123")
	}
	if got := captured.headers.Get("X-Slack-Request-Timestamp"); got != "1680000000" {
		t.Errorf("X-Slack-Request-Timestamp: got %q, want %q", got, "1680000000")
	}
	if got := captured.headers.Get("X-Km-Relayed"); got != "1" {
		t.Errorf("X-KM-Relayed: got %q, want %q", got, "1")
	}
	if got := captured.headers.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type: got %q, want application/json", got)
	}
}

// TestPeerRelayer_Parallel verifies that Broadcast delivers to ALL peers, not
// just the first one.
func TestPeerRelayer_Parallel(t *testing.T) {
	srv1, cap1 := newCaptureServer(t)
	srv2, cap2 := newCaptureServer(t)
	defer srv1.Close()
	defer srv2.Close()

	relayer := &HTTPPeerRelayer{
		PeerURLs:   []string{srv1.URL, srv2.URL},
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	body := `{"type":"event_callback"}`
	if err := relayer.Broadcast(t.Context(), body, map[string]string{}); err != nil {
		t.Fatalf("Broadcast returned unexpected error: %v", err)
	}

	if cap1.body != body {
		t.Errorf("peer1 body: got %q, want %q", cap1.body, body)
	}
	if cap2.body != body {
		t.Errorf("peer2 body: got %q, want %q", cap2.body, body)
	}
}

// TestPeerRelayer_BoundedTimeout verifies that a slow peer does not block
// Broadcast beyond the ~2.5s bounded context. A sleeping peer returns once
// the context expires; Broadcast must return promptly with a non-nil error.
func TestPeerRelayer_BoundedTimeout(t *testing.T) {
	// Peer that sleeps longer than the 2.5s broadcast timeout.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep 5s — well past the 2.5s broadcast context.
		select {
		case <-r.Context().Done():
			// context cancelled — return early
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer slowSrv.Close()

	relayer := &HTTPPeerRelayer{
		PeerURLs:   []string{slowSrv.URL},
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}

	start := time.Now()
	err := relayer.Broadcast(t.Context(), `{}`, map[string]string{})
	elapsed := time.Since(start)

	// Should complete within 3.5s (2.5s bounded + 1s leeway).
	if elapsed > 3500*time.Millisecond {
		t.Errorf("Broadcast blocked too long: %v (want < 3.5s)", elapsed)
	}
	// Should return a non-nil error (peer failed due to context cancellation).
	if err == nil {
		t.Error("expected non-nil error from slow peer; got nil")
	}
}

// TestPeerRelayer_FailingPeerNonFatal verifies that one 500 peer returns
// a non-nil aggregated error but does not prevent a healthy peer from
// receiving the POST.
func TestPeerRelayer_FailingPeerNonFatal(t *testing.T) {
	var healthyReceived atomic.Bool
	healthySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthyReceived.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer healthySrv.Close()

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	relayer := &HTTPPeerRelayer{
		PeerURLs:   []string{failSrv.URL, healthySrv.URL},
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	err := relayer.Broadcast(t.Context(), `{}`, map[string]string{})
	if err == nil {
		t.Error("expected non-nil error from failing peer; got nil")
	}
	if !healthyReceived.Load() {
		t.Error("healthy peer did not receive the POST despite failing peer error")
	}
}

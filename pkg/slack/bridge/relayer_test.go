package bridge

import (
	"encoding/json"
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

// newClaimedServer returns a server that responds with {"claimed":true}.
func newClaimedServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"claimed":true}`))
	}))
}

// newUnclaimedServer returns a server that responds with {"claimed":false,"channels":[...]}.
func newUnclaimedServer(t *testing.T, channels []SandboxChannelInfo) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := peerRelayResponse{Claimed: false, Channels: channels}
		b, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	}))
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

	results, err := relayer.Broadcast(t.Context(), body, slackHeaders)
	if err != nil {
		t.Fatalf("Broadcast returned unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
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
	results, err := relayer.Broadcast(t.Context(), body, map[string]string{})
	if err != nil {
		t.Fatalf("Broadcast returned unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
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
// the context expires; Broadcast must return promptly. The timed-out peer
// maps to Claimed:true (uncertain ownership = conservative).
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
	results, err := relayer.Broadcast(t.Context(), `{}`, map[string]string{})
	elapsed := time.Since(start)

	// Should complete within 3.5s (2.5s bounded + 1s leeway).
	if elapsed > 3500*time.Millisecond {
		t.Errorf("Broadcast blocked too long: %v (want < 3.5s)", elapsed)
	}
	// err may be nil — transport error is captured in the result, not returned.
	_ = err

	// The timed-out/transport-errored peer maps to Claimed:true (conservative).
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Claimed {
		t.Error("timed-out peer: expected Claimed:true (conservative), got false")
	}
}

// TestPeerRelayer_FailingPeerNonFatal verifies that one 500 peer does not prevent
// a healthy peer from receiving the POST. The 500 peer maps to Claimed:true (conservative).
func TestPeerRelayer_FailingPeerNonFatal(t *testing.T) {
	var healthyReceived atomic.Bool
	healthySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		healthyReceived.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"claimed":false}`))
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

	results, _ := relayer.Broadcast(t.Context(), `{}`, map[string]string{})
	if !healthyReceived.Load() {
		t.Error("healthy peer did not receive the POST despite failing peer error")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Find results by URL
	var failResult, healthResult *PeerClaimResult
	for i := range results {
		if results[i].PeerURL == failSrv.URL {
			failResult = &results[i]
		} else {
			healthResult = &results[i]
		}
	}
	if failResult == nil || healthResult == nil {
		t.Fatalf("could not find expected results by URL: %+v", results)
	}

	// HTTP 500 => Claimed:true (conservative rollout safety)
	if !failResult.Claimed {
		t.Error("HTTP 500 peer: expected Claimed:true (conservative), got false")
	}
	// Healthy peer returned {"claimed":false} — that is the parsed result
	if healthResult.Claimed {
		t.Error("healthy peer: expected Claimed:false (as returned by peer), got true")
	}
}

// TestPeerRelayer_GatherClaims verifies the core scatter-gather contract:
// two peers return different claim responses; Broadcast aggregates them into
// []PeerClaimResult with correct Claimed flags and Channels populated.
func TestPeerRelayer_GatherClaims(t *testing.T) {
	channels := []SandboxChannelInfo{
		{ID: "C1", Alias: "orc", Profile: "patch"},
	}
	claimedSrv := newClaimedServer(t)
	defer claimedSrv.Close()

	unclaimedSrv := newUnclaimedServer(t, channels)
	defer unclaimedSrv.Close()

	relayer := &HTTPPeerRelayer{
		PeerURLs:   []string{claimedSrv.URL, unclaimedSrv.URL},
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	results, err := relayer.Broadcast(t.Context(), `{}`, map[string]string{})
	if err != nil {
		t.Fatalf("Broadcast returned unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Find results by URL
	var claimedResult, unclaimedResult *PeerClaimResult
	for i := range results {
		if results[i].PeerURL == claimedSrv.URL {
			claimedResult = &results[i]
		} else {
			unclaimedResult = &results[i]
		}
	}
	if claimedResult == nil || unclaimedResult == nil {
		t.Fatalf("could not match results to servers: %+v", results)
	}

	if !claimedResult.Claimed {
		t.Error("claimed peer: expected Claimed:true, got false")
	}
	if len(claimedResult.Channels) != 0 {
		t.Errorf("claimed peer: expected no channels, got %v", claimedResult.Channels)
	}

	if unclaimedResult.Claimed {
		t.Error("unclaimed peer: expected Claimed:false, got true")
	}
	if len(unclaimedResult.Channels) != 1 {
		t.Fatalf("unclaimed peer: expected 1 channel, got %d", len(unclaimedResult.Channels))
	}
	if unclaimedResult.Channels[0].ID != "C1" || unclaimedResult.Channels[0].Alias != "orc" {
		t.Errorf("unclaimed peer: channel mismatch: got %+v", unclaimedResult.Channels[0])
	}
}

// TestPeerRelayer_LegacyResponseClaimedTrue verifies rollout safety: a peer
// returns the legacy plain "ok" body (200) => that result is Claimed:true,
// Channels empty.
func TestPeerRelayer_LegacyResponseClaimedTrue(t *testing.T) {
	legacySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer legacySrv.Close()

	relayer := &HTTPPeerRelayer{
		PeerURLs:   []string{legacySrv.URL},
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	results, err := relayer.Broadcast(t.Context(), `{}`, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Claimed {
		t.Error("legacy 'ok' response: expected Claimed:true (rollout safety), got false")
	}
	if len(results[0].Channels) != 0 {
		t.Errorf("legacy 'ok' response: expected no channels, got %v", results[0].Channels)
	}
}

// TestPeerRelayer_HTTPErrorClaimedTrue verifies rollout safety: a peer returns
// HTTP 500 => that result is Claimed:true (conservative).
func TestPeerRelayer_HTTPErrorClaimedTrue(t *testing.T) {
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal server error`))
	}))
	defer errSrv.Close()

	relayer := &HTTPPeerRelayer{
		PeerURLs:   []string{errSrv.URL},
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	results, err := relayer.Broadcast(t.Context(), `{}`, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Claimed {
		t.Error("HTTP 500 response: expected Claimed:true (conservative), got false")
	}
}

// TestPeerRelayer_MixedResults verifies 3 peers (json-claimed, legacy-ok,
// json-unclaimed) produce exactly one Claimed:false with channels and two Claimed:true.
func TestPeerRelayer_MixedResults(t *testing.T) {
	channels := []SandboxChannelInfo{
		{ID: "C2", Alias: "wrkr", Profile: "hardened"},
	}

	claimedSrv := newClaimedServer(t)
	defer claimedSrv.Close()

	legacySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	defer legacySrv.Close()

	unclaimedSrv := newUnclaimedServer(t, channels)
	defer unclaimedSrv.Close()

	relayer := &HTTPPeerRelayer{
		PeerURLs:   []string{claimedSrv.URL, legacySrv.URL, unclaimedSrv.URL},
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	results, err := relayer.Broadcast(t.Context(), `{}`, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	var claimedCount, unclaimedCount int
	for _, r := range results {
		if r.Claimed {
			claimedCount++
		} else {
			unclaimedCount++
			if len(r.Channels) != 1 || r.Channels[0].ID != "C2" {
				t.Errorf("unclaimed result: expected channel C2, got %+v", r.Channels)
			}
		}
	}

	if claimedCount != 2 {
		t.Errorf("expected 2 Claimed:true results, got %d", claimedCount)
	}
	if unclaimedCount != 1 {
		t.Errorf("expected 1 Claimed:false result, got %d", unclaimedCount)
	}
}

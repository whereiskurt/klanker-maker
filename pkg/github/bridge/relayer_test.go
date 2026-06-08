package bridge_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// HTTPPeerRelayer tests (Phase 100 — GH-FED-RELAY / GH-FED-VERIFY)
//
// These exercise the fire-and-forget (Phase-95-era) GitHub peer relayer:
// verbatim body + forwarded GitHub headers + X-KM-Relayed:1, parallel POSTs,
// bounded context, failing-peer tolerance, empty no-op, and HMAC re-verify.
// There is NO claim machinery (Broadcast returns a plain error) — that is the
// deferred Phase 101 orphan reply.
// ============================================================

const testRelayBody = `{"action":"created","issue":{"number":7}}`

func ghHeaders(sig, event, delivery string) map[string]string {
	return map[string]string{
		"x-hub-signature-256": sig,
		"x-github-event":      event,
		"x-github-delivery":   delivery,
	}
}

// TestHTTPPeerRelayer_Broadcast_ForwardsHeaders asserts each peer receives the
// verbatim body, the three forwarded GitHub headers, X-KM-Relayed:1, and
// Content-Type: application/json.
func TestHTTPPeerRelayer_Broadcast_ForwardsHeaders(t *testing.T) {
	const (
		wantSig      = "sha256=deadbeef"
		wantEvent    = "issue_comment"
		wantDelivery = "guid-abc-123"
	)

	type capture struct {
		body        string
		sig         string
		event       string
		delivery    string
		relayed     string
		contentType string
		hit         bool
	}

	var mu sync.Mutex
	caps := make(map[string]*capture)

	newServer := func(name string) *httptest.Server {
		caps[name] = &capture{}
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			b, _ := io.ReadAll(req.Body)
			mu.Lock()
			c := caps[name]
			c.body = string(b)
			c.sig = req.Header.Get("X-Hub-Signature-256")
			c.event = req.Header.Get("X-GitHub-Event")
			c.delivery = req.Header.Get("X-GitHub-Delivery")
			c.relayed = req.Header.Get("X-KM-Relayed")
			c.contentType = req.Header.Get("Content-Type")
			c.hit = true
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
	}

	s1 := newServer("a")
	s2 := newServer("b")
	defer s1.Close()
	defer s2.Close()

	r := &bridge.HTTPPeerRelayer{PeerURLs: []string{s1.URL, s2.URL}}
	if err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders(wantSig, wantEvent, wantDelivery)); err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}

	for name, c := range caps {
		if !c.hit {
			t.Errorf("peer %q never received POST", name)
			continue
		}
		if c.body != testRelayBody {
			t.Errorf("peer %q body = %q, want verbatim %q", name, c.body, testRelayBody)
		}
		if c.sig != wantSig {
			t.Errorf("peer %q X-Hub-Signature-256 = %q, want %q", name, c.sig, wantSig)
		}
		if c.event != wantEvent {
			t.Errorf("peer %q X-GitHub-Event = %q, want %q", name, c.event, wantEvent)
		}
		if c.delivery != wantDelivery {
			t.Errorf("peer %q X-GitHub-Delivery = %q, want %q", name, c.delivery, wantDelivery)
		}
		if c.relayed != "1" {
			t.Errorf("peer %q X-KM-Relayed = %q, want \"1\"", name, c.relayed)
		}
		if c.contentType != "application/json" {
			t.Errorf("peer %q Content-Type = %q, want application/json", name, c.contentType)
		}
	}
}

// TestHTTPPeerRelayer_Broadcast_AllPeers asserts every peer in a larger fanout
// receives the POST (parallel broadcast).
func TestHTTPPeerRelayer_Broadcast_AllPeers(t *testing.T) {
	const n = 5
	var hits int64
	servers := make([]*httptest.Server, 0, n)
	urls := make([]string, 0, n)
	for i := 0; i < n; i++ {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			atomic.AddInt64(&hits, 1)
			w.WriteHeader(http.StatusOK)
		}))
		servers = append(servers, s)
		urls = append(urls, s.URL)
	}
	defer func() {
		for _, s := range servers {
			s.Close()
		}
	}()

	r := &bridge.HTTPPeerRelayer{PeerURLs: urls}
	if err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1")); err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}

	if got := atomic.LoadInt64(&hits); got != n {
		t.Errorf("got %d peer hits, want %d", got, n)
	}
}

// TestHTTPPeerRelayer_Broadcast_FailingPeerNonFatal asserts that a 500 peer and
// an unreachable peer do not fail Broadcast, and the healthy peer is still hit.
func TestHTTPPeerRelayer_Broadcast_FailingPeerNonFatal(t *testing.T) {
	var healthyHit int64
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt64(&healthyHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()

	// Unreachable URL — synthetic on.aws-style Function URL that will not connect.
	unreachable := "https://abc123def456.lambda-url.us-east-1.on.aws/"

	r := &bridge.HTTPPeerRelayer{
		PeerURLs:   []string{healthy.URL, failing.URL, unreachable},
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}
	if err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1")); err != nil {
		t.Fatalf("Broadcast returned err on failing peers (must be non-fatal): %v", err)
	}

	if got := atomic.LoadInt64(&healthyHit); got != 1 {
		t.Errorf("healthy peer hit %d times, want 1 (failing peers must not block it)", got)
	}
}

// TestHTTPPeerRelayer_Broadcast_BoundedContext asserts a slow peer does not hang
// Broadcast beyond the bounded relay timeout.
func TestHTTPPeerRelayer_Broadcast_BoundedContext(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Sleep well beyond relayBroadcastTimeout (~5s). The bounded context must
		// cancel the in-flight request long before this completes.
		select {
		case <-req.Context().Done():
		case <-time.After(30 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()

	r := &bridge.HTTPPeerRelayer{
		PeerURLs:   []string{slow.URL},
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}

	start := time.Now()
	if err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1")); err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}
	elapsed := time.Since(start)

	// Allow a generous ceiling above the ~5s bounded timeout, but well under the
	// 30s server sleep — proving the bound (not the server) ended the request.
	if elapsed > 8*time.Second {
		t.Errorf("Broadcast took %v, want bounded well under server's 30s sleep (~5s timeout)", elapsed)
	}
}

// TestHTTPPeerRelayer_Broadcast_EmptyPeers_NoOp asserts an empty PeerURLs slice
// returns nil with no panic (dormancy).
func TestHTTPPeerRelayer_Broadcast_EmptyPeers_NoOp(t *testing.T) {
	r := &bridge.HTTPPeerRelayer{PeerURLs: nil}
	if err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1")); err != nil {
		t.Fatalf("empty Broadcast returned err: %v", err)
	}

	r2 := &bridge.HTTPPeerRelayer{PeerURLs: []string{}}
	if err := r2.Broadcast(context.Background(), []byte(testRelayBody), nil); err != nil {
		t.Fatalf("empty Broadcast (nil headers) returned err: %v", err)
	}
}

// TestHTTPPeerRelayer_RelayedVerify proves GH-FED-VERIFY: the forwarded
// X-Hub-Signature-256 over the verbatim body re-verifies against the shared
// secret via VerifyGitHubSignature on the receiving peer.
func TestHTTPPeerRelayer_RelayedVerify(t *testing.T) {
	const secret = "shared-app-webhook-secret"
	body := []byte(testRelayBody)

	// Compute the real GitHub signature the front door would forward.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	var verifyErr error
	var verified int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		received, _ := io.ReadAll(req.Body)
		fwdSig := req.Header.Get("X-Hub-Signature-256")
		// Peer re-verifies the forwarded signature over the verbatim body.
		verifyErr = bridge.VerifyGitHubSignature(secret, fwdSig, received)
		atomic.AddInt64(&verified, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	r := &bridge.HTTPPeerRelayer{PeerURLs: []string{peer.URL}}
	if err := r.Broadcast(context.Background(), body, ghHeaders(sig, "issue_comment", "guid-xyz")); err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}

	if atomic.LoadInt64(&verified) != 1 {
		t.Fatalf("peer did not receive the relayed request")
	}
	if verifyErr != nil {
		t.Errorf("peer re-verify failed: %v (verbatim forward must preserve the HMAC)", verifyErr)
	}
}

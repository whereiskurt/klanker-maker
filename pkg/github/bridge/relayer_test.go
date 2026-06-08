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
//
// Phase 101: Broadcast is now claim-aware — returns ([]PeerClaimResult, error).
// The Phase-100 tests are updated to the new 2-value return but their assertions
// are unchanged (they do not inspect claim results, only side-effects).
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
	if _, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders(wantSig, wantEvent, wantDelivery)); err != nil {
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
	if _, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1")); err != nil {
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
	if _, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1")); err != nil {
		t.Fatalf("Broadcast returned err on failing peers (must be non-fatal): %v", err)
	}

	if got := atomic.LoadInt64(&healthyHit); got != 1 {
		t.Errorf("healthy peer hit %d times, want 1 (failing peers must not block it)", got)
	}
}

// TestHTTPPeerRelayer_Broadcast_BoundedContext asserts a slow peer does not hang
// Broadcast beyond the bounded relay timeout.
func TestHTTPPeerRelayer_Broadcast_BoundedContext(t *testing.T) {
	// done is closed when the test finishes so the slow handler can release
	// promptly on httptest.Server.Close() instead of blocking on its timer.
	done := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Block well beyond relayBroadcastTimeout (~5s). The bounded context must
		// cancel the in-flight client request long before this would complete.
		select {
		case <-req.Context().Done(): // client/transport dropped the connection
		case <-done:                 // test teardown
		case <-time.After(30 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer slow.Close()
	defer close(done)

	r := &bridge.HTTPPeerRelayer{
		PeerURLs:   []string{slow.URL},
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}

	start := time.Now()
	if _, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1")); err != nil {
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
	if _, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1")); err != nil {
		t.Fatalf("empty Broadcast returned err: %v", err)
	}

	r2 := &bridge.HTTPPeerRelayer{PeerURLs: []string{}}
	if _, err := r2.Broadcast(context.Background(), []byte(testRelayBody), nil); err != nil {
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
	if _, err := r.Broadcast(context.Background(), body, ghHeaders(sig, "issue_comment", "guid-xyz")); err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}

	if atomic.LoadInt64(&verified) != 1 {
		t.Fatalf("peer did not receive the relayed request")
	}
	if verifyErr != nil {
		t.Errorf("peer re-verify failed: %v (verbatim forward must preserve the HMAC)", verifyErr)
	}
}

// ============================================================
// Phase 101 — Claim-aware scatter-gather tests
//
// These exercise the new ([]PeerClaimResult, error) return type and the
// rollout-safety invariant: any transport error, non-2xx status, OR
// unparseable/legacy body tallies as Claimed:true. Only an explicit
// {"claimed":false} response counts as unclaimed.
// ============================================================

// TestHTTPPeerRelayer_ClaimTally_MixedPeers: 3 peers — two return {"claimed":true},
// one returns {"claimed":false}. Broadcast must return one result per peer with
// matching Claimed booleans.
func TestHTTPPeerRelayer_ClaimTally_MixedPeers(t *testing.T) {
	makeServer := func(body string, status int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
	}

	claimedTrue1 := makeServer(`{"claimed":true}`, http.StatusOK)
	claimedFalse := makeServer(`{"claimed":false}`, http.StatusOK)
	claimedTrue2 := makeServer(`{"claimed":true}`, http.StatusOK)
	defer claimedTrue1.Close()
	defer claimedFalse.Close()
	defer claimedTrue2.Close()

	r := &bridge.HTTPPeerRelayer{
		PeerURLs: []string{claimedTrue1.URL, claimedFalse.URL, claimedTrue2.URL},
	}
	results, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g1"))
	if err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// There must be at least one Claimed:true and exactly one Claimed:false.
	var trueCount, falseCount int
	for _, res := range results {
		if res.Claimed {
			trueCount++
		} else {
			falseCount++
		}
	}
	if trueCount < 1 {
		t.Errorf("expected at least one Claimed:true, got %d", trueCount)
	}
	if falseCount != 1 {
		t.Errorf("expected exactly one Claimed:false, got %d", falseCount)
	}
}

// TestHTTPPeerRelayer_RolloutLegacyOk_ClaimedTrue: a peer returns 200 with the
// Phase-100 legacy plain-string `"ok"` body. json.Unmarshal fails → Claimed:true
// (rollout-safe, never a false orphan).
func TestHTTPPeerRelayer_RolloutLegacyOk_ClaimedTrue(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`"ok"`)) // Phase-100 fire-and-forget response body
	}))
	defer legacy.Close()

	r := &bridge.HTTPPeerRelayer{PeerURLs: []string{legacy.URL}}
	results, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g2"))
	if err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if !results[0].Claimed {
		t.Errorf("legacy 'ok' body must tally as Claimed:true (rollout safety), got Claimed:false")
	}
}

// TestHTTPPeerRelayer_RolloutNon2xx_ClaimedTrue: a peer returns HTTP 500 ⇒ Claimed:true.
func TestHTTPPeerRelayer_RolloutNon2xx_ClaimedTrue(t *testing.T) {
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"claimed":false}`)) // body is ignored on non-2xx
	}))
	defer errServer.Close()

	r := &bridge.HTTPPeerRelayer{PeerURLs: []string{errServer.URL}}
	results, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g3"))
	if err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if !results[0].Claimed {
		t.Errorf("non-2xx status must tally as Claimed:true (rollout safety), got Claimed:false")
	}
}

// TestHTTPPeerRelayer_RolloutTimeout_ClaimedTrue: a peer sleeps well past the
// relayBroadcastTimeout ⇒ transport/context error ⇒ Claimed:true; Broadcast
// still returns within a reasonable bound.
func TestHTTPPeerRelayer_RolloutTimeout_ClaimedTrue(t *testing.T) {
	done := make(chan struct{})
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		select {
		case <-req.Context().Done():
		case <-done:
		case <-time.After(30 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()
	defer close(done)

	r := &bridge.HTTPPeerRelayer{
		PeerURLs:   []string{slowServer.URL},
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}

	start := time.Now()
	results, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g4"))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}
	if elapsed > 8*time.Second {
		t.Errorf("Broadcast took %v (want bounded to ~5s relayBroadcastTimeout)", elapsed)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if !results[0].Claimed {
		t.Errorf("timeout/transport error must tally as Claimed:true (rollout safety), got Claimed:false")
	}
}

// TestHTTPPeerRelayer_ClaimedFalse_Parsed: a single peer returns {"claimed":false} ⇒
// exactly one result with Claimed:false — proves a true orphan repo is detectable.
func TestHTTPPeerRelayer_ClaimedFalse_Parsed(t *testing.T) {
	unownedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"claimed":false}`))
	}))
	defer unownedServer.Close()

	r := &bridge.HTTPPeerRelayer{PeerURLs: []string{unownedServer.URL}}
	results, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g5"))
	if err != nil {
		t.Fatalf("Broadcast returned err: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Claimed {
		t.Errorf("explicit {\"claimed\":false} must tally as Claimed:false, got Claimed:true")
	}
}

// TestHTTPPeerRelayer_Empty_NilNil: empty PeerURLs ⇒ (nil, nil).
func TestHTTPPeerRelayer_Empty_NilNil(t *testing.T) {
	r := &bridge.HTTPPeerRelayer{PeerURLs: nil}
	results, err := r.Broadcast(context.Background(), []byte(testRelayBody), ghHeaders("sha256=x", "issue_comment", "g6"))
	if err != nil {
		t.Fatalf("empty Broadcast returned err: %v", err)
	}
	if results != nil {
		t.Errorf("empty PeerURLs must return nil results, got %v", results)
	}

	r2 := &bridge.HTTPPeerRelayer{PeerURLs: []string{}}
	results2, err2 := r2.Broadcast(context.Background(), []byte(testRelayBody), nil)
	if err2 != nil {
		t.Fatalf("empty Broadcast (nil headers) returned err: %v", err2)
	}
	if results2 != nil {
		t.Errorf("empty PeerURLs must return nil results, got %v", results2)
	}
}

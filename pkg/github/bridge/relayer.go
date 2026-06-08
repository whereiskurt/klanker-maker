package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// relayBroadcastTimeout bounds the total wall-clock budget for a single
// Broadcast fan-out. GitHub's webhook ack window is ~10s; 5s leaves ample
// headroom for the rest of Handle's post-relay work while still covering a
// slow-but-reachable peer.
const relayBroadcastTimeout = 5 * time.Second

// peerRelayResponse is the JSON body returned by a peer-side relayed-request handler.
// Phase 101: peers now return structured JSON instead of plain "ok".
// Rollout safety: if unmarshal fails (legacy "ok" or non-JSON body), the caller
// treats the result as Claimed:true (conservative — never produce a false orphan).
//
// NO Channels field: the GitHub orphan reply has no repo list to return (unlike
// the Slack Phase-96 analog which lists running sandbox channels).
type peerRelayResponse struct {
	Claimed bool `json:"claimed"`
}

// HTTPPeerRelayer implements PeerRelayer by POSTing the verbatim GitHub webhook
// to each sibling km-install bridge in parallel, bounded by relayBroadcastTimeout.
//
// Phase 100: federated relay engine. A non-relayed Resolve() miss (the front
// door does not own the repo) triggers Broadcast, which fans out the raw webhook
// to all sibling installs so the owning peer can process it locally. The single
// X-KM-Relayed:1 header makes a relayed request TERMINAL at the peer — peers
// process if they own the repo and drop otherwise, NEVER re-relay (single hop).
//
// Phase 101 (claim-aware): Broadcast now returns ([]PeerClaimResult, error) so
// the front door can detect true orphan repos (zero claims across all peers) and
// post a helpful reply. Rollout safety: any legacy "ok" body, non-JSON body, HTTP
// error, or transport error maps to Claimed:true (conservative — never produce a
// false orphan in a mixed-version fleet).
//
// PITFALL 1 (100-RESEARCH.md / Slack Phase 95 PITFALL 4): Broadcast MUST be
// synchronous. AWS Lambda freezes the execution environment the instant Handle
// returns; any in-flight goroutine that has not completed finds its context
// already elapsed on the next thaw. sync.WaitGroup.Wait() is called BEFORE
// returning to guarantee all relay POSTs complete (or time out) within the
// Lambda invocation.
type HTTPPeerRelayer struct {
	PeerURLs   []string
	HTTPClient *http.Client
}

// Compile-time assertion that HTTPPeerRelayer satisfies PeerRelayer.
var _ PeerRelayer = (*HTTPPeerRelayer)(nil)

// Broadcast POSTs rawBody verbatim to all peer bridges in parallel, bounded by a
// relayBroadcastTimeout-derived child context. The following headers are set on
// each outbound request:
//
//   - Content-Type: application/json
//   - X-Hub-Signature-256: ghHeaders["x-hub-signature-256"]
//   - X-GitHub-Event:      ghHeaders["x-github-event"]
//   - X-GitHub-Delivery:   ghHeaders["x-github-delivery"]
//   - X-KM-Relayed: 1  (loop guard — receiving peers treat this as TERMINAL)
//
// The GitHub HMAC signature covers the verbatim body (no timestamp), so
// forwarding the unchanged body + original X-Hub-Signature-256 lets the peer
// re-verify with its copy of the shared App webhook secret (GH-FED-VERIFY).
//
// Each peer's JSON response is parsed into a PeerClaimResult. Rollout safety
// (GH-ORPHAN-ROLLOUT, LOCKED): any legacy "ok" body, non-JSON body, HTTP error,
// or transport error maps to Claimed:true — conservative, never produce a false
// orphan in a mixed-version fleet.
//
// Broadcast is SYNCHRONOUS: it calls wg.Wait() before returning so the Lambda
// runtime is not frozen with in-flight goroutines (see PITFALL 1 above). A
// failing or slow peer is logged and non-fatal — its result is Claimed:true
// (rollout safety). Empty PeerURLs ⇒ (nil, nil).
func (r *HTTPPeerRelayer) Broadcast(ctx context.Context, rawBody []byte, ghHeaders map[string]string) ([]PeerClaimResult, error) {
	if len(r.PeerURLs) == 0 {
		return nil, nil
	}

	// Derive a bounded child context. This is the maximum wall-clock budget for
	// all relay POSTs combined.
	bctx, cancel := context.WithTimeout(ctx, relayBroadcastTimeout)
	defer cancel()

	resultCh := make(chan PeerClaimResult, len(r.PeerURLs))
	var wg sync.WaitGroup

	for _, peerURL := range r.PeerURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			result, err := r.postToPeer(bctx, u, rawBody, ghHeaders)
			if err != nil {
				// Transport/context error — log and conservatively claim (uncertain ownership).
				slog.Warn("km-github-bridge: relay broadcast peer error",
					"peer", u, "err", err, "event", "github_relay_peer_error")
				resultCh <- PeerClaimResult{PeerURL: u, Claimed: true}
				return
			}
			resultCh <- result
		}(peerURL)
	}

	// MUST call Wait() before returning — Lambda freeze (PITFALL 1).
	wg.Wait()
	close(resultCh)

	// Collect all results.
	var out []PeerClaimResult
	for res := range resultCh {
		out = append(out, res)
	}
	return out, nil
}

// postToPeer sends a single relay POST to one peer bridge and returns the
// parsed PeerClaimResult. Rollout safety: any HTTP error, non-JSON body, or
// legacy "ok" response maps to Claimed:true (conservative).
func (r *HTTPPeerRelayer) postToPeer(ctx context.Context, peerURL string, rawBody []byte, ghHeaders map[string]string) (PeerClaimResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, peerURL, bytes.NewReader(rawBody))
	if err != nil {
		return PeerClaimResult{}, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// Forward GitHub auth + routing headers verbatim so the peer can
	// re-verify the HMAC signature with the shared App webhook secret.
	// Lambda Function URL headers are lowercased (lowercaseHeaders in
	// cmd/km-github-bridge/main.go), so read lowercase keys here.
	req.Header.Set("X-Hub-Signature-256", ghHeaders["x-hub-signature-256"])
	req.Header.Set("X-GitHub-Event", ghHeaders["x-github-event"])
	req.Header.Set("X-GitHub-Delivery", ghHeaders["x-github-delivery"])
	// Loop guard (GH-FED-LOOPGUARD): receiving peers treat X-KM-Relayed:1
	// as TERMINAL — they process if they own the repo, drop otherwise, and
	// NEVER re-relay. Single-hop only.
	req.Header.Set("X-KM-Relayed", "1")

	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		// Transport/context error — caller will treat as Claimed:true.
		return PeerClaimResult{}, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	// Phase 101: read the response body to parse the JSON claim result.
	// Rollout safety: legacy "ok", non-JSON, or HTTP error => Claimed:true (conservative).
	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// HTTP error => conservative claim (never produce a false orphan).
		return PeerClaimResult{PeerURL: peerURL, Claimed: true}, nil
	}

	var parsed peerRelayResponse
	if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
		// Legacy "ok" or non-JSON body => conservative claim.
		return PeerClaimResult{PeerURL: peerURL, Claimed: true}, nil
	}

	return PeerClaimResult{PeerURL: peerURL, Claimed: parsed.Claimed}, nil
}

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

// peerRelayResponse is the JSON body returned by a peer-side relayed-request handler.
// Phase 96: peers now return structured JSON instead of plain "ok".
// Rollout safety: if unmarshal fails (legacy "ok" or non-JSON body), the caller
// treats the result as Claimed:true (conservative — never produce a false orphan).
type peerRelayResponse struct {
	Claimed  bool                `json:"claimed"`
	Channels []SandboxChannelInfo `json:"channels,omitempty"`
}

// HTTPPeerRelayer implements PeerRelayer by POSTing the verbatim Slack event
// to each sibling km-install bridge in parallel, bounded by a ~2.5s context.
//
// Phase 95: federated relay engine. A non-relayed FetchByChannel miss triggers
// Broadcast, which fans out the raw Slack event to all sibling installs so the
// owning peer can process it locally.
//
// Phase 96: Broadcast now returns ([]PeerClaimResult, error) for claim-aware
// scatter-gather. Each peer's response is parsed; legacy/error/timeout => Claimed:true
// (rollout safety — mixed-version fleets never produce false orphan replies).
//
// PITFALL (Phase 95 RESEARCH.md Pitfall 4): Broadcast MUST be synchronous.
// AWS Lambda freezes the execution environment when Handle returns. Any
// in-flight goroutine that has not completed will find its context already
// elapsed on the next thaw. sync.WaitGroup.Wait() is called BEFORE returning
// to guarantee all relay POSTs complete (or time out) within the Lambda
// invocation.
type HTTPPeerRelayer struct {
	PeerURLs   []string
	HTTPClient *http.Client
}

// Broadcast POSTs rawBody to all peer bridges in parallel, bounded by a 2.5s
// context derived from the parent ctx. The following headers are set on each
// outbound request:
//
//   - Content-Type: application/json
//   - X-Slack-Signature: slackHeaders["x-slack-signature"]
//   - X-Slack-Request-Timestamp: slackHeaders["x-slack-request-timestamp"]
//   - X-KM-Relayed: 1  (loop guard — receiving peers treat this as TERMINAL)
//
// The Slack HMAC signature covers the (timestamp, body) tuple unchanged, so
// forwarding the verbatim body + original Slack headers lets the peer re-verify
// with the shared signing secret (SLACK-FED-VERIFY).
//
// Each peer's JSON response is parsed into a PeerClaimResult. Rollout safety
// (LOCKED): any legacy "ok" body, non-JSON body, HTTP error, or transport error
// maps to Claimed:true (conservative — never produce a false orphan in a mixed-version fleet).
//
// Broadcast is SYNCHRONOUS: it calls wg.Wait() before returning so the Lambda
// runtime is not frozen with in-flight goroutines.
func (r *HTTPPeerRelayer) Broadcast(ctx context.Context, rawBody string, slackHeaders map[string]string) ([]PeerClaimResult, error) {
	if len(r.PeerURLs) == 0 {
		return nil, nil
	}

	// Derive a bounded child context (~2.5s). This is the maximum wall-clock
	// budget for all relay POSTs. Slack's 3s ack window is the constraint;
	// 2.5s leaves 0.5s for the rest of Handle's post-return work.
	broadcastCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	resultCh := make(chan PeerClaimResult, len(r.PeerURLs))
	var wg sync.WaitGroup

	for _, peerURL := range r.PeerURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			result, err := r.postToPeer(broadcastCtx, u, rawBody, slackHeaders)
			if err != nil {
				// Transport/context error — log and conservatively claim (uncertain ownership).
				slog.Warn("km-slack-bridge: relay broadcast peer error",
					"peer", u, "err", err, "event", "slack_relay_peer_error")
				resultCh <- PeerClaimResult{PeerURL: u, Claimed: true}
				return
			}
			resultCh <- result
		}(peerURL)
	}

	// MUST call Wait() before returning — see PITFALL 4 in RESEARCH.md.
	wg.Wait()
	close(resultCh)

	// Collect all results.
	var results []PeerClaimResult
	for res := range resultCh {
		results = append(results, res)
	}
	return results, nil
}

// postToPeer sends a single relay POST to one peer bridge and returns the
// parsed PeerClaimResult. Rollout safety: any HTTP error, non-JSON body, or
// legacy "ok" response maps to Claimed:true (conservative).
func (r *HTTPPeerRelayer) postToPeer(ctx context.Context, peerURL, rawBody string, slackHeaders map[string]string) (PeerClaimResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, peerURL, bytes.NewReader([]byte(rawBody)))
	if err != nil {
		return PeerClaimResult{}, fmt.Errorf("build request: %w", err)
	}

	// Content-Type: Slack sends JSON bodies.
	req.Header.Set("Content-Type", "application/json")

	// Forward Slack authentication headers so the peer can re-verify the
	// HMAC signature with the shared signing secret.
	if sig := slackHeaders["x-slack-signature"]; sig != "" {
		req.Header.Set("X-Slack-Signature", sig)
	}
	if ts := slackHeaders["x-slack-request-timestamp"]; ts != "" {
		req.Header.Set("X-Slack-Request-Timestamp", ts)
	}

	// Loop guard (SLACK-FED-LOOP): receiving peers treat X-KM-Relayed: 1 as
	// a TERMINAL signal — they process if they own the channel, drop otherwise.
	// They NEVER re-relay. This makes loops structurally impossible.
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

	// Phase 96: read the response body to parse the JSON claim result.
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

	return PeerClaimResult{
		PeerURL:  peerURL,
		Claimed:  parsed.Claimed,
		Channels: parsed.Channels,
	}, nil
}

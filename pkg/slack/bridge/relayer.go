package bridge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPPeerRelayer implements PeerRelayer by POSTing the verbatim Slack event
// to each sibling km-install bridge in parallel, bounded by a ~2.5s context.
//
// Phase 95: federated relay engine. A non-relayed FetchByChannel miss triggers
// Broadcast, which fans out the raw Slack event to all sibling installs so the
// owning peer can process it locally.
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
// A failing peer (non-2xx response or transport error) is logged at Warn and
// contributes an error to the aggregated return value. The caller logs Warn and
// returns 200 regardless — a partial broadcast is better than dropping the
// event entirely.
//
// Broadcast is SYNCHRONOUS: it calls wg.Wait() before returning so the Lambda
// runtime is not frozen with in-flight goroutines.
func (r *HTTPPeerRelayer) Broadcast(ctx context.Context, rawBody string, slackHeaders map[string]string) error {
	if len(r.PeerURLs) == 0 {
		return nil
	}

	// Derive a bounded child context (~2.5s). This is the maximum wall-clock
	// budget for all relay POSTs. Slack's 3s ack window is the constraint;
	// 2.5s leaves 0.5s for the rest of Handle's post-return work.
	broadcastCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()

	type peerErr struct {
		url string
		err error
	}

	errCh := make(chan peerErr, len(r.PeerURLs))
	var wg sync.WaitGroup

	for _, peerURL := range r.PeerURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			if err := r.postToPeer(broadcastCtx, u, rawBody, slackHeaders); err != nil {
				slog.Warn("km-slack-bridge: relay broadcast peer error",
					"peer", u, "err", err, "event", "slack_relay_peer_error")
				errCh <- peerErr{url: u, err: err}
			}
		}(peerURL)
	}

	// MUST call Wait() before returning — see PITFALL 4 in RESEARCH.md.
	wg.Wait()
	close(errCh)

	// Collect errors and return an aggregated summary.
	var errs []string
	for pe := range errCh {
		errs = append(errs, fmt.Sprintf("%s: %v", pe.url, pe.err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("relay broadcast failed for %d peer(s): %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// postToPeer sends a single relay POST to one peer bridge.
func (r *HTTPPeerRelayer) postToPeer(ctx context.Context, peerURL, rawBody string, slackHeaders map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, peerURL, bytes.NewReader([]byte(rawBody)))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
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
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	// Discard response body — we only care about the status code.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("non-2xx response: %d", resp.StatusCode)
	}
	return nil
}

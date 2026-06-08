package bridge

import (
	"bytes"
	"context"
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

// HTTPPeerRelayer implements PeerRelayer by POSTing the verbatim GitHub webhook
// to each sibling km-install bridge in parallel, bounded by relayBroadcastTimeout.
//
// Phase 100: federated relay engine. A non-relayed Resolve() miss (the front
// door does not own the repo) triggers Broadcast, which fans out the raw webhook
// to all sibling installs so the owning peer can process it locally. The single
// X-KM-Relayed:1 header makes a relayed request TERMINAL at the peer — peers
// process if they own the repo and drop otherwise, NEVER re-relay (single hop).
//
// This is the Phase-95-era fire-and-forget SHAPE, deliberately simpler than the
// current Slack relayer (pkg/slack/bridge/relayer.go). It does NOT carry the
// Phase-96 claim machinery (no claim-result type, no peer-response struct, no
// response-body parsing): Broadcast returns a plain error. The orphan-repo reply
// that needs claim-aware scatter-gather is deferred to Phase 101.
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
// Broadcast is SYNCHRONOUS: it calls wg.Wait() before returning so the Lambda
// runtime is not frozen with in-flight goroutines (see PITFALL 1 above). A
// failing or slow peer is logged and non-fatal — Broadcast always returns nil
// on a non-empty fan-out, and an empty PeerURLs slice is a no-op returning nil.
func (r *HTTPPeerRelayer) Broadcast(ctx context.Context, rawBody []byte, ghHeaders map[string]string) error {
	if len(r.PeerURLs) == 0 {
		return nil
	}

	// Derive a bounded child context. This is the maximum wall-clock budget for
	// all relay POSTs combined.
	bctx, cancel := context.WithTimeout(ctx, relayBroadcastTimeout)
	defer cancel()

	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	var wg sync.WaitGroup
	for _, peerURL := range r.PeerURLs {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(bctx, http.MethodPost, u, bytes.NewReader(rawBody))
			if err != nil {
				slog.Warn("km-github-bridge: relay build request error",
					"peer", u, "err", err, "event", "github_relay_peer_error")
				return
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

			resp, err := client.Do(req)
			if err != nil {
				// Transport/context error — non-fatal. The owning peer (if any)
				// can still be reached via the other parallel POSTs.
				slog.Warn("km-github-bridge: relay peer error",
					"peer", u, "err", err, "event", "github_relay_peer_error")
				return
			}
			resp.Body.Close()
		}(peerURL)
	}

	// MUST call Wait() before returning — Lambda freeze (PITFALL 1).
	wg.Wait()
	return nil
}

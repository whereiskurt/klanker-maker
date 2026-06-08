// webhook_handler_phase100_test.go — Phase 100-03 tests for the federated relay
// reorder + loop-guard decision table + 700-repo scale fix.
//
// Covers:
//   - DecisionTable / Reorder (GH-FED-REORDER): Resolve() runs unconditionally ahead
//     of the thread-lookup + @-mention filter; the 4-row {relayed?, matched?} table is
//     correct; the matched path (incl. Phase 98 thread-bypass) is byte-identical.
//   - LoopGuard (GH-FED-LOOPGUARD): X-KM-Relayed:1 + !matched is a TERMINAL drop that
//     never invokes the relayer and never re-relays.
//   - NoWastedRead (GH-FED-SCALE): federation OFF + Threads configured — an unowned-repo
//     PR comment performs ZERO LookupSandbox DDB read; an owned-repo comment still reads.
package bridge_test

import (
	"context"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// Mock PeerRelayer (records Broadcast calls)
// ============================================================

type mockPeerRelayer struct {
	broadcastCalls int
	lastRawBody    []byte
	lastHeaders    map[string]string
	err            error
}

func (m *mockPeerRelayer) Broadcast(_ context.Context, rawBody []byte, ghHeaders map[string]string) error {
	m.broadcastCalls++
	m.lastRawBody = rawBody
	m.lastHeaders = ghHeaders
	return m.err
}

// Compile-time check: mockPeerRelayer must satisfy bridge.PeerRelayer.
var _ bridge.PeerRelayer = &mockPeerRelayer{}

// relayHandler builds a WebhookHandler wired for the relay/reorder tests. The
// caller supplies the resolver, threads store, relayer, and entries so each row
// of the decision table can be exercised independently.
func relayHandler(
	resolver bridge.SandboxAliasResolver,
	threads bridge.GitHubThreadStore,
	relayer bridge.PeerRelayer,
	entries []bridge.RepoEntry,
	sqsSender *mockSQS,
	reactor *mockReactor,
) *bridge.WebhookHandler {
	return &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       resolver,
		Publisher:      &mockPublisher{},
		SQS:            sqsSender,
		Reactor:        reactor,
		Entries:        entries,
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Threads:        threads,
		Relayer:        relayer,
	}
}

// ownedEntries matches the repo used by defaultOpts() ("myorg/myrepo").
func ownedEntries() []bridge.RepoEntry {
	return []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
}

// peerOwnedEntries deliberately does NOT match "myorg/myrepo" — this install does
// not own the repo, so Resolve() returns matched=false (the front-door miss).
func peerOwnedEntries() []bridge.RepoEntry {
	return []bridge.RepoEntry{
		{Match: "otherorg/otherrepo", Alias: "other-alias", Profile: "other-profile", Allow: []string{"bob"}},
	}
}

// withRelayedHeader marks a request as already relayed (X-KM-Relayed:1).
// Function URL headers are lowercased before Handle() sees them.
func withRelayedHeader(h map[string]string) {
	h["x-km-relayed"] = "1"
}

// ============================================================
// DecisionTable / Reorder (GH-FED-REORDER + GH-FED-LOOPGUARD)
// ============================================================

// TestDecisionTable_RelayLoopGuard exercises all 4 rows of the
// {x-km-relayed, matched} decision table.
func TestDecisionTable_RelayLoopGuard(t *testing.T) {
	tests := []struct {
		name           string
		relayed        bool
		matched        bool // owned repo when true
		wantBroadcast  int
		wantSQS        bool // local dispatch occurred
	}{
		{
			name:          "absent+matched -> process locally, no broadcast",
			relayed:       false,
			matched:       true,
			wantBroadcast: 0,
			wantSQS:       true,
		},
		{
			name:          "absent+unmatched -> broadcast once, no local dispatch",
			relayed:       false,
			matched:       false,
			wantBroadcast: 1,
			wantSQS:       false,
		},
		{
			name:          "present+matched -> process locally, no broadcast",
			relayed:       true,
			matched:       true,
			wantBroadcast: 0,
			wantSQS:       true,
		},
		{
			name:          "present+unmatched -> TERMINAL drop, no broadcast, no dispatch",
			relayed:       true,
			matched:       false,
			wantBroadcast: 0,
			wantSQS:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := buildPayloadJSON(defaultOpts()) // @-mention present, repo myorg/myrepo
			var req bridge.WebhookRequest
			if tc.relayed {
				req = buildRequest(body, withRelayedHeader)
			} else {
				req = buildRequest(body)
			}

			entries := ownedEntries()
			if !tc.matched {
				entries = peerOwnedEntries()
			}

			resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/github-queue"}
			sqsSender := &mockSQS{}
			reactor := &mockReactor{}
			relayer := &mockPeerRelayer{}

			h := relayHandler(resolver, nil, relayer, entries, sqsSender, reactor)

			resp := h.Handle(context.Background(), req)
			if resp.StatusCode != 200 {
				t.Errorf("StatusCode=%d want 200", resp.StatusCode)
			}
			if relayer.broadcastCalls != tc.wantBroadcast {
				t.Errorf("broadcastCalls=%d want %d", relayer.broadcastCalls, tc.wantBroadcast)
			}
			if sqsSender.called != tc.wantSQS {
				t.Errorf("SQS.called=%v want %v", sqsSender.called, tc.wantSQS)
			}
		})
	}
}

// TestReorder_PeerOwnedThreadFollowup_Relays verifies the reorder: a no-@-mention
// KNOWN-THREAD follow-up on a PEER-OWNED repo (matched=false at this install) is
// RELAYED at the front door — it is NOT dropped at the mention filter. The reorder
// moved Resolve() ahead of the thread-lookup + mention, so the miss short-circuits
// to the relay branch before the mention check would otherwise fire.
func TestReorder_PeerOwnedThreadFollowup_Relays(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "thanks, looks good" // NO @-mention
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	// A thread store that WOULD report a known thread — but on a peer-owned repo
	// Resolve() misses first, so LookupSandbox must never be consulted.
	threads := &mockGitHubThreadStore{sandboxID: "sb-known", sessionID: "sess"}
	resolver := &mockResolver{sandboxID: "sb-known", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}
	relayer := &mockPeerRelayer{}

	h := relayHandler(resolver, threads, relayer, peerOwnedEntries(), sqsSender, reactor)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if relayer.broadcastCalls != 1 {
		t.Errorf("broadcastCalls=%d want 1 (peer-owned thread follow-up must relay)", relayer.broadcastCalls)
	}
	if sqsSender.called {
		t.Error("SQS must NOT be called for a peer-owned repo (relay, not local dispatch)")
	}
	// SCALE invariant: no DDB thread read on the unowned path.
	if threads.lookupCalls != 0 {
		t.Errorf("lookupCalls=%d want 0 (resolve-miss short-circuits before thread-lookup)", threads.lookupCalls)
	}
}

// TestReorder_OwnedThreadFollowup_Dispatches verifies the Phase 98 thread-bypass is
// PRESERVED after the reorder: a no-@-mention known-thread follow-up on an OWNED repo
// still dispatches (the matched path runs thread-lookup -> bypass -> dispatch).
func TestReorder_OwnedThreadFollowup_Dispatches(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "thanks, looks good" // NO @-mention
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	threads := &mockGitHubThreadStore{sandboxID: "sb-known", sessionID: "sess"}
	resolver := &mockResolver{sandboxID: "sb-known", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}
	relayer := &mockPeerRelayer{}

	h := relayHandler(resolver, threads, relayer, ownedEntries(), sqsSender, reactor)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if !sqsSender.called {
		t.Error("SQS must be called (Phase 98 thread-bypass preserved on owned repo)")
	}
	if relayer.broadcastCalls != 0 {
		t.Errorf("broadcastCalls=%d want 0 (owned repo dispatches locally)", relayer.broadcastCalls)
	}
	// Owned path MUST consult the thread store (this is the bypass mechanism).
	if threads.lookupCalls == 0 {
		t.Error("lookupCalls=0; owned-path thread-bypass requires LookupSandbox")
	}
}

// ============================================================
// LoopGuard (GH-FED-LOOPGUARD)
// ============================================================

// TestLoopGuard_RelayedMiss_NeverRebroadcasts verifies a request carrying
// X-KM-Relayed:1 that this install does NOT own is dropped terminally — the relayer
// is never invoked, so the single-hop guard holds.
func TestLoopGuard_RelayedMiss_NeverRebroadcasts(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body, withRelayedHeader)

	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}
	relayer := &mockPeerRelayer{}

	// peer-owned repo => matched=false at this install
	h := relayHandler(resolver, nil, relayer, peerOwnedEntries(), sqsSender, reactor)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if relayer.broadcastCalls != 0 {
		t.Errorf("broadcastCalls=%d want 0 (relayed+miss must NEVER re-relay)", relayer.broadcastCalls)
	}
	if sqsSender.called {
		t.Error("SQS must NOT be called on terminal relayed-miss drop")
	}
}

// ============================================================
// NoWastedRead (GH-FED-SCALE, 700-repo fix)
// ============================================================

// TestNoWastedRead_UnownedRepo_ZeroLookup verifies that with federation OFF
// (Relayer nil) and Threads configured, a created PR comment on a repo NOT in
// Entries performs ZERO LookupSandbox DDB read — the resolve-miss short-circuits
// before the thread-lookup (4b).
func TestNoWastedRead_UnownedRepo_ZeroLookup(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	threads := &mockGitHubThreadStore{sandboxID: "sb-x", sessionID: "sess"}
	resolver := &mockResolver{sandboxID: "sb-x", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}

	// Relayer nil (federation off); peer-owned entries => unowned repo.
	h := relayHandler(resolver, threads, nil, peerOwnedEntries(), sqsSender, reactor)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if threads.lookupCalls != 0 {
		t.Errorf("lookupCalls=%d want 0 (unowned repo must NOT read km-github-threads)", threads.lookupCalls)
	}
	if sqsSender.called {
		t.Error("SQS must NOT be called for an unowned repo with federation off (silent drop)")
	}
}

// TestNoWastedRead_OwnedRepo_PerformsLookup verifies the lookup STILL happens on an
// owned repo — the scale fix only suppresses the read on the unowned path.
func TestNoWastedRead_OwnedRepo_PerformsLookup(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	threads := &mockGitHubThreadStore{sandboxID: "sb-x", sessionID: "sess"}
	resolver := &mockResolver{sandboxID: "sb-x", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}

	h := relayHandler(resolver, threads, nil, ownedEntries(), sqsSender, reactor)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if threads.lookupCalls < 1 {
		t.Errorf("lookupCalls=%d want >=1 (owned repo still reads km-github-threads)", threads.lookupCalls)
	}
	if !sqsSender.called {
		t.Error("SQS must be called on the owned-repo @-mention path")
	}
}

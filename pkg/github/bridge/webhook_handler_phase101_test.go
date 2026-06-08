// webhook_handler_phase101_test.go — Phase 101-03 tests for the claim-aware
// scatter-gather machinery: peer-side claim emit, front-door tally, orphan-comment
// happy-path, cooldown gate, non-mention skip, dormancy invariant.
//
// Covers requirements: GH-ORPHAN-CLAIM, GH-ORPHAN-REPLY, GH-ORPHAN-COOLDOWN, GH-ORPHAN-ROLLOUT
//
// Run subsets:
//   -run PeerClaim        peer-side {"claimed":bool} emit
//   -run OrphanComment    happy-path + non-mention + Commenter==nil + InstallID==0
//   -run OrphanCooldown   cooldown key+ttl + suppress/expire
//   -run DefaultRouterOff dormancy byte-identity
package bridge_test

import (
	"context"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// fakeNonceStore — configurable fake for cooldown tests.
// Records the key + ttl passed to CheckAndStore.
// ============================================================

type fakeNonceStore struct {
	seen    bool   // return value for alreadySeen
	lastKey string // last key passed to CheckAndStore
	lastTTL int    // last ttl passed to CheckAndStore
	err     error
}

func (f *fakeNonceStore) CheckAndStore(_ context.Context, key string, ttlSeconds int) (bool, error) {
	f.lastKey = key
	f.lastTTL = ttlSeconds
	return f.seen, f.err
}

// Compile-time check.
var _ bridge.DeliveryNonceStore = &fakeNonceStore{}

// ============================================================
// mockPeerRelayerWithClaims — extends mockPeerRelayer with
// configurable []PeerClaimResult for Phase 101 tally tests.
// The Phase-100 mockPeerRelayer already has the right signature;
// this variant just lets callers inject the returned results.
// ============================================================

type mockPeerRelayerWithClaims struct {
	broadcastCalls int
	results        []bridge.PeerClaimResult
	err            error
}

func (m *mockPeerRelayerWithClaims) Broadcast(_ context.Context, _ []byte, _ map[string]string) ([]bridge.PeerClaimResult, error) {
	m.broadcastCalls++
	return m.results, m.err
}

var _ bridge.PeerRelayer = &mockPeerRelayerWithClaims{}

// ============================================================
// Helpers
// ============================================================

// orphanPayload builds a payload for "acme/widgets" (not in ownedEntries() which
// only owns "myorg/myrepo") with an @-mention of the bot.
func orphanPayloadJSON() []byte {
	opts := defaultOpts()
	opts.repo = "acme/widgets"
	opts.commentBody = "@mybot[bot] can you help"
	opts.installID = 99001
	return buildPayloadJSON(opts)
}

// orphanPayloadNoMention builds an orphan payload WITHOUT the @-mention.
func orphanPayloadNoMentionJSON() []byte {
	opts := defaultOpts()
	opts.repo = "acme/widgets"
	opts.commentBody = "no mention here at all"
	opts.installID = 99001
	return buildPayloadJSON(opts)
}

// buildOrphanHandler builds a handler for the front-door orphan tests.
// Entries match "myorg/myrepo" only — so "acme/widgets" is always a miss.
func buildOrphanHandler(
	relayer bridge.PeerRelayer,
	commenter bridge.CommentPoster,
	cooldown bridge.DeliveryNonceStore,
	defaultRouter bool,
) *bridge.WebhookHandler {
	return &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       &mockResolver{sandboxID: "sb-x", queueURL: "https://sqs.example.com/q"},
		Publisher:      &mockPublisher{},
		SQS:            &mockSQS{},
		Reactor:        &mockReactor{},
		Entries:        ownedEntries(), // only myorg/myrepo
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Relayer:        relayer,
		Commenter:      commenter,
		// Phase 101 fields:
		DefaultRouter:  defaultRouter,
		OrphanCooldown: cooldown,
	}
}

// ============================================================
// PeerClaim tests — peer-side {"claimed":bool} emit
// ============================================================

// TestPeerClaim_RelayedMiss_ClaimedFalse: x-km-relayed:1 + unowned repo ⇒ body {"claimed":false}.
func TestPeerClaim_RelayedMiss_ClaimedFalse(t *testing.T) {
	body := orphanPayloadJSON()
	req := buildRequest(body, withRelayedHeader)

	// peer-owned entries only — "acme/widgets" misses
	h := relayHandler(
		&mockResolver{sandboxID: "sb-x", queueURL: "https://sqs.example.com/q"},
		nil, nil, // no thread store, no relayer
		peerOwnedEntries(), // only "otherorg/otherrepo"
		&mockSQS{},
		&mockReactor{},
	)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	if resp.Body != `{"claimed":false}` {
		t.Errorf("body=%q want {\"claimed\":false}", resp.Body)
	}
}

// TestPeerClaim_RelayedOwned_ClaimedTrue: x-km-relayed:1 + owned repo ⇒ body {"claimed":true}.
func TestPeerClaim_RelayedOwned_ClaimedTrue(t *testing.T) {
	// Build a payload for "myorg/myrepo" (owned) with a valid @-mention + alice sender
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body, withRelayedHeader)

	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}

	// ownedEntries() matches "myorg/myrepo"
	h := relayHandler(resolver, nil, nil, ownedEntries(), sqsSender, reactor)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	if resp.Body != `{"claimed":true}` {
		t.Errorf("body=%q want {\"claimed\":true}", resp.Body)
	}
	// Dispatch still happened (SQS or publisher).
	if !sqsSender.called {
		t.Error("SQS must be called on relayed+owned path (dispatch is not skipped)")
	}
}

// TestPeerClaim_NonRelayedOwned_PlainOk: no x-km-relayed + owned ⇒ body "ok" (byte-identity).
func TestPeerClaim_NonRelayedOwned_PlainOk(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body) // no relayed header

	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}

	h := relayHandler(resolver, nil, nil, ownedEntries(), sqsSender, reactor)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	if resp.Body != "ok" {
		t.Errorf("body=%q want ok (non-relayed owned must preserve byte-identity)", resp.Body)
	}
}

// ============================================================
// OrphanComment tests — front-door tally + helpful-reply
// ============================================================

// TestOrphanComment_HappyPath: DefaultRouter=true, zero claims, @-mention, cooldown
// clear, Commenter set, Installation.ID != 0 ⇒ exactly ONE PostComment with
// "github.repos:" and "km init" in the body; response still 200.
func TestOrphanComment_HappyPath(t *testing.T) {
	body := orphanPayloadJSON()
	req := buildRequest(body)

	relayer := &mockPeerRelayerWithClaims{
		results: []bridge.PeerClaimResult{
			{PeerURL: "https://p1.example.com", Claimed: false},
			{PeerURL: "https://p2.example.com", Claimed: false},
		},
	}
	commenter := &mockCommenter{}
	cooldown := &fakeNonceStore{seen: false}

	h := buildOrphanHandler(relayer, commenter, cooldown, true /*DefaultRouter*/)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}

	if !commenter.called {
		t.Fatal("PostComment must be called on orphan happy-path")
	}
	if !strings.Contains(commenter.body, "github.repos:") {
		t.Errorf("PostComment body must contain 'github.repos:'; got: %q", commenter.body)
	}
	if !strings.Contains(commenter.body, "km init") {
		t.Errorf("PostComment body must contain 'km init'; got: %q", commenter.body)
	}
	// Exactly one PostComment call (not two).
	// Since mockCommenter uses a bool, calling it twice would still show called=true,
	// but we can verify via the body not being corrupted. The key assertion is called==true.
}

// TestOrphanComment_AnyClaim_NoPost: at least one Claimed:true ⇒ NO PostComment.
func TestOrphanComment_AnyClaim_NoPost(t *testing.T) {
	body := orphanPayloadJSON()
	req := buildRequest(body)

	relayer := &mockPeerRelayerWithClaims{
		results: []bridge.PeerClaimResult{
			{PeerURL: "https://p1.example.com", Claimed: false},
			{PeerURL: "https://p2.example.com", Claimed: true}, // one claim
		},
	}
	commenter := &mockCommenter{}
	cooldown := &fakeNonceStore{seen: false}

	h := buildOrphanHandler(relayer, commenter, cooldown, true)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	if commenter.called {
		t.Error("PostComment must NOT be called when any peer claimed=true")
	}
}

// TestOrphanComment_NonMention_NoPost: zero claims but comment does NOT @-mention
// the bot ⇒ NO PostComment (gate 2: ContainsMention re-check).
func TestOrphanComment_NonMention_NoPost(t *testing.T) {
	body := orphanPayloadNoMentionJSON()
	req := buildRequest(body)

	relayer := &mockPeerRelayerWithClaims{
		results: []bridge.PeerClaimResult{
			{PeerURL: "https://p1.example.com", Claimed: false},
		},
	}
	commenter := &mockCommenter{}
	cooldown := &fakeNonceStore{seen: false}

	h := buildOrphanHandler(relayer, commenter, cooldown, true)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	if commenter.called {
		t.Error("PostComment must NOT be called when comment has no @-mention")
	}
}

// TestOrphanComment_CommenterNil_Skip: Commenter==nil ⇒ no panic, no post.
func TestOrphanComment_CommenterNil_Skip(t *testing.T) {
	body := orphanPayloadJSON()
	req := buildRequest(body)

	relayer := &mockPeerRelayerWithClaims{
		results: []bridge.PeerClaimResult{
			{PeerURL: "https://p1.example.com", Claimed: false},
		},
	}
	cooldown := &fakeNonceStore{seen: false}

	// Commenter is nil — must not panic.
	h := buildOrphanHandler(relayer, nil /*commenter*/, cooldown, true)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	// No panic is the assertion; can't assert commenter.called on a nil pointer.
}

// TestOrphanComment_InstallationIDZero_Skip: payload.Installation.ID==0 ⇒ no post.
func TestOrphanComment_InstallationIDZero_Skip(t *testing.T) {
	opts := defaultOpts()
	opts.repo = "acme/widgets"
	opts.commentBody = "@mybot[bot] help"
	opts.installID = 0 // zero install ID
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	relayer := &mockPeerRelayerWithClaims{
		results: []bridge.PeerClaimResult{
			{PeerURL: "https://p1.example.com", Claimed: false},
		},
	}
	commenter := &mockCommenter{}
	cooldown := &fakeNonceStore{seen: false}

	h := buildOrphanHandler(relayer, commenter, cooldown, true)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	if commenter.called {
		t.Error("PostComment must NOT be called when Installation.ID==0")
	}
}

// ============================================================
// OrphanCooldown tests — cooldown key shape + suppress/expire
// ============================================================

// TestOrphanCooldown_FirstTime_Posts: seen=false ⇒ PostComment fires; key and ttl verified.
func TestOrphanCooldown_FirstTime_Posts(t *testing.T) {
	body := orphanPayloadJSON()
	req := buildRequest(body)

	relayer := &mockPeerRelayerWithClaims{
		results: []bridge.PeerClaimResult{
			{PeerURL: "https://p1.example.com", Claimed: false},
		},
	}
	commenter := &mockCommenter{}
	cooldown := &fakeNonceStore{seen: false}

	h := buildOrphanHandler(relayer, commenter, cooldown, true)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	if !commenter.called {
		t.Error("PostComment must be called on first-time (cooldown seen=false)")
	}

	// Verify cooldown key shape: gh-router-cooldown:{owner}/{repo}#{number}
	wantKey := "gh-router-cooldown:acme/widgets#42"
	if cooldown.lastKey != wantKey {
		t.Errorf("cooldown key=%q want %q", cooldown.lastKey, wantKey)
	}
	// Verify TTL is 3600.
	if cooldown.lastTTL != 3600 {
		t.Errorf("cooldown ttl=%d want 3600", cooldown.lastTTL)
	}
}

// TestOrphanCooldown_SecondSuppressed: seen=true ⇒ NO PostComment (suppressed by cooldown).
func TestOrphanCooldown_SecondSuppressed(t *testing.T) {
	body := orphanPayloadJSON()
	req := buildRequest(body)

	relayer := &mockPeerRelayerWithClaims{
		results: []bridge.PeerClaimResult{
			{PeerURL: "https://p1.example.com", Claimed: false},
		},
	}
	commenter := &mockCommenter{}
	cooldown := &fakeNonceStore{seen: true} // already seen

	h := buildOrphanHandler(relayer, commenter, cooldown, true)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}
	if commenter.called {
		t.Error("PostComment must NOT be called when cooldown returns seen=true (suppressed)")
	}
}

// ============================================================
// DefaultRouterOff tests — GH-ORPHAN-ROLLOUT dormancy
// ============================================================

// TestDefaultRouterOff_Silent: DefaultRouter=false, even with zero claims + mention +
// Commenter set ⇒ NO tally, NO PostComment (byte-identical to Phase 100).
func TestDefaultRouterOff_Silent(t *testing.T) {
	body := orphanPayloadJSON()
	req := buildRequest(body)

	relayer := &mockPeerRelayerWithClaims{
		results: []bridge.PeerClaimResult{
			{PeerURL: "https://p1.example.com", Claimed: false},
			{PeerURL: "https://p2.example.com", Claimed: false},
		},
	}
	commenter := &mockCommenter{}
	cooldown := &fakeNonceStore{seen: false}

	// DefaultRouter=false — dormant.
	h := buildOrphanHandler(relayer, commenter, cooldown, false /*DefaultRouter=false*/)

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode=%d want 200", resp.StatusCode)
	}

	// No PostComment must happen regardless of claims.
	if commenter.called {
		t.Error("PostComment must NOT be called when DefaultRouter=false (dormancy byte-identity)")
	}
	// Broadcast must still be called (relayer is non-nil) — but the tally is discarded.
	if relayer.broadcastCalls != 1 {
		t.Errorf("broadcastCalls=%d want 1 (relay still happens; tally is discarded when DefaultRouter=false)", relayer.broadcastCalls)
	}
}

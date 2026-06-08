// webhook_handler_phase98_test.go — Phase 98-02 tests for WebhookHandler thread-bypass,
// and Phase 98-06 tests for Gap E (stale/cross-box session invalidation).
//
// Tests: TestHandle_ThreadBypass (GH-X-THREADBYPASS).
// TestHandle_StaleSession_Invalidated, TestHandle_SameBoxSession_NotInvalidated (Gap E).
// 98-04 tests (AutoResume, SandboxResumer, SandboxAliasResolverWithStatus) are in
// webhook_handler_phase98_04_test.go.
package bridge_test

import (
	"context"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// Mock for GitHubThreadStore (defined in interfaces.go by 98-02)
// ============================================================

type mockGitHubThreadStore struct {
	sandboxID string
	sessionID string
	err       error
	upsertErr error
	upserted  bool

	// Gap E tracking
	invalidateCalled    bool
	invalidateSandboxID string // the new sandbox_id passed to InvalidateStaleSession

	// Phase 100 (GH-FED-SCALE) tracking: counts LookupSandbox invocations so the
	// no-wasted-read test can assert ZERO DDB reads on the unowned-repo path.
	lookupCalls int
}

func (m *mockGitHubThreadStore) LookupSandbox(_ context.Context, _ string, _ int) (string, string, error) {
	m.lookupCalls++
	return m.sandboxID, m.sessionID, m.err
}

func (m *mockGitHubThreadStore) Upsert(_ context.Context, _ string, _ int, _ string) error {
	m.upserted = true
	return m.upsertErr
}

func (m *mockGitHubThreadStore) UpdateSession(_ context.Context, _ string, _ int, _ string) error {
	return nil
}

func (m *mockGitHubThreadStore) InvalidateStaleSession(_ context.Context, _ string, _ int, newSandboxID string) error {
	m.invalidateCalled = true
	m.invalidateSandboxID = newSandboxID
	return nil
}

// Compile-time check: mockGitHubThreadStore must satisfy bridge.GitHubThreadStore.
var _ bridge.GitHubThreadStore = &mockGitHubThreadStore{}

// ============================================================
// TestHandle_ThreadBypass (GH-X-THREADBYPASS)
// ============================================================

// TestHandle_ThreadBypass verifies that Handle() bypasses the @-mention check
// when the thread (repo, number) has an existing sandbox in the threads table.
// The comment body has NO @-mention but the thread is known → Handle should
// proceed to dispatch (not 200-drop at the mention step).
func TestHandle_ThreadBypass(t *testing.T) {
	// Build a payload with NO @-mention.
	opts := defaultOpts()
	opts.commentBody = "thanks, that looks good now" // NO @-mention
	opts.userLogin = "alice"                         // allowed user
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	// Thread store returns a known sandbox for (repo, number).
	threads := &mockGitHubThreadStore{
		sandboxID: "sb-known",
		sessionID: "sess-prev",
	}
	resolver := &mockResolver{sandboxID: "sb-known", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}

	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
	h := &bridge.WebhookHandler{
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
		Threads:        threads, // thread-bypass: known thread skips mention check
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Dispatch (SQS enqueue) MUST be called — the thread bypass skipped the mention check.
	if !sqsSender.called {
		t.Error("SQS.Send must be called on thread-bypass path (mention check skipped)")
	}
}

// TestHandle_UnknownThread_NoMention_Drops verifies that an unknown thread with no
// @-mention is still dropped (backward compat with Phase 97 behavior).
func TestHandle_UnknownThread_NoMention_Drops(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "no mention here at all"
	opts.userLogin = "alice"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	// Thread store returns no sandbox (unknown thread).
	threads := &mockGitHubThreadStore{sandboxID: "", sessionID: ""}
	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}

	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
	h := &bridge.WebhookHandler{
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
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// SQS must NOT be called — no mention + unknown thread → drop.
	if sqsSender.called {
		t.Error("SQS.Send must NOT be called when no mention and thread is unknown")
	}
}

// TestHandle_NilThreads_NoMention_Drops verifies nil Threads = Phase 97 behavior.
// No mention → drop regardless. Threads == nil is backward compatible.
func TestHandle_NilThreads_NoMention_Drops(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "no mention here"
	opts.userLogin = "alice"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}

	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
	h := &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       resolver,
		Publisher:      &mockPublisher{},
		SQS:            sqsSender,
		Reactor:        &mockReactor{},
		Entries:        entries,
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Threads:        nil, // nil = Phase 97 behavior (mention always required)
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if sqsSender.called {
		t.Error("SQS.Send must NOT be called when Threads is nil and no mention")
	}
}

// ============================================================
// Gap E tests (98-06 Task 5) — stale/cross-box session invalidation
// ============================================================

// TestHandle_StaleSession_Invalidated verifies that when the km-github-threads row
// holds a sandbox_id from a DIFFERENT (previously destroyed) box, Handle() calls
// InvalidateStaleSession with the current sandbox_id before dispatching.
//
// Gap E (live UAT 2026-06-07): PR #11's thread row pointed at a session from the
// first destroyed box. Every dispatch attempted --resume on the new box → "No
// conversation found" → exit 1 → head-of-line block. The row was only cleared by
// manual intervention. This test ensures the bridge invalidates the row automatically
// when the stored sandbox_id no longer matches the current alias-resolved sandbox.
func TestHandle_StaleSession_Invalidated(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	// Thread store: has a row from the OLD box.
	threads := &mockGitHubThreadStore{
		sandboxID: "sb-OLD-destroyed", // stale sandbox_id from previous box
		sessionID: "sess-from-old-box",
	}
	// Resolver returns a NEW box (sandbox recreated; alias now points to new box).
	resolver := &mockResolverWithStatus{
		sandboxID: "sb-NEW-current",
		status:    "running",
		queueURL:  "https://sqs.example.com/github-queue",
	}
	sqsSender := &mockSQS{}

	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
	h := &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       resolver,
		Publisher:      &mockPublisher{},
		SQS:            sqsSender,
		Reactor:        &mockReactor{},
		Entries:        entries,
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Threads:        threads,
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// InvalidateStaleSession MUST be called because stored_sandbox_id != current.
	if !threads.invalidateCalled {
		t.Error("InvalidateStaleSession must be called when stored sandbox_id differs from current (Gap E fix)")
	}
	// Must be called with the NEW sandbox_id so the row is updated.
	if threads.invalidateSandboxID != "sb-NEW-current" {
		t.Errorf("InvalidateStaleSession called with %q; want sb-NEW-current", threads.invalidateSandboxID)
	}

	// Dispatch must still proceed (warm enqueue) — invalidation does not abort.
	if !sqsSender.called {
		t.Error("SQS.Send must still be called after stale session invalidation")
	}
}

// TestHandle_SameBoxSession_NotInvalidated verifies that when the km-github-threads
// row holds the SAME sandbox_id as the alias-resolved current box, Handle() does NOT
// call InvalidateStaleSession — same-box continuity must not be disturbed.
func TestHandle_SameBoxSession_NotInvalidated(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	// Thread store: sandbox_id matches the current box (normal follow-up scenario).
	threads := &mockGitHubThreadStore{
		sandboxID: "sb-current",
		sessionID: "sess-from-current-box",
	}
	resolver := &mockResolverWithStatus{
		sandboxID: "sb-current", // same as what's in the thread row
		status:    "running",
		queueURL:  "https://sqs.example.com/github-queue",
	}
	sqsSender := &mockSQS{}

	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
	h := &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       resolver,
		Publisher:      &mockPublisher{},
		SQS:            sqsSender,
		Reactor:        &mockReactor{},
		Entries:        entries,
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Threads:        threads,
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// InvalidateStaleSession must NOT be called — same box, valid continuity.
	if threads.invalidateCalled {
		t.Error("InvalidateStaleSession must NOT be called when stored sandbox_id matches current")
	}
	if !sqsSender.called {
		t.Error("SQS.Send must be called on the warm enqueue path")
	}
}

// webhook_handler_phase98_test.go — Phase 98-02 tests for WebhookHandler thread-bypass.
//
// Tests: TestHandle_ThreadBypass (GH-X-THREADBYPASS).
// 98-04 tests (AutoResume, SandboxResumer, SandboxAliasResolverWithStatus) are in
// webhook_handler_phase98_04_test.go behind the phase98_wave3 build tag.
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
}

func (m *mockGitHubThreadStore) LookupSandbox(_ context.Context, _ string, _ int) (string, string, error) {
	return m.sandboxID, m.sessionID, m.err
}

func (m *mockGitHubThreadStore) Upsert(_ context.Context, _ string, _ int, _ string) error {
	m.upserted = true
	return m.upsertErr
}

func (m *mockGitHubThreadStore) UpdateSession(_ context.Context, _ string, _ int, _ string) error {
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

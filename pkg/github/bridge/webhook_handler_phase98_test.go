//go:build phase98_wave0

// webhook_handler_phase98_test.go — Phase 98 RED tests for WebhookHandler extensions.
//
// BUILD TAG: phase98_wave0
// This file extends handle_test.go without touching it. Tests reference fields
// and interfaces not yet implemented (Threads, Resumer, ResolveByAliasWithStatus).
// These will fail to compile until 98-02 (Threads field) and 98-04 (Resumer,
// ResolveByAliasWithStatus) add the implementations.
//
// HANDOFF TO 98-02:
//   1. Add Threads GitHubThreadStore field to WebhookHandler in webhook_handler.go.
//   2. In Handle(), after step 5 (mention check): if h.Threads != nil,
//      call h.Threads.LookupSandbox(repo, number); if sandboxID != "" skip mention check.
//   3. Remove the Threads-related build tag once 98-02 ships.
//
// HANDOFF TO 98-04:
//   1. Add Resumer SandboxResumer field to WebhookHandler.
//   2. Add ResolveByAliasWithStatus(ctx, alias) (sandboxID, status string, err error)
//      to SandboxAliasResolver interface.
//   3. In Handle() warm path: if status == "stopped" || "paused", call Resumer.StartSandbox.
//   4. Remove the Resumer-related build tag once 98-04 ships.
package bridge_test

import (
	"context"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// Mock for GitHubThreadStore (to be defined in interfaces.go by 98-02)
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
// bridge.GitHubThreadStore is NOT yet defined → RED (compile failure until 98-02).
var _ bridge.GitHubThreadStore = &mockGitHubThreadStore{}

// ============================================================
// Mock for SandboxResumer (to be defined by 98-04)
// ============================================================

type mockSandboxResumer struct {
	called    bool
	sandboxID string
	err       error
}

func (m *mockSandboxResumer) StartSandbox(_ context.Context, sandboxID string) error {
	m.called = true
	m.sandboxID = sandboxID
	return m.err
}

// Compile-time check: mockSandboxResumer must satisfy bridge.SandboxResumer.
// bridge.SandboxResumer is NOT yet defined → RED (compile failure until 98-04).
var _ bridge.SandboxResumer = &mockSandboxResumer{}

// ============================================================
// Mock for extended SandboxAliasResolver with status (to be defined by 98-04)
// ============================================================

type mockResolverWithStatus struct {
	sandboxID  string
	status     string
	queueURL   string
	resolveErr error
	queueErr   error
}

func (m *mockResolverWithStatus) ResolveByAlias(_ context.Context, alias string) (string, error) {
	return m.sandboxID, m.resolveErr
}

func (m *mockResolverWithStatus) ResolveByAliasWithStatus(_ context.Context, alias string) (string, string, error) {
	return m.sandboxID, m.status, m.resolveErr
}

func (m *mockResolverWithStatus) GitHubQueueURL(_ context.Context, sandboxID string) (string, error) {
	return m.queueURL, m.queueErr
}

// Compile-time check: mockResolverWithStatus must satisfy bridge.SandboxAliasResolverWithStatus.
// bridge.SandboxAliasResolverWithStatus is NOT yet defined → RED (compile failure until 98-04).
var _ bridge.SandboxAliasResolverWithStatus = &mockResolverWithStatus{}

// ============================================================
// TestHandle_ThreadBypass (GH-X-THREADBYPASS)
// ============================================================

// TestHandle_ThreadBypass verifies that Handle() bypasses the @-mention check
// when the thread (repo, number) has an existing sandbox in the threads table.
// The comment body has NO @-mention but the thread is known → Handle should
// proceed to dispatch (not 200-drop at the mention step).
//
// This test references h.Threads which does not exist yet → RED.
func TestHandle_ThreadBypass(t *testing.T) {
	// Build a payload with NO @-mention.
	opts := defaultOpts()
	opts.commentBody = "thanks, that looks good now"          // NO @-mention
	opts.userLogin = "alice"                                   // allowed user
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
		Threads:        threads, // NEW FIELD — not yet on WebhookHandler → RED
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

// ============================================================
// TestHandle_AutoResume (GH-X-RESUME)
// ============================================================

// TestHandle_AutoResume verifies that Handle() calls Resumer.StartSandbox when
// ResolveByAliasWithStatus returns status="stopped", and still enqueues.
//
// This test references h.Resumer which does not exist yet → RED.
func TestHandle_AutoResume(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	// Resolver returns stopped sandbox.
	resolver := &mockResolverWithStatus{
		sandboxID: "sb-stopped",
		status:    "stopped",
		queueURL:  "https://sqs.example.com/github-queue",
	}
	resumer := &mockSandboxResumer{}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}

	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
	h := &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       resolver,   // must also satisfy SandboxAliasResolverWithStatus — NEW
		Publisher:      &mockPublisher{},
		SQS:            sqsSender,
		Reactor:        reactor,
		Entries:        entries,
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Resumer:        resumer,    // NEW FIELD — not yet on WebhookHandler → RED
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Resumer MUST be called with the sandbox ID.
	if !resumer.called {
		t.Error("Resumer.StartSandbox must be called when alias resolves with status=stopped")
	}
	if resumer.sandboxID != "sb-stopped" {
		t.Errorf("Resumer called with sandboxID=%q; want sb-stopped", resumer.sandboxID)
	}

	// SQS must still be enqueued (prompt queued for post-boot drain).
	if !sqsSender.called {
		t.Error("SQS.Send must be called on auto-resume path (prompt queued for post-boot drain)")
	}
}

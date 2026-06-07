// webhook_handler_phase98_04_test.go — Phase 98-04 tests for WebhookHandler auto-resume.
//
// Tests: TestHandle_AutoResume (GH-X-RESUME).
package bridge_test

import (
	"context"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

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
		Resolver:       resolver,
		Publisher:      &mockPublisher{},
		SQS:            sqsSender,
		Reactor:        reactor,
		Entries:        entries,
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Resumer:        resumer, // NEW FIELD — not yet on WebhookHandler → RED
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

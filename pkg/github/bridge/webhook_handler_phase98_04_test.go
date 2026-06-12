// webhook_handler_phase98_04_test.go — Phase 98-04 tests for WebhookHandler auto-resume.
//
// Tests: TestHandle_AutoResume (GH-X-RESUME).
// 98-06 additions: TestHandle_AutoResume_WritesStatusRunning,
//
//	TestHandle_AutoResume_StatusWriteError_EnqueueContinues.
package bridge_test

import (
	"context"
	"errors"
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

// ============================================================
// Mock for SandboxStatusWriter (98-06 Gap B)
// ============================================================

type mockSandboxStatusWriter struct {
	called    bool
	sandboxID string
	err       error

	// Phase 109: DeleteSandboxRow tracking (orphaned stopped-row self-heal).
	deleteCalled     bool
	deletedSandboxID string
	deleteErr        error
}

func (m *mockSandboxStatusWriter) SetStatusRunning(_ context.Context, sandboxID string) error {
	m.called = true
	m.sandboxID = sandboxID
	return m.err
}

func (m *mockSandboxStatusWriter) DeleteSandboxRow(_ context.Context, sandboxID string) error {
	m.deleteCalled = true
	m.deletedSandboxID = sandboxID
	return m.deleteErr
}

// Compile-time check: mockSandboxStatusWriter must satisfy bridge.SandboxStatusWriter.
var _ bridge.SandboxStatusWriter = &mockSandboxStatusWriter{}

// ============================================================
// 98-06 Gap B: status write-back tests
// ============================================================

// TestHandle_AutoResume_WritesStatusRunning verifies that Handle() calls
// StatusWriter.SetStatusRunning after StartSandbox succeeds on the resume path.
// This ensures km-sandboxes status flips to running so km list and follow-up
// @-mentions see the correct state (Gap B fix from live UAT 2026-06-07).
func TestHandle_AutoResume_WritesStatusRunning(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolverWithStatus{
		sandboxID: "sb-paused",
		status:    "paused",
		queueURL:  "https://sqs.example.com/github-queue",
	}
	resumer := &mockSandboxResumer{} // StartSandbox succeeds (err=nil)
	statusWriter := &mockSandboxStatusWriter{}
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
		Resumer:        resumer,
		StatusWriter:   statusWriter,
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Resumer must be called (auto-resume path).
	if !resumer.called {
		t.Error("Resumer.StartSandbox must be called on auto-resume path")
	}

	// StatusWriter must be called with the correct sandbox ID after StartSandbox succeeds.
	if !statusWriter.called {
		t.Error("StatusWriter.SetStatusRunning must be called after StartSandbox succeeds (Gap B fix)")
	}
	if statusWriter.sandboxID != "sb-paused" {
		t.Errorf("StatusWriter called with sandboxID=%q; want sb-paused", statusWriter.sandboxID)
	}

	// SQS must still be enqueued (prompt must survive).
	if !sqsSender.called {
		t.Error("SQS.Send must be called on auto-resume path")
	}
}

// TestHandle_AutoResume_StatusWriteError_EnqueueContinues verifies that a
// SetStatusRunning error does NOT abort the enqueue. Status write-back is a
// best-effort non-fatal step — the prompt must never be lost due to a DDB error.
func TestHandle_AutoResume_StatusWriteError_EnqueueContinues(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolverWithStatus{
		sandboxID: "sb-stopped2",
		status:    "stopped",
		queueURL:  "https://sqs.example.com/github-queue",
	}
	resumer := &mockSandboxResumer{} // StartSandbox succeeds
	statusWriter := &mockSandboxStatusWriter{
		err: errors.New("dynamodb: provisioned throughput exceeded"),
	}
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
		Resumer:        resumer,
		StatusWriter:   statusWriter,
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200 (status write error must not change response)", resp.StatusCode)
	}

	// StatusWriter was attempted.
	if !statusWriter.called {
		t.Error("StatusWriter.SetStatusRunning must be attempted even if it subsequently errors")
	}

	// Critical: SQS must STILL be enqueued even though status write failed.
	// The prompt must not be lost due to a transient DDB error.
	if !sqsSender.called {
		t.Error("SQS.Send must be called even when SetStatusRunning errors (prompt must not be lost)")
	}
}

// TestHandle_AutoResume_NilStatusWriter_EnqueueContinues verifies backward
// compat: when StatusWriter is nil (pre-98-06 deploy), the resume path still
// works and SQS is enqueued.
func TestHandle_AutoResume_NilStatusWriter_EnqueueContinues(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolverWithStatus{
		sandboxID: "sb-old",
		status:    "stopped",
		queueURL:  "https://sqs.example.com/github-queue",
	}
	resumer := &mockSandboxResumer{}
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
		Resumer:        resumer,
		StatusWriter:   nil, // nil = pre-98-06 behavior (no status write-back)
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if !resumer.called {
		t.Error("Resumer.StartSandbox must be called")
	}
	if !sqsSender.called {
		t.Error("SQS.Send must be called on resume path even with nil StatusWriter")
	}
}

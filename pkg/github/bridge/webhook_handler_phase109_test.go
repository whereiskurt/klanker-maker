// webhook_handler_phase109_test.go — Phase 109 tests for the orphaned-stopped-row
// self-heal: when the resume target has no resumable EC2 instance
// (ErrNoResumableInstance), the bridge deletes the stale km-sandboxes row and
// cold-creates instead of enqueuing to a dead per-sandbox FIFO queue.
//
// Reuses fakes from handle_test.go (mockResolver/mockSQS/mockPublisher/...) and
// webhook_handler_phase98_04_test.go (mockResolverWithStatus, mockSandboxResumer,
// mockSandboxStatusWriter — extended with DeleteSandboxRow tracking).
package bridge_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

func phase109Entries() []bridge.RepoEntry {
	return []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
}

// TestHandle_OrphanedStoppedRow_ColdCreates verifies the core Phase 109 behavior:
// a status=stopped alias whose EC2 instance is gone (StartSandbox returns a wrapped
// ErrNoResumableInstance) must delete the stale row and cold-create — NOT enqueue
// to the dead queue, and NOT upsert a thread row.
func TestHandle_OrphanedStoppedRow_ColdCreates(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolverWithStatus{
		sandboxID: "github-e903e795",
		status:    "stopped",
		queueURL:  "https://sqs.example.com/dead-queue",
	}
	// The instance is gone → StartSandbox returns the terminal sentinel (wrapped).
	resumer := &mockSandboxResumer{
		err: fmt.Errorf("github-bridge: no stopped/stopping EC2 instances found: %w", bridge.ErrNoResumableInstance),
	}
	statusWriter := &mockSandboxStatusWriter{}
	publisher := &mockPublisher{}
	sqsSender := &mockSQS{}
	threads := &mockGitHubThreadStore{} // empty → thread unknown
	reactor := &mockReactor{}

	h := &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       resolver,
		Publisher:      publisher,
		SQS:            sqsSender,
		Reactor:        reactor,
		Entries:        phase109Entries(),
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Resumer:        resumer,
		StatusWriter:   statusWriter,
		Threads:        threads,
	}

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Resumer was attempted.
	if !resumer.called {
		t.Error("Resumer.StartSandbox must be attempted on the stopped path")
	}

	// Stale row MUST be deleted (so the alias becomes absent → no ambiguous-alias trap).
	if !statusWriter.deleteCalled {
		t.Error("DeleteSandboxRow must be called to clear the orphaned stopped row")
	}
	if statusWriter.deletedSandboxID != "github-e903e795" {
		t.Errorf("DeleteSandboxRow called with %q; want github-e903e795", statusWriter.deletedSandboxID)
	}

	// Cold-create MUST fire with the resolved alias/profile.
	if !publisher.called {
		t.Error("Publisher.PutSandboxCreate must be called (cold-create fallback)")
	}
	if publisher.alias != "myrepo-alias" {
		t.Errorf("Publisher alias=%q; want myrepo-alias", publisher.alias)
	}
	if publisher.profile != "myrepo-profile" {
		t.Errorf("Publisher profile=%q; want myrepo-profile", publisher.profile)
	}

	// Enqueue MUST NOT happen — the per-sandbox queue has no live poller.
	if sqsSender.called {
		t.Error("SQS.Send must NOT be called on the orphan-fallback path (dead queue)")
	}

	// No thread upsert — there is no live sandbox_id to record.
	if threads.upserted {
		t.Error("Threads.Upsert must NOT be called on the orphan-fallback path")
	}

	// StatusWriter.SetStatusRunning must NOT be called (resume did not succeed).
	if statusWriter.called {
		t.Error("SetStatusRunning must NOT be called when the instance is gone")
	}
}

// TestHandle_GenuinelyStopped_ResumesNotColdCreate verifies no regression: a
// genuinely stopped sandbox (StartSandbox succeeds) resumes + enqueues and never
// deletes the row or cold-creates.
func TestHandle_GenuinelyStopped_ResumesNotColdCreate(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolverWithStatus{
		sandboxID: "sb-genuinely-stopped",
		status:    "stopped",
		queueURL:  "https://sqs.example.com/github-queue",
	}
	resumer := &mockSandboxResumer{} // StartSandbox succeeds (err=nil)
	statusWriter := &mockSandboxStatusWriter{}
	publisher := &mockPublisher{}
	sqsSender := &mockSQS{}

	h := &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       resolver,
		Publisher:      publisher,
		SQS:            sqsSender,
		Reactor:        &mockReactor{},
		Entries:        phase109Entries(),
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

	if !statusWriter.called {
		t.Error("SetStatusRunning must be called on a successful resume")
	}
	if !sqsSender.called {
		t.Error("SQS.Send must be called on a successful resume (prompt queued)")
	}
	if publisher.called {
		t.Error("PutSandboxCreate must NOT be called when the instance resumes")
	}
	if statusWriter.deleteCalled {
		t.Error("DeleteSandboxRow must NOT be called when the instance resumes")
	}
}

// TestHandle_TransientResumeError_EnqueueContinues verifies no regression: a
// transient StartSandbox error (NOT wrapping the sentinel) keeps the existing
// log-non-fatal + enqueue behavior so the FIFO redelivers once the box recovers.
func TestHandle_TransientResumeError_EnqueueContinues(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolverWithStatus{
		sandboxID: "sb-transient",
		status:    "stopped",
		queueURL:  "https://sqs.example.com/github-queue",
	}
	resumer := &mockSandboxResumer{err: errors.New("throttled")} // NOT the sentinel
	statusWriter := &mockSandboxStatusWriter{}
	publisher := &mockPublisher{}
	sqsSender := &mockSQS{}

	h := &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       resolver,
		Publisher:      publisher,
		SQS:            sqsSender,
		Reactor:        &mockReactor{},
		Entries:        phase109Entries(),
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

	// Enqueue continues (transient error → FIFO redelivers later).
	if !sqsSender.called {
		t.Error("SQS.Send must be called on a transient resume error (enqueue continues)")
	}
	if publisher.called {
		t.Error("PutSandboxCreate must NOT be called on a transient resume error")
	}
	if statusWriter.deleteCalled {
		t.Error("DeleteSandboxRow must NOT be called on a transient resume error")
	}
}

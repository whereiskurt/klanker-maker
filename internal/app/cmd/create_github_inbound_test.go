package cmd

// create_github_inbound_test.go — Phase 97 Plan 03 tests
//
// Exercises provisionGitHubInboundQueue via local mocks — no real AWS connection.
// Covers: happy path, disabled no-op, DDB persist failure, SSM inject failure.
//
// Structure mirrors create_slack_inbound_test.go (deps-struct DI pattern).

import (
	"context"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// ============================================================
// Helpers
// ============================================================

// githubInboundDepsEnabled returns a githubInboundDeps with inbound enabled=enabled
// and the given fake SQS, DDB error, SSM error wired up.
func githubInboundDepsEnabled(t *testing.T, enabled bool, fSQS *fakeSQS,
	ddbErr, ssmErr error) (githubInboundDeps, *testState) {
	t.Helper()
	state := &testState{
		ddbAttrs:  make(map[string]string),
		ssmParams: make(map[string]string),
	}
	p := &profile.SandboxProfile{}
	p.Spec.Notification = &profile.NotificationSpec{
		Github: &profile.NotificationGitHubSpec{
			Inbound: &profile.NotificationGitHubInboundSpec{Enabled: &enabled},
		},
	}
	return githubInboundDeps{
		Profile:   p,
		Cfg:       &config.Config{ResourcePrefix: "km"},
		SandboxID: "sb-abc123",
		SQS:       fSQS,
		UpdateSandboxAttr: func(_ context.Context, _, attr, val string) error {
			if ddbErr != nil {
				return ddbErr
			}
			state.ddbAttrs[attr] = val
			return nil
		},
		PutSSMParameter: func(_ context.Context, name, val string) error {
			if ssmErr != nil {
				return ssmErr
			}
			state.ssmParams[name] = val
			return nil
		},
	}, state
}

// ============================================================
// Tests
// ============================================================

// TestCreate_GitHubInboundQueueProvisioned verifies the happy path:
//   - profile has notification.github.inbound.enabled=true
//   - CreateQueue is called exactly once with correct FIFO attributes
//   - DDB attr github_inbound_queue_url is written with the returned URL
//   - SSM parameter /{prefix}/sandbox/{id}/github-inbound-queue-url is written
//   - provisionGitHubInboundQueue returns the non-empty queue URL
func TestCreate_GitHubInboundQueueProvisioned(t *testing.T) {
	fs := &fakeSQS{}
	deps, state := githubInboundDepsEnabled(t, true, fs, nil, nil)

	url, err := provisionGitHubInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url == "" {
		t.Fatal("expected non-empty queue URL on success")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call, got %d", fs.createCalled)
	}
	// Queue name must follow {prefix}-github-inbound-{sandbox-id}.fifo
	expectedName := "km-github-inbound-sb-abc123.fifo"
	if fs.createName != expectedName {
		t.Fatalf("queue name: got %q, want %q", fs.createName, expectedName)
	}
	// Verify FIFO attributes
	if got := fs.createAttrs["FifoQueue"]; got != "true" {
		t.Errorf("FifoQueue attr: got %q, want %q", got, "true")
	}
	if got := fs.createAttrs["ContentBasedDeduplication"]; got != "false" {
		t.Errorf("ContentBasedDeduplication attr: got %q, want %q", got, "false")
	}
	if got := fs.createAttrs["VisibilityTimeout"]; got != "30" {
		t.Errorf("VisibilityTimeout attr: got %q, want %q", got, "30")
	}
	if got := fs.createAttrs["MessageRetentionPeriod"]; got != "1209600" {
		t.Errorf("MessageRetentionPeriod attr: got %q, want %q", got, "1209600")
	}
	// DDB must have the queue URL persisted
	if got := state.ddbAttrs["github_inbound_queue_url"]; got != url {
		t.Fatalf("DDB github_inbound_queue_url: got %q, want %q", got, url)
	}
	// SSM Parameter Store must have the queue URL
	expectedParam := "/km/sandbox/sb-abc123/github-inbound-queue-url"
	if got := state.ssmParams[expectedParam]; got != url {
		t.Fatalf("SSM param %s: got %q, want %q", expectedParam, got, url)
	}
}

// TestCreate_GitHubInboundDisabledZeroArtifacts verifies the no-op path:
//   - profile has notification.github.inbound.enabled=false
//   - provisionGitHubInboundQueue returns ("", nil)
//   - zero SQS API calls, zero DDB writes, zero SSM writes
func TestCreate_GitHubInboundDisabledZeroArtifacts(t *testing.T) {
	fs := &fakeSQS{}
	deps, state := githubInboundDepsEnabled(t, false, fs, nil, nil)

	url, err := provisionGitHubInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("inbound off: unexpected error: %v", err)
	}
	if url != "" {
		t.Fatalf("inbound off: expected empty URL, got %q", url)
	}
	if fs.createCalled != 0 {
		t.Fatalf("inbound off: expected 0 SQS calls, got %d", fs.createCalled)
	}
	if len(state.ddbAttrs) != 0 {
		t.Fatalf("inbound off: expected 0 DDB writes, got %v", state.ddbAttrs)
	}
	if len(state.ssmParams) != 0 {
		t.Fatalf("inbound off: expected 0 SSM parameter writes, got %v", state.ssmParams)
	}
}

// TestCreate_GitHubInboundNilProfile verifies the no-op path when profile is nil:
//   - provisionGitHubInboundQueue returns ("", nil)
func TestCreate_GitHubInboundNilProfile(t *testing.T) {
	fs := &fakeSQS{}
	deps := githubInboundDeps{
		Profile:   nil,
		Cfg:       &config.Config{ResourcePrefix: "km"},
		SandboxID: "sb-abc123",
		SQS:       fs,
		UpdateSandboxAttr: func(_ context.Context, _, _, _ string) error { return nil },
		PutSSMParameter:   func(_ context.Context, _, _ string) error { return nil },
	}
	url, err := provisionGitHubInboundQueue(context.Background(), deps)
	if err != nil {
		t.Fatalf("nil profile: unexpected error: %v", err)
	}
	if url != "" {
		t.Fatalf("nil profile: expected empty URL, got %q", url)
	}
}

// TestCreate_GitHubInboundSSMFailureRollback verifies SSM Parameter Store write
// failure triggers best-effort rollback:
//   - CreateQueue succeeds (1 call)
//   - DDB UpdateAttr succeeds
//   - PutSSMParameter fails
//   - DeleteQueue is called exactly once (best-effort rollback)
//   - provisionGitHubInboundQueue returns a non-nil error
func TestCreate_GitHubInboundSSMFailureRollback(t *testing.T) {
	fs := &fakeSQS{}
	ssmErr := errors.New("ssm put-parameter timeout")
	deps, _ := githubInboundDepsEnabled(t, true, fs, nil, ssmErr)

	url, err := provisionGitHubInboundQueue(context.Background(), deps)
	if err == nil {
		t.Fatal("expected error from SSM Parameter Store write failure")
	}
	if url != "" {
		t.Fatalf("expected empty URL on failure, got %q", url)
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue call before failure, got %d", fs.createCalled)
	}
	if fs.deleteCalled != 1 {
		t.Fatalf("expected 1 DeleteQueue rollback call, got %d", fs.deleteCalled)
	}
}

// TestCreate_GitHubInboundDDBPersistFailureRollback verifies DDB write failure
// triggers best-effort rollback:
//   - CreateQueue succeeds (1 call)
//   - UpdateSandboxAttr fails
//   - DeleteQueue is called exactly once (rollback delete)
//   - provisionGitHubInboundQueue returns a wrapped error
func TestCreate_GitHubInboundDDBPersistFailureRollback(t *testing.T) {
	fs := &fakeSQS{}
	ddbErr := errors.New("ddb conditional check failed")
	deps, _ := githubInboundDepsEnabled(t, true, fs, ddbErr, nil)

	_, err := provisionGitHubInboundQueue(context.Background(), deps)
	if err == nil {
		t.Fatal("expected error from DDB write failure")
	}
	if fs.createCalled != 1 {
		t.Fatalf("expected 1 CreateQueue before DDB failure, got %d", fs.createCalled)
	}
	if fs.deleteCalled != 1 {
		t.Fatalf("expected 1 DeleteQueue rollback call after DDB failure, got %d", fs.deleteCalled)
	}
}

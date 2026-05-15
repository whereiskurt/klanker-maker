// Package cmd — doctor_slack_inbound_test.go
// Plan 67-08 — tests for the three Slack-inbound doctor checks:
//   - checkSlackInboundQueueExists
//   - checkSlackInboundStaleQueues
//   - checkSlackAppEventsScopes
//
// All tests use the local fakeSQS (defined in create_slack_inbound_test.go)
// and inline closures for the listInbound / getScopes deps. No real AWS calls.
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// =============================================================================
// fakeSSMDeleter — Plan quick-7 in-memory SSMDeleterAPI for cleanup tests.
// =============================================================================

// fakeSSMDeleter records DeleteParameter calls and optionally returns an error.
type fakeSSMDeleter struct {
	deleteCalls []string
	deleteErr   error // returned for every call when set
}

func (f *fakeSSMDeleter) DeleteParameter(_ context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	if in != nil && in.Name != nil {
		f.deleteCalls = append(f.deleteCalls, *in.Name)
	}
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &ssm.DeleteParameterOutput{}, nil
}

// =============================================================================
// checkSlackInboundQueueExists
// =============================================================================

// TestDoctor_SlackInboundQueueExists_AllHealthy: every inbound row has a
// reachable queue → OK.
func TestDoctor_SlackInboundQueueExists_AllHealthy(t *testing.T) {
	sqsCli := &fakeSQS{} // GetQueueAttributes returns no error
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{
			{SandboxID: "sb-a", QueueURL: "https://sqs.us-east-1.amazonaws.com/1/km-slack-inbound-sb-a.fifo"},
			{SandboxID: "sb-b", QueueURL: "https://sqs.us-east-1.amazonaws.com/1/km-slack-inbound-sb-b.fifo"},
		}, nil
	}
	r := checkSlackInboundQueueExists(context.Background(), listInbound, sqsCli)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "2 sandbox(es)") {
		t.Errorf("expected message to mention 2 sandboxes, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundQueueExists_OneMissing: GetQueueAttributes fails for
// the only queue → FAIL with the queue URL surfaced in the message.
func TestDoctor_SlackInboundQueueExists_OneMissing(t *testing.T) {
	sqsCli := &fakeSQS{getAttrsErr: errors.New("queue does not exist")}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{
			{SandboxID: "sb-a", QueueURL: "https://sqs/orphan.fifo"},
		}, nil
	}
	r := checkSlackInboundQueueExists(context.Background(), listInbound, sqsCli)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL when queue unreachable, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "sb-a") {
		t.Errorf("expected message to mention sb-a, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundQueueExists_NoSandboxes: empty inbound list → OK with
// "no sandboxes" message (does not call SQS).
func TestDoctor_SlackInboundQueueExists_NoSandboxes(t *testing.T) {
	sqsCli := &fakeSQS{}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	r := checkSlackInboundQueueExists(context.Background(), listInbound, sqsCli)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s", r.Status)
	}
	if !strings.Contains(r.Message, "no sandboxes have inbound enabled") {
		t.Errorf("expected 'no sandboxes have inbound enabled' message, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundQueueExists_NilDeps: nil deps → SKIPPED.
func TestDoctor_SlackInboundQueueExists_NilDeps(t *testing.T) {
	r := checkSlackInboundQueueExists(context.Background(), nil, nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// checkSlackInboundStaleQueues
// =============================================================================

// TestDoctor_SlackInboundStaleQueues_FoundOrphans: SQS lists more queues than
// DDB knows about → WARN with the orphan URL surfaced in the message.
func TestDoctor_SlackInboundStaleQueues_FoundOrphans(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{
			"https://sqs/km-slack-inbound-sb-a.fifo",
			"https://sqs/km-slack-inbound-orphan.fifo",
		},
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{
			{SandboxID: "sb-a", QueueURL: "https://sqs/km-slack-inbound-sb-a.fifo"},
		}, nil
	}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km", true, false, nil)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "orphan") {
		t.Errorf("expected message to mention orphan queue, got: %s", r.Message)
	}
	// Dry-run remediation must point operators at the full --dry-run=false
	// --delete-sqs pair (the explicit-opt-in pattern shared with --delete-ebs),
	// not the legacy aws-cli command.
	if !strings.Contains(r.Remediation, "--dry-run=false") || !strings.Contains(r.Remediation, "--delete-sqs") {
		t.Errorf("expected remediation to mention both --dry-run=false and --delete-sqs, got: %s", r.Remediation)
	}
}

// TestDoctor_SlackInboundStaleQueues_AllAccountedFor: every SQS queue has a
// matching DDB row → OK.
func TestDoctor_SlackInboundStaleQueues_AllAccountedFor(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{
			"https://sqs/km-slack-inbound-sb-a.fifo",
			"https://sqs/km-slack-inbound-sb-b.fifo",
		},
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{
			{SandboxID: "sb-a", QueueURL: "https://sqs/km-slack-inbound-sb-a.fifo"},
			{SandboxID: "sb-b", QueueURL: "https://sqs/km-slack-inbound-sb-b.fifo"},
		}, nil
	}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km", true, false, nil)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "all 2 inbound queue(s) accounted for") {
		t.Errorf("expected 'all 2 ... accounted for' message, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundStaleQueues_NoQueues: SQS returns an empty list → OK
// (does not need to call DDB).
func TestDoctor_SlackInboundStaleQueues_NoQueues(t *testing.T) {
	sqsCli := &fakeSQS{listResult: nil}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km", true, false, nil)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s", r.Status)
	}
	if !strings.Contains(r.Message, "no inbound queues exist") {
		t.Errorf("expected 'no inbound queues exist' message, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundStaleQueues_NilDeps: nil deps → SKIPPED.
func TestDoctor_SlackInboundStaleQueues_NilDeps(t *testing.T) {
	r := checkSlackInboundStaleQueues(context.Background(), nil, nil, "km", true, false, nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// checkSlackAppEventsScopes
// =============================================================================

// TestDoctor_SlackInboundEventsSubscription_HasAllScopes: bot has all required
// scopes including reactions:write and files:read (Phase 75) → OK with success message naming all four.
func TestDoctor_SlackInboundEventsSubscription_HasAllScopes(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "groups:history", "channels:read", "reactions:write", "files:read"}, nil
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	// Verify success message names all required scopes (operators read this output).
	if !strings.Contains(r.Message, "reactions:write") {
		t.Errorf("expected success message to mention reactions:write, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "files:read") {
		t.Errorf("expected success message to mention files:read, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundEventsSubscription_MissingScope: bot is missing all
// three required scopes → FAIL with all three scope names surfaced in the message.
func TestDoctor_SlackInboundEventsSubscription_MissingScope(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write"}, nil // missing all three required scopes
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "channels:history") ||
		!strings.Contains(r.Message, "groups:history") ||
		!strings.Contains(r.Message, "reactions:write") {
		t.Errorf("expected message to list all three missing scopes, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Error("expected non-empty remediation hint")
	}
}

// TestDoctor_SlackInboundEventsSubscription_PartialScopes: bot has channels:history
// and reactions:write but is missing groups:history → FAIL listing only the missing one.
func TestDoctor_SlackInboundEventsSubscription_PartialScopes(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "reactions:write"}, nil // missing groups:history
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "groups:history") {
		t.Errorf("expected message to mention groups:history, got: %s", r.Message)
	}
}

// TestDoctor_SlackInboundEventsSubscription_MissingReactionsWrite: realistic Phase 67.1
// upgrade scenario — operator already has channels:history + groups:history from Phase 63
// inbound deployment but forgot to add reactions:write when upgrading.
// Should FAIL with only reactions:write surfaced in the message.
func TestDoctor_SlackInboundEventsSubscription_MissingReactionsWrite(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "groups:history", "channels:read"}, nil
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL when only reactions:write missing, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "reactions:write") {
		t.Errorf("expected message to mention reactions:write specifically, got: %s", r.Message)
	}
	// Should NOT mention the scopes that are present (avoid noise in the operator-facing message).
	if strings.Contains(r.Message, "channels:history") || strings.Contains(r.Message, "groups:history") {
		t.Errorf("expected message to ONLY list missing scopes, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Error("expected non-empty remediation hint")
	}
}

// TestDoctor_SlackInboundEventsSubscription_AuthTestError: getScopes returns an
// error → WARN (do not fail doctor).
func TestDoctor_SlackInboundEventsSubscription_AuthTestError(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return nil, errors.New("invalid_auth")
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on auth.test error, got %s", r.Status)
	}
}

// TestDoctor_SlackInboundEventsSubscription_NilDeps: nil getScopes → SKIPPED.
func TestDoctor_SlackInboundEventsSubscription_NilDeps(t *testing.T) {
	r := checkSlackAppEventsScopes(context.Background(), nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// TestDoctor_FilesReadScope_Missing_Reports: Phase 75 upgrade scenario — operator
// already has channels:history + groups:history + reactions:write from Phase 67.1
// but forgot to add files:read when upgrading to Phase 75.
// Should FAIL with only files:read surfaced in the message.
func TestDoctor_FilesReadScope_Missing_Reports(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "groups:history", "channels:read", "reactions:write"}, nil
	}
	r := checkSlackAppEventsScopes(context.Background(), getScopes)
	if r.Status != CheckError {
		t.Fatalf("expected FAIL when only files:read missing, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "files:read") {
		t.Errorf("expected message to mention files:read specifically, got: %s", r.Message)
	}
	// Should NOT mention the scopes that are present (avoid noise in the operator-facing message).
	if strings.Contains(r.Message, "channels:history") || strings.Contains(r.Message, "groups:history") || strings.Contains(r.Message, "reactions:write") {
		t.Errorf("expected message to ONLY list missing scopes, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Error("expected non-empty remediation hint")
	}

	// With all four scopes present, check should succeed and name files:read in the success message.
	getAll := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:history", "groups:history", "reactions:write", "files:read"}, nil
	}
	rOK := checkSlackAppEventsScopes(context.Background(), getAll)
	if rOK.Status != CheckOK {
		t.Fatalf("expected OK with all four scopes, got %s (msg=%s)", rOK.Status, rOK.Message)
	}
	if !strings.Contains(rOK.Message, "files:read") {
		t.Errorf("expected success message to enumerate files:read, got: %s", rOK.Message)
	}
}

// =============================================================================
// Plan quick-7 — checkSlackInboundStaleQueues cleanup-path tests
// =============================================================================

// TestDoctor_SlackInboundStaleQueues_DryRunTrue_NoDestructiveCalls: orphan
// queue exists, dryRun=true → WARN with orphan URL surfaced, NO DeleteQueue
// call, NO SSM call.
func TestDoctor_SlackInboundStaleQueues_DryRunTrue_NoDestructiveCalls(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{"https://sqs/km-slack-inbound-orphan.fifo"},
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	ssmCli := &fakeSSMDeleter{}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km", true, true, ssmCli)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "orphan") {
		t.Errorf("expected message to mention orphan URL, got: %s", r.Message)
	}
	if sqsCli.deleteCalled != 0 {
		t.Errorf("dryRun=true must not call DeleteQueue; saw %d calls", sqsCli.deleteCalled)
	}
	if len(ssmCli.deleteCalls) != 0 {
		t.Errorf("dryRun=true must not call SSM DeleteParameter; saw %d calls", len(ssmCli.deleteCalls))
	}
}

// TestDoctor_SlackInboundStaleQueues_DryRunFalse_HappyPath: 2 orphan queues,
// SQS DeleteQueue succeeds, SSM DeleteParameter succeeds → WARN with
// (2 deleted, 0 skipped), 2 SSM calls with the matching parameter names.
func TestDoctor_SlackInboundStaleQueues_DryRunFalse_HappyPath(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{
			"https://sqs/km-slack-inbound-sb-x1.fifo",
			"https://sqs/km-slack-inbound-sb-x2.fifo",
		},
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	ssmCli := &fakeSSMDeleter{}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km", false, true, ssmCli)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if sqsCli.deleteCalled != 2 {
		t.Errorf("expected 2 DeleteQueue calls, got %d", sqsCli.deleteCalled)
	}
	if !strings.Contains(r.Message, "(2 deleted, 0 skipped)") {
		t.Errorf("expected message to contain '(2 deleted, 0 skipped)', got: %s", r.Message)
	}
	if len(ssmCli.deleteCalls) != 2 {
		t.Fatalf("expected 2 SSM DeleteParameter calls, got %d", len(ssmCli.deleteCalls))
	}
	// Production code uses kmaws.SandboxParameterPath which formats as
	// /{prefix}/sandbox/{id}/{suffix}. Pre-prefix-migration this test asserted
	// the unprefixed form and silently broke when commit 26dd788 scoped paths
	// under /{prefix}/.
	expected := map[string]bool{
		"/km/sandbox/sb-x1/slack-inbound-queue-url": false,
		"/km/sandbox/sb-x2/slack-inbound-queue-url": false,
	}
	for _, name := range ssmCli.deleteCalls {
		if _, ok := expected[name]; !ok {
			t.Errorf("unexpected SSM param name %q", name)
			continue
		}
		expected[name] = true
	}
	for name, seen := range expected {
		if !seen {
			t.Errorf("missing SSM DeleteParameter call for %q", name)
		}
	}
}

// TestDoctor_SlackInboundStaleQueues_DryRunFalse_PartialFailure: 2 orphans,
// every DeleteQueue returns AccessDenied → WARN with (0 deleted, 2 skipped).
// SSM is NOT called for failed queue deletes (we only attempt SSM after a
// successful queue delete to avoid orphaning the param).
func TestDoctor_SlackInboundStaleQueues_DryRunFalse_PartialFailure(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{
			"https://sqs/km-slack-inbound-sb-y1.fifo",
			"https://sqs/km-slack-inbound-sb-y2.fifo",
		},
		deleteErr: errors.New("AccessDenied"),
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	ssmCli := &fakeSSMDeleter{}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km", false, true, ssmCli)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "(0 deleted, 2 skipped)") {
		t.Errorf("expected '(0 deleted, 2 skipped)', got: %s", r.Message)
	}
	if sqsCli.deleteCalled != 2 {
		t.Errorf("expected 2 DeleteQueue attempts, got %d", sqsCli.deleteCalled)
	}
	if len(ssmCli.deleteCalls) != 0 {
		t.Errorf("expected NO SSM calls when queue delete failed, got %d", len(ssmCli.deleteCalls))
	}
}

// TestDoctor_SlackInboundStaleQueues_DryRunFalse_SSMParameterNotFound:
// 1 orphan, SQS DeleteQueue succeeds, SSM returns ParameterNotFound →
// treated as success, WARN with (1 deleted, 0 skipped).
func TestDoctor_SlackInboundStaleQueues_DryRunFalse_SSMParameterNotFound(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{"https://sqs/km-slack-inbound-sb-z1.fifo"},
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	ssmCli := &fakeSSMDeleter{deleteErr: &ssmtypes.ParameterNotFound{}}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km", false, true, ssmCli)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "(1 deleted, 0 skipped)") {
		t.Errorf("ParameterNotFound must be treated as success; got msg: %s", r.Message)
	}
	if sqsCli.deleteCalled != 1 {
		t.Errorf("expected 1 DeleteQueue call, got %d", sqsCli.deleteCalled)
	}
	if len(ssmCli.deleteCalls) != 1 {
		t.Errorf("expected 1 SSM DeleteParameter call (which returned NotFound), got %d", len(ssmCli.deleteCalls))
	}
}

// TestDoctor_SlackInboundStaleQueues_DryRunFalseWithoutDeleteSQS_NoDestructiveCalls
// verifies the explicit-opt-in property: --dry-run=false alone is NOT enough
// to delete inbound queues — the operator must also pass --delete-sqs. Same
// pattern as --delete-ebs. Before this change, --dry-run=false implicitly
// deleted queues; the new opt-in is more conservative because deleting an
// SQS queue races with the 60-second AWS-side creation cooldown.
func TestDoctor_SlackInboundStaleQueues_DryRunFalseWithoutDeleteSQS_NoDestructiveCalls(t *testing.T) {
	sqsCli := &fakeSQS{
		listResult: []string{"https://sqs/km-slack-inbound-orphan.fifo"},
	}
	listInbound := func(_ context.Context) ([]inboundRow, error) {
		return []inboundRow{}, nil
	}
	ssmCli := &fakeSSMDeleter{}
	r := checkSlackInboundStaleQueues(context.Background(), listInbound, sqsCli, "km", false, false, ssmCli)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if sqsCli.deleteCalled != 0 {
		t.Errorf("--dry-run=false alone (without --delete-sqs) must NOT call DeleteQueue; saw %d calls", sqsCli.deleteCalled)
	}
	if len(ssmCli.deleteCalls) != 0 {
		t.Errorf("--dry-run=false alone (without --delete-sqs) must NOT call SSM DeleteParameter; saw %d calls", len(ssmCli.deleteCalls))
	}
	if !strings.Contains(r.Remediation, "--delete-sqs") {
		t.Errorf("expected remediation to mention --delete-sqs, got: %s", r.Remediation)
	}
	// And the remediation should NOT also nag about --dry-run=false (already set).
	if strings.Contains(r.Remediation, "--dry-run=false") {
		t.Errorf("remediation already in --dry-run=false mode shouldn't repeat the flag, got: %s", r.Remediation)
	}
}

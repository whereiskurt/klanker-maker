// aws_adapters_test.go — Phase 98 tests for EventBridgeAdapter (cold-create) and
// EC2Resumer (auto-resume Gap C: stopping-state tolerance).
//
// BUILD TAG: phase98_wave0
// The EventBridgeAdapter tests were originally RED stubs; they pass since 98-04.
// The EC2Resumer tests (Gap C, 98-06 Task 3) are unconditionally included.
package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// Fake EC2 client for EC2Resumer tests
// ============================================================

// fakeEC2Client implements bridge.EC2StartAPI for tests.
// describeResponses is a slice of responses returned in order on successive
// DescribeInstances calls (supports the polling-for-stopped behavior).
type fakeEC2Client struct {
	describeResponses []*ec2.DescribeInstancesOutput
	describeCallCount int
	describeErr       error

	startCalled     bool
	startInstanceIDs []string
	startErr        error
}

func (f *fakeEC2Client) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	idx := f.describeCallCount
	if idx >= len(f.describeResponses) {
		idx = len(f.describeResponses) - 1
	}
	f.describeCallCount++
	return f.describeResponses[idx], nil
}

func (f *fakeEC2Client) StartInstances(_ context.Context, params *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	f.startCalled = true
	f.startInstanceIDs = params.InstanceIds
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &ec2.StartInstancesOutput{}, nil
}

// makeInstance builds a minimal ec2types.Instance for test responses.
func makeInstance(id string, state ec2types.InstanceStateName) ec2types.Instance {
	return ec2types.Instance{
		InstanceId: awssdk.String(id),
		State:      &ec2types.InstanceState{Name: state},
	}
}

// singleReservation wraps instances in a single Reservation.
func singleReservation(instances ...ec2types.Instance) *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{Instances: instances},
		},
	}
}

// emptyDescribe returns an empty DescribeInstances output (no reservations).
func emptyDescribe() *ec2.DescribeInstancesOutput {
	return &ec2.DescribeInstancesOutput{}
}

// ============================================================
// TestEC2Resumer_StoppedInstance — baseline: stopped instance starts normally
// ============================================================

// TestEC2Resumer_StoppedInstance verifies that a sandbox already in the "stopped"
// state is started on the first DescribeInstances call (no stopping-state polling).
func TestEC2Resumer_StoppedInstance(t *testing.T) {
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{
			singleReservation(makeInstance("i-stopped123", ec2types.InstanceStateNameStopped)),
		},
	}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	if err := resumer.StartSandbox(context.Background(), "sb-test"); err != nil {
		t.Fatalf("expected nil error for stopped instance, got: %v", err)
	}
	if !fake.startCalled {
		t.Fatal("StartInstances must be called for a stopped instance")
	}
	if len(fake.startInstanceIDs) != 1 || fake.startInstanceIDs[0] != "i-stopped123" {
		t.Errorf("StartInstances called with %v; want [i-stopped123]", fake.startInstanceIDs)
	}
}

// ============================================================
// TestEC2Resumer_StoppingInstance — Gap C: stopping instance is not dropped
// ============================================================

// TestEC2Resumer_StoppingInstance verifies that a "stopping" instance is no longer
// silently dropped with "no stopped EC2 instances found". The resumer must find the
// instance and eventually call StartInstances on it (after polling reveals "stopped").
//
// This is the regression test for UAT Gap C (2026-06-07): quick pause→@-mention fired
// the bridge 35s after pause; the box was still "stopping"; the old code filtered only
// "stopped" so it found nothing and gave up. The prompt was enqueued but the box never
// started — it wedged until the operator re-posted the comment after full stop.
func TestEC2Resumer_StoppingInstance(t *testing.T) {
	// First DescribeInstances: instance is still "stopping".
	// Second DescribeInstances (poll for stopped): instance is now "stopped".
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{
			// First call (widened filter: stopped+stopping): returns "stopping".
			singleReservation(makeInstance("i-stopping456", ec2types.InstanceStateNameStopping)),
			// Second call (narrow filter: stopped only): returns "stopped".
			singleReservation(makeInstance("i-stopping456", ec2types.InstanceStateNameStopped)),
		},
	}
	resumer := &bridge.EC2Resumer{
		Client:          fake,
		SandboxIDTagKey: "km:sandbox-id",
	}

	if err := resumer.StartSandbox(context.Background(), "sb-stopping"); err != nil {
		t.Fatalf("expected nil error for stopping→stopped instance, got: %v", err)
	}
	if !fake.startCalled {
		t.Fatal("StartInstances must be called (after stopping instance reaches stopped)")
	}
	// Must have made at least 2 DescribeInstances calls: initial + at least one poll.
	if fake.describeCallCount < 2 {
		t.Errorf("expected ≥ 2 DescribeInstances calls (initial + polling); got %d", fake.describeCallCount)
	}
}

// ============================================================
// TestEC2Resumer_NoInstances — neither stopped nor stopping
// ============================================================

// TestEC2Resumer_NoInstances verifies that when no stopped/stopping instances exist
// (e.g. sandbox already running), an error is returned (not a silent no-op).
func TestEC2Resumer_NoInstances(t *testing.T) {
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{
			emptyDescribe(),
		},
	}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "sb-running")
	if err == nil {
		t.Fatal("expected error when no stopped/stopping instances exist")
	}
	if fake.startCalled {
		t.Error("StartInstances must NOT be called when no instances found")
	}
}

// ============================================================
// TestEC2Resumer_DescribeError — API error propagates
// ============================================================

// TestEC2Resumer_DescribeError verifies that a DescribeInstances API error is
// returned (not silently swallowed).
func TestEC2Resumer_DescribeError(t *testing.T) {
	fake := &fakeEC2Client{
		describeErr: errors.New("AWS: RequestExpired"),
	}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "sb-err")
	if err == nil {
		t.Fatal("expected error from DescribeInstances API failure")
	}
}

// ============================================================
// Fake EventBridge client
// ============================================================

// fakeEventBridgeClient captures the most recent PutEvents input.
type fakeEventBridgeClient struct {
	lastInput *eventbridge.PutEventsInput
	err       error
}

func (f *fakeEventBridgeClient) PutEvents(_ context.Context, params *eventbridge.PutEventsInput, _ ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	f.lastInput = params
	if f.err != nil {
		return nil, f.err
	}
	return &eventbridge.PutEventsOutput{FailedEntryCount: 0}, nil
}

// capturedDetail extracts the SandboxCreateDetail JSON from the last PutEvents call.
func capturedDetail(t *testing.T, fake *fakeEventBridgeClient) map[string]any {
	t.Helper()
	if fake.lastInput == nil || len(fake.lastInput.Entries) == 0 {
		t.Fatal("PutEvents was not called or has no entries")
	}
	detailStr := *fake.lastInput.Entries[0].Detail
	var detail map[string]any
	if err := json.Unmarshal([]byte(detailStr), &detail); err != nil {
		t.Fatalf("detail JSON is malformed: %v\nbody: %s", err, detailStr)
	}
	return detail
}

// ============================================================
// TestEventBridgeAdapter_SandboxID (GH-COLD-CREATE)
// ============================================================

// TestEventBridgeAdapter_SandboxID verifies that PutSandboxCreate emits a
// sandbox_id matching ^gh-[0-9a-f]{8}$ in the EventBridge detail JSON.
//
// TODAY: detail.sandbox_id = "" → this test is RED.
// AFTER 98-04: detail.sandbox_id = "gh-" + 8 hex chars → GREEN.
func TestEventBridgeAdapter_SandboxID(t *testing.T) {
	fake := &fakeEventBridgeClient{}
	adapter := &bridge.EventBridgeAdapter{
		Client:         fake,
		ArtifactBucket: "my-artifacts-bucket",
		ArtifactPrefix: "github-profiles",
	}

	err := adapter.PutSandboxCreate(context.Background(), "gh-shared", "github-review", `{"source":"github"}`)
	if err != nil {
		t.Fatalf("PutSandboxCreate returned error: %v", err)
	}

	detail := capturedDetail(t, fake)

	sandboxID, _ := detail["sandbox_id"].(string)
	if sandboxID == "" {
		t.Fatal("detail.sandbox_id is empty; want 'gh-' + 8 hex chars (98-04 must set this)")
	}

	pattern := regexp.MustCompile(`^gh-[0-9a-f]{8}$`)
	if !pattern.MatchString(sandboxID) {
		t.Errorf("detail.sandbox_id = %q; want pattern ^gh-[0-9a-f]{8}$", sandboxID)
	}
}

// ============================================================
// TestEventBridgeAdapter_ArtifactPrefix (GH-COLD-CREATE)
// ============================================================

// TestEventBridgeAdapter_ArtifactPrefix verifies that PutSandboxCreate sets
// detail.artifact_prefix = "github-profiles/" + profileSlug (no doubled path)
// and detail.artifact_bucket is non-empty.
//
// TODAY:
//   artifact_prefix = ArtifactPrefix + "/profiles/" + profile + ".yaml"
//   e.g. "github-profiles/profiles/github-review.yaml" — DOUBLED PATH (broken)
//
// AFTER 98-04:
//   artifact_prefix = "github-profiles/" + profileSlug
//   e.g. "github-profiles/github-review" — CORRECT.
func TestEventBridgeAdapter_ArtifactPrefix(t *testing.T) {
	fake := &fakeEventBridgeClient{}
	adapter := &bridge.EventBridgeAdapter{
		Client:         fake,
		ArtifactBucket: "my-artifacts-bucket",
		ArtifactPrefix: "github-profiles",
	}

	err := adapter.PutSandboxCreate(context.Background(), "gh-shared", "github-review", `{"source":"github"}`)
	if err != nil {
		t.Fatalf("PutSandboxCreate returned error: %v", err)
	}

	detail := capturedDetail(t, fake)

	// artifact_prefix must NOT contain the doubled "/profiles/" path segment.
	prefix, _ := detail["artifact_prefix"].(string)
	if prefix == "" {
		t.Fatal("detail.artifact_prefix is empty; want 'github-profiles/github-review'")
	}

	// The buggy path is "github-profiles/profiles/github-review.yaml".
	// The correct path is "github-profiles/github-review".
	if prefix == "github-profiles/profiles/github-review.yaml" || prefix == "" {
		t.Errorf("detail.artifact_prefix = %q; want 'github-profiles/github-review' (no /profiles/ doubling)", prefix)
	}

	// Must not end with .yaml (the prefix is a directory, not a file path).
	if len(prefix) > 5 && prefix[len(prefix)-5:] == ".yaml" {
		t.Errorf("detail.artifact_prefix = %q; must not end with .yaml (it is a prefix, not a file)", prefix)
	}

	// artifact_bucket must be non-empty.
	bucket, _ := detail["artifact_bucket"].(string)
	if bucket == "" {
		t.Error("detail.artifact_bucket is empty; want non-empty bucket name from EventBridgeAdapter.ArtifactBucket")
	}
	if bucket != "my-artifacts-bucket" {
		t.Errorf("detail.artifact_bucket = %q; want 'my-artifacts-bucket'", bucket)
	}
}

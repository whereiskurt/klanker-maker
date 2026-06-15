// aws_adapters_test.go — Phase 98 tests for EventBridgeAdapter (cold-create) and
// EC2Resumer (auto-resume Gap C: stopping-state tolerance).
// Also: Phase 99 gap-closure test for SSMCommandsFetcher (CommandSet envelope).
//
// BUILD TAG: phase98_wave0
// The EventBridgeAdapter tests were originally RED stubs; they pass since 98-04.
// The EC2Resumer tests (Gap C, 98-06 Task 3) are unconditionally included.
// The SSMCommandsFetcher test (SC3a gap closure, 2026-06-07) is unconditionally included.
package bridge_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
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
	// describeInputs records every DescribeInstancesInput so tests can assert the
	// filters (e.g. the tag key the resumer derived). Empty until DescribeInstances runs.
	describeInputs []*ec2.DescribeInstancesInput

	startCalled     bool
	startInstanceIDs []string
	startErr        error
}

func (f *fakeEC2Client) DescribeInstances(_ context.Context, params *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	f.describeInputs = append(f.describeInputs, params)
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
// TestEC2Resumer_NonKmPrefix_FiltersOnKmSandboxIdTag — non-"km" prefix regression
// ============================================================

// TestEC2Resumer_NonKmPrefix_FiltersOnKmSandboxIdTag is the regression for the
// 2026-06-12 incident on the `sec` install. On a non-"km" resource_prefix the resumer
// derived the EC2 tag key from ResourcePrefix ("sec:sandbox-id"), a tag km never
// applies — km always tags sandbox instances "km:sandbox-id" regardless of
// resource_prefix (the prefix lives in the separate "km:resource-prefix" tag). The
// filter matched nothing → StartSandbox returned ErrNoResumableInstance → the Phase-109
// self-heal needlessly deleted the alias row and cold-created a fresh sandbox even
// though the stopped instance was sitting there fully resumable.
//
// This reproduces the PRODUCTION wiring (ResourcePrefix set, SandboxIDTagKey empty),
// which the other resumer tests never exercise — they all set SandboxIDTagKey
// explicitly, hiding the buggy derivation branch.
func TestEC2Resumer_NonKmPrefix_FiltersOnKmSandboxIdTag(t *testing.T) {
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{
			singleReservation(makeInstance("i-stopped123", ec2types.InstanceStateNameStopped)),
		},
	}
	// Wire it exactly like cmd/km-github-bridge/main.go: ResourcePrefix set, no SandboxIDTagKey.
	resumer := &bridge.EC2Resumer{Client: fake, ResourcePrefix: "sec"}

	if err := resumer.StartSandbox(context.Background(), "gh-cc433b2e"); err != nil {
		t.Fatalf("StartSandbox on a non-km prefix install must resume the stopped instance, got: %v", err)
	}
	if !fake.startCalled {
		t.Fatal("StartInstances must be called — the stopped instance is resumable")
	}

	// The crux: the DescribeInstances filter must key on "tag:km:sandbox-id",
	// NOT "tag:sec:sandbox-id".
	if len(fake.describeInputs) == 0 {
		t.Fatal("DescribeInstances was never called")
	}
	var sandboxTagFilter string
	for _, fil := range fake.describeInputs[0].Filters {
		if fil.Name != nil && strings.HasSuffix(*fil.Name, ":sandbox-id") {
			sandboxTagFilter = *fil.Name
		}
	}
	if sandboxTagFilter != "tag:km:sandbox-id" {
		t.Errorf("resume filter tag key = %q; want \"tag:km:sandbox-id\" (km tags instances km:sandbox-id regardless of resource_prefix)", sandboxTagFilter)
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
// TestEC2Resumer_NoInstances_IsErrNoResumableInstance (Phase 109)
// ============================================================

// TestEC2Resumer_NoInstances_IsErrNoResumableInstance verifies that the
// "no stopped/stopping instances found" terminal failure wraps the exported
// ErrNoResumableInstance sentinel so the caller can branch with errors.Is and
// fall back to cold-create instead of enqueuing to a dead queue.
func TestEC2Resumer_NoInstances_IsErrNoResumableInstance(t *testing.T) {
	fake := &fakeEC2Client{
		describeResponses: []*ec2.DescribeInstancesOutput{emptyDescribe()},
	}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "sb-gone")
	if err == nil {
		t.Fatal("expected error when no resumable instances exist")
	}
	if !errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Errorf("errors.Is(err, ErrNoResumableInstance) = false; want true (err=%v)", err)
	}
	if fake.startCalled {
		t.Error("StartInstances must NOT be called when no instances found")
	}
}

// TestEC2Resumer_DescribeError_NotErrNoResumableInstance verifies that a
// transient DescribeInstances API error does NOT satisfy errors.Is for the
// sentinel — the caller must keep its log-and-enqueue (retry) behavior for it.
func TestEC2Resumer_DescribeError_NotErrNoResumableInstance(t *testing.T) {
	fake := &fakeEC2Client{describeErr: errors.New("AWS: RequestExpired")}
	resumer := &bridge.EC2Resumer{Client: fake, SandboxIDTagKey: "km:sandbox-id"}

	err := resumer.StartSandbox(context.Background(), "sb-err")
	if err == nil {
		t.Fatal("expected error from DescribeInstances API failure")
	}
	if errors.Is(err, bridge.ErrNoResumableInstance) {
		t.Error("a transient DescribeInstances error must NOT match ErrNoResumableInstance")
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

// ============================================================
// Fake SSM client for SSMCommandsFetcher tests
// ============================================================

// fakeSSMCommandsClient is a minimal SecretSSMClient for testing SSMCommandsFetcher.
type fakeSSMCommandsClient struct {
	value string // pre-seeded SSM parameter value (empty string = absent)
	err   error  // non-nil = return this error from GetParameter
}

func (f *fakeSSMCommandsClient) GetParameter(_ context.Context, input *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.value == "" {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	name := awssdk.ToString(input.Name)
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Name:  awssdk.String(name),
			Value: awssdk.String(f.value),
		},
	}, nil
}

// Compile-time check: fakeSSMCommandsClient satisfies bridge.SecretSSMClient.
// (bridge.SecretSSMClient is unexported by name; we verify via SSMCommandsFetcher.Client.)
var _ = &bridge.SSMCommandsFetcher{Client: (*fakeSSMCommandsClient)(nil)}

// ============================================================
// TestSSMCommandsFetcher_ParsesEnvelope — SC3a gap closure test
// ============================================================

// TestSSMCommandsFetcher_ParsesEnvelope verifies that SSMCommandsFetcher parses the
// CommandSet envelope and returns both the command map AND the install-wide
// default_command. This is the regression guard for Phase 99 gap SC3a: previously
// default_command was read from KM_GITHUB_DEFAULT_COMMAND env (which nothing ever
// wrote), so WebhookHandler.DefaultCommand was always "" at runtime.
func TestSSMCommandsFetcher_ParsesEnvelope(t *testing.T) {
	// Build a CommandSet envelope — this is what km init now writes to SSM.
	envelope := `{"commands":{"review":{"description":"Review PR","prompt":"Please review: {{args}}"},"patch":{"prompt":"Apply fix"}},"default_command":"review"}`

	fake := &fakeSSMCommandsClient{value: envelope}
	fetcher := &bridge.SSMCommandsFetcher{
		Client:   fake,
		Path:     "/km/config/github/commands",
		CacheTTL: time.Minute,
	}

	cmds, defaultCmd, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}

	// Install-wide default_command must be returned (SC3a fix).
	if defaultCmd != "review" {
		t.Errorf("defaultCmd = %q; want %q (SC3a gap: default_command was silently dropped)", defaultCmd, "review")
	}

	// Command map must be populated.
	if len(cmds) != 2 {
		t.Errorf("len(cmds) = %d; want 2", len(cmds))
	}
	if _, ok := cmds["review"]; !ok {
		t.Error("cmds missing 'review' key")
	}
	if _, ok := cmds["patch"]; !ok {
		t.Error("cmds missing 'patch' key")
	}
}

// TestSSMCommandsFetcher_DecodesBase64 verifies the production storage format:
// km init base64-encodes the CommandSet JSON before writing to SSM (because SSM
// rejects any value containing "{{...}}", and templates use the {{args}}
// placeholder). The fetcher must base64-decode before unmarshaling — and the
// {{args}} placeholder must survive the round-trip intact.
func TestSSMCommandsFetcher_DecodesBase64(t *testing.T) {
	jsonEnvelope := `{"commands":{"review":{"description":"Review PR","prompt":"Please review: {{args}}"}},"default_command":"review"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(jsonEnvelope))

	fake := &fakeSSMCommandsClient{value: encoded}
	fetcher := &bridge.SSMCommandsFetcher{
		Client:   fake,
		Path:     "/km/config/github/commands",
		CacheTTL: time.Minute,
	}

	cmds, defaultCmd, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch returned error on base64-encoded value: %v", err)
	}
	if defaultCmd != "review" {
		t.Errorf("defaultCmd = %q; want %q", defaultCmd, "review")
	}
	review, ok := cmds["review"]
	if !ok {
		t.Fatal("cmds missing 'review' key after base64 decode")
	}
	// The {{args}} placeholder — the reason we base64-encode — must be intact.
	if !strings.Contains(review.Prompt, "{{args}}") {
		t.Errorf("review.prompt lost the {{args}} placeholder: %q", review.Prompt)
	}
}

// TestSSMCommandsFetcher_Dormant verifies the dormant-by-default behavior:
// when the SSM parameter is absent, Fetch returns an empty map and "" default
// (not an error). This is the Phase 98 byte-identity invariant.
func TestSSMCommandsFetcher_Dormant(t *testing.T) {
	fake := &fakeSSMCommandsClient{value: ""} // ParameterNotFound
	fetcher := &bridge.SSMCommandsFetcher{
		Client:   fake,
		Path:     "/km/config/github/commands",
		CacheTTL: time.Minute,
	}

	cmds, defaultCmd, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch returned error on ParameterNotFound; want nil (dormant): %v", err)
	}
	if len(cmds) != 0 {
		t.Errorf("cmds non-empty on dormant path; want empty map, got %v", cmds)
	}
	if defaultCmd != "" {
		t.Errorf("defaultCmd = %q on dormant path; want \"\"", defaultCmd)
	}
}

// TestSSMCommandsFetcher_SSMError verifies that real SSM errors (other than
// ParameterNotFound) are returned to the caller (not swallowed as dormant).
func TestSSMCommandsFetcher_SSMError(t *testing.T) {
	fake := &fakeSSMCommandsClient{err: fmt.Errorf("SSM: ThrottlingException")}
	fetcher := &bridge.SSMCommandsFetcher{
		Client:   fake,
		Path:     "/km/config/github/commands",
		CacheTTL: time.Minute,
	}

	_, _, err := fetcher.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected error from SSM ThrottlingException, got nil")
	}
}

// TestSSMCommandsFetcher_NoDefaultCommand verifies that when the envelope has no
// default_command (omitempty), Fetch returns "" for the default without error.
func TestSSMCommandsFetcher_NoDefaultCommand(t *testing.T) {
	// Envelope with commands but no default_command field.
	envelope := `{"commands":{"review":{"prompt":"Review PR"}}}`
	fake := &fakeSSMCommandsClient{value: envelope}
	fetcher := &bridge.SSMCommandsFetcher{
		Client:   fake,
		Path:     "/km/config/github/commands",
		CacheTTL: time.Minute,
	}

	cmds, defaultCmd, err := fetcher.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if defaultCmd != "" {
		t.Errorf("defaultCmd = %q; want \"\" when envelope has no default_command", defaultCmd)
	}
	if len(cmds) != 1 {
		t.Errorf("len(cmds) = %d; want 1", len(cmds))
	}
}

package main

// Tests for saveExecTraceBeforeTeardownWith / findRunningInstanceIDForExecTrace
// — the fix-save-before-destroy defect: km-execlog.service's ExecStopPost
// cannot upload the exec trace during `km destroy` (TerminateInstances'
// shorter grace window, and/or terraform destroy removing the instance's
// networking alongside it), so the save must happen over SSM while the
// instance is still alive, before either TeardownFunc or SDKTeardownFunc
// runs. See CLAUDE.md and the PR body for the live before/after S3 evidence.

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	ec2pkg "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeExecTraceEC2Client is a hand-rolled fake satisfying execTraceEC2API —
// no real AWS calls.
type fakeExecTraceEC2Client struct {
	out *ec2pkg.DescribeInstancesOutput
	err error
}

func (f *fakeExecTraceEC2Client) DescribeInstances(_ context.Context, _ *ec2pkg.DescribeInstancesInput, _ ...func(*ec2pkg.Options)) (*ec2pkg.DescribeInstancesOutput, error) {
	return f.out, f.err
}

// fakeExecTraceSSMClient is a hand-rolled fake satisfying awspkg.SSMCommandRunner.
type fakeExecTraceSSMClient struct {
	sendErr   error
	sendCalls int
	status    ssmtypes.CommandInvocationStatus
}

func (f *fakeExecTraceSSMClient) SendCommand(_ context.Context, _ *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	f.sendCalls++
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &ssm.SendCommandOutput{Command: &ssmtypes.Command{CommandId: awssdk.String("cmd-1")}}, nil
}

func (f *fakeExecTraceSSMClient) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{
		Status:               f.status,
		StandardErrorContent: awssdk.String("boom"),
	}, nil
}

func execTraceHandlerInstancesOutput(ids ...string) *ec2pkg.DescribeInstancesOutput {
	var instances []ec2types.Instance
	for _, id := range ids {
		instances = append(instances, ec2types.Instance{InstanceId: awssdk.String(id)})
	}
	return &ec2pkg.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: instances}},
	}
}

func TestFindRunningInstanceIDForExecTrace_ReturnsMatch(t *testing.T) {
	fake := &fakeExecTraceEC2Client{out: execTraceHandlerInstancesOutput("i-abc123")}
	id, err := findRunningInstanceIDForExecTrace(context.Background(), fake, "sb-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "i-abc123" {
		t.Fatalf("expected i-abc123, got %q", id)
	}
}

func TestFindRunningInstanceIDForExecTrace_NoneRunningIsNotAnError(t *testing.T) {
	fake := &fakeExecTraceEC2Client{out: execTraceHandlerInstancesOutput()}
	id, err := findRunningInstanceIDForExecTrace(context.Background(), fake, "sb-abc123")
	if err != nil {
		t.Fatalf("no running instance must not be an error, got: %v", err)
	}
	if id != "" {
		t.Fatalf("expected empty instance id, got %q", id)
	}
}

func TestFindRunningInstanceIDForExecTrace_DescribeErrorIsPropagated(t *testing.T) {
	fake := &fakeExecTraceEC2Client{err: errors.New("describe boom")}
	_, err := findRunningInstanceIDForExecTrace(context.Background(), fake, "sb-abc123")
	if err == nil {
		t.Fatal("expected the DescribeInstances error to propagate")
	}
}

// TestSaveExecTraceBeforeTeardownWith_SuccessCallsSSMOnce verifies the happy
// path: a running instance is found and the SSM save is invoked exactly once.
func TestSaveExecTraceBeforeTeardownWith_SuccessCallsSSMOnce(t *testing.T) {
	ec2Fake := &fakeExecTraceEC2Client{out: execTraceHandlerInstancesOutput("i-abc123")}
	ssmFake := &fakeExecTraceSSMClient{status: ssmtypes.CommandInvocationStatusSuccess}

	saveExecTraceBeforeTeardownWith(context.Background(), ec2Fake, ssmFake, "sb-abc123")

	if ssmFake.sendCalls != 1 {
		t.Fatalf("expected exactly one SendCommand call, got %d", ssmFake.sendCalls)
	}
}

// TestSaveExecTraceBeforeTeardownWith_SSMErrorIsSwallowed verifies that an
// SSM failure never propagates — this runs on the destroy/TTL-expiry/idle
// hot path and must never turn a save failure into a blocked or failed
// teardown.
func TestSaveExecTraceBeforeTeardownWith_SSMErrorIsSwallowed(t *testing.T) {
	ec2Fake := &fakeExecTraceEC2Client{out: execTraceHandlerInstancesOutput("i-abc123")}
	ssmFake := &fakeExecTraceSSMClient{sendErr: errors.New("ssm boom")}

	// Must not panic and must return normally — there is no error return to
	// check, which is itself the point: the only way to observe failure is a
	// log line, and the caller (handleDestroy) proceeds regardless.
	saveExecTraceBeforeTeardownWith(context.Background(), ec2Fake, ssmFake, "sb-abc123")

	if ssmFake.sendCalls != 1 {
		t.Fatalf("expected SendCommand to have been attempted once, got %d", ssmFake.sendCalls)
	}
}

// TestSaveExecTraceBeforeTeardownWith_NoRunningInstanceSkipsSSM verifies the
// "instance already gone/stopped/never existed" case — the ordinary shape
// for a TTL expiry or idle reap of an already-stopped or orphaned sandbox —
// never calls SSM at all and never errors.
func TestSaveExecTraceBeforeTeardownWith_NoRunningInstanceSkipsSSM(t *testing.T) {
	ec2Fake := &fakeExecTraceEC2Client{out: execTraceHandlerInstancesOutput()}
	ssmFake := &fakeExecTraceSSMClient{status: ssmtypes.CommandInvocationStatusSuccess}

	saveExecTraceBeforeTeardownWith(context.Background(), ec2Fake, ssmFake, "sb-abc123")

	if ssmFake.sendCalls != 0 {
		t.Fatalf("expected SendCommand to never be called when no instance is running, got %d calls", ssmFake.sendCalls)
	}
}

// TestSaveExecTraceBeforeTeardownWith_DescribeErrorSkipsSSMWithoutPanicking
// verifies a DescribeInstances failure (e.g. a transient AWS error) degrades
// to a no-op rather than blocking or panicking.
func TestSaveExecTraceBeforeTeardownWith_DescribeErrorSkipsSSMWithoutPanicking(t *testing.T) {
	ec2Fake := &fakeExecTraceEC2Client{err: errors.New("describe boom")}
	ssmFake := &fakeExecTraceSSMClient{status: ssmtypes.CommandInvocationStatusSuccess}

	saveExecTraceBeforeTeardownWith(context.Background(), ec2Fake, ssmFake, "sb-abc123")

	if ssmFake.sendCalls != 0 {
		t.Fatalf("expected SendCommand to never be called when instance lookup fails, got %d calls", ssmFake.sendCalls)
	}
}

// TestHandleTTLEvent_DestroySkipsSaveExecTraceWhenFuncIsNil confirms the
// nil-SaveExecTraceFunc default used throughout the existing test suite
// (mirroring the nil-TeardownFunc convention) never touches AWS: a
// TTLHandler built without setting SaveExecTraceFunc must destroy cleanly
// with no panic and no attempt to call it.
func TestHandleTTLEvent_DestroySkipsSaveExecTraceWhenFuncIsNil(t *testing.T) {
	h := &TTLHandler{
		S3Client:      &mockS3GetPutAPI{},
		SESClient:     &mockSESAPI{},
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
		TeardownFunc:  func(ctx context.Context, sandboxID string) error { return nil },
		// SaveExecTraceFunc intentionally left nil.
	}
	if err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"}); err != nil {
		t.Fatalf("unexpected error with nil SaveExecTraceFunc: %v", err)
	}
}

// TestHandleTTLEvent_DestroyInvokesSaveExecTraceFuncBeforeTeardown confirms
// Step 5b actually runs (when wired) and runs before Step 6's teardown.
func TestHandleTTLEvent_DestroyInvokesSaveExecTraceFuncBeforeTeardown(t *testing.T) {
	var order []string
	h := &TTLHandler{
		S3Client:      &mockS3GetPutAPI{},
		SESClient:     &mockSESAPI{},
		Scheduler:     &mockSchedulerAPI{},
		Bucket:        "test-bucket",
		OperatorEmail: "",
		Domain:        "sandboxes.klankermaker.ai",
		SaveExecTraceFunc: func(ctx context.Context, sandboxID string) {
			order = append(order, "save-exec-trace")
		},
		TeardownFunc: func(ctx context.Context, sandboxID string) error {
			order = append(order, "teardown")
			return nil
		},
	}
	if err := h.HandleTTLEvent(context.Background(), TTLEvent{SandboxID: "sb-aabbccdd"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "save-exec-trace" || order[1] != "teardown" {
		t.Fatalf("expected [save-exec-trace teardown] in order, got %v", order)
	}
}

package cmd

// Tests for saveExecTraceBeforeDestroy / findRunningInstanceID — the
// fix-save-before-destroy defect: km-execlog.service's ExecStopPost cannot
// upload the exec trace during `km destroy` (TerminateInstances' shorter
// grace window, and/or terraform destroy removing the instance's networking
// alongside it), so the save must happen over SSM while the instance is
// still alive, before any teardown begins. See CLAUDE.md and the PR body for
// the live before/after S3 evidence.

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeExecTraceEC2 is a hand-rolled fake satisfying execTraceEC2API — no real
// AWS calls.
type fakeExecTraceEC2 struct {
	out *ec2.DescribeInstancesOutput
	err error
}

func (f *fakeExecTraceEC2) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return f.out, f.err
}

// fakeExecTraceSSM is a hand-rolled fake satisfying awspkg.SSMCommandRunner.
type fakeExecTraceSSM struct {
	sendErr   error
	sendCalls int
	status    ssmtypes.CommandInvocationStatus
}

func (f *fakeExecTraceSSM) SendCommand(_ context.Context, _ *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	f.sendCalls++
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &ssm.SendCommandOutput{Command: &ssmtypes.Command{CommandId: aws.String("cmd-1")}}, nil
}

func (f *fakeExecTraceSSM) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{
		Status:               f.status,
		StandardErrorContent: aws.String("boom"),
	}, nil
}

func execTraceInstancesOutput(ids ...string) *ec2.DescribeInstancesOutput {
	var instances []ec2types.Instance
	for _, id := range ids {
		instances = append(instances, ec2types.Instance{InstanceId: aws.String(id)})
	}
	return &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{Instances: instances}},
	}
}

func TestFindRunningInstanceID_ReturnsFirstMatch(t *testing.T) {
	fake := &fakeExecTraceEC2{out: execTraceInstancesOutput("i-abc123")}
	id, err := findRunningInstanceID(context.Background(), fake, "sb-abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "i-abc123" {
		t.Fatalf("expected i-abc123, got %q", id)
	}
}

func TestFindRunningInstanceID_NoneRunningIsNotAnError(t *testing.T) {
	fake := &fakeExecTraceEC2{out: execTraceInstancesOutput()}
	id, err := findRunningInstanceID(context.Background(), fake, "sb-abc123")
	if err != nil {
		t.Fatalf("no running instance must not be an error, got: %v", err)
	}
	if id != "" {
		t.Fatalf("expected empty instance id, got %q", id)
	}
}

func TestFindRunningInstanceID_DescribeErrorIsPropagated(t *testing.T) {
	fake := &fakeExecTraceEC2{err: errors.New("describe boom")}
	_, err := findRunningInstanceID(context.Background(), fake, "sb-abc123")
	if err == nil {
		t.Fatal("expected the DescribeInstances error to propagate")
	}
}

// TestSaveExecTraceBeforeDestroy_SuccessLogsAndCallsSSM verifies the happy
// path: a running instance is found and the SSM save is invoked exactly once.
// (Success is observed via the SSM fake's call count — saveExecTraceBeforeDestroy
// is void by design, matching deleteSopsBundleNonFatal's shape, since a
// best-effort step has nothing useful to report to its caller.)
func TestSaveExecTraceBeforeDestroy_SuccessLogsAndCallsSSM(t *testing.T) {
	ec2Fake := &fakeExecTraceEC2{out: execTraceInstancesOutput("i-abc123")}
	ssmFake := &fakeExecTraceSSM{status: ssmtypes.CommandInvocationStatusSuccess}

	saveExecTraceBeforeDestroy(context.Background(), ec2Fake, ssmFake, "sb-abc123")

	if ssmFake.sendCalls != 1 {
		t.Fatalf("expected exactly one SendCommand call, got %d", ssmFake.sendCalls)
	}
}

// TestSaveExecTraceBeforeDestroy_SSMErrorIsSwallowed verifies that an SSM
// failure never propagates — this function is called from the destroy hot
// path and must never turn a save failure into a blocked/failed teardown.
func TestSaveExecTraceBeforeDestroy_SSMErrorIsSwallowed(t *testing.T) {
	ec2Fake := &fakeExecTraceEC2{out: execTraceInstancesOutput("i-abc123")}
	ssmFake := &fakeExecTraceSSM{sendErr: errors.New("ssm boom")}

	// Must not panic and must return normally — there is no error return to
	// check, which is itself the point: there is no way for a caller to
	// observe failure other than a log line.
	saveExecTraceBeforeDestroy(context.Background(), ec2Fake, ssmFake, "sb-abc123")

	if ssmFake.sendCalls != 1 {
		t.Fatalf("expected SendCommand to have been attempted once, got %d", ssmFake.sendCalls)
	}
}

// TestSaveExecTraceBeforeDestroy_NoRunningInstanceSkipsSSM verifies the
// "instance already gone/stopped/never existed" case — the normal shape for
// most teardowns — never calls SSM at all and never errors.
func TestSaveExecTraceBeforeDestroy_NoRunningInstanceSkipsSSM(t *testing.T) {
	ec2Fake := &fakeExecTraceEC2{out: execTraceInstancesOutput()}
	ssmFake := &fakeExecTraceSSM{status: ssmtypes.CommandInvocationStatusSuccess}

	saveExecTraceBeforeDestroy(context.Background(), ec2Fake, ssmFake, "sb-abc123")

	if ssmFake.sendCalls != 0 {
		t.Fatalf("expected SendCommand to never be called when no instance is running, got %d calls", ssmFake.sendCalls)
	}
}

// TestSaveExecTraceBeforeDestroy_DescribeErrorSkipsSSMWithoutPanicking
// verifies a DescribeInstances failure (e.g. transient AWS error) degrades to
// a no-op rather than blocking or panicking.
func TestSaveExecTraceBeforeDestroy_DescribeErrorSkipsSSMWithoutPanicking(t *testing.T) {
	ec2Fake := &fakeExecTraceEC2{err: errors.New("describe boom")}
	ssmFake := &fakeExecTraceSSM{status: ssmtypes.CommandInvocationStatusSuccess}

	saveExecTraceBeforeDestroy(context.Background(), ec2Fake, ssmFake, "sb-abc123")

	if ssmFake.sendCalls != 0 {
		t.Fatalf("expected SendCommand to never be called when instance lookup fails, got %d calls", ssmFake.sendCalls)
	}
}

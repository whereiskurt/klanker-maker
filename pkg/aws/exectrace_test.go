package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeSSMCommandRunner is a hand-rolled fake — no real AWS calls — that lets
// each test script exactly what SendCommand/GetCommandInvocation should do.
type fakeSSMCommandRunner struct {
	sendErr      error
	sendCalls    int
	lastCommand  string                             // the "commands" parameter sent on the most recent SendCommand call
	invocations  []ssmtypes.CommandInvocationStatus // returned in order, one per GetCommandInvocation call
	invErr       error                              // if set, every GetCommandInvocation call fails with this
	getCallCount int
}

func (f *fakeSSMCommandRunner) SendCommand(ctx context.Context, params *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	f.sendCalls++
	if cmds := params.Parameters["commands"]; len(cmds) > 0 {
		f.lastCommand = cmds[0]
	}
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &ssm.SendCommandOutput{
		Command: &ssmtypes.Command{CommandId: aws.String("cmd-123")},
	}, nil
}

func (f *fakeSSMCommandRunner) GetCommandInvocation(ctx context.Context, params *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	if f.invErr != nil {
		return nil, f.invErr
	}
	idx := f.getCallCount
	if idx >= len(f.invocations) {
		idx = len(f.invocations) - 1
	}
	f.getCallCount++
	status := f.invocations[idx]
	return &ssm.GetCommandInvocationOutput{
		Status:                status,
		StandardErrorContent:  aws.String("boom"),
		StandardOutputContent: aws.String(""),
	}, nil
}

func TestSaveExecTraceOverSSM_NoInstanceIsANoOp(t *testing.T) {
	f := &fakeSSMCommandRunner{}
	if err := SaveExecTraceOverSSM(context.Background(), f, ""); err != nil {
		t.Fatalf("empty instance id must be a no-op, got: %v", err)
	}
	if f.sendCalls != 0 {
		t.Fatalf("SendCommand must not be called with an empty instance id, got %d calls", f.sendCalls)
	}
}

func TestSaveExecTraceOverSSM_NilClientIsANoOp(t *testing.T) {
	if err := SaveExecTraceOverSSM(context.Background(), nil, "i-abc123"); err != nil {
		t.Fatalf("nil client must be a no-op, got: %v", err)
	}
}

func TestSaveExecTraceOverSSM_SuccessOnFirstPoll(t *testing.T) {
	orig := execTracePollInterval
	execTracePollInterval = time.Millisecond
	defer func() { execTracePollInterval = orig }()

	f := &fakeSSMCommandRunner{invocations: []ssmtypes.CommandInvocationStatus{ssmtypes.CommandInvocationStatusSuccess}}
	if err := SaveExecTraceOverSSM(context.Background(), f, "i-abc123"); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if f.sendCalls != 1 {
		t.Fatalf("expected exactly one SendCommand call, got %d", f.sendCalls)
	}
}

func TestSaveExecTraceOverSSM_SucceedsAfterPending(t *testing.T) {
	orig := execTracePollInterval
	execTracePollInterval = time.Millisecond
	defer func() { execTracePollInterval = orig }()

	f := &fakeSSMCommandRunner{invocations: []ssmtypes.CommandInvocationStatus{
		ssmtypes.CommandInvocationStatusPending,
		ssmtypes.CommandInvocationStatusInProgress,
		ssmtypes.CommandInvocationStatusSuccess,
	}}
	if err := SaveExecTraceOverSSM(context.Background(), f, "i-abc123"); err != nil {
		t.Fatalf("expected eventual success, got: %v", err)
	}
}

func TestSaveExecTraceOverSSM_SendCommandErrorIsReturned(t *testing.T) {
	f := &fakeSSMCommandRunner{sendErr: errors.New("send boom")}
	err := SaveExecTraceOverSSM(context.Background(), f, "i-abc123")
	if err == nil {
		t.Fatal("expected an error when SendCommand fails")
	}
	if !strings.Contains(err.Error(), "send boom") {
		t.Fatalf("expected wrapped send error, got: %v", err)
	}
}

func TestSaveExecTraceOverSSM_FailedInvocationIsReported(t *testing.T) {
	orig := execTracePollInterval
	execTracePollInterval = time.Millisecond
	defer func() { execTracePollInterval = orig }()

	f := &fakeSSMCommandRunner{invocations: []ssmtypes.CommandInvocationStatus{ssmtypes.CommandInvocationStatusFailed}}
	err := SaveExecTraceOverSSM(context.Background(), f, "i-abc123")
	if err == nil {
		t.Fatal("expected an error when the invocation fails")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the stderr content in the error, got: %v", err)
	}
}

func TestSaveExecTraceOverSSM_DeadlineExceededIsReturnedNotBlocked(t *testing.T) {
	orig := execTracePollInterval
	execTracePollInterval = time.Millisecond
	defer func() { execTracePollInterval = orig }()

	f := &fakeSSMCommandRunner{invocations: []ssmtypes.CommandInvocationStatus{ssmtypes.CommandInvocationStatusInProgress}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := SaveExecTraceOverSSM(ctx, f, "i-abc123")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a deadline error, got nil")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("SaveExecTraceOverSSM must respect ctx deadline promptly, took %v", elapsed)
	}
}

// TestSaveExecTraceOverSSM_CommandSourcesNetpolicyEnvForRegion pins the
// belt-and-braces fix for the region bug: AWS-RunShellScript invokes a bare,
// non-login shell that never sources /etc/profile.d (where a root login gets
// AWS_DEFAULT_REGION from), so the SSM command itself must source
// /etc/km/netpolicy.env — where the region now lives — before invoking the
// verb, rather than depending on ambient environment the shell was never
// going to have.
func TestSaveExecTraceOverSSM_CommandSourcesNetpolicyEnvForRegion(t *testing.T) {
	f := &fakeSSMCommandRunner{invocations: []ssmtypes.CommandInvocationStatus{ssmtypes.CommandInvocationStatusSuccess}}
	if err := SaveExecTraceOverSSM(context.Background(), f, "i-abc123"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(f.lastCommand, "/etc/km/netpolicy.env") {
		t.Fatalf("command must source /etc/km/netpolicy.env before invoking the verb, got: %q", f.lastCommand)
	}
	sourceIdx := strings.Index(f.lastCommand, "/etc/km/netpolicy.env")
	verbIdx := strings.Index(f.lastCommand, "km-netpolicy execs save")
	if verbIdx < 0 {
		t.Fatalf("command must invoke 'km-netpolicy execs save', got: %q", f.lastCommand)
	}
	if sourceIdx > verbIdx {
		t.Fatalf("the env file must be sourced BEFORE the verb runs, got: %q", f.lastCommand)
	}
}

package cmd_test

import (
	"context"
	"encoding/base64"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- Mock SSM ----

type mockAgentSSM struct {
	sendCalls    []ssm.SendCommandInput
	sendErr      error
	sendOutput   *ssm.SendCommandOutput
	invocations  []*ssm.GetCommandInvocationOutput
	invIdx       int
	invErr       error
}

func (m *mockAgentSSM) SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	m.sendCalls = append(m.sendCalls, *input)
	if m.sendOutput != nil {
		return m.sendOutput, nil
	}
	return &ssm.SendCommandOutput{
		Command: &ssmtypes.Command{
			CommandId: awssdk.String("cmd-test-123"),
		},
	}, nil
}

func (m *mockAgentSSM) GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	if m.invErr != nil {
		return nil, m.invErr
	}
	if m.invIdx < len(m.invocations) {
		out := m.invocations[m.invIdx]
		m.invIdx++
		return out, nil
	}
	return &ssm.GetCommandInvocationOutput{
		Status:                ssmtypes.CommandInvocationStatusSuccess,
		StandardOutputContent: awssdk.String("KM_RUN_ID=20260410T120000Z"),
	}, nil
}

// ---- Mock EventBridge ----

type mockAgentEB struct {
	putCalls int64 // atomic for goroutine safety
	putErr   error
}

func (m *mockAgentEB) PutEvents(ctx context.Context, params *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error) {
	if m.putErr != nil {
		return nil, m.putErr
	}
	atomic.AddInt64(&m.putCalls, 1)
	return &eventbridge.PutEventsOutput{}, nil
}

// Compile-time checks
var _ cmd.SSMSendAPI = (*mockAgentSSM)(nil)
var _ kmaws.EventBridgeAPI = (*mockAgentEB)(nil)

// ---- Test helpers ----

func newRunningEC2Sandbox(id string) *fakeFetcher {
	return &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: id,
			Profile:   "open-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc123def456",
			},
		},
	}
}

// ---- Tests ----

// TestAgentNonInteractive_SendCommand verifies that SSM SendCommand is called
// (not start-session) when --prompt is used via the run subcommand.
func TestAgentNonInteractive_SendCommand(t *testing.T) {
	fetcher := newRunningEC2Sandbox("sb-test01")
	mockSSM := &mockAgentSSM{}
	mockEB := &mockAgentEB{}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "run", "sb-test01", "--prompt", "fix the tests"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(mockSSM.sendCalls) != 1 {
		t.Fatalf("expected 1 SendCommand call, got %d", len(mockSSM.sendCalls))
	}

	// Verify SendCommand was used (not start-session)
	call := mockSSM.sendCalls[0]
	if awssdk.ToString(call.DocumentName) != "AWS-RunShellScript" {
		t.Errorf("expected document AWS-RunShellScript, got %s", awssdk.ToString(call.DocumentName))
	}
	if len(call.InstanceIds) != 1 || call.InstanceIds[0] != "i-0abc123def456" {
		t.Errorf("expected instance ID i-0abc123def456, got %v", call.InstanceIds)
	}
}

// TestAgentNonInteractive_CommandConstruction verifies the shell command contains
// the correct Claude flags and base64-encoded prompt.
func TestAgentNonInteractive_CommandConstruction(t *testing.T) {
	prompt := "fix the failing tests"

	shellCmd := cmd.BuildAgentShellCommand(prompt)

	// Verify required Claude flags (AGENT-02)
	requiredParts := []string{
		"claude -p",
		"--output-format json",
		"--dangerously-skip-permissions",
		"--bare",
	}
	for _, part := range requiredParts {
		if !strings.Contains(shellCmd, part) {
			t.Errorf("shell command missing %q", part)
		}
	}

	// Verify base64-encoded prompt is present (AGENT-03)
	b64 := base64.StdEncoding.EncodeToString([]byte(prompt))
	if !strings.Contains(shellCmd, b64) {
		t.Errorf("shell command missing base64-encoded prompt %q", b64)
	}

	// Verify run directory setup
	if !strings.Contains(shellCmd, "/workspace/.km-agent/runs/") {
		t.Error("shell command missing run directory path")
	}
	if !strings.Contains(shellCmd, "KM_RUN_ID=") {
		t.Error("shell command missing KM_RUN_ID output")
	}
}

// TestAgentNonInteractive_PromptEscaping verifies that prompts with dangerous
// characters are base64-encoded and NOT interpolated directly into the shell.
func TestAgentNonInteractive_PromptEscaping(t *testing.T) {
	dangerous := []string{
		`'; rm -rf /; echo '`,
		"`whoami`",
		`$(cat /etc/passwd)`,
		`"double quotes" and 'single quotes'`,
		"newline\ninjection",
		`backslash \ test`,
	}

	for _, prompt := range dangerous {
		t.Run(prompt[:min(len(prompt), 20)], func(t *testing.T) {
			shellCmd := cmd.BuildAgentShellCommand(prompt)

			// The raw prompt text must NOT appear in the shell command
			if strings.Contains(shellCmd, prompt) {
				t.Errorf("dangerous prompt appears literally in shell command: %q", prompt)
			}

			// The base64-encoded version MUST appear
			b64 := base64.StdEncoding.EncodeToString([]byte(prompt))
			if !strings.Contains(shellCmd, b64) {
				t.Errorf("base64-encoded prompt missing from shell command")
			}
		})
	}
}

// TestAgentNonInteractive_IdleReset verifies that EventBridge extend events are
// published during --wait polling (AGENT-06).
func TestAgentNonInteractive_IdleReset(t *testing.T) {
	// Set very short intervals for testing
	origHeartbeat := cmd.AgentIdleResetInterval
	origPoll := cmd.AgentPollInterval
	cmd.AgentIdleResetInterval = 50 * time.Millisecond
	cmd.AgentPollInterval = 50 * time.Millisecond
	defer func() {
		cmd.AgentIdleResetInterval = origHeartbeat
		cmd.AgentPollInterval = origPoll
	}()

	fetcher := newRunningEC2Sandbox("sb-heartbeat")
	mockEB := &mockAgentEB{}

	// Return InProgress twice, then Success -- each poll waits 10s so we override
	// the poll interval by having the invocations complete after a brief delay
	mockSSM := &mockAgentSSM{
		invocations: []*ssm.GetCommandInvocationOutput{
			{Status: ssmtypes.CommandInvocationStatusInProgress},
			{Status: ssmtypes.CommandInvocationStatusInProgress},
			{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: awssdk.String("KM_RUN_ID=20260410T120000Z"),
			},
		},
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "run", "sb-heartbeat", "--prompt", "test heartbeat", "--wait"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// With 50ms heartbeat and polling delay, we should see at least 1 heartbeat
	calls := atomic.LoadInt64(&mockEB.putCalls)
	if calls < 1 {
		t.Errorf("expected at least 1 EventBridge heartbeat call, got %d", calls)
	}
}

// TestAgentNonInteractive_StoppedSandbox verifies that a stopped sandbox returns
// an error before any SSM command is sent (AGENT-08).
func TestAgentNonInteractive_StoppedSandbox(t *testing.T) {
	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-stopped",
			Profile:   "open-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "stopped",
			CreatedAt: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc123def456",
			},
		},
	}
	mockSSM := &mockAgentSSM{}
	mockEB := &mockAgentEB{}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "run", "sb-stopped", "--prompt", "do something"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for stopped sandbox, got nil")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Errorf("expected error to contain 'stopped', got: %v", err)
	}

	// Verify no SSM commands were sent
	if len(mockSSM.sendCalls) != 0 {
		t.Errorf("expected 0 SendCommand calls for stopped sandbox, got %d", len(mockSSM.sendCalls))
	}
}

// TestAgentCmd_BackwardCompat verifies that `km agent <sandbox-id> --claude`
// still dispatches to the interactive path (start-session, not SendCommand).
func TestAgentCmd_BackwardCompat(t *testing.T) {
	fetcher := newRunningEC2Sandbox("sb-compat")
	mockSSM := &mockAgentSSM{}
	mockEB := &mockAgentEB{}

	var capturedArgs []string
	execFn := func(c *exec.Cmd) error {
		capturedArgs = c.Args
		return nil
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, execFn, mockSSM, mockEB)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "sb-compat", "--claude"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify interactive path was used (start-session, not SendCommand)
	if len(mockSSM.sendCalls) != 0 {
		t.Errorf("expected 0 SendCommand calls for interactive mode, got %d", len(mockSSM.sendCalls))
	}

	fullCmd := strings.Join(capturedArgs, " ")
	if !strings.Contains(fullCmd, "start-session") {
		t.Errorf("expected 'start-session' in command for interactive mode, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "claude") {
		t.Errorf("expected 'claude' in command args, got: %s", fullCmd)
	}
}

// min is provided by email_test.go in this package

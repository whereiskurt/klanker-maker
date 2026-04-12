package cmd_test

import (
	"context"
	"encoding/base64"
	"os/exec"
	"regexp"
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
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB, nil)
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

	cmds, _ := cmd.BuildAgentShellCommands(prompt, "")
	shellCmd := strings.Join(cmds, "\n")

	// Verify required Claude flags (AGENT-02)
	requiredParts := []string{
		"claude -p",
		"--output-format json",
		"--dangerously-skip-permissions",
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

// TestAgentNonInteractive_NoBedrock verifies that --no-bedrock injects unset commands.
func TestAgentNonInteractive_NoBedrock(t *testing.T) {
	// Without --no-bedrock: no unset commands
	cmdsNoBR, _ := cmd.BuildAgentShellCommands("test prompt", "")
	shellCmd := strings.Join(cmdsNoBR, "\n")
	if strings.Contains(shellCmd, "unset CLAUDE_CODE_USE_BEDROCK") {
		t.Error("without --no-bedrock, should not contain unset commands")
	}

	// With --no-bedrock: unset commands present
	cmdsWithBR, _ := cmd.BuildAgentShellCommands("test prompt", "", true)
	shellCmd = strings.Join(cmdsWithBR, "\n")
	for _, envVar := range []string{
		"unset CLAUDE_CODE_USE_BEDROCK",
		"unset ANTHROPIC_BASE_URL",
		"unset ANTHROPIC_DEFAULT_SONNET_MODEL",
		"unset ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"unset ANTHROPIC_DEFAULT_OPUS_MODEL",
	} {
		if !strings.Contains(shellCmd, envVar) {
			t.Errorf("with --no-bedrock, missing %q", envVar)
		}
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
			cmdsEsc, _ := cmd.BuildAgentShellCommands(prompt, "")
			shellCmd := strings.Join(cmdsEsc, "\n")

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
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB, nil)
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
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB, nil)
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
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, execFn, mockSSM, mockEB, nil)
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

// ---- Mock SSM for results/list tests ----
// Supports multiple SendCommand calls with per-command-ID invocation responses.

type mockResultsSSM struct {
	sendCalls   []ssm.SendCommandInput
	sendErr     error
	cmdCounter  int
	cmdIDs      []string                             // command IDs returned in order
	invByCmd    map[string]*ssm.GetCommandInvocationOutput // per-command responses
}

func (m *mockResultsSSM) SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	m.sendCalls = append(m.sendCalls, *input)
	cmdID := m.cmdIDs[m.cmdCounter]
	m.cmdCounter++
	return &ssm.SendCommandOutput{
		Command: &ssmtypes.Command{
			CommandId: awssdk.String(cmdID),
		},
	}, nil
}

func (m *mockResultsSSM) GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	cmdID := awssdk.ToString(input.CommandId)
	if out, ok := m.invByCmd[cmdID]; ok {
		return out, nil
	}
	return &ssm.GetCommandInvocationOutput{
		Status:                ssmtypes.CommandInvocationStatusSuccess,
		StandardOutputContent: awssdk.String(""),
	}, nil
}

var _ cmd.SSMSendAPI = (*mockResultsSSM)(nil)

// ---- Results + List tests ----

// TestAgentResults verifies that `km agent results <sandbox-id>` fetches latest
// run output via two SSM commands (list latest, cat output).
func TestAgentResults(t *testing.T) {
	origPoll := cmd.AgentPollInterval
	cmd.AgentPollInterval = 1 * time.Millisecond
	defer func() { cmd.AgentPollInterval = origPoll }()

	fetcher := newRunningEC2Sandbox("sb-results01")
	mockEB := &mockAgentEB{}
	mockSSM := &mockResultsSSM{
		cmdIDs: []string{"cmd-ls-latest", "cmd-cat-output"},
		invByCmd: map[string]*ssm.GetCommandInvocationOutput{
			"cmd-ls-latest": {
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: awssdk.String("20260410T143000Z"),
			},
			"cmd-cat-output": {
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: awssdk.String(`{"result":"all tests pass","cost_usd":0.42}`),
			},
		},
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB, nil)
	root.AddCommand(agentCmd)

	// Capture stdout
	var buf strings.Builder
	root.SetOut(&buf)

	root.SetArgs([]string{"agent", "results", "sb-results01"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify two SSM commands were sent (ls + cat)
	if len(mockSSM.sendCalls) != 2 {
		t.Fatalf("expected 2 SendCommand calls, got %d", len(mockSSM.sendCalls))
	}

	output := buf.String()
	if !strings.Contains(output, `"result"`) {
		t.Errorf("expected output.json content in stdout, got: %s", output)
	}
}

// TestAgentResults_SpecificRun verifies that --run flag skips the ls step
// and directly cats the specified run's output.
func TestAgentResults_SpecificRun(t *testing.T) {
	origPoll := cmd.AgentPollInterval
	cmd.AgentPollInterval = 1 * time.Millisecond
	defer func() { cmd.AgentPollInterval = origPoll }()

	fetcher := newRunningEC2Sandbox("sb-results02")
	mockEB := &mockAgentEB{}
	mockSSM := &mockResultsSSM{
		cmdIDs: []string{"cmd-cat-specific"},
		invByCmd: map[string]*ssm.GetCommandInvocationOutput{
			"cmd-cat-specific": {
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: awssdk.String(`{"result":"specific run"}`),
			},
		},
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB, nil)
	root.AddCommand(agentCmd)

	var buf strings.Builder
	root.SetOut(&buf)

	root.SetArgs([]string{"agent", "results", "sb-results02", "--run", "20260410T143000Z"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Only 1 SendCommand (cat, no ls)
	if len(mockSSM.sendCalls) != 1 {
		t.Fatalf("expected 1 SendCommand call with --run, got %d", len(mockSSM.sendCalls))
	}

	// Verify the command references the specific run ID
	call := mockSSM.sendCalls[0]
	cmds := call.Parameters["commands"]
	if len(cmds) == 0 || !strings.Contains(cmds[0], "20260410T143000Z") {
		t.Errorf("expected cat command to reference run ID 20260410T143000Z, got: %v", cmds)
	}

	output := buf.String()
	if !strings.Contains(output, `"result":"specific run"`) {
		t.Errorf("expected specific run output, got: %s", output)
	}
}

// TestAgentResults_NoRuns verifies that when no runs exist, an appropriate message is shown.
func TestAgentResults_NoRuns(t *testing.T) {
	origPoll := cmd.AgentPollInterval
	cmd.AgentPollInterval = 1 * time.Millisecond
	defer func() { cmd.AgentPollInterval = origPoll }()

	fetcher := newRunningEC2Sandbox("sb-results03")
	mockEB := &mockAgentEB{}
	mockSSM := &mockResultsSSM{
		cmdIDs: []string{"cmd-ls-empty"},
		invByCmd: map[string]*ssm.GetCommandInvocationOutput{
			"cmd-ls-empty": {
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: awssdk.String(""),
			},
		},
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB, nil)
	root.AddCommand(agentCmd)

	var buf strings.Builder
	root.SetOut(&buf)

	root.SetArgs([]string{"agent", "results", "sb-results03"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "no agent runs found") {
		t.Errorf("expected 'no agent runs found' message, got: %s", output)
	}
}

// TestAgentList verifies that `km agent list <sandbox-id>` enumerates runs
// with status and output size in a formatted table.
func TestAgentList(t *testing.T) {
	origPoll := cmd.AgentPollInterval
	cmd.AgentPollInterval = 1 * time.Millisecond
	defer func() { cmd.AgentPollInterval = origPoll }()

	fetcher := newRunningEC2Sandbox("sb-list01")
	mockEB := &mockAgentEB{}
	mockSSM := &mockResultsSSM{
		cmdIDs: []string{"cmd-list-runs"},
		invByCmd: map[string]*ssm.GetCommandInvocationOutput{
			"cmd-list-runs": {
				Status: ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: awssdk.String(
					"20260410T150000Z|complete|4096\n20260410T143000Z|running|0\n20260410T120000Z|failed|1024\n",
				),
			},
		},
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB, nil)
	root.AddCommand(agentCmd)

	var buf strings.Builder
	root.SetOut(&buf)

	root.SetArgs([]string{"agent", "list", "sb-list01"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	output := buf.String()

	// Verify all three runs appear
	for _, runID := range []string{"20260410T150000Z", "20260410T143000Z", "20260410T120000Z"} {
		if !strings.Contains(output, runID) {
			t.Errorf("expected run ID %s in output, got: %s", runID, output)
		}
	}

	// Verify statuses appear
	for _, status := range []string{"complete", "running", "failed"} {
		if !strings.Contains(output, status) {
			t.Errorf("expected status %q in output, got: %s", status, output)
		}
	}
}

// TestBuildAgentShellCommands_TmuxWrapping verifies tmux session wrapping in generated commands.
func TestBuildAgentShellCommands_TmuxWrapping(t *testing.T) {
	cmds, runID := cmd.BuildAgentShellCommands("test tmux wrapping", "my-bucket")
	shellCmd := strings.Join(cmds, "\n")

	// Commands contain tmux new-session with the session name
	if !strings.Contains(shellCmd, "tmux new-session -d -s 'km-agent-") {
		t.Error("commands missing tmux new-session -d -s 'km-agent-'")
	}

	// Commands contain tmux wait-for for blocking
	if !strings.Contains(shellCmd, "tmux wait-for km-done-") {
		t.Error("commands missing tmux wait-for km-done- (blocking line)")
	}

	// Script body still contains claude -p and S3 upload
	if !strings.Contains(shellCmd, "claude -p") {
		t.Error("script missing 'claude -p'")
	}
	if !strings.Contains(shellCmd, "aws s3 cp") {
		t.Error("script missing S3 upload (aws s3 cp)")
	}

	// RUN_ID matches expected timestamp format
	runIDRe := regexp.MustCompile(`^\d{8}T\d{6}Z$`)
	if !runIDRe.MatchString(runID) {
		t.Errorf("RUN_ID %q does not match expected format YYYYMMDDTHHMMSSZ", runID)
	}

	// Script contains tmux wait-for -S (signal completion)
	if !strings.Contains(shellCmd, "tmux wait-for -S km-done-") {
		t.Error("script missing tmux wait-for -S km-done- (completion signal)")
	}

	// The exec bash pattern keeps tmux session alive after script completes
	if !strings.Contains(shellCmd, "exec bash") {
		t.Error("commands missing 'exec bash' pattern for tmux session persistence")
	}

	// NoBedrock variant still includes the unset lines
	cmdsNB, _ := cmd.BuildAgentShellCommands("test", "", true)
	shellNB := strings.Join(cmdsNB, "\n")
	if !strings.Contains(shellNB, "unset CLAUDE_CODE_USE_BEDROCK") {
		t.Error("NoBedrock variant missing unset lines inside tmux-wrapped script")
	}
}

// TestBuildAgentShellCommands_RunIDDeterministic verifies the returned RUN_ID matches timestamp format.
func TestBuildAgentShellCommands_RunIDDeterministic(t *testing.T) {
	_, runID := cmd.BuildAgentShellCommands("test deterministic", "")
	re := regexp.MustCompile(`^\d{8}T\d{6}Z$`)
	if !re.MatchString(runID) {
		t.Errorf("RUN_ID %q does not match pattern YYYYMMDDTHHMMSSZ", runID)
	}

	// Verify the RUN_ID is a valid time
	_, err := time.Parse("20060102T150405Z", runID)
	if err != nil {
		t.Errorf("RUN_ID %q is not a valid timestamp: %v", runID, err)
	}
}

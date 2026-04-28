package cmd_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

	t.Run("claude", func(t *testing.T) {
		cmds, _ := cmd.BuildAgentShellCommands(prompt, cmd.AgentRunOptions{AgentType: "claude"})
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
	})

	t.Run("codex", func(t *testing.T) {
		cmds, _ := cmd.BuildAgentShellCommands(prompt, cmd.AgentRunOptions{AgentType: "codex"})
		shellCmd := strings.Join(cmds, "\n")

		for _, part := range []string{"codex exec", "--json", "--dangerously-bypass-approvals-and-sandbox"} {
			if !strings.Contains(shellCmd, part) {
				t.Errorf("codex shell missing %q", part)
			}
		}
		if strings.Contains(shellCmd, "claude -p") {
			t.Error("codex shell must not contain claude invocation")
		}
		b64 := base64.StdEncoding.EncodeToString([]byte(prompt))
		if !strings.Contains(shellCmd, b64) {
			t.Error("codex missing b64 prompt")
		}
		// Regression guards: tmux scaffold identical
		if !strings.Contains(shellCmd, "/workspace/.km-agent/runs/") {
			t.Error("codex missing run dir")
		}
		if !strings.Contains(shellCmd, "KM_RUN_ID=") {
			t.Error("codex missing KM_RUN_ID")
		}
	})
}

// TestAgentNonInteractive_NoBedrock verifies that --no-bedrock injects unset commands.
func TestAgentNonInteractive_NoBedrock(t *testing.T) {
	// Without --no-bedrock: no unset commands
	cmdsNoBR, _ := cmd.BuildAgentShellCommands("test prompt", cmd.AgentRunOptions{AgentType: "claude"})
	shellCmd := strings.Join(cmdsNoBR, "\n")
	if strings.Contains(shellCmd, "unset CLAUDE_CODE_USE_BEDROCK") {
		t.Error("without --no-bedrock, should not contain unset commands")
	}

	// With --no-bedrock: unset commands present
	cmdsWithBR, _ := cmd.BuildAgentShellCommands("test prompt", cmd.AgentRunOptions{AgentType: "claude", NoBedrock: true})
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

	t.Run("codex_ignores_nobedrock", func(t *testing.T) {
		cmds, _ := cmd.BuildAgentShellCommands("test", cmd.AgentRunOptions{AgentType: "codex", NoBedrock: true})
		shellCmd := strings.Join(cmds, "\n")
		for _, forbidden := range []string{
			"unset CLAUDE_CODE_USE_BEDROCK",
			"unset ANTHROPIC_BASE_URL",
			"unset ANTHROPIC_DEFAULT_SONNET_MODEL",
			".claude/.credentials.json",
		} {
			if strings.Contains(shellCmd, forbidden) {
				t.Errorf("codex script contains claude-specific stanza %q (must never appear)", forbidden)
			}
		}
	})
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
			cmdsEsc, _ := cmd.BuildAgentShellCommands(prompt, cmd.AgentRunOptions{AgentType: "claude"})
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
	if !strings.Contains(fullCmd, "KM-Sandbox-Session") {
		t.Errorf("expected '--document-name KM-Sandbox-Session', got: %s", fullCmd)
	}
	if strings.Contains(fullCmd, "sudo -u sandbox") {
		t.Errorf("expected NO 'sudo -u sandbox' wrapper, got: %s", fullCmd)
	}
	if strings.Contains(fullCmd, "AWS-StartInteractiveCommand") {
		t.Errorf("expected NO legacy AWS-StartInteractiveCommand, got: %s", fullCmd)
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
	cmds, runID := cmd.BuildAgentShellCommands("test tmux wrapping", cmd.AgentRunOptions{AgentType: "claude", ArtifactsBucket: "my-bucket"})
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
	cmdsNB, _ := cmd.BuildAgentShellCommands("test", cmd.AgentRunOptions{AgentType: "claude", NoBedrock: true})
	shellNB := strings.Join(cmdsNB, "\n")
	if !strings.Contains(shellNB, "unset CLAUDE_CODE_USE_BEDROCK") {
		t.Error("NoBedrock variant missing unset lines inside tmux-wrapped script")
	}
}

// TestBuildAgentShellCommands_RunIDDeterministic verifies the returned RUN_ID matches timestamp format.
func TestBuildAgentShellCommands_RunIDDeterministic(t *testing.T) {
	_, runID := cmd.BuildAgentShellCommands("test deterministic", cmd.AgentRunOptions{AgentType: "claude"})
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

// TestBuildAgentShellCommands_Codex verifies that the codex branch of
// BuildAgentShellCommands emits the correct CLI invocation and never
// leaks claude-specific environment stanzas.
func TestBuildAgentShellCommands_Codex(t *testing.T) {
	tests := []struct {
		name        string
		opts        cmd.AgentRunOptions
		mustContain []string
		mustAbsent  []string
	}{
		{
			name: "base codex invocation",
			opts: cmd.AgentRunOptions{AgentType: "codex"},
			mustContain: []string{
				"codex exec",
				"--json",
				"--dangerously-bypass-approvals-and-sandbox",
			},
			mustAbsent: []string{
				"claude -p",
				"unset CLAUDE_CODE_USE_BEDROCK",
			},
		},
		{
			name: "codex with codexArgs",
			opts: cmd.AgentRunOptions{AgentType: "codex", CodexArgs: []string{"--model", "o4-mini"}},
			mustContain: []string{
				"--dangerously-bypass-approvals-and-sandbox --model o4-mini",
			},
			mustAbsent: []string{
				"claude -p",
			},
		},
		{
			name: "codex ignores claudeArgs",
			opts: cmd.AgentRunOptions{AgentType: "codex", ClaudeArgs: []string{"--bogus-claude-flag"}},
			mustAbsent: []string{
				"--bogus-claude-flag",
				"claude -p",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmds, _ := cmd.BuildAgentShellCommands("test prompt", tc.opts)
			shellCmd := strings.Join(cmds, "\n")
			for _, s := range tc.mustContain {
				if !strings.Contains(shellCmd, s) {
					t.Errorf("expected %q in codex shell script", s)
				}
			}
			for _, s := range tc.mustAbsent {
				if strings.Contains(shellCmd, s) {
					t.Errorf("unexpected %q found in codex shell script", s)
				}
			}
		})
	}
}

// ---- Attach + Interactive tests ----

// TestAgentAttach verifies that `km agent attach <sandbox>` resolves sandbox
// to instanceID and builds SSM start-session with tmux attach-session.
func TestAgentAttach(t *testing.T) {
	fetcher := newRunningEC2Sandbox("sb-attach01")

	var capturedArgs []string
	var stdinSet, stdoutSet, stderrSet bool
	execFn := func(c *exec.Cmd) error {
		capturedArgs = c.Args
		stdinSet = c.Stdin != nil
		stdoutSet = c.Stdout != nil
		stderrSet = c.Stderr != nil
		return nil
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, execFn, nil, nil, nil)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "attach", "sb-attach01"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	fullCmd := strings.Join(capturedArgs, " ")

	// Verify SSM start-session is used
	if !strings.Contains(fullCmd, "start-session") {
		t.Errorf("expected 'start-session' in command, got: %s", fullCmd)
	}

	// Verify target instance ID
	if !strings.Contains(fullCmd, "i-0abc123def456") {
		t.Errorf("expected instance ID i-0abc123def456 in command, got: %s", fullCmd)
	}

	// Verify tmux attach-session
	if !strings.Contains(fullCmd, "tmux attach-session") {
		t.Errorf("expected 'tmux attach-session' in command, got: %s", fullCmd)
	}

	// Verify km-agent session grep
	if !strings.Contains(fullCmd, "km-agent") {
		t.Errorf("expected 'km-agent' session filter in command, got: %s", fullCmd)
	}

	// Verify stdin/stdout/stderr are wired
	if !stdinSet {
		t.Error("expected stdin to be wired")
	}
	if !stdoutSet {
		t.Error("expected stdout to be wired")
	}
	if !stderrSet {
		t.Error("expected stderr to be wired")
	}
	if !strings.Contains(fullCmd, "KM-Sandbox-Session") {
		t.Errorf("expected '--document-name KM-Sandbox-Session', got: %s", fullCmd)
	}
	if strings.Contains(fullCmd, "sudo -u sandbox") {
		t.Errorf("expected NO 'sudo -u sandbox' wrapper, got: %s", fullCmd)
	}
	if strings.Contains(fullCmd, "AWS-StartInteractiveCommand") {
		t.Errorf("expected NO legacy AWS-StartInteractiveCommand, got: %s", fullCmd)
	}
}

// TestAgentInteractive verifies that --interactive writes script via SendCommand
// then opens SSM start-session with tmux new-session (no -d).
func TestAgentInteractive(t *testing.T) {
	fetcher := newRunningEC2Sandbox("sb-interact01")
	mockSSM := &mockAgentSSM{}
	mockEB := &mockAgentEB{}

	var capturedArgs []string
	execFn := func(c *exec.Cmd) error {
		capturedArgs = c.Args
		return nil
	}

	cfg := &config.Config{ArtifactsBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, execFn, mockSSM, mockEB, nil)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "run", "sb-interact01", "--prompt", "fix tests", "--interactive"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify SendCommand was called to write the script (first 2 commands only)
	if len(mockSSM.sendCalls) != 1 {
		t.Fatalf("expected 1 SendCommand call (script write), got %d", len(mockSSM.sendCalls))
	}
	call := mockSSM.sendCalls[0]
	cmds := call.Parameters["commands"]
	// Should have 2 commands: write script + chmod
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands in SendCommand (write + chmod), got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "km-agent-run.sh") {
		t.Errorf("expected script write command, got: %s", cmds[0])
	}
	if !strings.Contains(cmds[1], "chmod") {
		t.Errorf("expected chmod command, got: %s", cmds[1])
	}

	// Verify SSM start-session was called (via execFn)
	fullCmd := strings.Join(capturedArgs, " ")
	if !strings.Contains(fullCmd, "start-session") {
		t.Errorf("expected 'start-session' in command, got: %s", fullCmd)
	}

	// Verify tmux new-session (no -d flag)
	if !strings.Contains(fullCmd, "tmux new-session -s") {
		t.Errorf("expected 'tmux new-session -s' in command, got: %s", fullCmd)
	}
	if strings.Contains(fullCmd, "tmux new-session -d") {
		t.Errorf("interactive mode should NOT have -d (detached) flag, got: %s", fullCmd)
	}

	// Verify the session name contains km-agent-
	if !strings.Contains(fullCmd, "km-agent-") {
		t.Errorf("expected 'km-agent-' session name in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "KM-Sandbox-Session") {
		t.Errorf("expected '--document-name KM-Sandbox-Session', got: %s", fullCmd)
	}
	if strings.Contains(fullCmd, "sudo -u sandbox") {
		t.Errorf("expected NO 'sudo -u sandbox' wrapper, got: %s", fullCmd)
	}
	if strings.Contains(fullCmd, "AWS-StartInteractiveCommand") {
		t.Errorf("expected NO legacy AWS-StartInteractiveCommand, got: %s", fullCmd)
	}
}

// TestAgentInteractive_MutuallyExclusiveWithWait verifies --interactive and --wait
// are mutually exclusive.
func TestAgentInteractive_MutuallyExclusiveWithWait(t *testing.T) {
	fetcher := newRunningEC2Sandbox("sb-interact02")
	mockSSM := &mockAgentSSM{}
	mockEB := &mockAgentEB{}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, mockSSM, mockEB, nil)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "run", "sb-interact02", "--prompt", "test", "--interactive", "--wait"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for --interactive + --wait, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' in error, got: %v", err)
	}
}

// TestAgentAttach_StoppedSandbox verifies attach returns error for stopped sandboxes.
func TestAgentAttach_StoppedSandbox(t *testing.T) {
	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-stopped-attach",
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

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, nil, nil, nil, nil)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "attach", "sb-stopped-attach"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for stopped sandbox, got nil")
	}
	if !strings.Contains(err.Error(), "stopped") {
		t.Errorf("expected error to contain 'stopped', got: %v", err)
	}
}

// ---- Plan 03: --claude/--codex flag pair, mutex, no-bedrock gate tests ----

// trackingFetcher wraps fakeFetcher and counts FetchSandbox calls.
// Used to assert that early-exit validations fire BEFORE ResolveSandboxID reaches AWS.
type trackingFetcher struct {
	inner fakeFetcher
	calls int
}

func (t *trackingFetcher) FetchSandbox(ctx context.Context, id string) (*kmaws.SandboxRecord, error) {
	t.calls++
	return t.inner.FetchSandbox(ctx, id)
}

// TestAgentRun_ClaudeCodexMutex verifies that --claude and --codex together error
// before any SSM or FetchSandbox call (fires in RunE before ResolveSandboxID).
func TestAgentRun_ClaudeCodexMutex(t *testing.T) {
	fetcher := &trackingFetcher{}
	mockSSM := &mockAgentSSM{}
	cfg := &config.Config{ArtifactsBucket: "test-bucket"}

	runCmd := cmd.NewAgentRunCmd(cfg, fetcher, nil, mockSSM, nil)
	runCmd.SetArgs([]string{"sb-test123", "--claude", "--codex", "--prompt", "hi"})
	err := runCmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "--claude and --codex are mutually exclusive") {
		t.Errorf("got %q, want contains %q", err.Error(), "--claude and --codex are mutually exclusive")
	}
	if len(mockSSM.sendCalls) != 0 {
		t.Errorf("SendCommand called %d times; must not reach SSM after mutex error", len(mockSSM.sendCalls))
	}
	if fetcher.calls != 0 {
		t.Errorf("FetchSandbox called %d times; mutex check must fire before ResolveSandboxID", fetcher.calls)
	}
}

// TestAgentRun_CodexNoBedrockError verifies that --codex + --no-bedrock errors
// before any SSM or FetchSandbox call (fires in RunE before ResolveSandboxID).
func TestAgentRun_CodexNoBedrockError(t *testing.T) {
	fetcher := &trackingFetcher{}
	mockSSM := &mockAgentSSM{}
	cfg := &config.Config{ArtifactsBucket: "test-bucket"}

	runCmd := cmd.NewAgentRunCmd(cfg, fetcher, nil, mockSSM, nil)
	runCmd.SetArgs([]string{"sb-test123", "--codex", "--no-bedrock", "--prompt", "hi"})
	err := runCmd.Execute()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "--no-bedrock is only valid with --claude") {
		t.Errorf("got %q, want contains %q", err.Error(), "--no-bedrock is only valid with --claude")
	}
	if len(mockSSM.sendCalls) != 0 || fetcher.calls != 0 {
		t.Errorf("no-bedrock gate must fire BEFORE ResolveSandboxID and SSM (SendCommand=%d, FetchSandbox=%d)",
			len(mockSSM.sendCalls), fetcher.calls)
	}
}

// TestAgentRun_CodexFlag verifies that --codex dispatches codex (not claude) via SSM SendCommand.
func TestAgentRun_CodexFlag(t *testing.T) {
	fetcher := newRunningEC2Sandbox("sb-codex01")
	mockSSM := &mockAgentSSM{}
	cfg := &config.Config{ArtifactsBucket: "test-bucket"}

	runCmd := cmd.NewAgentRunCmd(cfg, fetcher, nil, mockSSM, nil)
	runCmd.SetArgs([]string{"sb-codex01", "--codex", "--prompt", "list files"})
	if err := runCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mockSSM.sendCalls) < 1 {
		t.Fatal("expected SendCommand call")
	}
	call := mockSSM.sendCalls[0]
	if awssdk.ToString(call.DocumentName) != "AWS-RunShellScript" {
		t.Errorf("wrong document: %s", awssdk.ToString(call.DocumentName))
	}
	script := strings.Join(call.Parameters["commands"], "\n")
	for _, want := range []string{"codex exec", "--json", "--dangerously-bypass-approvals-and-sandbox"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	if strings.Contains(script, "claude -p") {
		t.Error("script contains claude invocation — --codex should not emit claude")
	}
}

// TestAgentRun_DefaultClaudeBackwardCompat verifies that no flag (default) uses claude.
func TestAgentRun_DefaultClaudeBackwardCompat(t *testing.T) {
	fetcher := newRunningEC2Sandbox("sb-default01")
	mockSSM := &mockAgentSSM{}
	cfg := &config.Config{ArtifactsBucket: "test-bucket"}

	runCmd := cmd.NewAgentRunCmd(cfg, fetcher, nil, mockSSM, nil)
	runCmd.SetArgs([]string{"sb-default01", "--prompt", "no agent flag"}) // NO --claude, NO --codex
	if err := runCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mockSSM.sendCalls) < 1 {
		t.Fatal("expected SendCommand call")
	}
	script := strings.Join(mockSSM.sendCalls[0].Parameters["commands"], "\n")
	for _, want := range []string{"claude -p", "--output-format json", "--dangerously-skip-permissions"} {
		if !strings.Contains(script, want) {
			t.Errorf("default-claude script missing %q", want)
		}
	}
	if strings.Contains(script, "codex exec") {
		t.Error("default-claude script must not contain codex")
	}
}

// TestLoadProfileCLICodexArgs verifies that loadProfileCLICodexArgs returns nil
// when ArtifactsBucket is empty (fail-open) — exercised transitively via the
// codex-flag tests above which pass ArtifactsBucket="test-bucket" but no real S3.
// Correctness when the profile has codexArgs is covered by TestBuildAgentShellCommands_Codex.
func TestLoadProfileCLICodexArgs(t *testing.T) {
	// When cfg.ArtifactsBucket is empty, loadProfileCLI returns nil → helper returns nil (fail-open).
	// This is exercised by calling the run cmd with no bucket — should not panic.
	fetcher := newRunningEC2Sandbox("sb-codexargs01")
	mockSSM := &mockAgentSSM{}
	cfg := &config.Config{} // empty ArtifactsBucket → loadProfileCLI returns nil → CodexArgs nil

	runCmd := cmd.NewAgentRunCmd(cfg, fetcher, nil, mockSSM, nil)
	runCmd.SetArgs([]string{"sb-codexargs01", "--codex", "--prompt", "test args"})
	if err := runCmd.Execute(); err != nil {
		t.Fatalf("unexpected error with empty bucket (fail-open): %v", err)
	}
	// Codex branch still fires even with no profile args
	if len(mockSSM.sendCalls) < 1 {
		t.Fatal("expected SendCommand call")
	}
	script := strings.Join(mockSSM.sendCalls[0].Parameters["commands"], "\n")
	if !strings.Contains(script, "codex exec") {
		t.Error("codex script missing 'codex exec' even with nil codexArgs")
	}
}

// TestBuildAgentCommand verifies that the agent command builder composes
// the base command, profile-supplied default args, and user-supplied extra
// args in the correct order, and applies profile args only for claude.
func TestBuildAgentCommand(t *testing.T) {
	tests := []struct {
		name         string
		base         string
		profileArgs  []string
		userArgs     []string
		want         string
	}{
		{
			name: "claude with no extras",
			base: "claude",
			want: "claude",
		},
		{
			name:        "claude with profile claudeArgs only",
			base:        "claude",
			profileArgs: []string{"--dangerously-skip-permissions"},
			want:        "claude --dangerously-skip-permissions",
		},
		{
			name:        "claude with profile and user args — profile first, user last wins",
			base:        "claude",
			profileArgs: []string{"--dangerously-skip-permissions", "--model", "claude-opus-4-7"},
			userArgs:    []string{"--model", "claude-sonnet-4-6"},
			want:        "claude --dangerously-skip-permissions --model claude-opus-4-7 --model claude-sonnet-4-6",
		},
		{
			name:     "claude with only user args",
			base:     "claude",
			userArgs: []string{"-p", "hello world"},
			want:     "claude -p hello world",
		},
		{
			name:        "codex ignores claudeArgs from profile",
			base:        "codex",
			profileArgs: []string{"--dangerously-skip-permissions"},
			userArgs:    []string{"--flag"},
			want:        "codex --flag",
		},
		{
			name:        "empty profile args string is skipped",
			base:        "claude",
			profileArgs: []string{""},
			want:        "claude",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cmd.BuildAgentCommand(tc.base, tc.profileArgs, tc.userArgs)
			if got != tc.want {
				t.Errorf("BuildAgentCommand(%q, %v, %v) = %q, want %q",
					tc.base, tc.profileArgs, tc.userArgs, got, tc.want)
			}
		})
	}
}

// TestAgentParametersEscaping verifies that the --parameters JSON construction
// for km agent --claude / attach / run --interactive uses encoding/json.Marshal
// (round-trips correctly through json.Unmarshal) and does not rely on
// fmt.Sprintf + strings.ReplaceAll (which corrupts values containing nested
// quotes, single-quotes, or shell metacharacters).
func TestAgentParametersEscaping(t *testing.T) {
	fetcher := newRunningEC2Sandbox("sb-escape01")
	var capturedArgs []string
	execFn := func(c *exec.Cmd) error {
		capturedArgs = c.Args
		return nil
	}
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := cmd.NewAgentCmdWithDeps(cfg, fetcher, execFn, nil, nil, nil)
	root.AddCommand(agentCmd)
	root.SetArgs([]string{"agent", "attach", "sb-escape01"})
	if err := root.Execute(); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Find the --parameters value in capturedArgs
	var paramsJSON string
	for i, a := range capturedArgs {
		if a == "--parameters" && i+1 < len(capturedArgs) {
			paramsJSON = capturedArgs[i+1]
			break
		}
	}
	if paramsJSON == "" {
		t.Fatalf("--parameters not found in args: %v", capturedArgs)
	}

	var parsed map[string][]string
	if err := json.Unmarshal([]byte(paramsJSON), &parsed); err != nil {
		t.Fatalf("--parameters JSON does not parse (likely fmt.Sprintf+ReplaceAll corruption): %v\nvalue: %s", err, paramsJSON)
	}
	cmds, ok := parsed["command"]
	if !ok || len(cmds) != 1 {
		t.Fatalf("expected parsed['command'] to have exactly 1 entry, got: %v", parsed)
	}
	inner := cmds[0]
	if !strings.Contains(inner, "tmux attach-session") {
		t.Errorf("expected inner command to contain 'tmux attach-session', got: %s", inner)
	}
	if strings.Contains(inner, "sudo -u sandbox") {
		t.Errorf("expected NO 'sudo -u sandbox' wrapper in inner command, got: %s", inner)
	}
	if strings.Contains(inner, `\\"`) || strings.Contains(inner, `\\\"`) {
		t.Errorf("inner command appears to contain over-escaped backslashes (manual escaping leak), got: %q", inner)
	}
}

// agentTestBoolPtr is a helper for the notify gate tests (Phase 62 HOOK-04).
func agentTestBoolPtr(b bool) *bool { return &b }

// TestBuildAgentShellCommands_NotifyOnPermission verifies that a non-nil
// NotifyOnPermission=true pointer injects export KM_NOTIFY_ON_PERMISSION="1"
// into the generated script and does NOT inject KM_NOTIFY_ON_IDLE (idle nil).
func TestBuildAgentShellCommands_NotifyOnPermission(t *testing.T) {
	cmds, _ := cmd.BuildAgentShellCommands("test prompt", cmd.AgentRunOptions{
		AgentType:          "claude",
		NotifyOnPermission: agentTestBoolPtr(true),
		ArtifactsBucket:    "test-bucket",
	})
	script := strings.Join(cmds, "\n")
	if !strings.Contains(script, `export KM_NOTIFY_ON_PERMISSION="1"`) {
		t.Errorf("expected KM_NOTIFY_ON_PERMISSION=1 export, got:\n%s", script)
	}
	if strings.Contains(script, "KM_NOTIFY_ON_IDLE") {
		t.Errorf("did not expect KM_NOTIFY_ON_IDLE (idle pointer was nil), got:\n%s", script)
	}
}

// TestBuildAgentShellCommands_NotifyOnIdle verifies that NotifyOnIdle=true
// injects export KM_NOTIFY_ON_IDLE="1" and does NOT inject KM_NOTIFY_ON_PERMISSION.
func TestBuildAgentShellCommands_NotifyOnIdle(t *testing.T) {
	cmds, _ := cmd.BuildAgentShellCommands("test prompt", cmd.AgentRunOptions{
		AgentType:       "claude",
		NotifyOnIdle:    agentTestBoolPtr(true),
		ArtifactsBucket: "test-bucket",
	})
	script := strings.Join(cmds, "\n")
	if !strings.Contains(script, `export KM_NOTIFY_ON_IDLE="1"`) {
		t.Errorf("expected KM_NOTIFY_ON_IDLE=1 export, got:\n%s", script)
	}
	if strings.Contains(script, "KM_NOTIFY_ON_PERMISSION") {
		t.Errorf("did not expect KM_NOTIFY_ON_PERMISSION (permission pointer was nil), got:\n%s", script)
	}
}

// TestBuildAgentShellCommands_NotifyBoth verifies that both pointers set to true
// produce both export lines.
func TestBuildAgentShellCommands_NotifyBoth(t *testing.T) {
	cmds, _ := cmd.BuildAgentShellCommands("test prompt", cmd.AgentRunOptions{
		AgentType:          "claude",
		NotifyOnPermission: agentTestBoolPtr(true),
		NotifyOnIdle:       agentTestBoolPtr(true),
		ArtifactsBucket:    "test-bucket",
	})
	script := strings.Join(cmds, "\n")
	if !strings.Contains(script, `export KM_NOTIFY_ON_PERMISSION="1"`) {
		t.Errorf("expected KM_NOTIFY_ON_PERMISSION=1 export, got:\n%s", script)
	}
	if !strings.Contains(script, `export KM_NOTIFY_ON_IDLE="1"`) {
		t.Errorf("expected KM_NOTIFY_ON_IDLE=1 export, got:\n%s", script)
	}
}

// TestBuildAgentShellCommands_NotifyNeitherEmitsNoEnv verifies that nil pointers
// produce NO KM_NOTIFY_ON_* exports in the script.
func TestBuildAgentShellCommands_NotifyNeitherEmitsNoEnv(t *testing.T) {
	cmds, _ := cmd.BuildAgentShellCommands("test prompt", cmd.AgentRunOptions{
		AgentType:       "claude",
		ArtifactsBucket: "test-bucket",
	})
	script := strings.Join(cmds, "\n")
	if strings.Contains(script, "KM_NOTIFY_ON_PERMISSION") {
		t.Errorf("did not expect KM_NOTIFY_ON_PERMISSION when pointer is nil, got:\n%s", script)
	}
	if strings.Contains(script, "KM_NOTIFY_ON_IDLE") {
		t.Errorf("did not expect KM_NOTIFY_ON_IDLE when pointer is nil, got:\n%s", script)
	}
}

// TestBuildAgentShellCommands_NotifyExplicitFalse verifies that a non-nil pointer
// with value false emits export KM_NOTIFY_ON_PERMISSION="0" (explicit override).
func TestBuildAgentShellCommands_NotifyExplicitFalse(t *testing.T) {
	cmds, _ := cmd.BuildAgentShellCommands("test prompt", cmd.AgentRunOptions{
		AgentType:          "claude",
		NotifyOnPermission: agentTestBoolPtr(false),
		ArtifactsBucket:    "test-bucket",
	})
	script := strings.Join(cmds, "\n")
	if !strings.Contains(script, `export KM_NOTIFY_ON_PERMISSION="0"`) {
		t.Errorf("explicit false should emit KM_NOTIFY_ON_PERMISSION=0, got:\n%s", script)
	}
}

// TestBuildAgentShellCommands_NotifyOrderingBeforeAgentLaunch verifies that
// KM_NOTIFY_ON_* exports appear in the script BEFORE the agent invocation line.
func TestBuildAgentShellCommands_NotifyOrderingBeforeAgentLaunch(t *testing.T) {
	cmds, _ := cmd.BuildAgentShellCommands("test prompt", cmd.AgentRunOptions{
		AgentType:          "claude",
		NotifyOnPermission: agentTestBoolPtr(true),
		ArtifactsBucket:    "test-bucket",
	})
	script := strings.Join(cmds, "\n")
	permIdx := strings.Index(script, "KM_NOTIFY_ON_PERMISSION")
	claudeIdx := strings.Index(script, "claude -p")
	if permIdx < 0 {
		t.Fatalf("export KM_NOTIFY_ON_PERMISSION not found in script")
	}
	if claudeIdx < 0 {
		t.Fatalf("claude -p invocation not found in script")
	}
	if permIdx > claudeIdx {
		t.Errorf("export must appear before agent launch; perm at %d, claude at %d\nscript:\n%s", permIdx, claudeIdx, script)
	}
}

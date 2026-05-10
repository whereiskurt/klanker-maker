// Package cmd — agent_auth_test.go
// Unit tests for km agent auth (AUTH-01..AUTH-07, AUTH-11..AUTH-13).
// Uses internal package access (package cmd) to stub package-level dispatch vars
// and test unexported helpers (buildClaudeAuthArgs, verifyCredentialsWritten, etc.).
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ---- Test-local mocks (internal package, mirrors agent_test.go's cmd_test mocks) ----

// authTestSSM is a per-command-routing SSM mock used by agent_auth_test.go.
// Unlike the external mockAgentSSM, this one supports per-command routing so
// different commands return different outputs (conflict check vs cred check).
type authTestSSM struct {
	// routedOutputs maps a substring of the shell command to the stdout to return.
	// First matching entry wins. Fallback: returns successOutput.
	routedOutputs []authSSMRoute
	successOutput string
	sendErr       error
	sendCalls     []ssm.SendCommandInput
}

type authSSMRoute struct {
	cmdSubstr string
	output    string
}

func (m *authTestSSM) SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	m.sendCalls = append(m.sendCalls, *input)
	return &ssm.SendCommandOutput{
		Command: &ssmtypes.Command{
			CommandId: awssdk.String("cmd-auth-test-123"),
		},
	}, nil
}

func (m *authTestSSM) GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	// Look at the most-recent SendCommand to determine routing
	if len(m.sendCalls) > 0 {
		last := m.sendCalls[len(m.sendCalls)-1]
		if cmds, ok := last.Parameters["commands"]; ok && len(cmds) > 0 {
			cmdStr := cmds[0]
			for _, route := range m.routedOutputs {
				if strings.Contains(cmdStr, route.cmdSubstr) {
					return &ssm.GetCommandInvocationOutput{
						Status:                ssmtypes.CommandInvocationStatusSuccess,
						StandardOutputContent: awssdk.String(route.output),
					}, nil
				}
			}
		}
	}
	return &ssm.GetCommandInvocationOutput{
		Status:                ssmtypes.CommandInvocationStatusSuccess,
		StandardOutputContent: awssdk.String(m.successOutput),
	}, nil
}

// authTestFetcher is a minimal SandboxFetcher for agent_auth tests.
type authTestFetcher struct {
	record *kmaws.SandboxRecord
	err    error
}

func (f *authTestFetcher) FetchSandbox(_ context.Context, _ string) (*kmaws.SandboxRecord, error) {
	return f.record, f.err
}

func newRunningEC2SandboxAuth(id string) *authTestFetcher {
	return &authTestFetcher{
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

// captureAuthStdout redirects os.Stdout through a pipe for the duration of fn,
// then restores the original stdout and returns whatever fn wrote.
// Named differently from vscode_test.go's captureStdout to avoid redeclaration.
func captureAuthStdout(fn func()) string {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return ""
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- string(buf)
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

// ---- AUTH-01: Flag parsing ----

// TestAgentAuth_FlagParsing verifies that the auth subcommand registers
// --claude, --codex, --console, --sso, --claudeai, and --email flags.
func TestAgentAuth_FlagParsing(t *testing.T) {
	cfg := &config.Config{}
	authCmd := newAgentAuthCmd(cfg, nil, nil, nil)

	for _, flagName := range []string{"claude", "codex", "console", "sso", "claudeai"} {
		f := authCmd.Flags().Lookup(flagName)
		if f == nil {
			t.Errorf("flag --%s not registered", flagName)
			continue
		}
		if f.Value.Type() != "bool" {
			t.Errorf("flag --%s expected type bool, got %s", flagName, f.Value.Type())
		}
	}
	emailFlag := authCmd.Flags().Lookup("email")
	if emailFlag == nil {
		t.Error("flag --email not registered")
	} else if emailFlag.Value.Type() != "string" {
		t.Errorf("flag --email expected type string, got %s", emailFlag.Value.Type())
	}
}

// ---- AUTH-02: Mutual exclusion ----

// TestAgentAuth_MutuallyExclusive verifies that --claude and --codex together
// return an error containing "mutually exclusive".
func TestAgentAuth_MutuallyExclusive(t *testing.T) {
	cfg := &config.Config{}
	fetcher := newRunningEC2SandboxAuth("sb-mutex")
	mockSSM := &authTestSSM{}

	root := &cobra.Command{Use: "km"}
	agentCmd := &cobra.Command{Use: "agent"}
	authCmd := newAgentAuthCmd(cfg, fetcher, func(c *exec.Cmd) error { return nil }, mockSSM)
	agentCmd.AddCommand(authCmd)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "auth", "sb-mutex", "--claude", "--codex"})
	var buf bytes.Buffer
	root.SetErr(&buf)
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for --claude + --codex, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' in error, got: %v", err)
	}
}

// ---- AUTH-03: Default to --claude ----

// TestAgentAuth_DefaultClaude verifies that invoking auth with neither --claude
// nor --codex dispatches to the claude branch (not codex).
func TestAgentAuth_DefaultClaude(t *testing.T) {
	cfg := &config.Config{}
	fetcher := newRunningEC2SandboxAuth("sb-default")

	// Stub: SSM conflict check returns empty (no conflict), cred check returns ok
	mockSSM := &authTestSSM{
		routedOutputs: []authSSMRoute{
			{cmdSubstr: "tmux list-sessions", output: ""},
			{cmdSubstr: "stat '/home/sandbox/.claude/.credentials.json'", output: "ok"},
		},
	}

	var claudeInvoked bool
	var codexInvoked bool
	origClaude := runAgentAuthClaudeFn
	origCodex := runAgentAuthCodexFn
	defer func() {
		runAgentAuthClaudeFn = origClaude
		runAgentAuthCodexFn = origCodex
	}()
	runAgentAuthClaudeFn = func(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, console, sso, claudeai bool, email string) error {
		claudeInvoked = true
		return nil
	}
	runAgentAuthCodexFn = func(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string) error {
		codexInvoked = true
		return nil
	}

	root := &cobra.Command{Use: "km"}
	agentCmd := &cobra.Command{Use: "agent"}
	authCmd := newAgentAuthCmd(cfg, fetcher, func(c *exec.Cmd) error { return nil }, mockSSM)
	agentCmd.AddCommand(authCmd)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "auth", "sb-default"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !claudeInvoked {
		t.Error("expected claude branch to be invoked when no flag is set")
	}
	if codexInvoked {
		t.Error("codex branch must not be invoked when no flag is set (default is --claude)")
	}
}

// ---- AUTH-04: Sandbox ID resolution ----

// TestAgentAuth_SandboxIDResolution verifies that ResolveSandboxID is called with
// the provided reference and the resolved ID is passed to runAgentAuthClaude.
func TestAgentAuth_SandboxIDResolution(t *testing.T) {
	cfg := &config.Config{}
	// Use the "direct sandbox ID match" path: "sb-abc123" matches sandboxIDLike
	// so ResolveSandboxID returns it without a DynamoDB lookup (which would fail in tests).
	fetcher := newRunningEC2SandboxAuth("sb-abc123")

	var receivedID string
	origClaude := runAgentAuthClaudeFn
	defer func() { runAgentAuthClaudeFn = origClaude }()
	runAgentAuthClaudeFn = func(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, console, sso, claudeai bool, email string) error {
		receivedID = sandboxID
		return nil
	}

	root := &cobra.Command{Use: "km"}
	agentCmd := &cobra.Command{Use: "agent"}
	authCmd := newAgentAuthCmd(cfg, fetcher, func(c *exec.Cmd) error { return nil }, &authTestSSM{})
	agentCmd.AddCommand(authCmd)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "auth", "sb-abc123"})
	if err := root.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedID != "sb-abc123" {
		t.Errorf("expected resolved ID 'sb-abc123', got %q", receivedID)
	}
}

// ---- AUTH-05: Conflict detection ----

// TestAgentAuth_ConflictRefuse verifies that an active km-agent-* tmux session
// causes runAgentAuthClaude to return an error containing the session name and
// "km agent attach".
func TestAgentAuth_ConflictRefuse(t *testing.T) {
	fetcher := newRunningEC2SandboxAuth("sb-conflict")
	mockSSM := &authTestSSM{
		routedOutputs: []authSSMRoute{
			{cmdSubstr: "tmux list-sessions", output: "km-agent-abc123\n"},
		},
	}

	err := runAgentAuthClaude(context.Background(), &config.Config{}, fetcher, func(c *exec.Cmd) error { return nil }, mockSSM, "sb-conflict", false, false, false, "")
	if err == nil {
		t.Fatal("expected error for active agent session, got nil")
	}
	if !strings.Contains(err.Error(), "km-agent-abc123") {
		t.Errorf("expected session name 'km-agent-abc123' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "km agent attach") {
		t.Errorf("expected 'km agent attach' hint in error, got: %v", err)
	}
}

// ---- AUTH-06: verifyCredentialsWritten success ----

// TestVerifyCredentialsWritten_Success verifies that when stat returns "ok",
// verifyCredentialsWritten returns nil and prints the success confirmation line.
func TestVerifyCredentialsWritten_Success(t *testing.T) {
	mockSSM := &authTestSSM{
		routedOutputs: []authSSMRoute{
			{cmdSubstr: "stat '/home/sandbox/.claude/.credentials.json'", output: "ok"},
		},
	}

	var printed string
	printed = captureAuthStdout(func() {
		err := verifyCredentialsWritten(context.Background(), mockSSM, "i-0abc123def456", "claude", "sb-test", nil)
		if err != nil {
			t.Errorf("expected nil error, got: %v", err)
		}
	})

	if !strings.Contains(printed, "claude credentials written") {
		t.Errorf("expected '✓ claude credentials written' in output, got: %q", printed)
	}
	if !strings.Contains(printed, "/home/sandbox/.claude/.credentials.json") {
		t.Errorf("expected credential path in output, got: %q", printed)
	}
}

// ---- AUTH-07: verifyCredentialsWritten missing ----

// TestVerifyCredentialsWritten_Missing verifies that when stat returns "missing",
// verifyCredentialsWritten returns an error containing the expected message.
func TestVerifyCredentialsWritten_Missing(t *testing.T) {
	t.Run("sessionErr_nil", func(t *testing.T) {
		mockSSM := &authTestSSM{
			routedOutputs: []authSSMRoute{
				{cmdSubstr: "stat '/home/sandbox/.claude/.credentials.json'", output: "missing"},
			},
		}
		err := verifyCredentialsWritten(context.Background(), mockSSM, "i-0abc123def456", "claude", "sb-test", nil)
		if err == nil {
			t.Fatal("expected error for missing credentials, got nil")
		}
		if !strings.Contains(err.Error(), "not found at /home/sandbox/.claude/.credentials.json") {
			t.Errorf("expected path in error message, got: %v", err)
		}
	})

	t.Run("sessionErr_non_nil", func(t *testing.T) {
		mockSSM := &authTestSSM{
			routedOutputs: []authSSMRoute{
				{cmdSubstr: "stat '/home/sandbox/.claude/.credentials.json'", output: "missing"},
			},
		}
		sessionErr := fmt.Errorf("session exited non-zero")
		err := verifyCredentialsWritten(context.Background(), mockSSM, "i-0abc123def456", "claude", "sb-test", sessionErr)
		if err == nil {
			t.Fatal("expected error wrapping sessionErr, got nil")
		}
		if !strings.Contains(err.Error(), "session exited non-zero") {
			t.Errorf("expected wrapped sessionErr in error, got: %v", err)
		}
	})
}

// ---- AUTH-13: buildClaudeAuthArgs ----

// TestBuildClaudeAuthArgs verifies that flag combinations produce the exact expected
// command strings, and that invalid combinations return errors.
func TestBuildClaudeAuthArgs(t *testing.T) {
	tests := []struct {
		name      string
		console   bool
		sso       bool
		claudeai  bool
		email     string
		wantCmd   string
		wantErr   string
	}{
		{
			name:    "default_no_flags",
			wantCmd: "claude auth login --claudeai",
		},
		{
			name:    "claudeai_explicit",
			claudeai: true,
			wantCmd: "claude auth login --claudeai",
		},
		{
			name:    "console",
			console:  true,
			wantCmd: "claude auth login --console",
		},
		{
			name:    "sso_only",
			sso:     true,
			wantCmd: "claude auth login --sso",
		},
		{
			name:    "sso_with_email",
			sso:     true,
			email:   "op@example.com",
			wantCmd: "claude auth login --sso --email op@example.com",
		},
		{
			name:    "claudeai_with_email",
			email:   "op@example.com",
			wantCmd: "claude auth login --claudeai --email op@example.com",
		},
		{
			name:    "console_and_sso_error",
			console:  true,
			sso:     true,
			wantErr: "--console and --sso cannot be combined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildClaudeAuthArgs(tt.console, tt.sso, tt.claudeai, tt.email)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantCmd {
				t.Errorf("got %q, want %q", got, tt.wantCmd)
			}
		})
	}
}

// ---- AUTH-11: km shell --no-bedrock missing credentials hint ----

// TestShellCmd_NoBedrock_CredentialsMissingHint verifies that when noBedrock is true
// and ~/.claude/.credentials.json is missing on the sandbox, runShell returns an
// error containing the hint before opening an interactive SSM session.
func TestShellCmd_NoBedrock_CredentialsMissingHint(t *testing.T) {
	fetcher := newRunningEC2SandboxAuth("sb-nobedrock")

	// noBedrock pre-check SSM client: returns "missing" for the stat check.
	// Also intercepts the noBedrock profile.d write (which uses its own client).
	mockSSM := &authTestSSM{
		routedOutputs: []authSSMRoute{
			{cmdSubstr: "stat /home/sandbox/.claude/.credentials.json", output: "missing\n"},
		},
	}

	// Inject the SSM client into the shell command via NewShellCmdWithSSM.
	// Track whether execFn (interactive session) was ever called.
	var execCalled bool
	execFn := func(c *exec.Cmd) error {
		execCalled = true
		return nil
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	shellCmd := newShellCmdWithSSM(cfg, fetcher, execFn, mockSSM)
	root.AddCommand(shellCmd)

	root.SetArgs([]string{"shell", "sb-nobedrock", "--no-bedrock"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}
	if !strings.Contains(err.Error(), "claude credentials not found") {
		t.Errorf("expected 'claude credentials not found' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "km agent auth") {
		t.Errorf("expected 'km agent auth' hint in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "sb-nobedrock") {
		t.Errorf("expected sandbox ID in error, got: %v", err)
	}
	if execCalled {
		t.Error("interactive SSM session must NOT be opened when credentials are missing")
	}
}

// TestShellCmd_NoBedrock_CredentialsPresent verifies that when credentials exist,
// runShell proceeds normally (no early exit, execFn is called).
func TestShellCmd_NoBedrock_CredentialsPresent(t *testing.T) {
	fetcher := newRunningEC2SandboxAuth("sb-credsok")

	mockSSM := &authTestSSM{
		routedOutputs: []authSSMRoute{
			{cmdSubstr: "stat /home/sandbox/.claude/.credentials.json", output: "ok\n"},
		},
	}

	var execCalled bool
	execFn := func(c *exec.Cmd) error {
		execCalled = true
		return nil
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	shellCmd := newShellCmdWithSSM(cfg, fetcher, execFn, mockSSM)
	root.AddCommand(shellCmd)

	root.SetArgs([]string{"shell", "sb-credsok", "--no-bedrock"})
	// We expect NO error from the pre-check; the execFn returns nil (simulating normal exit)
	_ = root.Execute()

	if !execCalled {
		t.Error("interactive SSM session SHOULD be opened when credentials are present")
	}
}

// ---- AUTH-12: km agent run --no-bedrock missing credentials hint ----

// TestAgentRun_NoBedrock_CredentialsMissingHint verifies that when noBedrock is true
// and credentials are missing, runAgentNonInteractive returns an error with the hint
// BEFORE any tmux prep commands are sent.
func TestAgentRun_NoBedrock_CredentialsMissingHint(t *testing.T) {
	fetcher := newRunningEC2SandboxAuth("sb-run-missing")

	// The pre-check stat command fires before the tmux prep commands.
	// We track sendCalls to ensure the tmux setup never fires.
	mockSSM := &authTestSSM{
		routedOutputs: []authSSMRoute{
			{cmdSubstr: "stat /home/sandbox/.claude/.credentials.json", output: "missing\n"},
		},
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := NewAgentCmdWithDeps(cfg, fetcher, func(c *exec.Cmd) error { return nil }, mockSSM, nil, nil)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "run", "sb-run-missing", "--prompt", "hello", "--no-bedrock"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing credentials, got nil")
	}
	if !strings.Contains(err.Error(), "claude credentials not found") {
		t.Errorf("expected 'claude credentials not found' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "km agent auth") {
		t.Errorf("expected 'km agent auth' hint in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "sb-run-missing") {
		t.Errorf("expected sandbox ID in error, got: %v", err)
	}

	// Verify: only the stat pre-check SendCommand was issued (no tmux setup)
	tmuxPrepFired := false
	for _, call := range mockSSM.sendCalls {
		if cmds, ok := call.Parameters["commands"]; ok {
			for _, cmd := range cmds {
				if strings.Contains(cmd, "km-agent-run.sh") || strings.Contains(cmd, "tmux new-session") {
					tmuxPrepFired = true
				}
			}
		}
	}
	if tmuxPrepFired {
		t.Error("tmux session prep must NOT fire when credentials are missing")
	}
}

// TestAgentRun_NoBedrock_CredentialsPresent verifies that when credentials are present,
// agent run proceeds normally (tmux session is created).
func TestAgentRun_NoBedrock_CredentialsPresent(t *testing.T) {
	fetcher := newRunningEC2SandboxAuth("sb-run-ok")

	mockSSM := &authTestSSM{
		routedOutputs: []authSSMRoute{
			{cmdSubstr: "stat /home/sandbox/.claude/.credentials.json", output: "ok\n"},
		},
		// Fallback for all other SSM commands (tmux prep, run, etc.)
		successOutput: "KM_RUN_ID=20260410T120000Z",
	}

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	agentCmd := NewAgentCmdWithDeps(cfg, fetcher, func(c *exec.Cmd) error { return nil }, mockSSM, nil, nil)
	root.AddCommand(agentCmd)

	root.SetArgs([]string{"agent", "run", "sb-run-ok", "--prompt", "hello", "--no-bedrock"})
	// We only care that no "credentials not found" error is returned
	err := root.Execute()
	if err != nil && strings.Contains(err.Error(), "claude credentials not found") {
		t.Errorf("credentials pre-check should pass when credentials are present, got: %v", err)
	}
}

package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// SSMSendAPI is the narrow interface for SSM SendCommand + GetCommandInvocation.
// Defined for dependency injection in tests (same pattern as RollSSMAPI in roll.go).
type SSMSendAPI interface {
	SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

// AgentIdleResetInterval is the interval between idle-reset heartbeat events.
// Exported so tests can override it for faster execution.
var AgentIdleResetInterval = 5 * time.Minute

// AgentPollInterval is the interval between SSM GetCommandInvocation polls.
// Exported so tests can override it for faster execution.
var AgentPollInterval = 10 * time.Second

// NewAgentCmd creates the "km agent" parent command with backward-compatible
// interactive mode and the "run" subcommand for non-interactive execution.
//
// Backward compatibility:
//
//	km agent <sandbox-id> --claude       -> interactive SSM session (unchanged)
//	km agent run <sandbox-id> --prompt   -> non-interactive via SSM SendCommand
func NewAgentCmd(cfg *config.Config) *cobra.Command {
	return NewAgentCmdWithDeps(cfg, nil, nil, nil, nil)
}

// NewAgentCmdWithDeps builds the agent command with optional dependency injection
// for testing. Pass nil for real AWS-backed clients.
func NewAgentCmdWithDeps(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, ebClient kmaws.EventBridgeAPI) *cobra.Command {
	var useClaude bool
	var useCodex bool

	cmd := &cobra.Command{
		Use:   "agent <sandbox-id | #number> [-- extra-args...]",
		Short: "Launch an AI coding agent inside a sandbox",
		Long: `Launch an AI coding agent inside a running sandbox via SSM.

Connects as the sandbox user and runs the selected agent binary.
Extra arguments after -- are passed through to the agent.

Subcommands:
  run       Fire a prompt non-interactively via SSM SendCommand
  results   Fetch agent run output from sandbox disk
  list      Enumerate agent runs with status and output size

Examples:
  km agent 1 --claude                    # interactive claude session
  km agent 1 --claude -- -p "fix tests"  # pass args to claude
  km agent run sb-abc123 --prompt "fix the failing tests"
  km agent results sb-abc123             # fetch latest run output
  km agent results sb-abc123 --run 20260410T143000Z
  km agent list sb-abc123                # list all runs`,
		Args:             cobra.MinimumNArgs(1),
		TraverseChildren: true,
		SilenceUsage:     true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}

			var agentCmd string
			switch {
			case useClaude:
				agentCmd = "claude"
			case useCodex:
				agentCmd = "codex"
			default:
				return fmt.Errorf("specify an agent: --claude or --codex")
			}

			// Pass any extra args after the sandbox ID
			if len(args) > 1 {
				agentCmd += " " + strings.Join(args[1:], " ")
			}

			return runAgent(cmd, cfg, fetcher, execFn, sandboxID, agentCmd)
		},
	}

	cmd.Flags().BoolVar(&useClaude, "claude", false, "Launch Claude Code")
	cmd.Flags().BoolVar(&useCodex, "codex", false, "Launch OpenAI Codex (future)")

	// Add "run" subcommand for non-interactive execution
	cmd.AddCommand(newAgentRunCmd(cfg, fetcher, ssmClient, ebClient))

	// Add "results" subcommand for fetching run output
	cmd.AddCommand(newAgentResultsCmd(cfg, fetcher, ssmClient))

	// Add "list" subcommand for enumerating runs
	cmd.AddCommand(newAgentListCmd(cfg, fetcher, ssmClient))

	return cmd
}

// newAgentRunCmd creates the "km agent run" subcommand.
func newAgentRunCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, ebClient kmaws.EventBridgeAPI) *cobra.Command {
	var prompt string
	var wait bool

	cmd := &cobra.Command{
		Use:   "run <sandbox-id | #number>",
		Short: "Fire a prompt non-interactively via SSM SendCommand",
		Long: `Fire a Claude prompt into a sandbox via SSM SendCommand.

The prompt is base64-encoded to prevent shell injection. Output is written
to /workspace/.km-agent/runs/<timestamp>/ on the sandbox.

By default, fires and returns immediately with a run ID. Use --wait to
block until the agent completes and print output.

Examples:
  km agent run sb-abc123 --prompt "fix the failing tests"
  km agent run #1 --prompt "refactor auth module" --wait`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}

			return runAgentNonInteractive(ctx, cfg, fetcher, ssmClient, ebClient, sandboxID, prompt, wait)
		},
	}

	cmd.Flags().StringVar(&prompt, "prompt", "", "Prompt text to send to Claude (required)")
	cmd.Flags().BoolVar(&wait, "wait", false, "Block until agent completes and print output")
	_ = cmd.MarkFlagRequired("prompt")

	return cmd
}

// runAgent launches an interactive AI agent inside a sandbox via SSM start-session.
// This is the backward-compatible path for `km agent <sandbox-id> --claude`.
func runAgent(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID, claudeCmd string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, func() string {
			t := cfg.SandboxTableName
			if t == "" {
				t = "km-sandboxes"
			}
			return t
		}())
	}
	if execFn == nil {
		execFn = defaultShellExec
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped", sandboxID)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Open an interactive login shell as sandbox user that auto-execs the agent.
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
		"--document-name", "AWS-StartInteractiveCommand",
		"--parameters", fmt.Sprintf(
			`{"command":["sudo -u sandbox -i bash -c 'source /etc/profile.d/km-profile-env.sh 2>/dev/null; source /etc/profile.d/km-identity.sh 2>/dev/null; cd /workspace; exec %s'"]}`,
			claudeCmd))
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// runAgentNonInteractive fires a prompt into a sandbox via SSM SendCommand.
// It base64-encodes the prompt to prevent shell injection, creates a run directory
// on the sandbox, and either returns immediately (fire-and-forget) or waits for
// completion with idle-reset heartbeats.
func runAgentNonInteractive(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, ebClient kmaws.EventBridgeAPI, sandboxID, prompt string, wait bool) error {
	// Initialize real clients if not injected
	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, func() string {
			t := cfg.SandboxTableName
			if t == "" {
				t = "km-sandboxes"
			}
			return t
		}())
		if ssmClient == nil {
			ssmClient = ssm.NewFromConfig(awsCfg)
		}
		if ebClient == nil {
			ebClient = eventbridge.NewFromConfig(awsCfg)
		}
	}

	// Fetch sandbox record and validate state
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped -- start it with 'km resume %s' first", sandboxID, sandboxID)
	}

	// Extract instance ID from resources
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
	}

	// Base64-encode the prompt to prevent shell injection (AGENT-03)
	b64Prompt := base64.StdEncoding.EncodeToString([]byte(prompt))

	// Build the shell command that runs on the sandbox (AGENT-02)
	shellCmd := fmt.Sprintf(`sudo -u sandbox -i bash -c '
source /etc/profile.d/km-profile-env.sh 2>/dev/null
source /etc/profile.d/km-identity.sh 2>/dev/null
cd /workspace
RUN_ID=$(date -u +%%Y%%m%%dT%%H%%M%%SZ)
RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
mkdir -p "$RUN_DIR"
echo "running" > "$RUN_DIR/status"
PROMPT=$(echo "%s" | base64 -d)
claude -p "$PROMPT" --output-format json --dangerously-skip-permissions --bare \
  > "$RUN_DIR/output.json" 2>"$RUN_DIR/stderr.log"
EC=$?
if [ $EC -eq 0 ]; then echo "complete" > "$RUN_DIR/status"; else echo "failed" > "$RUN_DIR/status"; echo "$EC" > "$RUN_DIR/exit_code"; fi
echo "KM_RUN_ID=$RUN_ID"
'`, b64Prompt)

	// Send command via SSM (AGENT-01)
	cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:      []string{instanceID},
		DocumentName:     awssdk.String("AWS-RunShellScript"),
		TimeoutSeconds:   awssdk.Int32(28800), // 8 hours
		Parameters: map[string][]string{
			"commands":         {shellCmd},
			"executionTimeout": {"28800"},
		},
	})
	if err != nil {
		return fmt.Errorf("send agent command via SSM: %w", err)
	}

	commandID := awssdk.ToString(cmdOut.Command.CommandId)
	fmt.Fprintf(os.Stdout, "Agent dispatched to %s (command: %s)\n", sandboxID, commandID)

	if !wait {
		// Fire-and-forget: print command ID and return
		fmt.Fprintf(os.Stdout, "Use 'km agent results %s' to check output later.\n", sandboxID)
		return nil
	}

	// --wait mode: poll for completion with idle-reset heartbeat (AGENT-06)
	return waitForAgentCompletion(ctx, ssmClient, ebClient, sandboxID, commandID, instanceID)
}

// waitForAgentCompletion polls SSM GetCommandInvocation until the agent command
// completes. While polling, it publishes EventBridge extend events every
// AgentIdleResetInterval to prevent the sandbox from being destroyed by the TTL Lambda.
func waitForAgentCompletion(ctx context.Context, ssmClient SSMSendAPI, ebClient kmaws.EventBridgeAPI, sandboxID, commandID, instanceID string) error {
	// Start idle-reset heartbeat goroutine
	heartbeatCtx, heartbeatCancel := context.WithCancel(ctx)
	defer heartbeatCancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(AgentIdleResetInterval)
		defer ticker.Stop()

		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := kmaws.PublishSandboxCommand(heartbeatCtx, ebClient, sandboxID, "extend", "duration", "30m"); err != nil {
					log.Warn().Err(err).Str("sandbox", sandboxID).Msg("agent: failed to publish idle-reset heartbeat")
				} else {
					log.Debug().Str("sandbox", sandboxID).Msg("agent: published idle-reset heartbeat")
				}
			}
		}
	}()

	fmt.Fprintf(os.Stdout, "Waiting for agent to complete...")

	// Poll every 10 seconds for up to 8 hours
	maxAttempts := 2880 // 8 hours at 10s intervals
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			heartbeatCancel()
			wg.Wait()
			return ctx.Err()
		case <-time.After(AgentPollInterval):
		}

		inv, err := ssmClient.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  awssdk.String(commandID),
			InstanceId: awssdk.String(instanceID),
		})
		if err != nil {
			// Invocation may not be ready yet
			continue
		}

		status := string(inv.Status)
		switch status {
		case "Success":
			heartbeatCancel()
			wg.Wait()
			fmt.Fprintln(os.Stdout, " done")
			output := awssdk.ToString(inv.StandardOutputContent)
			fmt.Fprintln(os.Stdout, output)
			return nil

		case "Failed", "Cancelled", "TimedOut":
			heartbeatCancel()
			wg.Wait()
			fmt.Fprintln(os.Stdout)
			stderr := awssdk.ToString(inv.StandardErrorContent)
			return fmt.Errorf("agent command %s: %s", status, stderr)
		}

		// Still InProgress, Pending, etc. -- keep polling
		fmt.Fprint(os.Stdout, ".")
	}

	heartbeatCancel()
	wg.Wait()
	fmt.Fprintln(os.Stdout)
	return fmt.Errorf("timed out waiting for agent command to complete")
}

// newAgentResultsCmd creates the "km agent results" subcommand.
func newAgentResultsCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	var runID string

	cmd := &cobra.Command{
		Use:   "results <sandbox-id | #number>",
		Short: "Fetch agent run output from sandbox disk",
		Long: `Fetch Claude agent output from a sandbox via SSM SendCommand.

By default, fetches the latest run's output.json. Use --run to specify
a particular run ID (timestamp).

Note: SSM truncates output at 24KB. A warning is printed if the output
appears truncated.

Examples:
  km agent results sb-abc123
  km agent results sb-abc123 --run 20260410T143000Z`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			return runAgentResults(ctx, cmd, cfg, fetcher, ssmClient, sandboxID, runID)
		},
	}

	cmd.Flags().StringVar(&runID, "run", "", "Specific run ID (timestamp, e.g. 20260410T143000Z)")

	return cmd
}

// newAgentListCmd creates the "km agent list" subcommand.
func newAgentListCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <sandbox-id | #number>",
		Short: "List agent runs with status and output size",
		Long: `Enumerate all agent runs on a sandbox with status and output size.

Runs are listed newest-first.

Examples:
  km agent list sb-abc123`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			return runAgentList(ctx, cmd, cfg, fetcher, ssmClient, sandboxID)
		},
	}

	return cmd
}

// runAgentResults fetches a run's output.json from the sandbox via SSM.
func runAgentResults(ctx context.Context, cobraCmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID, runID string) error {
	// Initialize real clients if not injected
	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, func() string {
			t := cfg.SandboxTableName
			if t == "" {
				t = "km-sandboxes"
			}
			return t
		}())
		if ssmClient == nil {
			ssmClient = ssm.NewFromConfig(awsCfg)
		}
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped -- start it with 'km resume %s' first", sandboxID, sandboxID)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
	}

	w := cobraCmd.OutOrStdout()

	// If no run ID specified, find the latest run
	if runID == "" {
		latestCmd := `sudo -u sandbox bash -c 'ls -1t /workspace/.km-agent/runs/ 2>/dev/null | head -1'`
		latestOutput, err := sendSSMAndWait(ctx, ssmClient, instanceID, latestCmd)
		if err != nil {
			return fmt.Errorf("find latest run: %w", err)
		}
		runID = strings.TrimSpace(latestOutput)
		if runID == "" {
			fmt.Fprintln(w, "no agent runs found")
			return nil
		}
	}

	// Fetch the output.json for the specified run
	catCmd := fmt.Sprintf(`sudo -u sandbox cat /workspace/.km-agent/runs/%s/output.json`, runID)
	output, err := sendSSMAndWait(ctx, ssmClient, instanceID, catCmd)
	if err != nil {
		return fmt.Errorf("fetch run output: %w", err)
	}

	fmt.Fprint(w, output)

	// Warn if output may be truncated (SSM limit is 24KB)
	if len(output) >= 24000 {
		fmt.Fprintln(w, "\nWARNING: Output may be truncated (SSM 24KB limit). Use 'km shell' to access full output on disk.")
	}

	return nil
}

// runAgentList enumerates all agent runs on a sandbox.
func runAgentList(ctx context.Context, cobraCmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string) error {
	// Initialize real clients if not injected
	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, func() string {
			t := cfg.SandboxTableName
			if t == "" {
				t = "km-sandboxes"
			}
			return t
		}())
		if ssmClient == nil {
			ssmClient = ssm.NewFromConfig(awsCfg)
		}
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped -- start it with 'km resume %s' first", sandboxID, sandboxID)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
	}

	listCmd := `sudo -u sandbox bash -c '
for d in /workspace/.km-agent/runs/*/; do
  [ -d "$d" ] || continue
  id=$(basename "$d")
  status=$(cat "$d/status" 2>/dev/null || echo "unknown")
  size=$(stat -c%s "$d/output.json" 2>/dev/null || echo "0")
  echo "$id|$status|$size"
done
'`
	output, err := sendSSMAndWait(ctx, ssmClient, instanceID, listCmd)
	if err != nil {
		return fmt.Errorf("list agent runs: %w", err)
	}

	w := cobraCmd.OutOrStdout()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		fmt.Fprintln(w, "no agent runs found")
		return nil
	}

	// Print table header
	fmt.Fprintf(w, "%-22s %-10s %s\n", "RUN_ID", "STATUS", "SIZE")
	fmt.Fprintf(w, "%-22s %-10s %s\n", strings.Repeat("-", 22), strings.Repeat("-", 10), strings.Repeat("-", 10))

	for _, line := range lines {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		fmt.Fprintf(w, "%-22s %-10s %s\n", parts[0], parts[1], parts[2])
	}

	return nil
}

// sendSSMAndWait sends a shell command via SSM SendCommand and polls
// GetCommandInvocation until completion, returning stdout content.
func sendSSMAndWait(ctx context.Context, ssmClient SSMSendAPI, instanceID, shellCmd string) (string, error) {
	cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: awssdk.String("AWS-RunShellScript"),
		Parameters:   map[string][]string{"commands": {shellCmd}},
	})
	if err != nil {
		return "", fmt.Errorf("send SSM command: %w", err)
	}

	commandID := awssdk.ToString(cmdOut.Command.CommandId)

	// Poll for completion
	maxAttempts := 60 // ~10 minutes at AgentPollInterval
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(AgentPollInterval):
		}

		inv, err := ssmClient.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  awssdk.String(commandID),
			InstanceId: awssdk.String(instanceID),
		})
		if err != nil {
			continue // may not be ready yet
		}

		switch inv.Status {
		case ssmtypes.CommandInvocationStatusSuccess:
			return awssdk.ToString(inv.StandardOutputContent), nil
		case ssmtypes.CommandInvocationStatusFailed,
			ssmtypes.CommandInvocationStatusCancelled,
			ssmtypes.CommandInvocationStatusTimedOut:
			stderr := awssdk.ToString(inv.StandardErrorContent)
			return "", fmt.Errorf("command %s: %s", string(inv.Status), stderr)
		}
	}

	return "", fmt.Errorf("timed out waiting for SSM command %s", commandID)
}

// BuildAgentShellCommand builds the shell command string that will be sent via SSM.
// Exported for testing to verify command construction without needing SSM mocks.
func BuildAgentShellCommand(prompt string) string {
	b64Prompt := base64.StdEncoding.EncodeToString([]byte(prompt))
	return fmt.Sprintf(`sudo -u sandbox -i bash -c '
source /etc/profile.d/km-profile-env.sh 2>/dev/null
source /etc/profile.d/km-identity.sh 2>/dev/null
cd /workspace
RUN_ID=$(date -u +%%Y%%m%%dT%%H%%M%%SZ)
RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
mkdir -p "$RUN_DIR"
echo "running" > "$RUN_DIR/status"
PROMPT=$(echo "%s" | base64 -d)
claude -p "$PROMPT" --output-format json --dangerously-skip-permissions --bare \
  > "$RUN_DIR/output.json" 2>"$RUN_DIR/stderr.log"
EC=$?
if [ $EC -eq 0 ]; then echo "complete" > "$RUN_DIR/status"; else echo "failed" > "$RUN_DIR/status"; echo "$EC" > "$RUN_DIR/exit_code"; fi
echo "KM_RUN_ID=$RUN_ID"
'`, b64Prompt)
}

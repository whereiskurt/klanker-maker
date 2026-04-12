package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// SSMSendAPI is the narrow interface for SSM SendCommand + GetCommandInvocation.
// Defined for dependency injection in tests (same pattern as RollSSMAPI in roll.go).
type SSMSendAPI interface {
	SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}

// S3GetAPI is the narrow interface for S3 GetObject and ListObjectsV2.
// Used by agent results/list to fetch run output from S3 (fast path).
type S3GetAPI interface {
	GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
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
	return NewAgentCmdWithDeps(cfg, nil, nil, nil, nil, nil)
}

// NewAgentCmdWithDeps builds the agent command with optional dependency injection
// for testing. Pass nil for real AWS-backed clients.
func NewAgentCmdWithDeps(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, ebClient kmaws.EventBridgeAPI, s3Client S3GetAPI) *cobra.Command {
	var useClaude bool
	var useCodex bool
	var noBedrock bool

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

			// If --no-bedrock not explicitly set, check profile cli.noBedrock default
			if !cmd.Flags().Changed("no-bedrock") {
				if cliNB := loadProfileCLINoBedrock(ctx, cfg, sandboxID); cliNB {
					noBedrock = true
				}
			}

			return runAgent(cmd, cfg, fetcher, execFn, sandboxID, agentCmd, noBedrock)
		},
	}

	cmd.Flags().BoolVar(&useClaude, "claude", false, "Launch Claude Code")
	cmd.Flags().BoolVar(&useCodex, "codex", false, "Launch OpenAI Codex (future)")
	cmd.Flags().BoolVar(&noBedrock, "no-bedrock", false, "Use direct Anthropic API instead of Bedrock")

	// Add "attach" subcommand for connecting to live tmux sessions
	cmd.AddCommand(newAgentAttachCmd(cfg, fetcher, execFn))

	// Add "run" subcommand for non-interactive execution
	cmd.AddCommand(newAgentRunCmd(cfg, fetcher, execFn, ssmClient, ebClient))

	// Add "results" subcommand for fetching run output
	cmd.AddCommand(newAgentResultsCmd(cfg, fetcher, ssmClient, s3Client))

	// Add "list" subcommand for enumerating runs
	cmd.AddCommand(newAgentListCmd(cfg, fetcher, ssmClient, s3Client))

	return cmd
}

// newAgentRunCmd creates the "km agent run" subcommand.
func newAgentRunCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, ebClient kmaws.EventBridgeAPI) *cobra.Command {
	var prompt string
	var wait bool
	var interactive bool
	var noBedrock bool
	var autoStart bool

	cmd := &cobra.Command{
		Use:   "run <sandbox-id | #number>",
		Short: "Fire a prompt non-interactively via SSM SendCommand",
		Long: `Fire a Claude prompt into a sandbox via SSM SendCommand.

The prompt is base64-encoded to prevent shell injection. Output is written
to /workspace/.km-agent/runs/<timestamp>/ on the sandbox.

By default, fires and returns immediately with a run ID. Use --wait to
block until the agent completes and print output.

Use --interactive to create the tmux session and immediately attach to it.
This lets you watch the agent work in real-time. Detach with Ctrl-B d.

Use --auto-start to automatically resume a paused/stopped sandbox before
running the agent. Useful with 'km at' for scheduled agent runs.

Examples:
  km agent run sb-abc123 --prompt "fix the failing tests"
  km agent run #1 --prompt "refactor auth module" --wait
  km agent run #1 --prompt "refactor auth module" --interactive
  km agent run g1 --prompt "run tests" --auto-start --wait`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if interactive && wait {
				return fmt.Errorf("--interactive and --wait are mutually exclusive")
			}

			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}

			// If --no-bedrock not explicitly set, check profile cli.noBedrock default
			if !cmd.Flags().Changed("no-bedrock") {
				if cliNB := loadProfileCLINoBedrock(ctx, cfg, sandboxID); cliNB {
					noBedrock = true
				}
			}

			return runAgentNonInteractive(ctx, cfg, fetcher, execFn, ssmClient, ebClient, sandboxID, prompt, wait, interactive, noBedrock, autoStart)
		},
	}

	cmd.Flags().StringVar(&prompt, "prompt", "", "Prompt text to send to Claude (required)")
	cmd.Flags().BoolVar(&wait, "wait", false, "Block until agent completes and print output")
	cmd.Flags().BoolVar(&interactive, "interactive", false, "Create tmux session and immediately attach (blocks until detach)")
	cmd.Flags().BoolVar(&noBedrock, "no-bedrock", false, "Use direct Anthropic API instead of Bedrock")
	cmd.Flags().BoolVar(&autoStart, "auto-start", false, "Automatically resume sandbox if paused/stopped")
	_ = cmd.MarkFlagRequired("prompt")

	return cmd
}

// runAgent launches an interactive AI agent inside a sandbox via SSM start-session.
// This is the backward-compatible path for `km agent <sandbox-id> --claude`.
func runAgent(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID, claudeCmd string, noBedrock bool) error {
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

	// Optionally prepend nobedrock() call to unset Bedrock env vars
	noBedrockPrefix := ""
	if noBedrock {
		noBedrockPrefix = "nobedrock; "
	}

	// Open an interactive login shell as sandbox user that auto-execs the agent.
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
		"--document-name", "AWS-StartInteractiveCommand",
		"--parameters", fmt.Sprintf(
			`{"command":["sudo -u sandbox -i bash -c 'source /etc/profile.d/km-profile-env.sh 2>/dev/null; source /etc/profile.d/km-identity.sh 2>/dev/null; cd /workspace; %sexec %s'"]}`,
			noBedrockPrefix, claudeCmd))
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// newAgentAttachCmd creates the "km agent attach" subcommand.
// It connects to the latest running km-agent-* tmux session via SSM start-session.
func newAgentAttachCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <sandbox-id | #number>",
		Short: "Attach to a running agent tmux session",
		Long: `Attach to the latest running agent tmux session inside a sandbox.

Connects via SSM start-session and attaches to the most recent km-agent-*
tmux session. Detach with Ctrl-B d to return to your terminal.

Examples:
  km agent attach sb-abc123
  km agent attach #1`,
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
				return fmt.Errorf("sandbox %s is stopped -- start it with 'km resume %s' first", sandboxID, sandboxID)
			}

			instanceID, err := extractResourceID(rec.Resources, ":instance/")
			if err != nil {
				return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
			}

			// Build SSM start-session with tmux attach to the latest km-agent-* session
			tmuxCmd := `sudo -u sandbox -i bash -c "tmux attach-session -t $(tmux list-sessions -F '#{session_name}' 2>/dev/null | grep km-agent | tail -1) 2>/dev/null || echo No agent tmux sessions found"`
			c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
				"--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
				"--document-name", "AWS-StartInteractiveCommand",
				"--parameters", fmt.Sprintf(`{"command":["%s"]}`, strings.ReplaceAll(tmuxCmd, `"`, `\"`)))
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return execFn(c)
		},
	}

	return cmd
}

// runAgentNonInteractive fires a prompt into a sandbox via SSM SendCommand.
// It base64-encodes the prompt to prevent shell injection, creates a run directory
// on the sandbox, and either returns immediately (fire-and-forget) or waits for
// completion with idle-reset heartbeats.
func runAgentNonInteractive(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, ebClient kmaws.EventBridgeAPI, sandboxID, prompt string, wait, interactive, noBedrock, autoStart bool) error {
	printBanner("km agent run", sandboxID)

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
	if rec.Status != "running" {
		if !autoStart {
			return fmt.Errorf("sandbox %s is %s -- use --auto-start to resume automatically, or 'km resume %s' first", sandboxID, rec.Status, sandboxID)
		}
		fmt.Fprintf(os.Stdout, "Sandbox %s is %s, starting...\n", sandboxID, rec.Status)
		// Retry resume in a loop — instance may be in a transitional state (stopping→stopped)
		fmt.Fprintf(os.Stdout, "Waiting for sandbox to be running...")
		for i := 0; i < 36; i++ { // up to ~3 minutes
			rec, _ = fetcher.FetchSandbox(ctx, sandboxID)
			if rec != nil && rec.Status == "running" {
				fmt.Fprintln(os.Stdout, " running")
				break
			}
			// Try resume every 15 seconds (handles stopping→stopped→starting→running)
			if i%3 == 0 {
				_ = runResume(ctx, cfg, sandboxID)
			}
			time.Sleep(5 * time.Second)
			fmt.Fprint(os.Stdout, ".")
		}
		if rec == nil || rec.Status != "running" {
			status := "unknown"
			if rec != nil {
				status = rec.Status
			}
			return fmt.Errorf("sandbox %s did not reach running state (current: %s)", sandboxID, status)
		}
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
	}

	// If we just resumed, wait for SSM agent to register by probing with a no-op command
	if autoStart {
		fmt.Fprintf(os.Stdout, "Waiting for SSM agent...")
		ready := false
		for attempt := 0; attempt < 24; attempt++ { // up to ~2 minutes
			time.Sleep(5 * time.Second)
			testOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
				InstanceIds:  []string{instanceID},
				DocumentName: awssdk.String("AWS-RunShellScript"),
				Parameters:   map[string][]string{"commands": {"echo ready"}},
			})
			if err == nil {
				// Command accepted — wait briefly for it to complete
				time.Sleep(3 * time.Second)
				inv, invErr := ssmClient.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
					CommandId:  awssdk.String(awssdk.ToString(testOut.Command.CommandId)),
					InstanceId: awssdk.String(instanceID),
				})
				if invErr == nil && string(inv.Status) == "Success" {
					ready = true
					fmt.Fprintln(os.Stdout, " ready")
					break
				}
			}
			fmt.Fprint(os.Stdout, ".")
		}
		if !ready {
			return fmt.Errorf("SSM agent on %s did not become ready after resume", sandboxID)
		}
	}

	// Build the shell commands using shared helper (AGENT-02, AGENT-03)
	cmds, runID := BuildAgentShellCommands(prompt, cfg.ArtifactsBucket, noBedrock)

	// --interactive mode: write script to disk, then open SSM session with tmux new-session (attached)
	if interactive {
		if execFn == nil {
			execFn = defaultShellExec
		}

		// Send only the script-write commands (first 2: cat script + chmod)
		_, err = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
			InstanceIds:  []string{instanceID},
			DocumentName: awssdk.String("AWS-RunShellScript"),
			Parameters:   map[string][]string{"commands": cmds[:2]},
		})
		if err != nil {
			return fmt.Errorf("write agent script via SSM: %w", err)
		}

		// Wait briefly for script to land on disk
		time.Sleep(2 * time.Second)

		// Build SSM start-session with tmux new-session (attached, no -d)
		sessionName := fmt.Sprintf("km-agent-%s", runID)
		tmuxCmd := fmt.Sprintf("sudo -u sandbox -i tmux new-session -s '%s' '/tmp/km-agent-run.sh; exec bash'", sessionName)
		c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
			"--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
			"--document-name", "AWS-StartInteractiveCommand",
			"--parameters", fmt.Sprintf(`{"command":["%s"]}`, strings.ReplaceAll(tmuxCmd, `"`, `\"`)))
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return execFn(c)
	}

	// Send command via SSM (AGENT-01)
	cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:      []string{instanceID},
		DocumentName:     awssdk.String("AWS-RunShellScript"),
		TimeoutSeconds:   awssdk.Int32(28800), // 8 hours
		Parameters: map[string][]string{
			"commands":         cmds,
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

	// Poll with fast start: 2s, 3s, 5s, then 10s steady state.
	// Total timeout ~8 hours.
	pollDelay := 2 * time.Second
	maxDuration := 8 * time.Hour
	deadline := time.Now().Add(maxDuration)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			heartbeatCancel()
			wg.Wait()
			return ctx.Err()
		case <-time.After(pollDelay):
		}
		// Ramp up: 2s → 3s → 5s → 10s
		if pollDelay < AgentPollInterval {
			pollDelay = pollDelay * 3 / 2
			if pollDelay > AgentPollInterval {
				pollDelay = AgentPollInterval
			}
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
			// Parse run ID from stdout (format: KM_RUN_ID=<timestamp>)
			runID := ""
			for _, line := range strings.Split(output, "\n") {
				if strings.HasPrefix(line, "KM_RUN_ID=") {
					runID = strings.TrimPrefix(line, "KM_RUN_ID=")
					break
				}
			}
			if runID != "" {
				fmt.Fprintf(os.Stdout, "KM_RUN_ID=%s\n", runID)
				// Fetch and print the output.json content
				catCmd := fmt.Sprintf(`sudo -u sandbox cat /workspace/.km-agent/runs/%s/output.json`, runID)
				resultOutput, err := sendSSMAndWait(ctx, ssmClient, instanceID, catCmd)
				if err != nil {
					return fmt.Errorf("fetch run output after completion: %w", err)
				}
				fmt.Fprintln(os.Stdout, resultOutput)
			} else {
				fmt.Fprintln(os.Stdout, output)
			}
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
func newAgentResultsCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, s3Client S3GetAPI) *cobra.Command {
	var runID string

	cmd := &cobra.Command{
		Use:     "results <sandbox-id | #number>",
		Aliases: []string{"result"},
		Short:   "Fetch agent run output from sandbox disk",
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
			return runAgentResults(ctx, cmd, cfg, fetcher, ssmClient, s3Client, sandboxID, runID)
		},
	}

	cmd.Flags().StringVar(&runID, "run", "", "Specific run ID (timestamp, e.g. 20260410T143000Z)")

	return cmd
}

// newAgentListCmd creates the "km agent list" subcommand.
func newAgentListCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, s3Client S3GetAPI) *cobra.Command {
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
			return runAgentList(ctx, cmd, cfg, fetcher, ssmClient, s3Client, sandboxID)
		},
	}

	return cmd
}

// runAgentResults fetches a run's output.json from the sandbox via SSM.
func runAgentResults(ctx context.Context, cobraCmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, s3Client S3GetAPI, sandboxID, runID string) error {
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
		if s3Client == nil {
			s3Client = s3.NewFromConfig(awsCfg)
		}
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}

	w := cobraCmd.OutOrStdout()
	bucket := cfg.ArtifactsBucket

	// Try S3 fast path first (no SSM round-trip needed)
	if bucket != "" && s3Client != nil {
		output, err := fetchResultFromS3(ctx, s3Client, bucket, sandboxID, runID, w)
		if err == nil {
			return nil
		}
		// S3 miss — fall through to SSM
		_ = output
	}

	// SSM fallback — need running sandbox
	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped -- start it with 'km resume %s' first", sandboxID, sandboxID)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
	}

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

// fetchResultFromS3 tries to fetch agent run output from S3.
// If runID is empty, it lists runs and picks the latest.
// Returns the output string or an error if the S3 path doesn't exist.
func fetchResultFromS3(ctx context.Context, s3Client S3GetAPI, bucket, sandboxID, runID string, w io.Writer) (string, error) {
	prefix := fmt.Sprintf("agent-runs/%s/", sandboxID)

	if runID == "" {
		// List runs to find latest
		listOut, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:    awssdk.String(bucket),
			Prefix:    awssdk.String(prefix),
			Delimiter: awssdk.String("/"),
		})
		if err != nil {
			return "", err
		}
		if len(listOut.CommonPrefixes) == 0 {
			// No S3 data — fall through to SSM (agent may still be running)
			return "", fmt.Errorf("no S3 runs")
		}
		// CommonPrefixes are lexicographic — last is latest (timestamps sort naturally)
		latest := awssdk.ToString(listOut.CommonPrefixes[len(listOut.CommonPrefixes)-1].Prefix)
		// Extract run ID from prefix: "agent-runs/<sandbox>/<runID>/"
		parts := strings.Split(strings.TrimSuffix(latest, "/"), "/")
		runID = parts[len(parts)-1]
	}

	key := fmt.Sprintf("agent-runs/%s/%s/output.json", sandboxID, runID)
	getOut, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(key),
	})
	if err != nil {
		return "", err
	}
	defer getOut.Body.Close()

	body, err := io.ReadAll(getOut.Body)
	if err != nil {
		return "", err
	}

	fmt.Fprint(w, string(body))
	return string(body), nil
}

// listAgentRunsFromS3 lists agent runs from S3 (fast path, no SSM needed).
func listAgentRunsFromS3(ctx context.Context, s3Client S3GetAPI, bucket, sandboxID string, w io.Writer) error {
	prefix := fmt.Sprintf("agent-runs/%s/", sandboxID)
	listOut, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:    awssdk.String(bucket),
		Prefix:    awssdk.String(prefix),
		Delimiter: awssdk.String("/"),
	})
	if err != nil {
		return err
	}
	if len(listOut.CommonPrefixes) == 0 {
		// No S3 data — fall through to SSM
		return fmt.Errorf("no S3 runs")
	}

	// Print table header
	fmt.Fprintf(w, "%-22s %-10s %s\n", "RUN_ID", "STATUS", "SIZE")
	fmt.Fprintf(w, "%-22s %-10s %s\n", strings.Repeat("-", 22), strings.Repeat("-", 10), strings.Repeat("-", 10))

	for _, cp := range listOut.CommonPrefixes {
		runPrefix := awssdk.ToString(cp.Prefix)
		parts := strings.Split(strings.TrimSuffix(runPrefix, "/"), "/")
		runID := parts[len(parts)-1]

		// Fetch status file
		status := "unknown"
		statusOut, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: awssdk.String(bucket),
			Key:    awssdk.String(runPrefix + "status"),
		})
		if err == nil {
			body, _ := io.ReadAll(statusOut.Body)
			statusOut.Body.Close()
			status = strings.TrimSpace(string(body))
		}

		// Get output.json size
		size := "0"
		outputOut, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: awssdk.String(bucket),
			Key:    awssdk.String(runPrefix + "output.json"),
		})
		if err == nil {
			if outputOut.ContentLength != nil {
				size = fmt.Sprintf("%d", *outputOut.ContentLength)
			}
			outputOut.Body.Close()
		}

		fmt.Fprintf(w, "%-22s %-10s %s\n", runID, status, size)
	}

	return nil
}

// runAgentList enumerates all agent runs on a sandbox.
func runAgentList(ctx context.Context, cobraCmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, s3Client S3GetAPI, sandboxID string) error {
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
		if s3Client == nil {
			s3Client = s3.NewFromConfig(awsCfg)
		}
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}

	w := cobraCmd.OutOrStdout()
	bucket := cfg.ArtifactsBucket

	// Try S3 fast path first
	if bucket != "" && s3Client != nil {
		if listAgentRunsFromS3(ctx, s3Client, bucket, sandboxID, w) == nil {
			return nil
		}
		// S3 miss — fall through to SSM
	}

	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped -- start it with 'km resume %s' first", sandboxID, sandboxID)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
	}

	listCmd := `sudo -u sandbox bash -c 'for d in /workspace/.km-agent/runs/*/; do [ -d "$d" ] || continue; id=$(basename "$d"); status=$(cat "$d/status" 2>/dev/null || echo "unknown"); size=$(stat -f%z "$d/output.json" 2>/dev/null || stat -c%s "$d/output.json" 2>/dev/null || echo "0"); echo "$id|$status|$size"; done'`
	output, err := sendSSMAndWait(ctx, ssmClient, instanceID, listCmd)
	if err != nil {
		return fmt.Errorf("list agent runs: %w", err)
	}

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
// Uses a fast 2-second poll interval since these are short utility commands.
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

	// Poll for completion — fast interval for short utility commands
	pollInterval := 2 * time.Second
	maxAttempts := 150 // 5 minutes at 2s
	for i := 0; i < maxAttempts; i++ {
		// Brief initial delay to let SSM register the invocation
		delay := pollInterval
		if i == 0 {
			delay = 1 * time.Second
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
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

// BuildAgentShellCommands returns SSM RunShellScript commands for non-interactive
// Claude execution. Each element becomes a separate line in the SSM command array.
// The script is written to a temp file and executed as the sandbox user.
func BuildAgentShellCommands(prompt string, artifactsBucket string, noBedrock ...bool) ([]string, string) {
	b64Prompt := base64.StdEncoding.EncodeToString([]byte(prompt))
	runID := time.Now().UTC().Format("20060102T150405Z")
	noBedrockLines := ""
	if len(noBedrock) > 0 && noBedrock[0] {
		noBedrockLines = `unset CLAUDE_CODE_USE_BEDROCK
unset ANTHROPIC_BASE_URL
unset ANTHROPIC_DEFAULT_SONNET_MODEL
unset ANTHROPIC_DEFAULT_HAIKU_MODEL
unset ANTHROPIC_DEFAULT_OPUS_MODEL
# Extract OAuth token from Claude credentials for --bare mode (which skips OAuth)
if [ -z "$ANTHROPIC_API_KEY" ] && [ -f "$HOME/.claude/.credentials.json" ]; then
  OAUTH_TOKEN=$(python3 -c "import json; d=json.load(open('$HOME/.claude/.credentials.json')); print(d.get('claudeAiOauth',{}).get('accessToken',''))" 2>/dev/null)
  if [ -n "$OAUTH_TOKEN" ]; then export ANTHROPIC_API_KEY="$OAUTH_TOKEN"; fi
fi`
	}

	// Write a bash script to a temp file, then execute inside a tmux session.
	// The tmux session persists after SSM disconnect, allowing live attach.
	// Completion is signaled via tmux wait-for channel so --wait can block.
	script := fmt.Sprintf(`#!/bin/bash
export HOME=/home/sandbox
source /etc/profile.d/km-profile-env.sh 2>/dev/null
source /etc/profile.d/km-identity.sh 2>/dev/null
%s
KM_ARTIFACTS_BUCKET="%s"
cd /workspace
RUN_ID="%s"
RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
mkdir -p "$RUN_DIR"
echo "running" > "$RUN_DIR/status"
PROMPT=$(echo "%s" | base64 -d)
claude -p "$PROMPT" --output-format json --dangerously-skip-permissions --bare \
  > "$RUN_DIR/output.json" 2>"$RUN_DIR/stderr.log"
EC=$?
if [ $EC -eq 0 ]; then echo "complete" > "$RUN_DIR/status"; else echo "failed" > "$RUN_DIR/status"; echo "$EC" > "$RUN_DIR/exit_code"; fi
if [ -n "$KM_ARTIFACTS_BUCKET" ] && [ -n "$KM_SANDBOX_ID" ]; then
  aws s3 cp "$RUN_DIR/output.json" "s3://$KM_ARTIFACTS_BUCKET/agent-runs/$KM_SANDBOX_ID/$RUN_ID/output.json" --quiet 2>/dev/null || true
  aws s3 cp "$RUN_DIR/status" "s3://$KM_ARTIFACTS_BUCKET/agent-runs/$KM_SANDBOX_ID/$RUN_ID/status" --quiet 2>/dev/null || true
fi
tmux wait-for -S km-done-%s`, noBedrockLines, artifactsBucket, runID, b64Prompt, runID)

	sessionName := fmt.Sprintf("km-agent-%s", runID)

	return []string{
		fmt.Sprintf("cat > /tmp/km-agent-run.sh << 'KMEOF'\n%s\nKMEOF", script),
		"chmod +x /tmp/km-agent-run.sh",
		fmt.Sprintf("sudo -u sandbox bash -c \"tmux new-session -d -s '%s' '/tmp/km-agent-run.sh; exec bash'\"", sessionName),
		fmt.Sprintf("sudo -u sandbox bash -c \"tmux wait-for km-done-%s\"", runID),
		fmt.Sprintf("echo \"KM_RUN_ID=%s\"", runID),
		"rm -f /tmp/km-agent-run.sh",
	}, runID
}

// loadProfileCLINoBedrock fetches the sandbox's profile from S3 and returns
// the cli.noBedrock setting. Returns false on any error (fail open).
func loadProfileCLINoBedrock(ctx context.Context, cfg *config.Config, sandboxID string) bool {
	if cfg.ArtifactsBucket == "" {
		return false
	}
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return false
	}
	s3Client := s3.NewFromConfig(awsCfg)
	profileKey := fmt.Sprintf("artifacts/%s/.km-profile.yaml", sandboxID)
	obj, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(cfg.ArtifactsBucket),
		Key:    awssdk.String(profileKey),
	})
	if err != nil {
		return false
	}
	defer obj.Body.Close()
	data, err := io.ReadAll(obj.Body)
	if err != nil {
		return false
	}
	p, err := profile.Parse(data)
	if err != nil || p == nil {
		return false
	}
	return p.Spec.CLI != nil && p.Spec.CLI.NoBedrock
}

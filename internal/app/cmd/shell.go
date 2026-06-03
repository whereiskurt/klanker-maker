package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"


	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/allowlistgen"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ShellExecFunc is the function signature for executing the AWS CLI subprocess.
// It is package-level so tests can replace it to capture args without executing.
type ShellExecFunc func(c *exec.Cmd) error

// defaultShellExec calls cmd.Run() — the real subprocess execution path.
func defaultShellExec(c *exec.Cmd) error {
	return c.Run()
}

// bakeFromSandboxFn is the function used to bake an AMI from a sandbox record.
// Package-level variable allows tests to inject a mock without AWS credentials.
var bakeFromSandboxFn = BakeFromSandbox

// flushEC2ObservationsFn is the function used to flush eBPF observations.
// Package-level variable allows tests to inject a mock and verify call ordering.
var flushEC2ObservationsFn = flushEC2Observations

// fetchEC2ObservedJSONFn is the function used to fetch the observed JSON from S3.
// Package-level variable allows tests to inject canned observed-state data.
var fetchEC2ObservedJSONFn = fetchEC2ObservedJSON

// runSSMInteractiveSubprocess runs a subprocess that hosts an interactive SSM
// session and ignores terminal signals (SIGINT, SIGQUIT, SIGTSTP) for the
// duration so the session-manager-plugin can handle them — Ctrl+C / Ctrl-\
// reach the remote PTY instead of killing km and orphaning the plugin
// (which would error with "read /dev/stdin: input/output error" on Ctrl+C
// or trigger the Go runtime SIGQUIT goroutine dump on Ctrl-\). Mirrors
// the SSH client signal model.
func runSSMInteractiveSubprocess(execFn ShellExecFunc, c *exec.Cmd) error {
	signal.Ignore(os.Interrupt, syscall.SIGQUIT, syscall.SIGTSTP)
	defer signal.Reset(os.Interrupt, syscall.SIGQUIT, syscall.SIGTSTP)
	return execFn(c)
}

// resolveNotifyFlags returns the effective per-invocation notify gate overrides
// based on which cobra flags were explicitly changed. Returns (nil, nil) when
// neither positive nor negative flag was set — the profile.d file written at
// compile time (Plan 02) supplies defaults in that case, and no SSM SendCommand
// for notify is issued (avoids pointless network traffic).
//
// When a CLI flag WAS changed, returns a non-nil *bool matching its value.
func resolveNotifyFlags(cmd *cobra.Command) (perm, idle *bool) {
	if cmd.Flags().Changed("notify-on-permission") {
		v, _ := cmd.Flags().GetBool("notify-on-permission")
		perm = &v
	} else if cmd.Flags().Changed("no-notify-on-permission") {
		f := false
		perm = &f
	}
	if cmd.Flags().Changed("notify-on-idle") {
		v, _ := cmd.Flags().GetBool("notify-on-idle")
		idle = &v
	} else if cmd.Flags().Changed("no-notify-on-idle") {
		f := false
		idle = &f
	}
	return perm, idle
}

// resolveTranscriptFlag returns the effective per-invocation override for
// Phase 68 Slack transcript streaming (Plan 68-07).
//
//   - nil           = neither --transcript-stream nor --no-transcript-stream
//     was supplied; the profile-derived
//     /etc/profile.d/km-notify-env.sh value applies and no
//     SSM SendCommand for transcript is needed.
//   - non-nil true  = --transcript-stream was set; force-enable for session.
//   - non-nil false = --no-transcript-stream was set; force-disable for session.
func resolveTranscriptFlag(cmd *cobra.Command) *bool {
	if cmd.Flags().Changed("transcript-stream") {
		v, _ := cmd.Flags().GetBool("transcript-stream")
		return &v
	}
	if cmd.Flags().Changed("no-transcript-stream") {
		f := false
		return &f
	}
	return nil
}

// buildNotifySendCommands returns the bash command lines for SSM RunShellScript
// to write/remove /etc/profile.d/zz-km-notify.sh with KM_NOTIFY_ON_* and
// KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED overrides.
//
// Returns (nil, nil) when all pointers are nil — no SSM SendCommand is issued.
// When at least one pointer is non-nil, returns a write slice (heredoc + chmod)
// and a cleanup slice (rm -f) to bracket the SSM session.
//
// transcript is the Plan 68-07 override for Slack transcript streaming.
func buildNotifySendCommands(perm, idle, transcript *bool) (writeCmds, cleanupCmds []string) {
	if perm == nil && idle == nil && transcript == nil {
		return nil, nil
	}
	var lines []string
	if perm != nil {
		v := "0"
		if *perm {
			v = "1"
		}
		lines = append(lines, fmt.Sprintf(`export KM_NOTIFY_ON_PERMISSION=%q`, v))
	}
	if idle != nil {
		v := "0"
		if *idle {
			v = "1"
		}
		lines = append(lines, fmt.Sprintf(`export KM_NOTIFY_ON_IDLE=%q`, v))
	}
	if transcript != nil {
		v := "0"
		if *transcript {
			v = "1"
		}
		lines = append(lines, fmt.Sprintf(`export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=%q`, v))
	}
	body := strings.Join(lines, "\n")
	writeCmds = []string{
		fmt.Sprintf("cat > /etc/profile.d/zz-km-notify.sh << 'KM_NOTIFY_OVERRIDE_EOF'\n%s\nKM_NOTIFY_OVERRIDE_EOF", body),
		`chmod 644 /etc/profile.d/zz-km-notify.sh`,
	}
	cleanupCmds = []string{`rm -f /etc/profile.d/zz-km-notify.sh`}
	return writeCmds, cleanupCmds
}

// NewShellCmd creates the "km shell" subcommand using the real AWS-backed fetcher.
// Usage: km shell <sandbox-id>
func NewShellCmd(cfg *config.Config) *cobra.Command {
	return NewShellCmdWithFetcher(cfg, nil, nil)
}

// newShellCmdWithSSM is an internal constructor for tests that need to inject an
// SSM client (e.g. for the --no-bedrock credentials pre-check in AUTH-11 tests).
// Production code uses NewShellCmdWithFetcher (which passes nil ssmClient).
func newShellCmdWithSSM(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var asRoot bool
	var noBedrock bool
	var ports []string
	var learn bool
	var learnOutput string
	var amiFlag bool

	cmd := &cobra.Command{
		Use:          "shell <sandbox-id | #number>",
		Aliases:      []string{"sh"},
		Short:        "Open an interactive shell into a running sandbox",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if amiFlag && !learn {
				return fmt.Errorf("--ami requires --learn")
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			if len(ports) > 0 {
				return runPortForward(cmd, cfg, fetcher, execFn, sandboxID, ports)
			}
			if !cmd.Flags().Changed("no-bedrock") {
				if cliNB := loadProfileCLINoBedrock(ctx, cfg, sandboxID); cliNB {
					noBedrock = true
				}
			}
			notifyPerm, notifyIdle := resolveNotifyFlags(cmd)
			transcriptStream := resolveTranscriptFlag(cmd)
			runErr := runShellWithSSM(cmd, cfg, fetcher, execFn, ssmClient, sandboxID, asRoot, noBedrock, notifyPerm, notifyIdle, transcriptStream)
			if learn {
				if runErr != nil {
					return runErr
				}
				return runLearnPostExit(ctx, cfg, fetcher, sandboxID, learnOutput, amiFlag)
			}
			// Return the error directly — this constructor is used in tests where
			// pre-flight errors (e.g. missing credentials) must propagate. Interactive
			// session exit errors also propagate here (unlike NewShellCmdWithFetcher
			// which intentionally swallows them to avoid spurious cobra output).
			return runErr
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "Connect as root instead of the restricted sandbox user")
	cmd.Flags().BoolVar(&noBedrock, "no-bedrock", false, "Unset Bedrock env vars (use direct Anthropic API)")
	cmd.Flags().StringSliceVar(&ports, "ports", nil, "Port forwards: 8080, 8080:80, or comma-separated list")
	cmd.Flags().BoolVar(&learn, "learn", false, "Run in learning mode: observe traffic and generate profile on exit")
	cmd.Flags().StringVar(&learnOutput, "learn-output", "", "Path to write the generated SandboxProfile YAML")
	cmd.Flags().BoolVar(&amiFlag, "ami", false, "Bake an AMI from the sandbox state when learn-mode exits (requires --learn)")
	cmd.Flags().Bool("notify-on-permission", false, "Email operator on Claude permission prompts (overrides profile default for this session)")
	cmd.Flags().Bool("no-notify-on-permission", false, "Force-disable Claude permission-prompt emails for this session")
	cmd.Flags().Bool("notify-on-idle", false, "Email operator when Claude finishes a turn (overrides profile default for this session)")
	cmd.Flags().Bool("no-notify-on-idle", false, "Force-disable Claude idle emails for this session")
	cmd.Flags().Bool("transcript-stream", false, "Stream Claude transcript turns to per-sandbox Slack thread")
	cmd.Flags().Bool("no-transcript-stream", false, "Force-disable transcript streaming for this session")
	return cmd
}

// NOTE: NewAgentCmd has been moved to agent.go with support for the "run" subcommand.

// NewShellCmdWithFetcher builds the shell command with an optional custom fetcher and
// exec function. Pass nil for real AWS-backed clients. Used in tests for DI.
func NewShellCmdWithFetcher(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) *cobra.Command {
	var asRoot bool
	var noBedrock bool
	var ports []string
	var learn bool
	var learnOutput string
	var amiFlag bool

	cmd := &cobra.Command{
		Use:     "shell <sandbox-id | #number>",
		Aliases: []string{"sh"},
		Short:   "Open an interactive shell into a running sandbox",
		Long: `Open an interactive SSM session into a running sandbox.

Port forwarding:
  --ports 8080         forward localhost:8080 → remote:8080
  --ports 8080:80      forward localhost:8080 → remote:80
  --ports 8080,3000    forward multiple ports
  --ports 8080:80,3000 mix of mapped and same-port forwards`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			if amiFlag && !learn {
				return fmt.Errorf("--ami requires --learn")
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			if len(ports) > 0 {
				return runPortForward(cmd, cfg, fetcher, execFn, sandboxID, ports)
			}
			// If --no-bedrock not explicitly set, check profile cli.noBedrock default
			if !cmd.Flags().Changed("no-bedrock") {
				if cliNB := loadProfileCLINoBedrock(ctx, cfg, sandboxID); cliNB {
					noBedrock = true
				}
			}
			// Phase 62 (HOOK-04): resolve per-invocation notify gate overrides from CLI flags.
			notifyPerm, notifyIdle := resolveNotifyFlags(cmd)
			// Plan 68-07: resolve transcript-stream override from CLI flags.
			transcriptStream := resolveTranscriptFlag(cmd)
			// Run the shell (blocks until user exits). The exec error is
			// deliberately discarded in the non-learn path so cobra does not
			// print a spurious error after a normal session exit (see the
			// TestShellCmd_MissingSSMDoc comment for context).
			runErr := runShell(cmd, cfg, fetcher, execFn, sandboxID, asRoot, noBedrock, notifyPerm, notifyIdle, transcriptStream)

			// --learn post-exit: generate profile from observed traffic.
			// Skip post-exit when the SSM session never landed — otherwise the
			// "no observation data found" error masks the real fault (e.g.
			// "RunAs user sandbox does not exist" from a half-bootstrapped box).
			if learn {
				if runErr != nil {
					return runErr
				}
				return runLearnPostExit(ctx, cfg, fetcher, sandboxID, learnOutput, amiFlag)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&asRoot, "root", false, "Connect as root instead of the restricted sandbox user")
	cmd.Flags().BoolVar(&noBedrock, "no-bedrock", false, "Unset Bedrock env vars (use direct Anthropic API)")
	cmd.Flags().StringSliceVar(&ports, "ports", nil, "Port forwards: 8080, 8080:80, or comma-separated list")
	cmd.Flags().BoolVar(&learn, "learn", false, "Run in learning mode: observe traffic and generate profile on exit")
	cmd.Flags().StringVar(&learnOutput, "learn-output", "", "Path to write the generated SandboxProfile YAML (default: learned.<sandbox-id>.YYYYMMDDHHMMSS.yaml)")
	cmd.Flags().BoolVar(&amiFlag, "ami", false, "Bake an AMI from the sandbox state when learn-mode exits (requires --learn). Snapshot fires before the eBPF flush.")
	// Phase 62 (HOOK-04): notify gate overrides for this session.
	cmd.Flags().Bool("notify-on-permission", false, "Email operator on Claude permission prompts (overrides profile default for this session)")
	cmd.Flags().Bool("no-notify-on-permission", false, "Force-disable Claude permission-prompt emails for this session")
	cmd.Flags().Bool("notify-on-idle", false, "Email operator when Claude finishes a turn (overrides profile default for this session)")
	cmd.Flags().Bool("no-notify-on-idle", false, "Force-disable Claude idle emails for this session")
	// Plan 68-07: per-invocation transcript-stream override (Phase 68 Slack transcript streaming).
	cmd.Flags().Bool("transcript-stream", false, "Stream Claude transcript turns to per-sandbox Slack thread + upload final JSONL (overrides profile default for this session)")
	cmd.Flags().Bool("no-transcript-stream", false, "Force-disable transcript streaming for this session")

	return cmd
}

// runShell is the command RunE logic for km shell.
// notifyPerm, notifyIdle, and transcriptStream are the resolved per-invocation
// overrides (nil = no override; use profile.d defaults from compile time).
// transcriptStream is the Plan 68-07 Slack transcript-streaming override.
// Delegates to runShellWithSSM with a nil ssmClient (real production path:
// the noBedrock pre-check and profile.d write each create their own SSM client).
func runShell(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID string, asRoot, noBedrock bool, notifyPerm, notifyIdle, transcriptStream *bool) error {
	return runShellWithSSM(cmd, cfg, fetcher, execFn, nil, sandboxID, asRoot, noBedrock, notifyPerm, notifyIdle, transcriptStream)
}

// runShellWithSSM is the full implementation of km shell.
// ssmClient may be nil — when nil, the noBedrock pre-check is skipped (production
// path where the check is done via execSSMSession's own client creation). When
// non-nil (test injection path), the credentials pre-check runs before the
// interactive session is opened.
func runShellWithSSM(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, asRoot, noBedrock bool, notifyPerm, notifyIdle, transcriptStream *bool) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml")
		}
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
	}

	if execFn == nil {
		execFn = defaultShellExec
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}

	if rec.Status == "stopped" {
		return fmt.Errorf("sandbox %s is stopped — start it with 'km budget add %s --compute <amount>' first", sandboxID, sandboxID)
	}

	switch rec.Substrate {
	case "ec2", "ec2spot", "ec2demand":
		instanceID, err := extractResourceID(rec.Resources, ":instance/")
		if err != nil {
			return fmt.Errorf("find EC2 instance for sandbox %s: %w", sandboxID, err)
		}

		// Phase 78 (AUTH-11): when --no-bedrock is active and an SSM client is
		// injected (test path) or available, check that ~/.claude/.credentials.json
		// exists on the sandbox before opening the interactive session. This prevents
		// a confusing silent failure where the session opens but claude immediately
		// fails due to a missing OAuth token.
		//
		// Production path: ssmClient is nil here; execSSMSession's noBedrock block
		// creates its own client for the profile.d write. We mirror that approach
		// here for the pre-check so tests can inject the client without touching
		// the production AWS credential path.
		if noBedrock && ssmClient != nil {
			checkOut, checkErr := sendSSMAndWait(ctx, ssmClient, instanceID,
				"stat /home/sandbox/.claude/.credentials.json 2>/dev/null && echo ok || echo missing")
			if checkErr == nil && strings.TrimSpace(checkOut) == "missing" {
				return fmt.Errorf(
					"claude credentials not found on sandbox %s\n"+
						"  Run: km agent auth %s --claude\n"+
						"  Then retry: km shell --no-bedrock %s",
					sandboxID, sandboxID, sandboxID)
			}
			// If checkErr != nil (SSM transient), proceed silently — the interactive
			// session will surface any real auth failure to the operator.
		}

		return execSSMSession(ctx, cfg, instanceID, rec.Region, asRoot, noBedrock, notifyPerm, notifyIdle, transcriptStream, execFn)
	case "ecs":
		clusterARN, err := findResourceARN(rec.Resources, ":cluster/")
		if err != nil {
			return fmt.Errorf("find ECS cluster for sandbox %s: %w", sandboxID, err)
		}
		taskARN, err := findResourceARN(rec.Resources, ":task/")
		if err != nil {
			return fmt.Errorf("find ECS task for sandbox %s: %w", sandboxID, err)
		}
		return execECSCommand(ctx, clusterARN, taskARN, rec.Region, execFn)
	case "docker":
		return execDockerShell(ctx, sandboxID, asRoot, execFn)
	default:
		return fmt.Errorf("unsupported substrate %q for km shell", rec.Substrate)
	}
}

// NOTE: runAgent has been moved to agent.go.

// ssmRetryMaxAttempts is the number of times to retry a transient SSM connection failure.
const ssmRetryMaxAttempts = 3

// ssmRetryThreshold is the minimum session duration to consider "connected".
// Sessions that fail faster than this are treated as transient connection errors.
const ssmRetryThreshold = 10 * time.Second

// ssmRetryDelay is the pause between retry attempts.
const ssmRetryDelay = 3 * time.Second

// execSSMSession builds and runs an SSM session.
// When root is false, it uses the per-install sandbox session document
// (Standard_Stream + runAsDefaultUser=sandbox), which lands as the restricted
// sandbox user with a real PTY so Ctrl+C is forwarded as a byte to the remote
// shell. When root is true, it starts a default SSM session (root via SSM agent).
//
// notifyPerm and notifyIdle are Phase 62 (HOOK-04) per-invocation notify gate
// overrides. transcriptStream is the Plan 68-07 Slack transcript-streaming
// override. nil = no SSM SendCommand for notify (profile.d defaults apply).
// Non-nil = write /etc/profile.d/zz-km-notify.sh before session, remove after.
func execSSMSession(ctx context.Context, cfg *config.Config, instanceID, region string, root, noBedrock bool, notifyPerm, notifyIdle, transcriptStream *bool, execFn ShellExecFunc) error {
	if root {
		return execSSMWithRetry(ctx, func() *exec.Cmd {
			return exec.CommandContext(ctx, "aws", "ssm", "start-session",
				"--target", instanceID, "--region", region, "--profile", "klanker-terraform")
		}, execFn)
	}

	// Non-root: use SSM document to start session as 'sandbox' user
	if noBedrock {
		// Deploy a profile.d script that unsets Bedrock vars (runs last due to zz- prefix).
		// Uses SSM SendCommand then starts the interactive session. Cleaned up on exit.
		awsCfg, ssmErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if ssmErr == nil {
			ssmClient := ssm.NewFromConfig(awsCfg)
			_, _ = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
				InstanceIds:  []string{instanceID},
				DocumentName: awssdk.String("AWS-RunShellScript"),
				Parameters: map[string][]string{
					"commands": {
						`echo 'unset CLAUDE_CODE_USE_BEDROCK ANTHROPIC_BASE_URL ANTHROPIC_DEFAULT_SONNET_MODEL ANTHROPIC_DEFAULT_HAIKU_MODEL ANTHROPIC_DEFAULT_OPUS_MODEL' > /etc/profile.d/zz-km-no-bedrock.sh`,
						`chmod 644 /etc/profile.d/zz-km-no-bedrock.sh`,
					},
				},
			})
			time.Sleep(2 * time.Second)

			// Clean up after session exits
			defer func() {
				_, _ = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
					InstanceIds:  []string{instanceID},
					DocumentName: awssdk.String("AWS-RunShellScript"),
					Parameters:   map[string][]string{"commands": {"rm -f /etc/profile.d/zz-km-no-bedrock.sh"}},
				})
			}()
		}
	}

	// Phase 62 (HOOK-04) + Plan 68-07: per-invocation notify env override.
	// Only issues a SendCommand when at least one pointer is non-nil.
	writeCmds, cleanupCmds := buildNotifySendCommands(notifyPerm, notifyIdle, transcriptStream)
	if len(writeCmds) > 0 {
		awsCfg, ssmErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if ssmErr == nil {
			ssmClient := ssm.NewFromConfig(awsCfg)
			_, _ = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
				InstanceIds:  []string{instanceID},
				DocumentName: awssdk.String("AWS-RunShellScript"),
				Parameters:   map[string][]string{"commands": writeCmds},
			})
			time.Sleep(2 * time.Second)
			defer func() {
				_, _ = ssmClient.SendCommand(context.Background(), &ssm.SendCommandInput{
					InstanceIds:  []string{instanceID},
					DocumentName: awssdk.String("AWS-RunShellScript"),
					Parameters:   map[string][]string{"commands": cleanupCmds},
				})
			}()
		}
	}

	// The sandbox session document (Standard_Stream sessionType) forwards Ctrl+C
	// as a PTY byte (SSH-like) instead of tearing down the session. No
	// --parameters needed: the doc's `command` parameter defaults to "" which
	// triggers the `exec bash -l` branch of the shellProfile (interactive shell
	// as the sandbox user via runAsDefaultUser).
	docName := cfg.GetSandboxSessionDocumentName()
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID, "--region", region, "--profile", "klanker-terraform",
		"--document-name", docName)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	err := runSSMInteractiveSubprocess(execFn, c)
	if isSSMDocumentMissingErr(err) {
		return fmt.Errorf("%s not provisioned in region %s; run `km init %s` to update regional infrastructure (was: %w)", docName, region, region, err)
	}
	return err
}

// isSSMDocumentMissingErr reports whether err looks like an SSM document not
// found / invalid document error (the AWS CLI prints "InvalidDocument",
// "DocumentNotFound", or "document was not found" to stderr depending on
// the API path). Comparison is case-insensitive on the lower-cased message.
// Exported as IsSSMDocumentMissingErr for testing.
func isSSMDocumentMissingErr(err error) bool {
	return IsSSMDocumentMissingErr(err)
}

// IsSSMDocumentMissingErr is the exported form of isSSMDocumentMissingErr,
// available for testing. Reports whether err looks like an SSM document not
// found / invalid document error.
func IsSSMDocumentMissingErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invaliddocument") ||
		strings.Contains(msg, "documentnotfound") ||
		strings.Contains(msg, "document was not found")
}

// execSSMWithRetry runs an SSM session command, retrying on transient failures.
// A failure is considered transient if the session exits (with or without error)
// in less than ssmRetryThreshold — meaning it never established a real connection.
//
// stdout/stderr are passed through directly to preserve TTY capabilities
// (colors, cursor movement, bracketed paste) needed by interactive programs
// like Claude Code. The session-manager-plugin exits 0 even on connection
// failure, but a session that dies in <10s is reliably transient.
func execSSMWithRetry(ctx context.Context, buildCmd func() *exec.Cmd, execFn ShellExecFunc) error {
	var lastErr error
	for attempt := 1; attempt <= ssmRetryMaxAttempts; attempt++ {
		c := buildCmd()
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr

		start := time.Now()
		lastErr = runSSMInteractiveSubprocess(execFn, c)
		elapsed := time.Since(start)

		// If the session lasted long enough, the user was actually connected —
		// don't retry regardless of how it ended.
		if elapsed >= ssmRetryThreshold {
			return lastErr
		}

		// Session died quickly — treat as transient (DNS failure, websocket
		// setup error, EOF, etc.). The session-manager-plugin prints its own
		// error to stderr so the user sees what happened.
		if attempt < ssmRetryMaxAttempts {
			fmt.Fprintf(os.Stderr, "\n⚡ SSM session exited after %s — retrying (%d/%d)...\n\n",
				elapsed.Round(time.Millisecond), attempt, ssmRetryMaxAttempts)
			time.Sleep(ssmRetryDelay)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("SSM session failed after %d attempts: %w", ssmRetryMaxAttempts, lastErr)
	}
	return fmt.Errorf("SSM session failed after %d attempts", ssmRetryMaxAttempts)
}

// execECSCommand builds and runs:
// aws ecs execute-command --cluster <clusterARN> --task <taskARN>
// --interactive --command /bin/bash --region <region>
func execECSCommand(ctx context.Context, clusterARN, taskARN, region string, execFn ShellExecFunc) error {
	c := exec.CommandContext(ctx, "aws", "ecs", "execute-command",
		"--cluster", clusterARN, "--task", taskARN,
		"--interactive", "--command", "/bin/bash", "--region", region, "--profile", "klanker-terraform")
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// runPortForward starts SSM port forwarding sessions for each requested port.
// Ports are specified as "local" (same port both sides) or "local:remote".
func runPortForward(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, sandboxID string, ports []string) error {
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
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
	}
	if execFn == nil {
		execFn = defaultShellExec
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Parse port specs and launch SSM port forwarding sessions
	// For multiple ports, launch all but the last in background, last in foreground.
	parsed := parsePortSpecs(ports)
	if len(parsed) == 0 {
		return fmt.Errorf("no valid port specifications provided")
	}

	fmt.Printf("Port forwarding for %s (%s):\n", sandboxID, instanceID)
	for _, p := range parsed {
		fmt.Printf("  localhost:%s → remote:%s\n", p.local, p.remote)
	}
	fmt.Println()

	// Launch all but the last as background processes
	var bgProcs []*exec.Cmd
	for i := 0; i < len(parsed)-1; i++ {
		p := parsed[i]
		c := buildPortForwardCmd(ctx, instanceID, rec.Region, p.local, p.remote)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] failed to start port forward %s:%s: %v\n", p.local, p.remote, err)
			continue
		}
		bgProcs = append(bgProcs, c)
	}

	// Last port forward runs in foreground (blocks until Ctrl+C)
	last := parsed[len(parsed)-1]
	c := buildPortForwardCmd(ctx, instanceID, rec.Region, last.local, last.remote)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	fgErr := execFn(c)

	// Clean up background processes
	for _, bg := range bgProcs {
		if bg.Process != nil {
			bg.Process.Kill()
		}
	}

	return fgErr
}

type portSpec struct {
	local  string
	remote string
}

// parsePortSpecs parses port specifications like "8080", "8080:80", or comma-separated.
func parsePortSpecs(specs []string) []portSpec {
	var result []portSpec
	for _, spec := range specs {
		// StringSliceVar already splits on comma, but handle nested commas too
		for _, s := range strings.Split(spec, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			parts := strings.SplitN(s, ":", 2)
			if len(parts) == 1 {
				result = append(result, portSpec{local: parts[0], remote: parts[0]})
			} else {
				result = append(result, portSpec{local: parts[0], remote: parts[1]})
			}
		}
	}
	return result
}

// buildPortForwardCmd constructs the AWS SSM port forwarding command.
func buildPortForwardCmd(ctx context.Context, instanceID, region, localPort, remotePort string) *exec.Cmd {
	return exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID,
		"--region", region,
		"--profile", "klanker-terraform",
		"--document-name", "AWS-StartPortForwardingSession",
		"--parameters", fmt.Sprintf(`{"portNumber":["%s"],"localPortNumber":["%s"]}`, remotePort, localPort))
}

// tunnelProbe reports whether the forwarded local port is carrying live traffic.
// Returns true when healthy. Used to detect a silently-hung session-manager-plugin
// (local port still bound, but the underlying WebSocket is dead).
type tunnelProbe func() bool

// httpsTunnelProbe probes a forwarded HTTPS service (e.g. KasmVNC on :8444). Any
// HTTP response — including 401/403 — means the tunnel carries data end-to-end.
// TLS verification is skipped (KasmVNC uses a self-signed/loopback cert).
func httpsTunnelProbe(localPort int) tunnelProbe {
	client := &http.Client{
		Timeout:   6 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // loopback self-signed cert behind the SSM tunnel
	}
	url := fmt.Sprintf("https://127.0.0.1:%d/", localPort)
	return func() bool {
		resp, err := client.Get(url)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	}
}

// sshBannerTunnelProbe probes a forwarded SSH service (e.g. sshd on :22). An SSH
// server sends its "SSH-2.0-…" banner immediately on connect, so a successful
// read confirms the tunnel carries data; a bound-but-silent local port times out.
func sshBannerTunnelProbe(localPort int) tunnelProbe {
	addr := fmt.Sprintf("127.0.0.1:%d", localPort)
	return func() bool {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return false
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 8)
		n, err := conn.Read(buf)
		return err == nil && n > 0
	}
}

// runReconnectingPortForward runs an SSM port-forward and auto-reconnects when the
// tunnel drops, until the operator presses Ctrl-C. The session-manager-plugin has
// no keep-alive and frequently dies (or hangs) on laptop sleep / Wi-Fi roam / NAT
// idle-timeout; the remote service (KasmVNC, sshd) survives, so re-establishing the
// tunnel drops the operator straight back into the same session.
//
// buildCmd must return a FRESH *exec.Cmd each call (exec.CommandContext is
// single-use); it is given a context that is cancelled on Ctrl-C. If probe is
// non-nil, a liveness loop recycles a hung-but-bound plugin so the loop reconnects.
// reconnect=false preserves the old one-shot behaviour (and keeps unit tests with a
// mock execFn from looping).
func runReconnectingPortForward(ctx context.Context, execFn ShellExecFunc, buildCmd func(context.Context) *exec.Cmd, probe tunnelProbe, reconnect bool, out io.Writer) error {
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	attempt := 0
	for {
		cmd := buildCmd(sigCtx)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		var probeDone chan struct{}
		if probe != nil && reconnect {
			probeDone = make(chan struct{})
			go watchTunnelLiveness(sigCtx, cmd, probe, out, probeDone)
		}

		err := execFn(cmd)
		if probeDone != nil {
			close(probeDone)
		}

		// Operator pressed Ctrl-C / TERM — quit cleanly, do not reconnect.
		if sigCtx.Err() != nil {
			return nil
		}
		// Clean exit (incl. mock execFn returning nil) or reconnect disabled — done.
		if err == nil || !reconnect {
			return err
		}
		attempt++
		fmt.Fprintf(out, "\n⚠ SSM tunnel dropped (%v) — reconnecting (attempt %d; Ctrl-C to stop)...\n", err, attempt)
		select {
		case <-sigCtx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// watchTunnelLiveness kills cmd when the probe reports the tunnel unresponsive for
// two consecutive checks, so runReconnectingPortForward re-establishes it. A grace
// period lets the tunnel + remote service come up before the first probe.
func watchTunnelLiveness(ctx context.Context, cmd *exec.Cmd, probe tunnelProbe, out io.Writer, done <-chan struct{}) {
	select {
	case <-time.After(20 * time.Second): // grace: first boot of the remote service
	case <-ctx.Done():
		return
	case <-done:
		return
	}
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	fails := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if probe() {
				fails = 0
				continue
			}
			fails++
			if fails >= 2 {
				fmt.Fprintf(out, "\n⚠ tunnel unresponsive — recycling the SSM session...\n")
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				return
			}
		}
	}
}

// execDockerShell builds and runs: docker exec -it [(-u root)] km-{sandboxID}-main /bin/bash.
// The container name is derived from the sandbox ID using the fixed naming convention
// set in the docker-compose.yml template: container_name: km-{sandboxID}-main.
func execDockerShell(ctx context.Context, sandboxID string, root bool, execFn ShellExecFunc) error {
	containerName := fmt.Sprintf("km-%s-main", sandboxID)
	args := []string{"exec", "-it"}
	if root {
		args = append(args, "-u", "root")
	} else {
		args = append(args, "-u", "sandbox")
	}
	// Use login shell so /etc/profile.d/ scripts run (env vars, shutdown hooks).
	args = append(args, containerName, "bash", "--login")
	c := exec.CommandContext(ctx, "docker", args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return execFn(c)
}

// extractResourceID finds an ARN containing pattern and extracts the resource ID
// portion after the last "/". Example: "arn:....:instance/i-0abc123" -> "i-0abc123".
func extractResourceID(resources []string, pattern string) (string, error) {
	arn, err := findResourceARN(resources, pattern)
	if err != nil {
		return "", err
	}
	parts := strings.Split(arn, "/")
	return parts[len(parts)-1], nil
}

// findResourceARN returns the first ARN in resources that contains pattern.
func findResourceARN(resources []string, pattern string) (string, error) {
	for _, arn := range resources {
		if strings.Contains(arn, pattern) {
			return arn, nil
		}
	}
	return "", fmt.Errorf("no resource matching %q found in %v", pattern, resources)
}

// learnObservedState is the JSON format shared between ebpf-attach --observe
// (EC2) and collectDockerObservations (Docker). Both produce this structure
// which is then consumed by GenerateProfileFromJSON to generate a SandboxProfile.
type learnObservedState struct {
	DNS      []string `json:"dns"`
	Hosts    []string `json:"hosts"`
	Repos    []string `json:"repos"`
	Refs     []string `json:"refs,omitempty"`
	Commands []string `json:"commands,omitempty"`
}

// DefaultLearnFilename returns the default output path for `km shell --learn`
// when the user did not pass --learn-output. The name embeds the sandbox ID
// and a second-precision timestamp so repeated sessions against different
// sandboxes don't collide:
//
//	learned.<sandbox-id>.YYYYMMDDHHMMSS.yaml
//
// An empty sandboxID is tolerated and produces "learned.<timestamp>.yaml".
func DefaultLearnFilename(sandboxID string, now time.Time) string {
	stamp := now.Format("20060102150405")
	if sandboxID == "" {
		return "learned." + stamp + ".yaml"
	}
	return "learned." + sandboxID + "." + stamp + ".yaml"
}

// GenerateProfileFromJSON parses an observed-state JSON blob and returns
// a SandboxProfile YAML. It is exported so tests can call it directly without
// AWS credentials or Docker.
//
// base is an optional profile name for the Extends field (pass "" to omit).
// amiID is an optional AMI ID to embed in spec.runtime.ami (pass "" to omit).
func GenerateProfileFromJSON(data []byte, base, amiID string) ([]byte, error) {
	var state learnObservedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse observed state: %w", err)
	}
	rec := allowlistgen.NewRecorder()
	for _, d := range state.DNS {
		rec.RecordDNSQuery(d)
	}
	for _, h := range state.Hosts {
		rec.RecordHost(h)
	}
	for _, r := range state.Repos {
		rec.RecordRepo(r)
	}
	for _, ref := range state.Refs {
		rec.RecordRef(ref)
	}
	for _, cmd := range state.Commands {
		rec.RecordCommand(cmd)
	}
	if amiID != "" {
		rec.RecordAMI(amiID)
	}
	return rec.GenerateAnnotatedYAML(base)
}

// auditLogLine is the JSON structure for each line in the audit-log container output.
type auditLogLine struct {
	EventType string `json:"event_type"`
	Detail    struct {
		Command string `json:"command"`
	} `json:"detail"`
}

// ParseAuditLogCommands reads JSON-line audit-log container output, extracts
// lines with event_type=="command", and records each command into rec.
// Malformed JSON lines and empty commands are silently skipped.
// Exported so shell_test.go can test it directly without a running container.
func ParseAuditLogCommands(r io.Reader, rec *allowlistgen.Recorder) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry auditLogLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.EventType != "command" {
			continue
		}
		if entry.Detail.Command == "" {
			continue
		}
		rec.RecordCommand(entry.Detail.Command)
	}
}

// CollectDockerObservations reads zerolog JSON from DNS proxy, HTTP proxy, and
// audit-log container log readers (e.g. from docker logs), feeds them into an
// allowlistgen.Recorder, and returns the observed state as JSON.
// Any reader may be nil. Exported for testing without requiring a running Docker daemon.
//
// Container name for audit-log is confirmed against compose.go: km-{{ .SandboxID }}-audit-log.
func CollectDockerObservations(sandboxID string, dnsLogs, httpLogs, auditLogs io.Reader) ([]byte, error) {
	rec := allowlistgen.NewRecorder()
	if err := allowlistgen.ParseProxyLogs(dnsLogs, httpLogs, rec); err != nil {
		return nil, fmt.Errorf("parse proxy logs for sandbox %s: %w", sandboxID, err)
	}
	if auditLogs != nil {
		ParseAuditLogCommands(auditLogs, rec)
	}
	state := learnObservedState{
		DNS:      rec.DNSDomains(),
		Hosts:    rec.Hosts(),
		Repos:    rec.Repos(),
		Refs:     rec.Refs(),
		Commands: rec.Commands(),
	}
	return json.MarshalIndent(state, "", "  ")
}

// runLearnPostExit is called after the shell exits when --learn is active.
// It fetches observed traffic data, generates a SandboxProfile YAML, writes it
// to learnOutput, and uploads the raw observed JSON to S3 for future aggregation.
// bakeAMI triggers an AMI snapshot (EC2 only) before the eBPF flush when true.
func runLearnPostExit(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, sandboxID, learnOutput string, bakeAMI bool) error {
	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox for learn: %w", err)
	}

	var observedJSON []byte
	var amiID string
	var bakeErr error

	switch rec.Substrate {
	case "ec2", "ec2spot", "ec2demand":
		// Bake AMI BEFORE the eBPF flush (CONTEXT.md locked decision: snapshot captures
		// the state the operator just shaped; flush runs after against a stable instance).
		if bakeAMI {
			fmt.Fprintln(os.Stderr, "Baking AMI from sandbox state...")
			amiID, bakeErr = bakeFromSandboxFn(ctx, cfg, *rec, sandboxID, rec.Profile, cfg.Version)
			if bakeErr != nil {
				log.Warn().Err(bakeErr).Msg("AMI bake failed — generating profile without ami field")
				amiID = ""
			} else {
				fmt.Fprintf(os.Stderr, "AMI ready: %s\n", amiID)
			}
		}

		// Trigger SIGUSR1 on the eBPF enforcer to flush observations to disk + S3.
		fmt.Fprintln(os.Stderr, "Flushing eBPF observations...")
		if flushErr := flushEC2ObservationsFn(ctx, cfg, sandboxID); flushErr != nil {
			log.Warn().Err(flushErr).Msg("learn: flush via SIGUSR1 failed (will try S3 anyway)")
		}
		observedJSON, err = fetchEC2ObservedJSONFn(ctx, cfg, sandboxID)
		if err != nil {
			return err
		}

	case "docker":
		if bakeAMI {
			log.Warn().Str("substrate", rec.Substrate).Msg("AMI bake skipped: substrate docker not supported")
		}

		dnsContainer := fmt.Sprintf("km-%s-dns-proxy", sandboxID)
		httpContainer := fmt.Sprintf("km-%s-http-proxy", sandboxID)
		// Container name matches compose.go template: km-{{ .SandboxID }}-audit-log
		auditContainer := fmt.Sprintf("km-%s-audit-log", sandboxID)

		dnsBuf := &bytes.Buffer{}
		httpBuf := &bytes.Buffer{}
		auditBuf := &bytes.Buffer{}

		if dnsOut, dnsErr := exec.CommandContext(ctx, "docker", "logs", dnsContainer).Output(); dnsErr == nil {
			dnsBuf = bytes.NewBuffer(dnsOut)
		} else {
			log.Warn().Err(dnsErr).Str("container", dnsContainer).Msg("learn: failed to get DNS proxy logs (non-fatal)")
		}
		if httpOut, httpErr := exec.CommandContext(ctx, "docker", "logs", httpContainer).Output(); httpErr == nil {
			httpBuf = bytes.NewBuffer(httpOut)
		} else {
			log.Warn().Err(httpErr).Str("container", httpContainer).Msg("learn: failed to get HTTP proxy logs (non-fatal)")
		}
		if auditOut, auditErr := exec.CommandContext(ctx, "docker", "logs", auditContainer).Output(); auditErr == nil {
			auditBuf = bytes.NewBuffer(auditOut)
		} else {
			log.Warn().Err(auditErr).Str("container", auditContainer).Msg("learn: audit-log container logs unavailable (non-fatal)")
		}

		observedJSON, err = CollectDockerObservations(sandboxID, dnsBuf, httpBuf, auditBuf)
		if err != nil {
			return fmt.Errorf("collect docker observations: %w", err)
		}

		// Upload observed JSON to S3 for future aggregation.
		uploadLearnSession(ctx, cfg, sandboxID, observedJSON)

	case "ecs":
		if bakeAMI {
			log.Warn().Str("substrate", rec.Substrate).Msg("AMI bake skipped: substrate ecs not supported")
		}
		fmt.Fprintln(os.Stderr, "Learning mode is not yet supported on ECS substrate. Use EC2 or Docker.")
		return nil

	default:
		return fmt.Errorf("unsupported substrate %q for --learn", rec.Substrate)
	}

	yamlBytes, err := GenerateProfileFromJSON(observedJSON, "", amiID)
	if err != nil {
		return fmt.Errorf("generate profile: %w", err)
	}

	if learnOutput == "" {
		learnOutput = DefaultLearnFilename(sandboxID, time.Now())
	}

	if err := os.WriteFile(learnOutput, yamlBytes, 0o644); err != nil {
		return fmt.Errorf("write profile to %s: %w", learnOutput, err)
	}

	fmt.Fprintf(os.Stderr, "\nGenerated SandboxProfile: %s\nReview and apply with: km validate %s\n", learnOutput, learnOutput)

	// If the AMI bake failed, return a non-nil error so the process exits non-zero.
	// The profile is already written (without the ami field) so the operator is not blocked.
	if bakeErr != nil {
		return fmt.Errorf("AMI bake failed: %w (profile generated without ami field)", bakeErr)
	}
	return nil
}

// flushEC2Observations sends SIGUSR1 to the eBPF enforcer on the instance,
// triggering it to write observed state to disk and upload to S3.
// We wait briefly for the S3 upload to complete.
func flushEC2Observations(ctx context.Context, cfg *config.Config, sandboxID string) error {
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	// Look up instance ID from DynamoDB record.
	fetcher := newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find instance: %w", err)
	}

	ssmClient := ssm.NewFromConfig(awsCfg)
	cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: awssdk.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands": {"pkill -USR1 -f 'km ebpf-attach' && sleep 3"},
		},
	})
	if err != nil {
		return fmt.Errorf("send SIGUSR1 via SSM: %w", err)
	}

	// Wait for the command to complete.
	waiter := ssm.NewCommandExecutedWaiter(ssmClient)
	if waitErr := waiter.Wait(ctx, &ssm.GetCommandInvocationInput{
		CommandId:  cmdOut.Command.CommandId,
		InstanceId: awssdk.String(instanceID),
	}, 30*time.Second); waitErr != nil {
		return fmt.Errorf("wait for flush command: %w", waitErr)
	}

	return nil
}

// fetchEC2ObservedJSON fetches the observed JSON from S3 (primary) or SSM RunCommand
// (fallback) after the sandbox session exits.
func fetchEC2ObservedJSON(ctx context.Context, cfg *config.Config, sandboxID string) ([]byte, error) {
	bucket := cfg.ArtifactsBucket
	if bucket == "" {
		bucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	if bucket == "" {
		return nil, fmt.Errorf("no artifacts bucket configured (set KM_ARTIFACTS_BUCKET or artifacts_bucket in km-config.yaml)")
	}

	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	s3Client := s3.NewFromConfig(awsCfg)

	prefix := fmt.Sprintf("learn/%s/", sandboxID)
	listOut, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: awssdk.String(bucket),
		Prefix: awssdk.String(prefix),
	})
	if err != nil {
		return nil, fmt.Errorf("list S3 learn sessions (prefix %s): %w", prefix, err)
	}

	if len(listOut.Contents) == 0 {
		return nil, fmt.Errorf("no observation data found. Ensure the sandbox was created with learning mode enabled (--observe flag on ebpf-attach)")
	}

	// Find the most recent key (latest timestamp lexicographically).
	latestKey := ""
	for _, obj := range listOut.Contents {
		if obj.Key != nil && (latestKey == "" || *obj.Key > latestKey) {
			latestKey = *obj.Key
		}
	}

	getOut, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(bucket),
		Key:    awssdk.String(latestKey),
	})
	if err != nil {
		return nil, fmt.Errorf("download observed JSON from S3 key %s: %w", latestKey, err)
	}
	defer getOut.Body.Close()

	data, err := io.ReadAll(getOut.Body)
	if err != nil {
		return nil, fmt.Errorf("read observed JSON from S3: %w", err)
	}
	return data, nil
}

// uploadLearnSession uploads the observed JSON to S3 at learn/{sandboxID}/{timestamp}.json.
// Failures are logged as warnings but do not abort the profile generation.
func uploadLearnSession(ctx context.Context, cfg *config.Config, sandboxID string, data []byte) {
	bucket := cfg.ArtifactsBucket
	if bucket == "" {
		bucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	if bucket == "" {
		log.Warn().Msg("learn: KM_ARTIFACTS_BUCKET not set, skipping S3 upload of Docker observations")
		return
	}

	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		log.Warn().Err(err).Msg("learn: failed to load AWS config for S3 upload")
		return
	}
	s3Client := s3.NewFromConfig(awsCfg)

	timestamp := time.Now().UTC().Format("20060102T150405Z")
	key := fmt.Sprintf("learn/%s/%s.json", sandboxID, timestamp)
	_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(key),
		Body:        bytes.NewReader(data),
		ContentType: awssdk.String("application/json"),
	})
	if putErr != nil {
		log.Warn().Err(putErr).Str("key", key).Msg("learn: S3 upload of Docker observations failed (non-fatal)")
		return
	}
	log.Info().Str("key", key).Msg("learn: uploaded Docker observations to S3")
}

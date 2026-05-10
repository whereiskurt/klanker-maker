package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// browserOpener is the platform-specific URL launcher. Tests override it
// to capture invocations without spawning a real subprocess.
var browserOpener = openInBrowser

// runAgentAuthClaudeFn and runAgentAuthCodexFn are package-level dispatch vars
// that allow tests to stub the implementation without touching real AWS.
// The pattern mirrors how agent_test.go stubs runAgent indirection.
var runAgentAuthClaudeFn = runAgentAuthClaude
var runAgentAuthCodexFn = runAgentAuthCodex

// newAgentAuthCmd creates the "km agent auth" subcommand.
// It mediates the OAuth login flow for the Claude and Codex CLIs inside the sandbox,
// using SSM as the operator-laptop ↔ sandbox channel.
func newAgentAuthCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var useClaude bool
	var useCodex bool
	var consoleFlag bool
	var ssoFlag bool
	var claudeaiFlag bool
	var emailFlag string

	cmd := &cobra.Command{
		Use:   "auth <sandbox-id | #number>",
		Short: "Authenticate claude or codex CLI inside a sandbox via SSM",
		Long: `Authenticate the claude or codex CLI inside a running sandbox via SSM.

For --claude: opens an interactive SSM session running 'claude auth login'.
The operator opens the URL printed by the CLI in their browser, completes OAuth
at claude.ai, then pastes the displayed code back into the terminal. km verifies
that ~/.claude/.credentials.json was written on the sandbox.

For --codex (Wave 2): opens an SSM port-forward on localhost:1455 and runs
'codex login' interactively. The browser callback flows through the tunnel.

Default: --claude (when neither flag is set).

Examples:
  km agent auth sb-abc123 --claude
  km agent auth my-sandbox --claude --console
  km agent auth #1 --claude --sso --email me@example.com
  km agent auth sb-abc123 --codex`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			// Default to --claude when neither flag is set
			if !useClaude && !useCodex {
				useClaude = true
			}
			if useClaude && useCodex {
				return fmt.Errorf("--claude and --codex are mutually exclusive")
			}

			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}

			f, e, s, err := resolveAuthDeps(ctx, cfg, fetcher, execFn, ssmClient)
			if err != nil {
				return err
			}

			if useClaude {
				return runAgentAuthClaudeFn(ctx, cfg, f, e, s, sandboxID, consoleFlag, ssoFlag, claudeaiFlag, emailFlag)
			}
			return runAgentAuthCodexFn(ctx, cfg, f, e, s, sandboxID)
		},
	}

	cmd.Flags().BoolVar(&useClaude, "claude", false, "Authenticate the claude CLI (default when neither flag is set)")
	cmd.Flags().BoolVar(&useCodex, "codex", false, "Authenticate the codex CLI via port-forward (Wave 2)")
	cmd.Flags().BoolVar(&consoleFlag, "console", false, "Use Anthropic Console OAuth endpoint")
	cmd.Flags().BoolVar(&ssoFlag, "sso", false, "Use SSO OAuth endpoint")
	cmd.Flags().BoolVar(&claudeaiFlag, "claudeai", false, "Use claude.ai OAuth endpoint (default for --claude path)")
	cmd.Flags().StringVar(&emailFlag, "email", "", "Email address for SSO login (combinable with --sso)")

	return cmd
}

// runAgentAuthClaude mediates the claude CLI's OAuth login flow via an interactive
// SSM session. It verifies credentials were written after the session exits.
func runAgentAuthClaude(ctx context.Context, _ *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, console, sso, claudeai bool, email string) error {
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}

	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	// Pre-flight: sandbox must be running
	if rec.Status != "running" {
		return fmt.Errorf("sandbox %s is not running (status: %s) — run 'km resume %s' first",
			sandboxID, rec.Status, sandboxID)
	}

	// Conflict check: refuse if a km-agent-* tmux session is active on the sandbox
	if err := checkAgentSessionConflict(ctx, ssmClient, instanceID); err != nil {
		return err
	}

	// Build the claude auth login command with pass-through flags
	loginArgs, err := buildClaudeAuthArgs(console, sso, claudeai, email)
	if err != nil {
		return err
	}

	// Tee claude's stdout into a sandbox-scoped temp file so a parallel SSM
	// poller can scrape the OAuth URL and open it in the operator's browser.
	teePath := fmt.Sprintf("/tmp/km-claude-auth-%s.out", sandboxID)
	innerCmd := fmt.Sprintf(
		"source /etc/profile.d/km-profile-env.sh 2>/dev/null; source /etc/profile.d/km-identity.sh 2>/dev/null; rm -f %s; set -o pipefail; %s 2>&1 | tee %s",
		teePath, loginArgs, teePath)
	paramsJSON, err := json.Marshal(map[string][]string{"command": {innerCmd}})
	if err != nil {
		return fmt.Errorf("marshal --parameters: %w", err)
	}

	// Spawn URL detector BEFORE the interactive session so it picks up the
	// OAuth URL as soon as claude prints it. ctx is shared so the goroutine
	// terminates cleanly when the session exits or the operator hits Ctrl-C.
	detectCtx, cancelDetect := context.WithCancel(ctx)
	defer cancelDetect()
	go detectAndOpenOAuthURL(detectCtx, ssmClient, instanceID, teePath)

	printClaudeAuthInstructions(sandboxID, loginArgs)
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
		"--document-name", "KM-Sandbox-Session",
		"--parameters", string(paramsJSON))
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	sessionErr := runSSMInteractiveSubprocess(execFn, c)
	cancelDetect()

	// Best-effort cleanup of the tee file (silent failure is fine — /tmp on the sandbox).
	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, _ = sendSSMAndWait(cleanCtx, ssmClient, instanceID, fmt.Sprintf("rm -f %s", teePath))
	cleanCancel()

	// Post-exit: verify credentials file was written on the sandbox
	return verifyCredentialsWritten(ctx, ssmClient, instanceID, "claude", sandboxID, sessionErr)
}

// detectAndOpenOAuthURL polls the tee'd claude stdout on the sandbox via SSM
// looking for the OAuth authorize URL. When found, it opens the URL in the
// operator's local browser. Silent on failure — operator can still copy the
// URL by hand. Polls every 0.5s for up to 25s (covers slow SSM session start).
func detectAndOpenOAuthURL(ctx context.Context, ssmClient SSMSendAPI, instanceID, teePath string) {
	pollScript := fmt.Sprintf(`for i in $(seq 1 50); do
  url=$(grep -oE 'https://(claude\.com|claude\.ai|console\.anthropic\.com)/[^[:space:]]+' '%s' 2>/dev/null | head -1)
  if [ -n "$url" ]; then echo "$url"; exit 0; fi
  sleep 0.5
done
exit 1`, teePath)

	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, pollScript)
	if err != nil {
		return
	}
	url := strings.TrimSpace(out)
	if !strings.HasPrefix(url, "https://") {
		return
	}
	if err := browserOpener(url); err == nil {
		fmt.Fprintln(os.Stderr, "✓ Opened OAuth URL in your default browser")
	}
}

// printClaudeAuthInstructions prints a clear, framed step-by-step block before
// the SSM session opens. claude's OAuth flow uses a manual-paste redirect that
// reads stdin in non-echo mode, so operators see no feedback while pasting.
// This block sets expectations.
func printClaudeAuthInstructions(sandboxID, loginArgs string) {
	const bar = "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	fmt.Println(bar)
	fmt.Println("  km agent auth — claude OAuth flow")
	fmt.Println(bar)
	fmt.Println("  1. We'll auto-open claude.ai in your default browser.")
	fmt.Println("  2. Authorize, then copy the code shown on the next page.")
	fmt.Println("  3. Paste it below at `Paste code here if prompted >`")
	fmt.Println("     (input is hidden — that's normal — then hit Enter)")
	fmt.Println(bar)
	fmt.Println()
	fmt.Printf("Opening SSM session to run `%s` on %s...\n", loginArgs, sandboxID)
}

// openInBrowser launches the operator's default browser at url. Returns an
// error if the platform isn't supported or the launch command fails to start.
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported OS for browser open: %s", runtime.GOOS)
	}
	return cmd.Start()
}

// runAgentAuthCodex is a Wave-1 stub. The real codex port-forward implementation
// ships in Plan 02 (Wave 2) and replaces this body.
func runAgentAuthCodex(_ context.Context, _ *config.Config, _ SandboxFetcher, _ ShellExecFunc, _ SSMSendAPI, _ string) error {
	return fmt.Errorf("--codex auth flow ships in Plan 02 (Wave 2) — see .planning/phases/78-km-agent-auth-ssm-mediated-oauth-login-for-claude-and-codex-clis-inside-sandboxes-paste-code-for-claude-port-forward-1455-for-codex/78-02-PLAN.md")
}

// probeCodexPort tries 1455 first (codex's hardcoded default), then 1457
// (codex's own fallback). Returns the first available local port, or an error
// listing both as occupied.
//
// Codex CLI cannot be told which port to use — these are the only two it
// will ever bind on the sandbox side. Confirmed in openai/codex
// codex-rs/login/src/server.rs (DEFAULT_PORT=1455, FALLBACK_PORT=1457).
func probeCodexPort() (int, error) {
	for _, port := range []int{1455, 1457} {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf(
		"ports 1455 and 1457 are both in use locally — kill the local listener and retry " +
			"(codex CLI hardcodes both, no override flag exists)")
}

// buildClaudeAuthArgs composes the exact 'claude auth login ...' command string
// based on the passed flag values. Returns an error for invalid flag combinations.
func buildClaudeAuthArgs(console, sso, claudeai bool, email string) (string, error) {
	if console && sso {
		return "", fmt.Errorf("--console and --sso cannot be combined")
	}

	if console {
		return "claude auth login --console", nil
	}

	if sso {
		if email != "" {
			return fmt.Sprintf("claude auth login --sso --email %s", email), nil
		}
		return "claude auth login --sso", nil
	}

	// Default: claudeai (whether or not the flag was explicitly set)
	if email != "" {
		return fmt.Sprintf("claude auth login --claudeai --email %s", email), nil
	}
	return "claude auth login --claudeai", nil
}

// verifyCredentialsWritten checks whether the CLI's credentials file exists on
// the sandbox after the login session exits. cliType must be "claude" or "codex".
// On success, prints a confirmation line to stdout. On failure, wraps sessionErr
// if provided so the operator sees both the session failure and the missing-file fact.
func verifyCredentialsWritten(ctx context.Context, ssmClient SSMSendAPI, instanceID, cliType, sandboxID string, sessionErr error) error {
	var credPath, cliName string
	switch cliType {
	case "codex":
		credPath = "/home/sandbox/.codex/auth.json"
		cliName = "codex"
	default:
		credPath = "/home/sandbox/.claude/.credentials.json"
		cliName = "claude"
	}

	checkCmd := fmt.Sprintf("test -f '%s' && echo ok || echo missing", credPath)
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, checkCmd)
	if err != nil {
		// SSM check failed — report session error if any, else the SSM error
		if sessionErr != nil {
			return fmt.Errorf("auth session error: %w", sessionErr)
		}
		return fmt.Errorf("could not verify credentials: %w", err)
	}

	if strings.TrimSpace(out) == "ok" {
		fmt.Println()
		if cliType == "claude" {
			fmt.Println("✓ OAuth code accepted")
		}
		fmt.Printf("✓ %s credentials written to %s\n", cliName, credPath)
		fmt.Printf("  Run 'km shell --no-bedrock %s' or 'km agent run --no-bedrock %s --prompt ...'\n", sandboxID, sandboxID)
		return nil
	}

	// Credentials not written
	if sessionErr != nil {
		return fmt.Errorf("auth session failed and credentials not found at %s: %w", credPath, sessionErr)
	}
	return fmt.Errorf("session exited but %s credentials not found at %s — login may have been incomplete", cliName, credPath)
}

// checkAgentSessionConflict checks whether a km-agent-* tmux session is active
// on the sandbox. If one is found, it returns a clear error pointing at km agent attach.
func checkAgentSessionConflict(ctx context.Context, ssmClient SSMSendAPI, instanceID string) error {
	checkCmd := "tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^km-agent-' | head -1"
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, checkCmd)
	if err != nil {
		// SSM check failed — proceed; the session will surface any real conflict
		return nil
	}

	sessionName := strings.TrimSpace(out)
	if sessionName != "" {
		return fmt.Errorf("agent session '%s' is running on sandbox — wait for completion or run 'km agent attach <sandbox>' to monitor",
			sessionName)
	}
	return nil
}

// resolveAuthDeps initialises real AWS clients when test-injected nil deps are provided.
// Mirrors resolveVSCodeDeps from vscode.go.
func resolveAuthDeps(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) (SandboxFetcher, ShellExecFunc, SSMSendAPI, error) {
	if fetcher == nil {
		if cfg.StateBucket == "" {
			return nil, nil, nil, fmt.Errorf("state bucket not configured")
		}
		awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
		if ssmClient == nil {
			ssmClient = ssm.NewFromConfig(awsCfg)
		}
	}
	if execFn == nil {
		execFn = defaultShellExec
	}
	return fetcher, execFn, ssmClient, nil
}

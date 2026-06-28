package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
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
func runAgentAuthClaude(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, console, sso, claudeai bool, email string) error {
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
	docName := cfg.GetSandboxSessionDocumentName()
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
		"--document-name", docName,
		"--parameters", string(paramsJSON))
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	sessionErr := runSSMInteractiveSubprocess(execFn, c)
	cancelDetect()

	// Post-exit: verify auth via `claude auth status` (BEFORE cleanup so the
	// verifier can peek at the tee file for a precise diagnostic when status
	// reports loggedIn=false despite OAuth succeeding — see verifyClaudeAuthStatus).
	verifyErr := verifyCredentialsWritten(ctx, ssmClient, instanceID, "claude", sandboxID, sessionErr)

	// Best-effort cleanup of the tee file (silent failure is fine — /tmp on the sandbox).
	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, _ = sendSSMAndWait(cleanCtx, ssmClient, instanceID, fmt.Sprintf("rm -f %s", teePath))
	cleanCancel()

	if verifyErr != nil {
		return verifyErr
	}

	// `claude auth login --claudeai` writes ~/.claude/.credentials.json but does
	// not mark the first-launch wizard complete. Without that, interactive
	// `claude` re-runs the wizard's "Select login method" screen and discards
	// the OAuth tokens we just persisted. Set the gating keys directly so the
	// REPL trusts what we just authed.
	if err := markClaudeOnboardingComplete(ctx, ssmClient, instanceID); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not mark claude onboarding complete: %v\n"+
			"  Interactive `claude` may still prompt to log in. Run `claude` once on the sandbox\n"+
			"  and complete the wizard manually if it does.\n", err)
	}
	return nil
}

// markClaudeOnboardingComplete writes hasCompletedOnboarding=true and
// lastOnboardingVersion=<claude --version> into ~/.claude.json on the sandbox.
// These two keys are what Claude Code v2.1.x checks before deciding whether to
// run the first-launch wizard. Without them the wizard re-runs every REPL
// startup and re-issues OAuth, discarding what `claude auth login --claudeai`
// wrote moments earlier. Idempotent — re-running this is safe.
//
// Runs the JSON read-modify-write as the sandbox user so file ownership and
// HOME both resolve correctly (SSM SendCommand runs as root by default).
//
// The python source is base64-encoded so the transport (SSM RunShellScript
// document) cannot strip newlines — earlier attempts with python3 -c and
// nested heredocs got their multi-line bodies collapsed to a single line
// before reaching the interpreter.
func markClaudeOnboardingComplete(ctx context.Context, ssmClient SSMSendAPI, instanceID string) error {
	pyBody := `import json, os, subprocess, pathlib
path = pathlib.Path("/home/sandbox/.claude.json")
try:
    data = json.loads(path.read_text()) if path.exists() else {}
except json.JSONDecodeError:
    data = {}
try:
    ver = subprocess.check_output(["claude", "--version"], text=True).strip().split()[0]
except Exception:
    ver = ""
data["hasCompletedOnboarding"] = True
if ver:
    data["lastOnboardingVersion"] = ver
path.write_text(json.dumps(data, indent=2))
os.chmod(path, 0o600)
print("ok")
`
	b64 := base64.StdEncoding.EncodeToString([]byte(pyBody))
	// Decode the base64 blob, pipe into `python3` running as the sandbox user.
	// `sudo -u sandbox -i` would also reset $PATH and CWD — that's fine, the
	// script only uses absolute paths and runs `claude --version` via PATH.
	script := fmt.Sprintf("echo %s | base64 -d | sudo -u sandbox -i python3", b64)
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, script)
	if err != nil {
		return fmt.Errorf("ssm: %w", err)
	}
	if strings.TrimSpace(out) != "ok" {
		return fmt.Errorf("unexpected output: %q", strings.TrimSpace(out))
	}
	return nil
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

// detectAndOpenCodexURL is the codex-flavored sibling of detectAndOpenOAuthURL.
// codex prints its OAuth URL pointing at auth.openai.com (with localhost:1455
// callback). Polls every 0.5s for up to 25s.
func detectAndOpenCodexURL(ctx context.Context, ssmClient SSMSendAPI, instanceID, teePath string) {
	pollScript := fmt.Sprintf(`for i in $(seq 1 50); do
  url=$(grep -oE 'https://auth\.openai\.com/[^[:space:]]+' '%s' 2>/dev/null | head -1)
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

// runAgentAuthCodex mediates the codex CLI's OAuth login flow via an SSM port-forward.
//
// Lifecycle (mirrors runVSCodeStart in vscode.go):
//  1. Fetch sandbox record + extract EC2 instance ID.
//  2. Pre-flight: sandbox must be running.
//  3. Conflict check: refuse if a km-agent-* tmux session is active.
//  4. Probe local port availability (1455 primary, 1457 fallback).
//  5. Start background SSM port-forward localhost:PORT ↔ sandbox:PORT.
//  6. defer kill the background process — covers success, error, and panic paths.
//  7. Sleep 1s for session-manager-plugin to bind the local port.
//  8. Open foreground interactive SSM session running "codex login".
//  9. Verify ~/.codex/auth.json was written post-session.
//
// The SSM port-forward enables the codex OAuth callback:
// browser hits laptop:1455 → SSM tunnel → sandbox:1455 where codex's callback
// server completes the token exchange and writes ~/.codex/auth.json.
func runAgentAuthCodex(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string) error {
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

	// Probe local port availability (1455 primary, 1457 fallback per codex source)
	localPort, err := probeCodexPort()
	if err != nil {
		return err
	}

	fmt.Printf("Opening SSM port-forward localhost:%d ↔ sandbox:%d for codex login...\n", localPort, localPort)

	// Start background port-forward subprocess.
	// Both localPort and remotePort use the same chosen port because codex binds
	// the same port (1455 or 1457) on the sandbox side that it expects on the client.
	pfCmd := buildPortForwardCmd(ctx, instanceID, rec.Region, strconv.Itoa(localPort), strconv.Itoa(localPort))
	pfCmd.Stdout = os.Stdout
	pfCmd.Stderr = os.Stderr
	if err := pfCmd.Start(); err != nil {
		return fmt.Errorf("start SSM port-forward: %w", err)
	}
	// CRITICAL: deferred Kill covers all exit paths (success, error, panic).
	// runSSMInteractiveSubprocess masks SIGINT process-wide, so Ctrl+C during
	// the foreground session does NOT propagate to the background process.
	// Only the deferred Kill cleans it up reliably.
	defer func() {
		if pfCmd.Process != nil {
			_ = pfCmd.Process.Kill()
		}
	}()

	// Allow session-manager-plugin time to bind the local port before codex starts.
	sleep(1 * time.Second)

	// Open foreground codex login session.
	// codex prints the OAuth URL to stdout (xdg-open fails on headless EC2).
	// Tee that stdout into a sandbox-scoped temp file so a parallel SSM poller
	// can scrape the OAuth URL and open it on the operator's laptop. The
	// browser callback then flows through the SSM port-forward to codex's
	// callback server on the sandbox.
	teePath := fmt.Sprintf("/tmp/km-codex-auth-%s.out", sandboxID)
	innerCmd := fmt.Sprintf(
		"source /etc/profile.d/km-profile-env.sh 2>/dev/null; rm -f %s; set -o pipefail; codex login 2>&1 | tee %s",
		teePath, teePath)
	paramsJSON, err := json.Marshal(map[string][]string{"command": {innerCmd}})
	if err != nil {
		return fmt.Errorf("marshal --parameters: %w", err)
	}

	detectCtx, cancelDetect := context.WithCancel(ctx)
	defer cancelDetect()
	go detectAndOpenCodexURL(detectCtx, ssmClient, instanceID, teePath)

	docName := cfg.GetSandboxSessionDocumentName()
	c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
		"--target", instanceID,
		"--region", rec.Region,
		"--profile", "klanker-terraform",
		"--document-name", docName,
		"--parameters", string(paramsJSON))
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	sessionErr := runSSMInteractiveSubprocess(execFn, c)
	cancelDetect()

	// Best-effort cleanup of the tee file on the sandbox.
	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, _ = sendSSMAndWait(cleanCtx, ssmClient, instanceID, fmt.Sprintf("rm -f %s", teePath))
	cleanCancel()

	// Post-exit: verify credentials file was written on the sandbox.
	// deferred pfCmd.Process.Kill() fires after this returns.
	return verifyCredentialsWritten(ctx, ssmClient, instanceID, "codex", sandboxID, sessionErr)
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

// verifyCredentialsWritten checks whether the CLI is authenticated after the
// login session exits. cliType must be "claude" or "codex".
//
// For claude: runs `claude auth status` and parses the loggedIn JSON field.
// This is more robust than a file-existence check because v2.1.x claude on
// Linux uses libsecret/keyring for credential storage when available and
// silently leaves credentials only in-memory when neither libsecret nor
// gnome-keyring is installed (common on minimal headless AMIs). The
// authoritative source of truth is `claude auth status`, not a file path
// that may or may not exist depending on the storage backend.
//
// For codex: file-based check at /home/sandbox/.codex/auth.json (unchanged
// — codex writes there by design).
//
// On success, prints a confirmation line. On failure, distinguishes between:
//   - OAuth-succeeded-but-not-persisted (claude printed "Login successful"
//     but `auth status` says loggedIn=false → libsecret missing diagnostic)
//   - Login-actually-incomplete (no Login successful, just bailed out)
func verifyCredentialsWritten(ctx context.Context, ssmClient SSMSendAPI, instanceID, cliType, sandboxID string, sessionErr error) error {
	if cliType == "claude" {
		return verifyClaudeAuthStatus(ctx, ssmClient, instanceID, sandboxID, sessionErr)
	}

	// codex still uses file-based check (codex writes ~/.codex/auth.json
	// directly with no keyring dependency).
	const credPath = "/home/sandbox/.codex/auth.json"
	const cliName = "codex"

	checkCmd := fmt.Sprintf("test -f '%s' && echo ok || echo missing", credPath)
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, checkCmd)
	if err != nil {
		if sessionErr != nil {
			return fmt.Errorf("auth session error: %w", sessionErr)
		}
		return fmt.Errorf("could not verify credentials: %w", err)
	}

	if strings.TrimSpace(out) == "ok" {
		fmt.Println()
		fmt.Printf("✓ %s credentials written to %s\n", cliName, credPath)
		fmt.Printf("  Run 'km shell --no-bedrock %s' or 'km agent run --no-bedrock %s --prompt ...'\n", sandboxID, sandboxID)
		return nil
	}

	if sessionErr != nil {
		return fmt.Errorf("auth session failed and credentials not found at %s: %w", credPath, sessionErr)
	}
	return fmt.Errorf("session exited but %s credentials not found at %s — login may have been incomplete", cliName, credPath)
}

// verifyClaudeAuthStatus runs `claude auth status` on the sandbox and parses
// the JSON output. loggedIn=true is the authoritative success signal,
// regardless of where credentials actually live on disk.
//
// On loggedIn=false, also peeks at the tee file from the auth session to see
// whether OAuth completed ("Login successful" appears in stdout) and emits a
// targeted diagnostic about the libsecret-missing failure mode. The tee path
// is the same one the caller wrote to, by convention.
func verifyClaudeAuthStatus(ctx context.Context, ssmClient SSMSendAPI, instanceID, sandboxID string, sessionErr error) error {
	const cliName = "claude"
	statusCmd := "sudo -u sandbox bash -lc 'claude auth status 2>&1' 2>&1"
	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, statusCmd)
	if err != nil {
		if sessionErr != nil {
			return fmt.Errorf("auth session error: %w", sessionErr)
		}
		return fmt.Errorf("could not verify claude auth status: %w", err)
	}

	// claude auth status emits a JSON object with a `loggedIn` boolean.
	// We do a string-level check because (a) the surrounding lines may
	// include other config (`apiProvider`, `authMethod`), (b) we don't want
	// to fail noisily on minor format drift between claude versions.
	statusOut := strings.TrimSpace(out)
	if strings.Contains(statusOut, `"loggedIn": true`) || strings.Contains(statusOut, `"loggedIn":true`) {
		fmt.Println()
		fmt.Println("✓ OAuth complete; claude reports authenticated")
		fmt.Printf("  Verified via: claude auth status (loggedIn=true)\n")
		fmt.Printf("  Run 'km shell --no-bedrock %s' or 'km agent run --no-bedrock %s --prompt ...'\n", sandboxID, sandboxID)
		return nil
	}

	// Not logged in. Was OAuth at least started? Check the tee file (it may
	// have been cleaned up by the caller — best-effort).
	teePath := fmt.Sprintf("/tmp/km-claude-auth-%s.out", sandboxID)
	teePeekCmd := fmt.Sprintf("cat '%s' 2>/dev/null | tail -20 || true", teePath)
	teeOut, _ := sendSSMAndWait(ctx, ssmClient, instanceID, teePeekCmd)
	oauthAppearedComplete := strings.Contains(teeOut, "Login successful")

	if oauthAppearedComplete {
		// This is the silent-persistence-failure case the operator just hit.
		msg := strings.Builder{}
		msg.WriteString("claude reported OAuth success but `claude auth status` says loggedIn=false.\n")
		msg.WriteString("\n")
		msg.WriteString("  Most likely cause: claude v2.1.x on Linux persists OAuth tokens via libsecret\n")
		msg.WriteString("  (system keyring). When neither libsecret nor gnome-keyring is installed (common\n")
		msg.WriteString("  on minimal headless AMIs), the token exchange succeeds but the token is held\n")
		msg.WriteString("  in-memory only, then lost when the auth process exits.\n")
		msg.WriteString("\n")
		msg.WriteString("  Workarounds:\n")
		msg.WriteString("    1. Use Bedrock-mode profiles (useBedrock: true) — IAM-based auth, no keyring needed.\n")
		msg.WriteString("    2. Set CLAUDE_CODE_OAUTH_TOKEN env-var in spec.execution.env with a token from\n")
		msg.WriteString("       a desktop machine where `claude auth login` works (see OPERATOR-GUIDE.md).\n")
		msg.WriteString("    3. (Brittle) Install libsecret + gnome-keyring + dbus-launch session bus.\n")
		if sessionErr != nil {
			msg.WriteString(fmt.Sprintf("\n  (Session also reported: %v)\n", sessionErr))
		}
		return fmt.Errorf("%s OAuth succeeded but credentials were not persisted: %s", cliName, msg.String())
	}

	// OAuth did not complete (no "Login successful" in tee, or tee unavailable).
	if sessionErr != nil {
		return fmt.Errorf("auth session ended without completing OAuth: %w", sessionErr)
	}
	return fmt.Errorf("session exited but `claude auth status` reports loggedIn=false — OAuth flow may have been interrupted (browser tab closed without authorizing, or paste step skipped)")
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

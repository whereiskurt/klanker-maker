# Phase 78: km agent auth — Research

**Researched:** 2026-05-10
**Domain:** Go/Cobra CLI authoring; AWS SSM Session Manager (interactive + port-forward); file-mtime polling via SSM RunCommand; OAuth flow mediation
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**CLI surface**
- New subcommand: `km agent auth <sandbox-id> [flags]`
- Default flag: `--claude` (no flag = `--claude`)
- Alternate: `--codex`
- Sandbox identifier: same formats as `km shell` / `km vscode start` (full ID, alias, row number)
- Pass-through flags for `--claude`: `--console`, `--sso`, `--claudeai` (default), `--email <addr>`
- Skip codex `--api-key` shortcut for v1

**Auth flow — `--claude` (paste-the-code)**
- claude CLI uses a manual redirect URI, NO localhost callback server
- Binary contains `MANUAL_REDIRECT_URL: https://platform.claude.com/oauth/code/callback`
- Implementation: SSM interactive session running `claude auth login [flags]` as sandbox user; operator opens URL in laptop browser; hosted page shows code; operator pastes back; CLI writes `~/.claude/.credentials.json` on sandbox
- Success signal: `~/.claude/.credentials.json` exists/mtime advanced AND CLI exits 0

**Auth flow — `--codex` (port-forward 1455)**
- codex uses `127.0.0.1:1455` (fallback 1457) — no CLI flag to override port
- Implementation: SSM port-forward `localhost:1455 ↔ sandbox:1455` (reuse `buildPortForwardCmd`), SSM-exec `codex login`, capture/relay URL, operator clicks, callback flows back through tunnel, codex writes `~/.codex/auth.json`
- Success signal: codex CLI exits 0 AND `~/.codex/auth.json` mtime advanced
- Port-collision: try 1455, then 1457; both taken → fail fast with clear error

**Wave phasing**
- Wave 1: `--claude` path (single PR)
- Wave 2: `--codex` path (port-forward lifecycle)

**Auto-trigger from km shell / km agent run**
- Missing credentials → print hint ("Run `km agent auth <sandbox>` first") → exit non-zero
- Do NOT silently auto-bootstrap

**Conflict handling**
- If `km agent run` tmux session in flight → refuse with error pointing at `km agent attach`
- Use dedicated tmux session name `km-auth-<random>` (distinct from `km-agent-*`)

**Reuse / no new infra**
- Reuse existing SSM helpers, `ResolveSandboxID`, `buildPortForwardCmd`
- New file: `internal/app/cmd/agent_auth.go` (or similar)
- NO new IAM, SSM parameters, DDB schema, infra/modules, userdata, Lambda changes

**Token paths on sandbox** (confirmed in-session)
- Claude: `~/.claude/.credentials.json`
- Codex: `~/.codex/auth.json`

### Claude's Discretion
- Exact filename/path within `internal/app/cmd/`
- Operator-side URL relay for codex (auto-open vs print)
- Test layout within established `pkg/aws/ssm/` patterns
- Polling interval/timeout (suggested: 1s poll, 10-minute timeout)
- Error message wording for missing-credentials hint
- Whether to add `km agent auth status <sandbox>` subcommand

### Deferred Ideas (OUT OF SCOPE)
- `codex login --api-key <KEY>` mode (different auth path)
- Token rotation / refresh-on-expiry orchestration
- Operator-laptop ↔ sandbox token migration
- Multi-user / concurrent auth on same sandbox
- `km agent auth status <sandbox>` (may implement only if trivial)
- Auto-bootstrap (silent re-auth) inside `km shell --no-bedrock`
- Web/UI integration
</user_constraints>

---

## Summary

Phase 78 adds a new `km agent auth` subcommand to `internal/app/cmd/agent_auth.go`. The implementation draws on two well-established SSM patterns already in the codebase: (1) the interactive SSM session path from `km shell` / `km agent <sb> --claude` and (2) the foreground port-forward path from `km vscode start` and `km shell --ports`. No new AWS primitives are needed.

The `--claude` path (Wave 1) is the thin-wrapper case: call `execSSMSession` (or factored equivalent) with `claude auth login` as the inner command, identical to how `km agent <sb> --claude` starts an interactive Claude session, then poll `~/.claude/.credentials.json` existence via `sendSSMAndWait`. Wave 1 is small enough for a single Cobra command + interactive SSM session + mtime check.

The `--codex` path (Wave 2) requires concurrent state: a goroutine holding the SSM port-forward open (via `buildPortForwardCmd` + background goroutine), a second SSM interactive session running `codex login` (foreground), URL capture from stdout, operator-side URL relay, then teardown. This mirrors the pattern `km shell --ports` uses for background port-forwards, but adds a second foreground interactive session on top.

**Primary recommendation:** Register `newAgentAuthCmd` as an `AddCommand` child of the existing `NewAgentCmd` parent (same pattern as `attach`, `run`, `results`, `list`). Wave 1 ships in a single tight PR; Wave 2 adds the port-forward goroutine.

---

## Requirements Mapping

Phase 78 is operator-tooling with no direct mapping to existing `SCHM-*`, `PROV-*`, `NETW-*`, or `HOOK-*` requirements in `REQUIREMENTS.md`. The requirements file explicitly defers advanced operator experience items. No new requirement IDs are needed; the planner should note "no REQUIREMENTS.md mapping — operator ergonomics phase" in the plan header.

---

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/spf13/cobra` | (project-pinned) | Cobra command/subcommand | All km subcommands use Cobra |
| `github.com/aws/aws-sdk-go-v2/service/ssm` | (project-pinned) | SSM SendCommand + session | Already used throughout cmd package |
| `os/exec` | stdlib | Subprocess for SSM session-manager-plugin | Same as `km shell`, `km vscode start` |
| `net` | stdlib | Local TCP port probe (collision check) | Same as `runVSCodeStart` |
| `os/signal` | stdlib | Signal masking during SSM interactive subprocess | Same as `runSSMInteractiveSubprocess` |

### No New Dependencies

Phase 78 adds zero new Go module dependencies. All needed packages are already in `go.mod`.

---

## Architecture Patterns

### Existing SSM Primitive Inventory

**HIGH confidence** — read from source.

#### 1. Interactive SSM session (`KM-Sandbox-Session` document)

Used by `km shell`, `km agent <sb> --claude`, `km agent attach`, `km agent run --interactive`.

```go
// shell.go:393-403 — interactive session pattern
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", region, "--profile", "klanker-terraform",
    "--document-name", "KM-Sandbox-Session",
    "--parameters", paramsJSON)  // {"command": ["<inner-cmd>"]}
c.Stdin = os.Stdin
c.Stdout = os.Stdout
c.Stderr = os.Stderr
err = runSSMInteractiveSubprocess(execFn, c)
```

`runSSMInteractiveSubprocess` masks SIGINT, SIGQUIT, SIGTSTP for the subprocess duration so Ctrl+C reaches the remote PTY instead of killing km.

For `km agent auth --claude`: the inner command is `source /etc/profile.d/km-profile-env.sh 2>/dev/null; claude auth login [flags]`. This is almost identical to how `runAgent` composes the `claude` command, but using the auth login subcommand instead.

#### 2. Background SSM port-forward (`AWS-StartPortForwardingSession` document)

Used by `km shell --ports` (multiple ports in background/foreground), `km vscode start` (foreground only, blocks until Ctrl-C).

```go
// shell.go:586-593 — buildPortForwardCmd (exported, reuse directly)
func buildPortForwardCmd(ctx context.Context, instanceID, region, localPort, remotePort string) *exec.Cmd {
    return exec.CommandContext(ctx, "aws", "ssm", "start-session",
        "--target", instanceID,
        "--region", region,
        "--profile", "klanker-terraform",
        "--document-name", "AWS-StartPortForwardingSession",
        "--parameters", fmt.Sprintf(`{"portNumber":["%s"],"localPortNumber":["%s"]}`, remotePort, localPort))
}
```

For `km agent auth --codex` (Wave 2): call `buildPortForwardCmd(ctx, instanceID, region, "1455", "1455")`, start it with `cmd.Start()` in a background goroutine, then open the foreground `codex login` interactive session.

#### 3. SSM RunCommand fire-and-wait (`sendSSMAndWait`)

Used everywhere for short utility commands: `km vscode status`, `km vscode rekey`, `km agent results`, `km agent list`, `km vscode rekey` readback check.

```go
// agent.go:1071 — sendSSMAndWait (package-internal, call directly from agent_auth.go)
func sendSSMAndWait(ctx context.Context, ssmClient SSMSendAPI, instanceID, shellCmd string) (string, error)
```

Polls `GetCommandInvocation` at 2s intervals, max 5 minutes, returns stdout content. For Phase 78: use to check `stat ~/.claude/.credentials.json` or `stat ~/.codex/auth.json` after login completes.

### Recommended File Structure

```
internal/app/cmd/
├── agent.go                   # existing — NewAgentCmd, runAgent, runAgentNonInteractive, etc.
├── agent_auth.go              # NEW — newAgentAuthCmd, runAgentAuth, pollCredFile
├── agent_auth_test.go         # NEW — unit tests for Cobra wiring + mtime polling logic
└── agent_test.go              # existing — do not modify
```

`agent_auth.go` registers `newAgentAuthCmd` inside `NewAgentCmdWithDeps` via `cmd.AddCommand(newAgentAuthCmd(cfg, fetcher, execFn, ssmClient))`.

### Pattern: Subcommand Registration

```go
// agent.go (existing pattern to follow)
func NewAgentCmdWithDeps(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, ebClient kmaws.EventBridgeAPI, s3Client S3GetAPI) *cobra.Command {
    // ... existing flags and RunE ...
    cmd.AddCommand(newAgentAttachCmd(cfg, fetcher, execFn))
    cmd.AddCommand(newAgentRunCmd(cfg, fetcher, execFn, ssmClient, ebClient))
    cmd.AddCommand(newAgentResultsCmd(cfg, fetcher, ssmClient, s3Client))
    cmd.AddCommand(newAgentListCmd(cfg, fetcher, ssmClient, s3Client))
    // ADD:
    cmd.AddCommand(newAgentAuthCmd(cfg, fetcher, execFn, ssmClient))
    return cmd
}
```

### Pattern: Resolving Sandbox ID

```go
// sandbox_ref.go:25 — already exported, call directly
sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
// Accepts: "sb-abc123" / "learn-14853201" / alias / "#3108"
```

### Pattern: AWS Client Initialization (deps resolution)

Follow `resolveVSCodeDeps` pattern from `vscode.go:102`:

```go
func resolveAuthDeps(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) (SandboxFetcher, ShellExecFunc, SSMSendAPI, error) {
    if fetcher == nil {
        awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
        if err != nil { return nil, nil, nil, fmt.Errorf("load AWS config: %w", err) }
        fetcher = newRealFetcher(awsCfg, cfg.StateBucket, cfg.GetSandboxTableName())
        if ssmClient == nil { ssmClient = ssm.NewFromConfig(awsCfg) }
    }
    if execFn == nil { execFn = defaultShellExec }
    return fetcher, execFn, ssmClient, nil
}
```

### Pattern: Local Port Probe (Wave 2 — codex)

Exact same pattern as `runVSCodeStart`:

```go
// vscode.go:131-135
probeLn, probeErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
if probeErr != nil {
    return fmt.Errorf("local port %d is already in use — ...", localPort)
}
probeLn.Close()
```

For codex Wave 2: try 1455 first, then 1457 (codex's own fallback). If both fail, return error.

### Pattern: Credentials-File Mtime Polling via SSM

No existing pattern for this in the codebase — must implement from scratch using `sendSSMAndWait`. The SSM command to use:

```bash
# For claude:
stat -c %Y /home/sandbox/.claude/.credentials.json 2>/dev/null || echo ""

# For codex:
stat -c %Y /home/sandbox/.codex/auth.json 2>/dev/null || echo ""
```

Poll every 1 second in a goroutine with a 10-minute total timeout. On the main goroutine, wait for the interactive session to complete (process exit). Whichever fires first (process exit OR mtime advance) determines the outcome.

**Implementation approach for Wave 1 (`--claude`):** poll in a goroutine; if `~/.claude/.credentials.json` appears AND the interactive session exited 0, print success. If session exits non-zero, report failure. If timeout reached before file appears, warn operator that credentials may not have been written.

### Pattern: Conflict Detection (active agent run)

Before opening the auth session, check whether any `km-agent-*` tmux session is active on the sandbox:

```bash
# SSM RunCommand check
tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^km-agent-' | head -1
```

Use `sendSSMAndWait` for this check. If output is non-empty, return error: "agent session `km-agent-<id>` is running — wait for it to complete or use `km agent attach <sandbox>` to monitor".

### Pattern: Signal Handling for Long-Running Port-Forward (Wave 2)

For the codex path: after starting the background port-forward goroutine, the main thread opens the foreground interactive session. When the interactive session exits (success or failure), kill the background port-forward via `cmd.Process.Kill()`. Pattern from `runPortForward` in `shell.go:549-556`.

### Anti-Patterns to Avoid

- **Calling `execSSMWithRetry` for auth sessions:** auth sessions are interactive and user-initiated; retry logic would create confusing "retrying" messages mid-OAuth-flow. Use `runSSMInteractiveSubprocess(execFn, c)` directly instead.
- **Running codex login without the port-forward up:** the port-forward must be confirmed started (goroutine running, process alive) before starting `codex login`. A `time.Sleep(1 * time.Second)` after `cmd.Start()` is acceptable as the session-manager-plugin print indicates readiness; or check stdout for "Port xxxx opened" from the plugin before proceeding.
- **Using `defaultShellExec` directly instead of `execFn`:** always accept the injected `execFn` parameter so tests can stub subprocess execution.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Sandbox ID resolution | Custom alias/number lookup | `ResolveSandboxID(ctx, cfg, ref)` | DynamoDB GSI + local number file already implemented |
| Port-forward subprocess | Custom SSM API call | `buildPortForwardCmd(ctx, instanceID, region, local, remote)` | Already correct, returns `*exec.Cmd` |
| SSM RunCommand + poll | Custom polling loop | `sendSSMAndWait(ctx, ssmClient, instanceID, cmd)` | Returns stdout, handles 5-min timeout |
| Interactive SSM session | Raw `aws ssm start-session` subprocess | `runSSMInteractiveSubprocess(execFn, c)` | Signal masking already handled |
| AWS client init | `aws.NewConfig()` directly | `kmaws.LoadAWSConfig(ctx, "klanker-terraform")` | Profile name is a project convention |
| Local port availability check | `net.Dial` retry | `net.Listen("tcp", "127.0.0.1:PORT")` probe-and-close | Exact same pattern as `runVSCodeStart` |

---

## Common Pitfalls

### Pitfall 1: `sendSSMAndWait` 24KB stdout limit

**What goes wrong:** SSM RunCommand truncates stdout at 24KB. `~/.claude/.credentials.json` is typically small (< 2KB), but `~/.codex/auth.json` may be larger. The mtime-check command (`stat -c %Y ...`) returns only a 10-digit integer, so this is not a practical concern.
**Why it happens:** SSM design limit.
**How to avoid:** Never use `sendSSMAndWait` to cat credentials files — only use it to check mtime values.

### Pitfall 2: SSM agent not ready after very recent `km resume`

**What goes wrong:** If the operator runs `km agent auth` immediately after `km resume`, SSM may not have registered the instance yet, and `sendSSMAndWait` fails.
**Why it happens:** SSM agent needs ~5-15 seconds after EC2 start to register.
**How to avoid:** Check `rec.Status == "running"` (same as other commands) and return early with a clear error: "sandbox is running but SSM agent may not be ready — wait 15 seconds and retry".

### Pitfall 3: `claude auth login` invoked without login-shell env

**What goes wrong:** If the inner command doesn't source `/etc/profile.d/`, `claude` may not be on PATH.
**Why it happens:** SSM sessions do not automatically source profile.d.
**How to avoid:** Prefix the inner command the same way `runAgent` does: `source /etc/profile.d/km-profile-env.sh 2>/dev/null; claude auth login [flags]`.

### Pitfall 4: codex background port-forward process orphaned on panic

**What goes wrong:** If `km agent auth --codex` panics after starting the port-forward goroutine, the `aws ssm start-session` process is orphaned and keeps the local port bound.
**Why it happens:** Go panics skip deferred cleanup of background processes.
**How to avoid:** Use `defer bg.Process.Kill()` immediately after `bg.Start()` succeeds. Also register a signal handler (SIGINT/SIGTERM) to kill the background process — same pattern as `km vscode start` which prints "Press Ctrl-C to close the tunnel".

### Pitfall 5: `runSSMInteractiveSubprocess` masks SIGINT globally

**What goes wrong:** `runSSMInteractiveSubprocess` calls `signal.Ignore(os.Interrupt, ...)` for the process duration. For the codex path where a background goroutine is also running, any Ctrl+C during the interactive session is masked. When the user eventually Ctrl+C's out, the background port-forward won't be killed by the signal — it must be killed explicitly in `defer`.
**Why it happens:** `signal.Ignore` is process-wide, not goroutine-scoped.
**How to avoid:** In Wave 2, kill the background process in a `defer` registered before calling `runSSMInteractiveSubprocess`.

### Pitfall 6: Missing-credentials check placement in `km shell` / `km agent run`

**What goes wrong:** If the credentials check is placed before `ResolveSandboxID` or before fetching the sandbox record, it requires an extra SSM round-trip (or fails because we don't yet have the instance ID).
**Why it happens:** We need the instance ID to run `sendSSMAndWait`.
**How to avoid:** Place the check AFTER the sandbox record is fetched and instance ID extracted. The check is: if `--no-bedrock` is set (or profile says noBedrock), run `sendSSMAndWait(ctx, ssmClient, instanceID, "stat /home/sandbox/.claude/.credentials.json 2>/dev/null && echo ok || echo missing")` and if output is "missing", print the hint and return non-zero.

---

## Code Examples

### Example 1: `km agent auth --claude` command skeleton

```go
// Source: derived from agent.go newAgentAttachCmd pattern (HIGH confidence — read from source)
func newAgentAuthCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
    var useClaude, useCodex bool
    var consoleFlag, ssoFlag, claudeaiFlag bool
    var emailFlag string

    cmd := &cobra.Command{
        Use:          "auth <sandbox-id | #number>",
        Short:        "Authenticate claude or codex CLI inside a sandbox via SSM",
        Args:         cobra.ExactArgs(1),
        SilenceUsage: true,
        RunE: func(c *cobra.Command, args []string) error {
            ctx := c.Context()
            if ctx == nil { ctx = context.Background() }

            // Default to --claude when neither flag set
            if !useClaude && !useCodex { useClaude = true }
            if useClaude && useCodex {
                return fmt.Errorf("--claude and --codex are mutually exclusive")
            }

            sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
            if err != nil { return err }

            f, e, s, err := resolveAuthDeps(ctx, cfg, fetcher, execFn, ssmClient)
            if err != nil { return err }

            if useClaude {
                return runAgentAuthClaude(ctx, cfg, f, e, s, sandboxID, consoleFlag, ssoFlag, claudeaiFlag, emailFlag)
            }
            return runAgentAuthCodex(ctx, cfg, f, e, s, sandboxID)
        },
    }
    cmd.Flags().BoolVar(&useClaude, "claude", false, "Authenticate the claude CLI (default)")
    cmd.Flags().BoolVar(&useCodex, "codex", false, "Authenticate the codex CLI via port-forward")
    cmd.Flags().BoolVar(&consoleFlag, "console", false, "Use Anthropic Console OAuth endpoint")
    cmd.Flags().BoolVar(&ssoFlag, "sso", false, "Use SSO OAuth endpoint")
    cmd.Flags().BoolVar(&claudeaiFlag, "claudeai", false, "Use claude.ai OAuth endpoint (default for --claude)")
    cmd.Flags().StringVar(&emailFlag, "email", "", "Email address for SSO login")
    return cmd
}
```

### Example 2: Claude auth interactive session

```go
// Source: derived from runAgent (agent.go:344-362) and execSSMSession (shell.go:327-403)
func runAgentAuthClaude(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, console, sso, claudeai bool, email string) error {
    rec, err := fetcher.FetchSandbox(ctx, sandboxID)
    if err != nil { return fmt.Errorf("fetch sandbox: %w", err) }
    instanceID, err := extractResourceID(rec.Resources, ":instance/")
    if err != nil { return fmt.Errorf("find EC2 instance: %w", err) }

    // Conflict check: refuse if agent run is in flight
    if err := checkAgentSessionConflict(ctx, ssmClient, instanceID); err != nil { return err }

    // Build `claude auth login` command with pass-through flags
    loginArgs := buildClaudeAuthArgs(console, sso, claudeai, email)
    innerCmd := fmt.Sprintf(
        "source /etc/profile.d/km-profile-env.sh 2>/dev/null; source /etc/profile.d/km-identity.sh 2>/dev/null; %s",
        loginArgs)
    paramsJSON, _ := json.Marshal(map[string][]string{"command": {innerCmd}})

    fmt.Printf("Opening SSM session to run `%s` on %s...\n", loginArgs, sandboxID)
    c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
        "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
        "--document-name", "KM-Sandbox-Session",
        "--parameters", string(paramsJSON))
    c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
    sessionErr := runSSMInteractiveSubprocess(execFn, c)

    // Post-exit: verify credentials file was written
    return verifyCredentialsWritten(ctx, ssmClient, instanceID, "claude", sessionErr)
}
```

### Example 3: File mtime verification via SSM

```go
// Source: novel pattern, no existing code — based on sendSSMAndWait (agent.go:1071)
func verifyCredentialsWritten(ctx context.Context, ssmClient SSMSendAPI, instanceID, cliType string, sessionErr error) error {
    var credPath, cliName string
    switch cliType {
    case "codex":
        credPath = "/home/sandbox/.codex/auth.json"
        cliName = "codex"
    default:
        credPath = "/home/sandbox/.claude/.credentials.json"
        cliName = "claude"
    }

    checkCmd := fmt.Sprintf("stat '%s' 2>/dev/null && echo ok || echo missing", credPath)
    out, err := sendSSMAndWait(ctx, ssmClient, instanceID, checkCmd)
    if err != nil {
        // SSM check failed — report session error if any, else the SSM error
        if sessionErr != nil { return fmt.Errorf("auth session error: %w", sessionErr) }
        return fmt.Errorf("could not verify credentials: %w", err)
    }

    if strings.TrimSpace(out) == "ok" {
        fmt.Printf("✓ %s credentials written to %s\n", cliName, credPath)
        fmt.Printf("  Run 'km shell --no-bedrock %s' or 'km agent run --no-bedrock <sandbox>'\n", "<sandbox>")
        return nil
    }

    if sessionErr != nil {
        return fmt.Errorf("auth session failed and credentials not found: %w", sessionErr)
    }
    return fmt.Errorf("session exited but %s credentials not found at %s — login may have been incomplete", cliName, credPath)
}
```

### Example 4: Missing-credentials hint in `km shell --no-bedrock`

```go
// Source: shell.go:336-363 — add AFTER noBedrock SendCommand, BEFORE execSSMSession
// This belongs in runShell() or execSSMSession(), after instanceID is known.
if noBedrock {
    checkOut, checkErr := sendSSMAndWait(ctx, ssmClient, instanceID,
        "stat /home/sandbox/.claude/.credentials.json 2>/dev/null && echo ok || echo missing")
    if checkErr == nil && strings.TrimSpace(checkOut) == "missing" {
        return fmt.Errorf(
            "claude credentials not found on sandbox %s\n"+
            "  Run: km agent auth %s --claude\n"+
            "  Then retry: km shell --no-bedrock %s",
            sandboxID, sandboxID, sandboxID)
    }
}
```

### Example 5: codex port-forward + interactive session (Wave 2 skeleton)

```go
// Source: derived from runVSCodeStart (vscode.go:125-193) + runPortForward (shell.go:484-556)
func runAgentAuthCodex(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string) error {
    rec, err := fetcher.FetchSandbox(ctx, sandboxID)
    if err != nil { return fmt.Errorf("fetch sandbox: %w", err) }
    instanceID, err := extractResourceID(rec.Resources, ":instance/")
    if err != nil { return fmt.Errorf("find EC2 instance: %w", err) }

    // Port probe (1455 → 1457 fallback)
    localPort, err := probeCodexPort()
    if err != nil { return err }

    // Start background port-forward
    pfCmd := buildPortForwardCmd(ctx, instanceID, rec.Region, strconv.Itoa(localPort), strconv.Itoa(localPort))
    pfCmd.Stdout, pfCmd.Stderr = os.Stdout, os.Stderr
    if err := pfCmd.Start(); err != nil {
        return fmt.Errorf("start SSM port-forward: %w", err)
    }
    defer pfCmd.Process.Kill()
    time.Sleep(1 * time.Second) // let session-manager-plugin bind the local port

    // Open foreground codex login session
    innerCmd := "source /etc/profile.d/km-profile-env.sh 2>/dev/null; codex login"
    paramsJSON, _ := json.Marshal(map[string][]string{"command": {innerCmd}})
    c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
        "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
        "--document-name", "KM-Sandbox-Session",
        "--parameters", string(paramsJSON))
    c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
    sessionErr := runSSMInteractiveSubprocess(execFn, c)

    return verifyCredentialsWritten(ctx, ssmClient, instanceID, "codex", sessionErr)
}

func probeCodexPort() (int, error) {
    for _, port := range []int{1455, 1457} {
        if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
            ln.Close()
            return port, nil
        }
    }
    return 0, fmt.Errorf("ports 1455 and 1457 are both in use locally — kill the local listener and retry")
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `AWS-StartInteractiveCommand` SSM document | `KM-Sandbox-Session` (Standard_Stream + runAsDefaultUser) | Phase 61 | Ctrl+C reaches remote PTY; sandbox user via SSM natively |
| Raw `aws ssm start-session` (no signal masking) | `runSSMInteractiveSubprocess` (masks SIGINT/SIGQUIT/SIGTSTP) | Phase 61 | Ctrl+C forwarded to remote shell, not km |
| Alias resolved via S3 scan | `ResolveSandboxID` (DynamoDB GSI first, S3 fallback, then number) | Phase 53/66 | Alias resolution much faster |

---

## Open Questions

1. **`km shell --no-bedrock` credentials check — SSM cost**
   - What we know: adding a `sendSSMAndWait` pre-check to `km shell --no-bedrock` adds ~1-3 seconds before the interactive session opens
   - What's unclear: is this acceptable UX or should the check be skipped and the hint added only to the session startup message
   - Recommendation: planner should include the pre-check since the design says "print hint and exit non-zero" (non-zero exit is not possible from inside the session); alternatively add the check to `execSSMSession` before the actual session opens

2. **`km agent run --no-bedrock` credentials check — timing**
   - What we know: `runAgentNonInteractive` already runs SSM SendCommand for prep commands; adding one more check is natural
   - What's unclear: the check must precede the tmux session launch (cmds[0..3]); if we gate on the check, failed auth gives a clear error before any work starts
   - Recommendation: add the check between "fetch sandbox record" and "send prep commands"

3. **Conflict detection via SSM — latency**
   - What we know: `sendSSMAndWait` for a tmux list-sessions takes ~3-5 seconds
   - What's unclear: is this acceptable before every auth invocation?
   - Recommendation: yes, this is a once-per-auth-attempt check, not a hot path

---

## Validation Architecture

`nyquist_validation` is enabled (absent from `.planning/config.json` explicit `false`).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (project standard — no external test framework) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./internal/app/cmd/... -run TestAgentAuth -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| ID | Behavior | Test Type | Automated Command | File Exists? |
|----|----------|-----------|-------------------|-------------|
| AUTH-01 | `km agent auth --claude` flags parsed correctly | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_FlagParsing` | No — Wave 0 |
| AUTH-02 | `km agent auth --claude` and `--codex` are mutually exclusive | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_MutuallyExclusive` | No — Wave 0 |
| AUTH-03 | Default (no flag) resolves to `--claude` | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_DefaultClaude` | No — Wave 0 |
| AUTH-04 | Sandbox ID resolution accepts alias and row number | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_SandboxIDResolution` | No — Wave 0 (uses `ResolveSandboxID` which is already tested in sandbox_ref_test.go) |
| AUTH-05 | Active agent run tmux session → refuses with error | unit | `go test ./internal/app/cmd/... -run TestAgentAuth_ConflictRefuse` | No — Wave 0 |
| AUTH-06 | `verifyCredentialsWritten` returns success when stat returns "ok" | unit | `go test ./internal/app/cmd/... -run TestVerifyCredentialsWritten_Success` | No — Wave 0 |
| AUTH-07 | `verifyCredentialsWritten` returns error when stat returns "missing" | unit | `go test ./internal/app/cmd/... -run TestVerifyCredentialsWritten_Missing` | No — Wave 0 |
| AUTH-08 | `probeCodexPort` returns 1455 when available | unit | `go test ./internal/app/cmd/... -run TestProbeCodexPort_Primary` | No — Wave 0 |
| AUTH-09 | `probeCodexPort` returns 1457 when 1455 in use | unit | `go test ./internal/app/cmd/... -run TestProbeCodexPort_Fallback` | No — Wave 0 |
| AUTH-10 | `probeCodexPort` returns error when both ports in use | unit | `go test ./internal/app/cmd/... -run TestProbeCodexPort_BothInUse` | No — Wave 0 |
| AUTH-11 | `km shell --no-bedrock` emits hint when credentials missing | unit | `go test ./internal/app/cmd/... -run TestShellCmd_NoBedrock_CredentialsMissingHint` | No — Wave 0 |
| AUTH-12 | `km agent run --no-bedrock` emits hint when credentials missing | unit | `go test ./internal/app/cmd/... -run TestAgentRun_NoBedrock_CredentialsMissingHint` | No — Wave 0 |
| AUTH-13 | `buildClaudeAuthArgs` builds correct flag set | unit | `go test ./internal/app/cmd/... -run TestBuildClaudeAuthArgs` | No — Wave 0 |
| INT-01 | SSM session lifecycle: start → hold → exit | integration/manual | manual UAT (see below) | manual-only |
| INT-02 | File mtime change confirmed after login | integration/manual | manual UAT (see below) | manual-only |

**Manual-only justification for INT-01/INT-02:** SSM interactive sessions require real AWS credentials, a running EC2 instance, and human operator browser interaction. No feasible mock can simulate the OAuth code exchange.

### Manual UAT Scenarios

**Scenario A — `--claude` happy path:**
1. `km create profiles/open-dev.yaml --alias auth-test`
2. `km agent auth auth-test --claude`
3. Click URL in browser, paste code into terminal
4. Expected: "✓ claude credentials written to ~/.claude/.credentials.json"
5. `km shell --no-bedrock auth-test` — expected: shell opens without "credentials not found" error
6. `km destroy auth-test --remote --yes`

**Scenario B — `--codex` happy path (Wave 2):**
1. `km create profiles/open-dev.yaml --alias auth-test`
2. `km agent auth auth-test --codex`
3. Click URL printed by codex, browser hits localhost:1455, auth completes
4. Expected: "✓ codex credentials written to ~/.codex/auth.json"
5. `km destroy auth-test --remote --yes`

**Scenario C — missing credentials hint:**
1. `km create profiles/open-dev.yaml --alias hint-test` (with `noBedrock: true` in profile or use `--no-bedrock` flag)
2. WITHOUT running `km agent auth`, run: `km shell --no-bedrock hint-test`
3. Expected: non-zero exit + message containing "km agent auth hint-test"
4. `km destroy hint-test --remote --yes`

**Scenario D — port 1455 in use locally (Wave 2):**
1. `nc -l 1455 &` (bind 1455 on laptop)
2. `nc -l 1457 &` (bind 1457 on laptop)
3. `km agent auth <sandbox> --codex`
4. Expected: error "ports 1455 and 1457 are both in use locally"
5. `kill %1 %2` cleanup

**Scenario E — sandbox not running:**
1. `km pause <sandbox>`
2. `km agent auth <sandbox> --claude`
3. Expected: clear error "sandbox is stopped" (same message as `km shell` with stopped sandbox)

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/... -run TestAgentAuth -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/agent_auth.go` — production code (Wave 1 creates this)
- [ ] `internal/app/cmd/agent_auth_test.go` — unit tests for AUTH-01 through AUTH-13
- [ ] No framework install needed — `go test` already works

---

## Sources

### Primary (HIGH confidence)
- Direct read of `internal/app/cmd/shell.go` (2026-05-10) — `runSSMInteractiveSubprocess`, `buildPortForwardCmd`, `execSSMSession`, `runPortForward`, `sendSSMAndWait`
- Direct read of `internal/app/cmd/agent.go` (2026-05-10) — `NewAgentCmdWithDeps`, `runAgent`, `newAgentRunCmd`, `newAgentAttachCmd`, `sendSSMAndWait`, `BuildAgentShellCommands`
- Direct read of `internal/app/cmd/vscode.go` (2026-05-10) — `runVSCodeStart`, `resolveVSCodeDeps`, `newVSCodeCmdInternal`, `runVSCodeRekey`, `parseVSCodeStatus`
- Direct read of `internal/app/cmd/sandbox_ref.go` (2026-05-10) — `ResolveSandboxID` signature and behavior
- Direct read of `internal/app/cmd/agent_test.go` (2026-05-10) — mock SSM pattern (`mockAgentSSM`), `fakeFetcher` structure
- Direct read of `internal/app/cmd/vscode_test.go` (2026-05-10) — `vsCodeSSMMock`, `sequencedSSMMock`, test structure for SSM-touching code
- Direct read of `.planning/phases/78-.../78-CONTEXT.md` (2026-05-10) — all locked decisions
- Direct read of `.planning/STATE.md` line 948 (2026-05-10) — Phase 78 roadmap entry confirming design

### Secondary (MEDIUM confidence)
- `.planning/REQUIREMENTS.md` — confirmed no existing requirement IDs map to Phase 78 operator-ergonomics work
- `.planning/config.json` — confirmed `nyquist_validation: true` (key absent = treat as enabled)

---

## Metadata

**Confidence breakdown:**
- SSM helper functions: HIGH — read directly from source at specified line numbers
- Subcommand registration pattern: HIGH — read from `NewAgentCmdWithDeps` source
- Credentials file paths: HIGH — confirmed in-session (CONTEXT.md specifics section)
- File mtime polling via SSM: HIGH — pattern derived from `sendSSMAndWait` + `stat -c %Y` is standard Linux
- Wave 2 codex port-forward architecture: HIGH — derived from `runVSCodeStart` and `runPortForward` which use exact same AWS primitives
- Test patterns: HIGH — read from `agent_test.go` and `vscode_test.go`
- Requirements mapping: HIGH — read REQUIREMENTS.md, confirmed no applicable IDs

**Research date:** 2026-05-10
**Valid until:** 2026-06-10 (stable codebase; only risk is breaking changes to `sendSSMAndWait` or `buildPortForwardCmd` signatures)

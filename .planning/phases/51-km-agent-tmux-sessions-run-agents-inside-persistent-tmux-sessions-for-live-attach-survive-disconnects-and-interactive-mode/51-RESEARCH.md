# Phase 51: km agent tmux sessions - Research

**Researched:** 2026-04-10
**Domain:** SSM/tmux integration, Go CLI (Cobra), shell scripting
**Confidence:** HIGH

## Summary

This phase wraps all non-interactive agent execution (`km agent run`) in persistent tmux sessions on the sandbox, enabling operators to attach live to running agents, survive SSM disconnects, and scroll back through output. The key changes are: (1) modify `BuildAgentShellCommands` to wrap Claude execution in `tmux new-session -d`, (2) add `km agent attach <sandbox>` subcommand using SSM start-session to attach to the tmux pane, and (3) add `--interactive` flag to `km agent run` that opens an SSM session attached directly to the tmux session.

The existing codebase has clear separation between interactive (`runAgent` via `execSSMSession`) and non-interactive (`runAgentNonInteractive` via `SendCommand`) paths. The tmux wrapping applies primarily to the non-interactive path's script generation in `BuildAgentShellCommands`. The interactive attach path reuses the `execSSMSession` pattern from `shell.go`.

**Primary recommendation:** Modify `BuildAgentShellCommands` to wrap the Claude command inside `tmux new-session -d -s "km-agent-<RUN_ID>"`, add a new `attach` subcommand, and add `--interactive` flag -- all within `agent.go`.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| tmux | 3.x (AL2023 repo) | Persistent terminal sessions | Standard Unix multiplexer, already in agent profiles |
| AWS SSM SendCommand | v2 SDK | Fire-and-forget command execution | Existing pattern in `runAgentNonInteractive` |
| AWS SSM start-session | CLI | Interactive session for attach | Existing pattern in `execSSMSession` |
| cobra | existing | CLI subcommand structure | Already used throughout CLI |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| base64 (stdlib) | go1.x | Prompt encoding | Already used for shell injection prevention |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| tmux | screen | tmux is more modern, better scripting API, already installed in profiles |
| SSM start-session for attach | SSM SendCommand + polling | start-session provides real interactive TTY, required for live viewing |

## Architecture Patterns

### Recommended Changes to Existing Files

```
internal/app/cmd/
├── agent.go           # Modified: BuildAgentShellCommands wraps in tmux,
│                      #   new attach subcommand, --interactive flag on run
└── agent_test.go      # Modified: test tmux command generation, attach args
```

### Pattern 1: tmux-wrapped non-interactive execution

**What:** Modify `BuildAgentShellCommands` to create a tmux session instead of running the script directly.
**When to use:** All `km agent run` invocations (fire-and-forget and --wait).

The current flow:
1. Write script to `/tmp/km-agent-run.sh`
2. `sudo -u sandbox -i /tmp/km-agent-run.sh`

The new flow:
1. Write script to `/tmp/km-agent-run.sh` (unchanged)
2. `sudo -u sandbox -i tmux new-session -d -s "km-agent-<RUN_ID>" '/tmp/km-agent-run.sh; exec bash'`
3. The script still writes output.json, status, uploads to S3 (unchanged)

**Key detail:** The `exec bash` at the end keeps the tmux session alive after the script completes so operators can attach to see results and scrollback. Without it, the session dies when the script exits.

```go
// In BuildAgentShellCommands, change the execution line from:
//   "sudo -u sandbox -i /tmp/km-agent-run.sh",
// to:
//   fmt.Sprintf("sudo -u sandbox -i tmux new-session -d -s 'km-agent-%s' '/tmp/km-agent-run.sh; exec bash'", runID),
```

**Important:** The RUN_ID is generated inside the script (via `date -u`), but tmux session name must be known at session creation time. Solution: generate the RUN_ID in the SSM command layer (outside the script) and pass it into the script as an environment variable, or generate it before writing the script.

### Pattern 2: SSM start-session for attach

**What:** `km agent attach <sandbox>` uses the same `execSSMSession` pattern from shell.go but connects to a tmux session.
**When to use:** Operator wants to watch a running or completed agent.

```go
// Build SSM start-session command that runs tmux attach inside the sandbox
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", region, "--profile", "klanker-terraform",
    "--document-name", "AWS-StartInteractiveCommand",
    "--parameters", `{"command":["sudo -u sandbox -i tmux attach-session -t km-agent"]}`)
```

Use `tmux attach-session -t km-agent` with a prefix match. Or list sessions and pick the most recent. The `-t` flag does exact match by default. Use `tmux list-sessions -F '#{session_name}'` to find the latest `km-agent-*` session.

### Pattern 3: --interactive flag on km agent run

**What:** Instead of fire-and-forget, `--interactive` creates the tmux session detached then immediately attaches via SSM start-session.
**When to use:** Operator wants to watch the agent from the start.

Flow:
1. SendCommand creates tmux session (detached)
2. Brief sleep (1-2s) for session to initialize
3. SSM start-session attaches to the tmux session (interactive, blocks)

Alternative (simpler): Use `tmux new-session -A -s <name>` which creates-and-attaches in one step, run directly via SSM start-session. This is cleaner but means the command must be sent interactively, not via SendCommand.

**Recommended approach:** For `--interactive`, skip SendCommand entirely. Use SSM start-session with `tmux new-session -s "km-agent-<RUN_ID>" '/tmp/km-agent-run.sh; exec bash'` (without `-d` so it attaches immediately). This means the script must be written first via a quick SendCommand, then the interactive session starts.

### Anti-Patterns to Avoid
- **Generating RUN_ID inside the script when tmux needs it outside:** The tmux session name must be deterministic and known before the session starts. Generate it in Go code and pass it in.
- **Using tmux -A for fire-and-forget:** `-A` means "attach or create" -- only useful for interactive mode. For fire-and-forget, always use `-d` (detached).
- **Forgetting to keep session alive:** If the script exits and tmux session closes, operators lose scrollback. Use `exec bash` or `read` to keep it open.
- **Not handling tmux not installed:** Some profiles may not have tmux. Fall back to current behavior (direct execution without tmux wrapper) with a warning.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Session persistence | Custom daemon/nohup | tmux | Battle-tested, scrollback, multiple windows |
| Interactive terminal | Raw PTY management | SSM start-session + tmux attach | AWS handles PTY, tmux handles session |
| Session listing | Custom state tracking | `tmux list-sessions` via SendCommand | tmux tracks its own sessions |

## Common Pitfalls

### Pitfall 1: RUN_ID timing mismatch
**What goes wrong:** RUN_ID is generated inside the script via `date -u`, but tmux session name needs it before session creation.
**Why it happens:** Current design generates RUN_ID at script runtime, not at dispatch time.
**How to avoid:** Generate RUN_ID in Go code (`time.Now().UTC().Format("20060102T150405Z")`) and pass into both the tmux session name and the script.
**Warning signs:** tmux session name doesn't match the run directory name.

### Pitfall 2: tmux not available
**What goes wrong:** `km agent run` fails because tmux is not installed on the sandbox.
**Why it happens:** Only agent profiles (ao.yaml, goose.yaml) install tmux via initCommands. Custom profiles may not.
**How to avoid:** Before wrapping in tmux, check if tmux exists. Or make the tmux wrap conditional -- if tmux is present, use it; otherwise fall back to direct execution. Alternatively, install tmux as part of the agent run script if missing.
**Warning signs:** SSM command fails with "tmux: command not found".

### Pitfall 3: tmux session naming collisions
**What goes wrong:** Two concurrent `km agent run` calls create sessions with the same name.
**Why it happens:** If RUN_ID has second-level precision and two runs start in the same second.
**How to avoid:** Use RUN_ID with sub-second precision or append a short random suffix. The timestamp format `20060102T150405Z` has second granularity which is usually sufficient, but adding a 4-char random suffix eliminates the risk.

### Pitfall 4: SSM start-session document compatibility
**What goes wrong:** `AWS-StartInteractiveCommand` may not be available in all regions or SSM configurations.
**Why it happens:** This is a custom/built-in document. The existing codebase already uses it in `execSSMSession` and `runAgent`.
**How to avoid:** Reuse the exact same SSM document and parameter format already proven in shell.go.

### Pitfall 5: --wait mode interaction with tmux
**What goes wrong:** In `--wait` mode, the SSM command (SendCommand) returns immediately because `tmux new-session -d` is a quick command. The poll loop sees "Success" before Claude finishes.
**Why it happens:** tmux detaches and the SendCommand script exits right after creating the session.
**How to avoid:** After creating the tmux session, the SendCommand script must wait for the agent to complete. Options: (a) `tmux wait-for` channel, (b) poll the status file, (c) keep the SSM command running by tailing the tmux pane. **Recommended:** Use `tmux wait-for -S done-<RUN_ID>` in the script after Claude exits, and have the calling script `tmux wait-for done-<RUN_ID>` to block. Or simpler: run the script directly (not in tmux -d) from SSM's perspective, but also create a tmux session -- i.e., the SSM command runs `tmux new-session -s <name> '<script>'` WITHOUT `-d`, so SSM waits for the tmux session to exit, while operators can attach from another terminal.

**Best approach for --wait:** The SSM SendCommand script should:
1. Create the tmux session detached: `tmux new-session -d -s <name> '<script>; tmux wait-for -S km-done-<RUN_ID>'`
2. Then block: `tmux wait-for km-done-<RUN_ID>`
3. After wait completes, echo KM_RUN_ID as today

This way SSM polls see the command as still running while Claude runs in tmux, and operators can `km agent attach` to watch.

### Pitfall 6: sudo and tmux session ownership
**What goes wrong:** tmux session created by root via `sudo -u sandbox` may have socket permission issues.
**Why it happens:** tmux server socket is per-user; creating session as one user and attaching as another fails.
**How to avoid:** All tmux commands (new-session, attach) must run as the `sandbox` user via `sudo -u sandbox -i`. The attach command must also run as sandbox user.

## Code Examples

### Modified BuildAgentShellCommands (core change)

```go
func BuildAgentShellCommands(prompt string, artifactsBucket string, noBedrock ...bool) []string {
    // Generate RUN_ID in Go for deterministic tmux session naming
    runID := time.Now().UTC().Format("20060102T150405Z")
    tmuxSession := fmt.Sprintf("km-agent-%s", runID)
    b64Prompt := base64.StdEncoding.EncodeToString([]byte(prompt))

    // ... existing script generation with runID injected as variable ...

    script := fmt.Sprintf(`#!/bin/bash
export HOME=/home/sandbox
# ... existing env setup ...
RUN_ID="%s"
RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
mkdir -p "$RUN_DIR"
# ... existing claude invocation ...
`, runID)

    return []string{
        fmt.Sprintf("cat > /tmp/km-agent-run.sh << 'KMEOF'\n%s\nKMEOF", script),
        "chmod +x /tmp/km-agent-run.sh",
        // Create tmux session (detached) running the script
        fmt.Sprintf(
            `sudo -u sandbox -i tmux new-session -d -s '%s' '/tmp/km-agent-run.sh; tmux wait-for -S km-done-%s; exec bash'`,
            tmuxSession, runID,
        ),
        // Block until agent completes (for --wait mode compatibility)
        fmt.Sprintf(`sudo -u sandbox -i tmux wait-for km-done-%s`, runID),
        fmt.Sprintf(`echo "KM_RUN_ID=%s"`, runID),
        "rm -f /tmp/km-agent-run.sh",
    }
}
```

### New attach subcommand

```go
func newAgentAttachCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc) *cobra.Command {
    cmd := &cobra.Command{
        Use:   "attach <sandbox-id | #number>",
        Short: "Attach to a running or completed agent tmux session",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            // Resolve sandbox, get instanceID (same pattern as runAgent)
            // SSM start-session with:
            //   sudo -u sandbox -i tmux attach-session -t <latest-km-agent-session>
            // Use tmux list-sessions to find latest if not specified
        },
    }
    return cmd
}
```

### Interactive mode via SSM start-session

```go
// For --interactive: write script via SendCommand, then attach via start-session
// Step 1: SendCommand to write script + create tmux session
// Step 2: SSM start-session to attach
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", region, "--profile", "klanker-terraform",
    "--document-name", "AWS-StartInteractiveCommand",
    "--parameters", fmt.Sprintf(
        `{"command":["sudo -u sandbox -i tmux new-session -A -s '%s' '%s; exec bash'"]}`,
        tmuxSession, "/tmp/km-agent-run.sh",
    ))
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Direct SSM SendCommand | tmux-wrapped SendCommand | Phase 51 | Agent survives disconnects, live attach |
| No live viewing | `km agent attach` | Phase 51 | Operators can watch agents in real-time |
| Fire-and-forget only | `--interactive` flag | Phase 51 | Direct interactive mode with persistence |

## Open Questions

1. **Should `BuildAgentShellCommands` return the RUN_ID?**
   - What we know: Currently returns `[]string` commands. RUN_ID would need to be accessible for `--interactive` mode to know the tmux session name.
   - What's unclear: Best API change -- return struct, or separate function for RUN_ID generation.
   - Recommendation: Change signature to return `([]string, string)` where second value is the generated RUN_ID. Or create a struct `AgentRunConfig` that carries both.

2. **Should tmux wrapping be conditional on tmux availability?**
   - What we know: Agent profiles install tmux, but custom profiles might not.
   - What's unclear: Whether to require tmux for `km agent run` or gracefully degrade.
   - Recommendation: Check for tmux at the start of the script. If missing, install it (`yum install -y tmux`) or fall back to direct execution with a warning.

3. **Session cleanup policy**
   - What we know: tmux sessions persist until explicitly killed or the sandbox is destroyed.
   - What's unclear: Should old sessions be auto-cleaned? How many to keep?
   - Recommendation: Keep all sessions (they're lightweight). The sandbox is ephemeral anyway. `km agent list` already shows runs. Let operators `tmux kill-session` manually if needed.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing + testify (existing) |
| Config file | `internal/app/cmd/agent_test.go` (existing) |
| Quick run command | `go test ./internal/app/cmd/ -run TestAgent -v -count=1` |
| Full suite command | `go test ./internal/app/cmd/ -v -count=1` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| TMUX-01 | BuildAgentShellCommands generates tmux session | unit | `go test ./internal/app/cmd/ -run TestBuildAgentShellCommands -v` | Needs update |
| TMUX-02 | RUN_ID is deterministic and used in tmux name | unit | `go test ./internal/app/cmd/ -run TestRunID -v` | Wave 0 |
| TMUX-03 | attach subcommand builds correct SSM args | unit | `go test ./internal/app/cmd/ -run TestAgentAttach -v` | Wave 0 |
| TMUX-04 | --interactive flag triggers start-session path | unit | `go test ./internal/app/cmd/ -run TestAgentInteractive -v` | Wave 0 |
| TMUX-05 | --wait mode blocks until tmux session completes | unit | `go test ./internal/app/cmd/ -run TestAgentWait -v` | Needs update |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run TestAgent -v -count=1`
- **Per wave merge:** `go test ./internal/app/cmd/ -v -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/agent_test.go` -- update TestBuildAgentShellCommands to verify tmux wrapping
- [ ] `internal/app/cmd/agent_test.go` -- add TestAgentAttach for attach subcommand
- [ ] `internal/app/cmd/agent_test.go` -- add TestAgentInteractive for --interactive flag

## Sources

### Primary (HIGH confidence)
- `internal/app/cmd/agent.go` -- current agent implementation, BuildAgentShellCommands, runAgentNonInteractive, sendSSMAndWait
- `internal/app/cmd/shell.go` -- execSSMSession pattern, SSM start-session with AWS-StartInteractiveCommand document
- `internal/app/cmd/agent_test.go` -- existing test patterns with mock SSM/EventBridge
- `profiles/ao.yaml`, `profiles/goose.yaml` -- confirm tmux is in initCommands for agent profiles
- tmux man page -- session management flags (-d, -A, -s, wait-for)

### Secondary (MEDIUM confidence)
- AWS SSM documentation -- SendCommand vs start-session behavior, AWS-StartInteractiveCommand document support

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH -- all components (tmux, SSM, cobra) already in use in codebase
- Architecture: HIGH -- straightforward modification of existing BuildAgentShellCommands + new subcommand following established patterns
- Pitfalls: HIGH -- identified through code reading (RUN_ID timing, --wait interaction, sudo ownership)

**Research date:** 2026-04-10
**Valid until:** 2026-05-10 (stable domain, no external API changes expected)

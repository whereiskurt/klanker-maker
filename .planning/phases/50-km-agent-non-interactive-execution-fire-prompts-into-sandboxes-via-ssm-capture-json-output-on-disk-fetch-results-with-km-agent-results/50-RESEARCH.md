# Phase 50: km agent non-interactive execution - Research

**Researched:** 2026-04-10
**Domain:** CLI command extension, SSM send-command, Claude Code headless mode
**Confidence:** HIGH

## Summary

This phase extends the existing `km agent` command (currently interactive-only via SSM start-session) with a non-interactive `--prompt` flag that fires Claude Code prompts into sandboxes using SSM SendCommand. The pattern is well-established in this codebase: `rsync.go`, `roll.go`, and `shell.go` all use `ssm.SendCommand` with `AWS-RunShellScript` and poll `GetCommandInvocation` for completion. The idle-reset background loop uses EventBridge extend events via `PublishSandboxCommand`, which is already production-proven.

Claude Code's headless mode (`claude -p "..." --output-format json --dangerously-skip-permissions`) produces structured JSON output with `result`, `session_id`, and metadata fields. Output is written to stdout, which SSM SendCommand captures automatically in `StandardOutputContent`. The sandbox-side wrapper script redirects this to `/workspace/.km-agent/runs/<timestamp>/output.json`.

**Primary recommendation:** Extend `NewAgentCmd` in `shell.go` with `--prompt` flag that dispatches to a new `runAgentNonInteractive` function using `ssm.SendCommand`. Add `km agent results` and `km agent list` as subcommands. Use a goroutine for idle-reset heartbeat during long-running agent tasks.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/ssm` | v2 | SSM SendCommand + GetCommandInvocation | Already used in shell.go, rsync.go, roll.go |
| `github.com/aws/aws-sdk-go-v2/service/eventbridge` | v2 | PublishSandboxCommand for idle-reset | Already used in idle_event.go |
| `github.com/spf13/cobra` | latest | CLI command structure | Standard across all km commands |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `encoding/json` | stdlib | Parse Claude JSON output | Decoding results from SSM output |
| `time` | stdlib | Timestamps for run IDs, idle-reset ticker | Run directory naming, heartbeat loop |

## Architecture Patterns

### Recommended Project Structure

No new packages needed. All changes are within existing structure:

```
internal/app/cmd/
  shell.go          # Extend NewAgentCmd, add runAgentNonInteractive
  agent.go          # NEW: agent subcommands (results, list) — or keep in shell.go
pkg/aws/
  idle_event.go     # Already has PublishSandboxCommand (reuse as-is)
```

### Pattern 1: SSM SendCommand with Polling (from rsync.go)

**What:** Fire a shell command via SSM SendCommand, poll GetCommandInvocation until complete.
**When to use:** Non-interactive remote command execution on EC2 sandboxes.
**Example:**
```go
// Source: internal/app/cmd/rsync.go lines 217-230 and 583-616
cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
    InstanceIds:  []string{instanceID},
    DocumentName: awssdk.String("AWS-RunShellScript"),
    Parameters: map[string][]string{
        "commands": {shellCmd},
    },
})
if err != nil {
    return fmt.Errorf("send command: %w", err)
}
commandID := awssdk.ToString(cmdOut.Command.CommandId)
// Poll with pollSSMCommand or custom polling loop
```

### Pattern 2: Idle-Reset Heartbeat via EventBridge

**What:** Publish "extend" events to keep sandbox alive during long agent runs.
**When to use:** Agent prompts that run for minutes/hours would otherwise trigger idle timeout.
**Example:**
```go
// Source: pkg/aws/idle_event.go
// Launch goroutine that publishes extend events every N minutes
go func() {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-done:
            return
        case <-ticker.C:
            _ = awspkg.PublishSandboxCommand(ctx, ebClient, sandboxID, "extend", "duration", "30m")
        }
    }
}()
```

### Pattern 3: Cobra Subcommand Registration (from root.go)

**What:** The existing `km agent` is a simple command, not a parent command with subcommands. It needs restructuring.
**When to use:** Adding `km agent results` and `km agent list` requires making `km agent` a parent command.
**Critical decision:** The current `km agent <sandbox-id> --claude` must continue working. Two approaches:

1. **Make `km agent` a parent command** with `run` (or implicit default), `results`, and `list` subcommands. The current interactive usage becomes `km agent run <sandbox-id> --claude` or stays as default behavior when no subcommand matches.

2. **Keep `km agent` as-is and add `km agent-results` / `km agent-list` as separate top-level commands.** Simpler but messier UX.

**Recommendation:** Approach 1 is cleaner. Cobra supports `Args: cobra.ArbitraryArgs` on parent commands to handle the "no subcommand" case as the default (interactive) behavior.

### Pattern 4: Claude Headless Command Construction

**What:** Build the shell command that runs inside the sandbox.
**When to use:** Constructing the SSM command payload.
**Example:**
```bash
# The command that runs inside the sandbox via SSM SendCommand:
sudo -u sandbox -i bash -c '
  source /etc/profile.d/km-profile-env.sh 2>/dev/null
  source /etc/profile.d/km-identity.sh 2>/dev/null
  cd /workspace
  RUN_ID=$(date +%Y%m%dT%H%M%SZ)
  mkdir -p /workspace/.km-agent/runs/$RUN_ID
  claude -p "THE_PROMPT" \
    --output-format json \
    --dangerously-skip-permissions \
    --bare \
    > /workspace/.km-agent/runs/$RUN_ID/output.json 2>&1
  echo "KM_AGENT_RUN_ID=$RUN_ID"
  echo "KM_AGENT_STATUS=$?"
'
```

### Anti-Patterns to Avoid
- **Using start-session for non-interactive work:** start-session requires a PTY and is designed for interactive terminals. SendCommand is the correct API for fire-and-forget.
- **Waiting synchronously for long agent runs:** Claude agent tasks can run for hours. The CLI should return the run ID immediately, not block. Use fire-and-forget with `km agent results` to fetch output later.
- **Forgetting to escape the prompt:** User-provided prompts may contain single quotes, double quotes, dollar signs, backticks. Must escape properly in the shell command string passed to SSM.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| SSM command execution | Custom HTTP calls | `ssm.SendCommand` + `GetCommandInvocation` | AWS SDK handles auth, retries, regional routing |
| Idle-reset | Custom DynamoDB writes | `PublishSandboxCommand(ctx, eb, id, "extend", ...)` | Existing Lambda handles all the TTL extension logic |
| Result fetching from sandbox | S3 upload/download pipeline | SSM SendCommand to `cat` the output file | Results are small JSON; no need for S3 artifact pipeline |
| Prompt escaping | Custom escaper | `fmt.Sprintf` with `%q` or base64-encode the prompt | Shell injection is a real risk with user prompts |

**Key insight:** The entire SSM SendCommand + polling pattern already exists in `rsync.go` (`pollSSMCommand` function, lines 583-616). Reuse that pattern directly.

## Common Pitfalls

### Pitfall 1: Prompt Injection via Shell Escaping
**What goes wrong:** User prompt contains `'; rm -rf /; echo '` and it gets interpreted by bash.
**Why it happens:** Prompt is interpolated into a shell command string.
**How to avoid:** Base64-encode the prompt on the client side, decode it inside the sandbox. This avoids all shell escaping issues.
```go
encoded := base64.StdEncoding.EncodeToString([]byte(prompt))
// In shell command: echo "$ENCODED" | base64 -d | claude -p - ...
```
**Warning signs:** Any use of `fmt.Sprintf` to embed user text into shell commands.

### Pitfall 2: SSM SendCommand Output Truncation
**What goes wrong:** SSM `GetCommandInvocation.StandardOutputContent` is truncated at 24,000 characters. Claude JSON output can exceed this.
**Why it happens:** AWS SSM has a hard limit on output size returned via GetCommandInvocation.
**How to avoid:** Write output to a file on the sandbox disk first (`> /workspace/.km-agent/runs/$RUN_ID/output.json`), then use a separate SSM command to retrieve it (or `cat` it). For large outputs, consider S3 upload.
**Warning signs:** Truncated JSON in results.

### Pitfall 3: Idle Timeout Kills Running Agent
**What goes wrong:** Agent runs for 30+ minutes, sandbox idle timeout fires and destroys/stops the sandbox.
**Why it happens:** SSM SendCommand activity doesn't count as "user activity" for the idle timer.
**How to avoid:** Background goroutine publishes EventBridge extend events every 5 minutes while the agent is running. Stop the heartbeat when the agent completes.
**Warning signs:** Sandbox destroyed while agent is mid-run.

### Pitfall 4: SSM SendCommand Timeout
**What goes wrong:** SSM RunShellScript has a default execution timeout of 3600 seconds (1 hour). Long agent tasks may exceed this.
**Why it happens:** AWS default timeout on the SSM document.
**How to avoid:** Set `executionTimeout` parameter in SendCommand to a longer value (e.g., 28800 = 8 hours). Or use the `TimeoutSeconds` field.
```go
Parameters: map[string][]string{
    "commands":         {shellCmd},
    "executionTimeout": {"28800"},  // 8 hours
},
```
**Warning signs:** Command shows "TimedOut" status.

### Pitfall 5: Race Between Fire-and-Forget and Results Check
**What goes wrong:** User runs `km agent results` before the agent has written its output file.
**Why it happens:** Agent is still running.
**How to avoid:** The `km agent list` command should show run status (running/complete/failed). Check for a `.km-agent/runs/<id>/status` sentinel file. The wrapper script writes "running" at start, "complete" or "failed" at end.

### Pitfall 6: Making km agent a Parent Command Breaks Existing Usage
**What goes wrong:** `km agent <sandbox-id> --claude` stops working because Cobra interprets `<sandbox-id>` as a subcommand name.
**Why it happens:** Cobra prioritizes subcommand matching over positional args.
**How to avoid:** Use Cobra's `TraverseChildren` and custom `RunE` on the parent, or structure as:
- `km agent <sandbox-id> --claude` (implicit "run" subcommand via parent RunE)
- `km agent results <sandbox-id>`
- `km agent list <sandbox-id>`
Check if the first arg matches a known subcommand; if not, treat as sandbox ID for backward compat.

## Code Examples

### Claude Headless Invocation (Sandbox-Side Shell Script)
```bash
# Source: Claude Code docs (https://code.claude.com/docs/en/headless)
# Run non-interactively with JSON output
RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
mkdir -p "$RUN_DIR"
echo "running" > "$RUN_DIR/status"

claude -p "$PROMPT" \
  --output-format json \
  --dangerously-skip-permissions \
  --bare \
  > "$RUN_DIR/output.json" 2>"$RUN_DIR/stderr.log"

EXIT_CODE=$?
if [ $EXIT_CODE -eq 0 ]; then
  echo "complete" > "$RUN_DIR/status"
else
  echo "failed" > "$RUN_DIR/status"
  echo "$EXIT_CODE" > "$RUN_DIR/exit_code"
fi
```

### SSM SendCommand for Agent Run (Go)
```go
// Construct the sandbox-side command
encoded := base64.StdEncoding.EncodeToString([]byte(prompt))
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
  if [ $EC -eq 0 ]; then echo "complete" > "$RUN_DIR/status"; else echo "failed" > "$RUN_DIR/status"; fi
  echo "KM_RUN_ID=$RUN_ID"
'`, encoded)
```

### Fetching Results via SSM (Go)
```go
// Retrieve output.json from a specific run
catCmd := fmt.Sprintf(`sudo -u sandbox cat /workspace/.km-agent/runs/%s/output.json`, runID)
cmdOut, err := ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
    InstanceIds:  []string{instanceID},
    DocumentName: awssdk.String("AWS-RunShellScript"),
    Parameters: map[string][]string{
        "commands": {catCmd},
    },
})
// Poll for completion, extract StandardOutputContent
```

### Listing Runs via SSM (Go)
```go
// List all runs with their status
listCmd := `sudo -u sandbox bash -c '
  for d in /workspace/.km-agent/runs/*/; do
    id=$(basename "$d")
    status=$(cat "$d/status" 2>/dev/null || echo "unknown")
    size=$(stat -c%s "$d/output.json" 2>/dev/null || echo "0")
    echo "$id|$status|$size"
  done
'`
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `claude --json` (deprecated flag name) | `claude -p --output-format json` | 2025 | Use `--output-format json`, not `--json` |
| No `--bare` mode | `--bare` skips CLAUDE.md, hooks, plugins | 2025 | Faster startup for headless; recommended for scripted use |
| Permission prompts block headless | `--dangerously-skip-permissions` or `--permission-mode bypassPermissions` | 2025 | Enables fully unattended execution |

**Deprecated/outdated:**
- `--json` flag: Use `--output-format json` instead
- Direct ANTHROPIC_API_KEY in env may be needed for `--bare` mode if sandbox doesn't have OAuth token configured

## Open Questions

1. **Claude authentication inside sandbox**
   - What we know: Sandboxes have network access to api.anthropic.com (if allowed by profile). Claude Code needs either ANTHROPIC_API_KEY env var or OAuth login.
   - What's unclear: How is Claude Code authenticated inside sandboxes currently? The interactive `km agent --claude` path uses `start-session` which inherits the user's terminal, but headless mode via SendCommand runs without user context.
   - Recommendation: Assume ANTHROPIC_API_KEY is set in sandbox env (via SSM Parameter Store or profile env vars). The wrapper script sources `/etc/profile.d/km-profile-env.sh` which likely sets this. Validate during implementation.

2. **Fire-and-forget vs. wait-for-completion**
   - What we know: Design says fire-and-forget. But users may want to wait.
   - What's unclear: Should `--prompt` block until complete by default, or return immediately with a run ID?
   - Recommendation: Default to fire-and-forget (return run ID immediately), with `--wait` flag to optionally block and poll for completion. This matches the async SSM pattern.

3. **Output size limits**
   - What we know: SSM GetCommandInvocation truncates stdout at 24KB. Claude JSON output can be large.
   - What's unclear: Typical output size for agent runs.
   - Recommendation: Always write to disk first, fetch via separate SSM command. For results > 24KB, consider S3 upload as a future enhancement.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | None (Go convention) |
| Quick run command | `go test ./internal/app/cmd/ -run TestAgent -count=1 -v` |
| Full suite command | `go test ./internal/app/cmd/ -count=1 -v` |

### Phase Requirements -> Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| AGENT-01 | --prompt flag dispatches to SendCommand (not start-session) | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_SendCommand -v` | Wave 0 |
| AGENT-02 | Shell command includes claude -p with correct flags | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_CommandConstruction -v` | Wave 0 |
| AGENT-03 | Prompt is base64-encoded to prevent injection | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_PromptEscaping -v` | Wave 0 |
| AGENT-04 | km agent results fetches output via SSM | unit | `go test ./internal/app/cmd/ -run TestAgentResults -v` | Wave 0 |
| AGENT-05 | km agent list enumerates runs via SSM | unit | `go test ./internal/app/cmd/ -run TestAgentList -v` | Wave 0 |
| AGENT-06 | Idle-reset heartbeat publishes extend events | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_IdleReset -v` | Wave 0 |
| AGENT-07 | Existing km agent --claude interactive path unchanged | unit | `go test ./internal/app/cmd/ -run TestShellCmd -v` | Exists (shell_test.go) |
| AGENT-08 | Stopped sandbox returns error for --prompt | unit | `go test ./internal/app/cmd/ -run TestAgentNonInteractive_StoppedSandbox -v` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run TestAgent -count=1 -v`
- **Per wave merge:** `go test ./internal/app/cmd/ -count=1 -v`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/agent_test.go` -- covers AGENT-01 through AGENT-08
- [ ] Mock SSM client for SendCommand/GetCommandInvocation (pattern exists in roll_test.go)
- [ ] Mock EventBridge client for idle-reset heartbeat testing

## Sources

### Primary (HIGH confidence)
- Codebase: `internal/app/cmd/shell.go` -- existing NewAgentCmd and runAgent implementation
- Codebase: `internal/app/cmd/rsync.go` -- SendCommand + pollSSMCommand pattern (lines 217-230, 583-616)
- Codebase: `internal/app/cmd/roll.go` -- SendCommand fire-and-forget pattern (lines 604-605)
- Codebase: `pkg/aws/idle_event.go` -- PublishSandboxCommand for extend events
- Official docs: https://code.claude.com/docs/en/cli-usage -- Claude CLI flags reference
- Official docs: https://code.claude.com/docs/en/headless -- Headless/SDK mode documentation

### Secondary (MEDIUM confidence)
- AWS SSM docs -- SendCommand output truncation limit (24KB on StandardOutputContent)
- AWS SSM docs -- executionTimeout parameter for AWS-RunShellScript

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH -- all libraries already used in codebase
- Architecture: HIGH -- follows existing patterns from rsync.go and shell.go
- Pitfalls: HIGH -- shell escaping and SSM truncation are well-documented issues
- Claude headless flags: HIGH -- verified from official docs at code.claude.com

**Research date:** 2026-04-10
**Valid until:** 2026-05-10 (stable domain, established patterns)

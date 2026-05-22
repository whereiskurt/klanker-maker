# Phase 86: km-create-prompt-queue — Research

**Researched:** 2026-05-19
**Domain:** Go CLI extension, SSM push primitives, on-box bash systemd service
**Confidence:** HIGH

## Summary

Phase 86 adds a repeatable `--prompt` flag to `km create` that queues prompt text on the EC2 sandbox and drains it via an on-box bash runner seeded during boot. The design is pure composition — no schema changes, no Lambda changes, no Terragrunt changes. The implementation touches exactly five files: `create.go` (flag + SSM push + optional `--wait` poll), `agent.go` (`--queue` view extension), `userdata.go` (runner + systemd unit seeding), and their test files.

No open architecture questions remain from the spec. The codebase was read directly. All patterns (`sendSSMAndWait`, `BuildAgentShellCommands`, `km-mail-poller` systemd unit, `configFiles` template section) are fully understood and confirmed in the source.

**Primary recommendation:** Seed `km-queue-runner` bash script + `km-queue.service` systemd unit via the existing `configFiles` template section in `userdata.go`, push queue files via `sendSSMAndWait`, kick the runner with a one-shot SSM `systemctl start km-queue`, and poll via `sendSSMAndWait` for `--wait`.

---

## User Constraints (from CONTEXT.md)

No CONTEXT.md exists for phase 86 at research time. The spec (`docs/superpowers/specs/2026-05-19-km-create-prompt-queue-design.md`) is the authoritative decision document. Locked decisions extracted from it:

### Locked Decisions
- Bash runner (not Go binary). Seeded via `configFiles` (not AMI bake).
- Queue layout: `/workspace/.km-agent/queue/{NNN}.prompt` + `{NNN}.meta.json`.
- Runner wraps in `tmux new-session -d -s km-queue` so operator can attach.
- Systemd unit `km-queue.service` with `Restart=always` so runner survives reboot/resume.
- `--prompt` with `@file` convention (leading `@` means read file; `@@` escapes literal `@`).
- Auth probe: Bedrock mode probes `aws bedrock-runtime invoke-model`; direct-API mode probes `~/.claude/.credentials.json` (note: spec says `.credentials.json`, codebase uses `/home/sandbox/.claude/.credentials.json`).
- Failure stops the chain: first failed prompt marks all remaining as `skipped`.
- No `--prompt` on `km resume` in v1.
- `--prompt + --docker` is a hard-fail (docker substrate does not support SSM push).
- `km agent list <sb> --queue` is the new observability subcommand.

### Claude's Discretion
- Exact SSM command structure for multi-file push (one `sendSSMAndWait` call batching all writes, or N calls).
- Whether to embed runner via `configFiles` map (operator-side string literal) or a separate Go constant in userdata.go.
- Test coverage breakdown (which tests go in `create_test.go` vs `agent_test.go`).

### Deferred Ideas (OUT OF SCOPE)
- `spec.agent.prompts: []` in SandboxProfile (option B).
- `km play plan.yaml <sb>` playbook files (option C).
- `km agent queue` subcommand (clear/retry/pause).
- `km resume --prompt`.
- Templating inside prompt text.

---

## Standard Stack

### Core
| Library / Tool | Version | Purpose | Why Standard |
|----------------|---------|---------|--------------|
| `github.com/spf13/cobra` | v1.9.1 | CLI flag registration | Project standard; `StringArrayVar` for repeatable flags |
| `github.com/aws/aws-sdk-go-v2/service/ssm` | project go.mod | SSM SendCommand push + poll | Already used in `sendSSMAndWait` |
| `text/template` (stdlib) | go stdlib | userdata.go template rendering | Already used for all systemd unit generation |
| `bash` (on-box) | any | queue runner script | ~80 lines; no new binary needed |
| `tmux` | preinstalled on AL2023/Ubuntu | runner session wrapper | Already used by `km agent run` for agent sessions |
| `systemd` | preinstalled on AL2023/Ubuntu | `km-queue.service` unit | All existing sidecars use systemd |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `encoding/base64` (stdlib) | go stdlib | Encode prompt text for SSM command | Already used in `BuildAgentShellCommands` |
| `encoding/json` (stdlib) | go stdlib | Serialize `meta.json` | Inline in SSM push script |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `StringArrayVar` (preserves `--prompt a --prompt b`) | `StringSliceVar` | StringSliceVar splits on comma — breaks prompts with commas in text. Use `StringArrayVar`. |
| `sendSSMAndWait` for push | SSM PutParameter (used for Slack channel) | SSM PutParameter can't write to sandbox filesystem. `sendSSMAndWait` is the right pattern. |
| systemd unit seeded via `configFiles` | initCommands | `configFiles` runs AFTER initCommands (confirmed at userdata.go:2854). Preferred for file placement. |

**Installation:** No new dependencies. Pure reuse of existing primitives.

---

## Architecture Patterns

### `km agent run` Mirror Points (HIGH confidence — read directly from source)

**Flag registration** (`agent.go:156-290`):
- `--prompt` is `StringVar` (single value) in `agent run`
- Phase 86 needs `StringArrayVar` on `km create` for repeatable multi-prompt
- `--wait`, `--no-bedrock` reuse identical patterns

**SSM push path** (`agent.go:1291-1299`, `BuildAgentShellCommands`):
- SSM document: `AWS-RunShellScript`
- Commands array: `[]string{prepDir, "cat > /tmp/km-agent-run.sh << 'KMEOF'\n...\nKMEOF", "chmod +x ...", "sudo -u sandbox bash -c \"tmux new-session -d -s '...' '...'\"", "sudo -u sandbox bash -c \"tmux wait-for km-done-...\"", "echo KM_RUN_ID=...", "rm -f ..."}`
- All SSM RunShellScript executes as root by default; sandbox-user commands use `sudo -u sandbox`
- `prepDir` = `"mkdir -p /workspace/.km-agent/runs && chown sandbox:sandbox /workspace/.km-agent /workspace/.km-agent/runs"` — must run first

**The `sendSSMAndWait` helper** (`agent.go:1094-1144`):
- Single multi-line shell command pushed via `ssm.SendCommand` with document `AWS-RunShellScript`
- Polls `GetCommandInvocation` at 2s then steady state until `Success/Failed/Cancelled/TimedOut`
- 5-minute max wait (150 attempts × 2s)
- Returns stdout as string
- Reuse directly for queue-file push and for `--wait` poll of `*.meta.json` status

**tmux session naming** (`agent.go:613`):
- Format: `km-agent-{runID}` for agent runs
- Phase 86 runner: `km-queue` (static, single session per box per spec)

**The `BuildAgentShellCommands` function** (`agent.go:1175-1299`):
- Returns `([]string, string)` — commands slice + runID
- Script template sets `HOME=/home/sandbox`, sources profile.d, decodes prompt from base64, writes to `/workspace/.km-agent/runs/$RUN_ID/output.json`
- Exactly what the queue runner calls per entry (runner invokes the claude binary the same way)

**`--wait` polling** (`agent.go:665-771`, `waitForAgentCompletion`):
- Polls SSM `GetCommandInvocation` on the long-running SendCommand (up to 8h)
- Sends EventBridge heartbeat every 5 minutes to prevent idle-timeout destroy
- Phase 86 `--wait` is simpler: poll via `sendSSMAndWait` reading `*.meta.json` status fields, no 8h timeout needed (queue drain is bounded by the number of prompts)

### `km create` Current Shape (HIGH confidence — read directly from source)

**Flag block** (`create.go:180-211`): `cmd.Flags()` calls. New `--prompt` repeatable flag and `--wait` non-blocking flag insert here. `--no-bedrock` already exists.

**Docker detection** (`create.go:140-143, 295-302`): `dockerShortcut` bool is set if `--docker` flag used; `substrateOverride = "docker"` after. Hard-fail for `--prompt + docker` should fire early in `NewCreateCmd`'s `RunE`, before `runCreate`/`runCreateRemote` dispatch, checking `dockerShortcut || substrateOverride == "docker"`.

**SSM reachability gate**: `km create` does NOT have an explicit SSM-reachability gate today. The sandbox is reachable implicitly once `runCreate` returns success (Step 10, terragrunt apply done). The instance has SSM agent running (it starts before `amazon-ssm-agent.service` which `km-bootstrap.service` precedes). Prompt push happens in a new Step 16 after the existing Step 14 (lifecycle notification) and Step 15 (identity provisioning) at lines ~1323. The instance is reachable by then. The existing `sendSSMAndWait` retry loop handles the brief window where SSM isn't yet ready.

**Post-provision hook**: No existing hook — Step 16 is a new step added at the bottom of `runCreate`, following the existing Step 15 pattern. Same for `runCreateRemote` — it delegates to Lambda which calls `runCreate` internally; but `runCreateRemote` on the operator side does NOT run post-provision steps (it fires and returns). The `--prompt` push therefore runs in the LOCAL `runCreate` call only. This is correct: remote creates happen in the Lambda subprocess which has full AWS access and runs the full `runCreate` body including all steps.

**Remote vs local**: `runCreateRemote` dispatches via EventBridge to the create-handler Lambda, which re-invokes `km create` with `KM_REMOTE_CREATE=true`. The full `runCreate` body runs inside Lambda. The `--prompt` flags must be forwarded to the Lambda invocation (via the EventBridge message JSON). This is a non-trivial extension to `runCreateRemote` — see Pitfall #4 below.

### `configFiles` Write Path (HIGH confidence — read directly from source)

**Materialization in userdata.go** (`userdata.go:2851-2865`, template section 7.6):
```go
{{- if .ConfigFiles }}
{{- range $path, $content := .ConfigFiles }}
CFDIR="$(dirname '{{ $path }}')"
mkdir -p "$CFDIR"
cat > '{{ $path }}' << 'KM_CONFIG_EOF'
{{ $content }}
KM_CONFIG_EOF
chown -R sandbox:sandbox "$CFDIR"
{{- end }}
{{- end }}
```

**Boot sequence position**: Section 7.6 runs AFTER `initCommands` (section 7.5) and BEFORE the "sidecar restart" block (section 8). Specifically:
- Sections 2-5: identity, profile.d, sidecars, tools
- Section 7.5: initCommands (from S3 km-init.sh)
- **Section 7.6: configFiles** ← runner script + systemd unit land here
- Then: `systemctl enable km-queue` must be added to the existing enable line

**Can it own systemd unit files?** YES. The path `/etc/systemd/system/km-queue.service` is outside `/home/sandbox` and `/workspace`, but `configFiles` runs as root (the template uses `cat > '/path'` without `sudo`). The existing `km-mail-poller.service` unit (`userdata.go:1831-1845`) is written with an identical `cat > /etc/systemd/system/...` pattern — but that one is unconditional/hardcoded in userdata.go. The `configFiles` map approach puts both the bash script and the systemd unit in the `configFiles` map under `params.ConfigFiles` in `compiler.Compile` (see `userdata.go:2984-2985`, `3283`, `3398`).

**Decision: inject via userdata.go params, not configFiles map**: The runner and unit are unconditional (every EC2/ECS sandbox needs them, regardless of profile). They should be added as unconditional template blocks in `userdata.go` alongside the `km-mail-poller` and `km-presence` blocks, NOT via `spec.execution.configFiles`. This avoids operator YAML changes and keeps runner versioning tied to the km binary that compiled the userdata.

### Systemd Pattern (HIGH confidence — read directly from source)

Established pattern from `km-mail-poller.service` and `km-presence.service`:

```
[Unit]
Description=...
After=network.target
[Service]
User=root
Environment=SANDBOX_ID={{ .SandboxID }}
ExecStart=/opt/km/bin/km-queue-runner
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
```

The `km-queue.service` must use `User=root` (runner writes queue files to `/workspace/.km-agent/queue/`, then invokes `sudo -u sandbox` for the claude call, matching the agent pattern). `Restart=always` gives the reboot-resume auto-restart behavior the spec requires.

**Enable line** (`userdata.go:2452, 2459`): Existing lines end with conditionals for `km-mail-poller` and `km-slack-inbound-poller`. The `km-queue` service enable should be added unconditionally (like `km-presence`), not behind a conditional.

### Queue Runner Invocation of `claude` (HIGH confidence)

The on-box runner must invoke `claude` identically to `BuildAgentShellCommands`. Key elements:
```bash
export HOME=/home/sandbox
source /etc/profile.d/km-profile-env.sh 2>/dev/null
source /etc/profile.d/km-identity.sh 2>/dev/null
source /etc/profile.d/km-audit.sh 2>/dev/null
RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
mkdir -p "$RUN_DIR"
echo "running" > "$RUN_DIR/status"
sudo -u sandbox bash -c "tmux new-session -d -s 'km-agent-$RUN_ID' '/tmp/km-agent-run.sh; exec bash'"
sudo -u sandbox bash -c "tmux wait-for km-done-$RUN_ID"
```

The runner MUST use the same `BuildAgentShellCommands`-style script writing approach (write to `/tmp/km-agent-run.sh`, chmod, tmux launch) so that:
1. Output lands in `/workspace/.km-agent/runs/<ts>/output.json`
2. The run is visible to `km agent list <sb>` (same directory)
3. The run is attachable via `km agent attach` (same tmux session naming `km-agent-*`)

The runner can call the existing on-box `claude` binary directly OR use SSM SendCommand from operator side. The spec says on-box runner calls claude directly (it's an on-box bash script). The runner therefore calls `BuildAgentShellCommands`-equivalent shell logic inline.

### `tmux` Availability (HIGH confidence)

`tmux` is already used by `km agent run` and `km agent attach`. `km agent run` issues `tmux new-session` via SSM, and `km agent attach` queries `tmux list-sessions`. If tmux weren't available, all existing agent functionality would be broken. No installation step needed.

### `systemd` Availability (HIGH confidence)

All three base AMIs (Amazon Linux 2023, Ubuntu 24.04, Ubuntu 22.04) use systemd. The existing `km-bootstrap.service`, `km-audit-log.service`, `km-dns-proxy.service`, `km-http-proxy.service`, `km-tracing.service`, `km-presence.service`, `km-mail-poller.service`, and `km-slack-inbound-poller.service` are all generated inline in `userdata.go`. Full precedent.

### `km pause` / `km resume` Semantics (HIGH confidence)

- `km pause` = EC2 `StopInstances` (preserves EBS volume). NOT hibernate.
- `km resume` = EC2 `StartInstances`.
- cloud-init does NOT re-run `runcmd` on resume (confirmed comment at `userdata.go:1079`). The queue files in `/workspace/.km-agent/queue/` are on the persistent EBS volume and SURVIVE pause/resume.
- The systemd unit with `Restart=always` + `WantedBy=multi-user.target` + `systemctl enable` ensures the runner auto-starts on every boot including resume.
- The runner's start-time reconcile step (reset `running` → `pending`) handles the case where the runner was mid-execution when pause occurred.

### Atomic SSM Push (HIGH confidence)

`sendSSMAndWait` accepts a single shell command string. A multi-file push can be a single heredoc-based shell command:
```bash
mkdir -p /workspace/.km-agent/queue
printf '%s' '$(cat <<PROMEOF\n...\nPROMEOF)' > /workspace/.km-agent/queue/001.prompt
printf '%s' '{"status":"pending","no_bedrock":false,...}' > /workspace/.km-agent/queue/001.meta.json
...
chown -R sandbox:sandbox /workspace/.km-agent/queue
```

Use base64 encoding for prompt content (same pattern as `BuildAgentShellCommands`) to avoid heredoc nesting issues with special characters. One `sendSSMAndWait` call for all N queue files is cleaner and atomic from the operator's perspective.

### `@file` Convention (MEDIUM confidence — spec-defined, no existing code precedent for `@`)

`km agent run` currently handles `--prompt <file-path>` by checking if the value is an existing file (`os.Stat`). The `@file` convention is new for Phase 86. Implementation:
```go
for i, raw := range promptFlags {
    switch {
    case strings.HasPrefix(raw, "@@"):
        prompts[i] = raw[1:] // strip one @, use rest literally
    case strings.HasPrefix(raw, "@"):
        path := raw[1:]
        data, err := os.ReadFile(path)
        if err != nil { return fmt.Errorf("--prompt @%s: %w", path, err) }
        prompts[i] = string(data)
    default:
        prompts[i] = raw
    }
}
```

The `km agent run` file-reading logic (`agent.go:263-269`) uses `os.Stat` (if it's a file, read it). Phase 86 uses explicit `@` prefix per the spec — different convention, but both belong to the create/agent command pair.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| SSM command push + poll | Custom polling loop | `sendSSMAndWait` in `agent.go:1094` | Already handles retry, status codes, 5-min timeout |
| tmux session wrapper | New process manager | `tmux new-session -d -s km-queue` | Already used by every `km agent run` |
| systemd service skeleton | New init system abstraction | Inline heredoc in userdata.go template | 8+ existing precedents in the codebase |
| Prompt file reading | Custom file parser | `os.ReadFile` + `@` prefix stripping | 4 lines of Go, no library needed |
| Base64 prompt encoding | Shell escaping | `base64.StdEncoding.EncodeToString` | Same as `BuildAgentShellCommands` |
| `--wait` completion detection | New IPC mechanism | `sendSSMAndWait` polling `cat *.meta.json` | Avoids adding any new IPC; meta.json is already the state store |

---

## Common Pitfalls

### Pitfall 1: Remote Create Does Not Run Post-Provision Steps Operator-Side

**What goes wrong:** `runCreateRemote` dispatches to the Lambda and returns. The Lambda subprocess runs the full `runCreate` body, including all post-provision steps. If `--prompt` flags are only passed to `runCreate` (local path) but not forwarded in the EventBridge message to the Lambda, `km create profiles/learn.yaml --prompt "..."` via the default remote path silently ignores the prompts.

**Why it happens:** The remote path only forwards: `profilePath`, `onDemand`, `noBedrock`, `awsProfile`, `aliasOverride`, `ttlOverride`, `idleOverride`, `computeBudgetOverride`, `aiBudgetOverride`. Any new parameters need explicit forwarding in the EventBridge message JSON schema.

**How to avoid:** Either (a) forward `prompts []string` in the EventBridge message and handle in the Lambda, or (b) run the post-provision prompt-push AFTER `runCreateRemote` returns on the operator side (the Lambda's `runCreate` has already completed by then, sandbox is reachable). Option (b) is simpler and keeps the Lambda untouched. The operator-side `runCreateRemote` already gets back the sandboxID. Step 16 runs operator-side in both code paths.

**Warning signs:** `--prompt` works with `--local` but silently does nothing with `--remote`.

### Pitfall 2: configFiles `chown -R` Breaks `/etc/systemd/system/` Files

**What goes wrong:** The `configFiles` template section (`userdata.go:2862`) runs `chown -R sandbox:sandbox "$CFDIR"` on the parent directory of every config file. If `/etc/systemd/system/km-queue.service` is written via `configFiles`, then `/etc/systemd/system/` gets `chown`'d to `sandbox:sandbox`, breaking all other systemd units.

**Why it happens:** The `configFiles` template does a directory-level `chown -R` without checking if the target is a system directory.

**How to avoid:** Do NOT write the systemd unit via `spec.execution.configFiles`. Write both the runner script and the unit as unconditional template blocks in `userdata.go`, following the `km-mail-poller` pattern. These blocks write directly with `cat >` and use `chmod +x` appropriately. The runner script goes to `/opt/km/bin/km-queue-runner` (consistent with other km binaries); the unit goes to `/etc/systemd/system/km-queue.service`.

### Pitfall 3: Queue Runner Runs as Root, Claude Needs Sandbox User Context

**What goes wrong:** The systemd unit runs as `User=root`. If the runner invokes `claude` as root, session files (`~/.claude/`) are in `/root/`, not `/home/sandbox/.claude/`. The auth probe also checks the wrong path.

**Why it happens:** The SSM-based `BuildAgentShellCommands` already runs as root (SSM default) and wraps with `sudo -u sandbox bash -c "..."`. The runner must replicate this wrapper.

**How to avoid:** The runner script invokes claude via: `sudo -u sandbox bash -c "..."` — identical to the existing `km agent run` pattern. The `HOME=/home/sandbox` export ensures claude finds its session files. The auth probe checks `/home/sandbox/.claude/.credentials.json` (not `/root/.claude/`).

### Pitfall 4: SSM SendCommand 24KB Stdout Limit

**What goes wrong:** When pushing many large prompts in one SSM command, the stdout of `sendSSMAndWait` is capped at 24KB (SSM limit). The push itself doesn't need stdout, but if the combined echo statements exceed 24KB, the "KM_RUN_ID=..." echo could be truncated.

**Why it happens:** SSM `GetCommandInvocation.StandardOutputContent` is capped.

**How to avoid:** The queue-file push command doesn't need to produce large stdout. Use minimal echo statements. The `sendSSMAndWait` helper already warns on `>= 24000 bytes` in `runAgentResults`.

### Pitfall 5: Runner State-Machine on Reboot — `running` Entry Stalls

**What goes wrong:** If the box reboots mid-execution (e.g., `km pause`), the runner was executing entry `002` which is in `running` state. On reboot, systemd starts `km-queue.service`. The runner sees `002.meta.json` with `status: running`. If it skips `running` entries and only processes `pending`, the chain stalls forever.

**Why it happens:** The runner exits after draining pending entries. The `running` entry is never reset.

**How to avoid:** The runner's first action on startup: for every entry with `status: running`, atomically reset to `status: pending`. Then begin the pending drain loop. The spec explicitly specifies this reconcile step. The implementation must do this before any auth probe.

### Pitfall 6: `Restart=always` on a Completed Runner = CPU Loop

**What goes wrong:** Once all prompts are `done`, the runner exits (no more pending entries). `Restart=always` immediately restarts it. The runner finds no pending entries, exits again. This creates a rapid restart loop consuming CPU.

**Why it happens:** `Restart=always` restarts the service regardless of exit code.

**How to avoid:** Two options: (a) Use `Restart=on-failure` — the runner exits 0 when done, so systemd doesn't restart it. Only restart on non-zero exit (crash). (b) Have the runner `sleep 300` when it finds no pending entries (polling mode). Option (a) is correct per the spec's intent: the runner exits when done; if new prompts appear, the operator uses `systemctl start km-queue`.

---

## Code Examples

### Repeatable Flag Registration (Go)
```go
// In NewCreateCmd, var block:
var prompts []string
// In cmd.Flags():
cmd.Flags().StringArrayVar(&prompts, "prompt", nil,
    "Queue a prompt (repeatable). @file reads from file; @@ escapes literal @.")
// Note: StringArrayVar preserves --prompt a --prompt b as ["a","b"]
// StringSliceVar would split on commas inside prompt text — wrong behavior.
```

### `@file` Parsing (Go)
```go
// Source: spec design + os.ReadFile stdlib pattern from agent.go:263-269
func resolvePrompts(raw []string) ([]string, error) {
    out := make([]string, len(raw))
    for i, v := range raw {
        switch {
        case strings.HasPrefix(v, "@@"):
            out[i] = v[1:] // @@ -> literal @ prefix
        case strings.HasPrefix(v, "@"):
            data, err := os.ReadFile(v[1:])
            if err != nil {
                return nil, fmt.Errorf("--prompt %s: %w", v, err)
            }
            out[i] = string(data)
        default:
            out[i] = v
        }
    }
    return out, nil
}
```

### SSM Batch Queue File Push (Go)
```go
// Source: sendSSMAndWait pattern from agent.go:1097-1144 + BuildAgentShellCommands base64 pattern
func pushQueueFiles(ctx context.Context, ssmClient SSMSendAPI, instanceID string, prompts []string, noBedrock bool) error {
    var sb strings.Builder
    sb.WriteString("mkdir -p /workspace/.km-agent/queue\n")
    for i, p := range prompts {
        num := fmt.Sprintf("%03d", i+1)
        b64 := base64.StdEncoding.EncodeToString([]byte(p))
        noBedrockVal := "false"
        if noBedrock { noBedrockVal = "true" }
        sb.WriteString(fmt.Sprintf(`echo %s | base64 -d > /workspace/.km-agent/queue/%s.prompt`+"\n", b64, num))
        sb.WriteString(fmt.Sprintf(`printf '{"status":"pending","no_bedrock":%s,"created_at":"%s"}' > /workspace/.km-agent/queue/%s.meta.json`+"\n",
            noBedrockVal, time.Now().UTC().Format(time.RFC3339), num))
    }
    sb.WriteString("chown -R sandbox:sandbox /workspace/.km-agent/queue\n")
    _, err := sendSSMAndWait(ctx, ssmClient, instanceID, sb.String())
    return err
}
```

### Systemd Unit (userdata.go template block)
```go
// Source: km-mail-poller.service pattern at userdata.go:1831-1845
// Add unconditionally (all EC2/ECS sandboxes get the queue runner)
cat > /etc/systemd/system/km-queue.service << 'UNIT'
[Unit]
Description=Klankrmkr prompt queue runner
After=network.target
[Service]
User=root
Environment=SANDBOX_ID={{ .SandboxID }}
ExecStart=/opt/km/bin/km-queue-runner
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
```

### `km agent list --queue` (Go, agent.go extension)
```go
// Add --queue flag to newAgentListCmd; if set, read /workspace/.km-agent/queue/*.meta.json
// via sendSSMAndWait and display as table. Same instance fetch + SSM pattern.
listQueueCmd := `sudo -u sandbox bash -c '
for f in /workspace/.km-agent/queue/*.meta.json 2>/dev/null; do
  [ -f "$f" ] || continue
  num=$(basename "$f" .meta.json)
  status=$(jq -r .status "$f" 2>/dev/null || echo "unknown")
  prompt=$(cat "${f%.meta.json}.prompt" 2>/dev/null | head -c 60)
  echo "$num|$status|$prompt"
done'`
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `km agent run` requires sandbox to be running already | Phase 86: `km create --prompt` bundles create + prompt queuing | Phase 86 | Operator can express multi-step plans at provision time |
| `--prompt` on `km agent run` takes a single string or file path (via `os.Stat`) | Phase 86 uses explicit `@file` prefix convention | Phase 86 | Less ambiguous; `@` is well-established curl convention |
| No runner persistence between `km create` exit and agent | Phase 86: `km-queue.service` persists | Phase 86 | Network drop / operator machine shutdown doesn't lose queue |

**Note on `km agent run` existing file-reading:** The current `agent.go:263-269` reads file if it exists via `os.Stat` — no `@` prefix required. Phase 86 uses the explicit `@` prefix because `km create --prompt` is a new flag that needs to distinguish file references from literal text without ambiguity. The `km agent run` behavior is unchanged.

---

## Open Questions

1. **Remote create forwarding for `--prompt`**
   - What we know: `runCreateRemote` dispatches via EventBridge JSON to the Lambda. The Lambda re-invokes `runCreate` internally. Post-provision prompt-push is a new Step 16.
   - What's unclear: Should Step 16 run operator-side (after `runCreateRemote` returns) or inside the Lambda?
   - Recommendation: Run Step 16 operator-side. `runCreateRemote` already returns the `sandboxID`; the operator-side code can call `pushQueueFiles` + `systemctl start km-queue` after the Lambda completes. This keeps the Lambda untouched and avoids a Lambda redeploy. The Lambda's `runCreate` body is already complete before `runCreateRemote` returns.

2. **`Restart=on-failure` vs `Restart=always` for `km-queue.service`**
   - What we know: Spec says `Restart=always`. This causes a restart loop when the runner completes normally (exit 0).
   - What's unclear: Was "Restart=always" in the spec intentional (for crash recovery only)?
   - Recommendation: Use `Restart=on-failure`. The runner exits 0 when done, exits non-zero on crash. Systemd only restarts on non-zero. On reboot/resume, systemd starts the service fresh (not restart-triggered). This avoids the CPU loop and is semantically correct.

---

## Validation Architecture

Nyquist validation is enabled (`nyquist_validation: true` in `.planning/config.json`).

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib) + `go test ./...` |
| Config file | none — standard `go test` |
| Quick run command | `go test ./internal/app/cmd/ -run TestCreate -count=1 -v` |
| Full suite command | `go test ./... -count=1` |
| Build command | `make build` |

### Phase Requirements → Test Map

From the spec (BRIEF.md not present; requirements derived from spec document):

| Req ID | Behavior | Test Type | Automated Command | File |
|--------|----------|-----------|-------------------|------|
| PQ-01 | `--prompt` flag registered on `km create`, repeatable | unit | `go test ./internal/app/cmd/ -run TestCreatePromptFlag -v` | `create_test.go` (Wave 0) |
| PQ-02 | `@file` reads file content; `@@` escapes literal `@`; missing file errors | unit | `go test ./internal/app/cmd/ -run TestResolvePrompts -v` | `create_test.go` (Wave 0) |
| PQ-03 | `--prompt + --docker` hard-fails before any provisioning | unit | `go test ./internal/app/cmd/ -run TestCreatePromptDockerReject -v` | `create_test.go` (Wave 0) |
| PQ-04 | SSM queue-file push sends correct base64 content + meta.json structure | unit | `go test ./internal/app/cmd/ -run TestPushQueueFiles -v` | `create_test.go` (Wave 0) |
| PQ-05 | `--wait` polls meta.json until all `done`; exits 0 | unit (mock SSM) | `go test ./internal/app/cmd/ -run TestCreatePromptWait -v` | `create_test.go` (Wave 0) |
| PQ-06 | `--wait` exits non-zero when first prompt fails; remaining are `skipped` | unit (mock SSM) | `go test ./internal/app/cmd/ -run TestCreatePromptWaitFail -v` | `create_test.go` (Wave 0) |
| PQ-07 | `km agent list --queue` shows queue entries with status | unit (mock SSM) | `go test ./internal/app/cmd/ -run TestAgentListQueue -v` | `agent_test.go` (Wave 0) |
| PQ-08 | Queue runner bash: reconcile `running`→`pending` on start | unit (bash / table-test) | `go test ./internal/app/cmd/ -run TestQueueRunnerStateMachine -v` | `create_test.go` or `runner_test.go` (Wave 0) |
| PQ-09 | `km create --prompt "echo hello" --wait` exits 0, run appears in `km agent list` | operator UAT | real-AWS, manual | UAT plan item 1 |
| PQ-10 | Two prompts run in order; second only after first succeeds | operator UAT | real-AWS, manual | UAT plan item 2 |
| PQ-11 | Failed first prompt marks second `skipped` (no `--wait`) | operator UAT | real-AWS, manual | UAT plan item 3 |
| PQ-12 | Queue survives `km pause` + `km resume`; running entry restarts | operator UAT | real-AWS, manual | UAT plan item 5 |
| PQ-13 | Direct-API mode waits indefinitely for credentials | operator UAT (manual) | real-AWS, operator-in-loop | UAT plan item 4 |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run TestCreate -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps (test files to create)
- [ ] `internal/app/cmd/create_prompt_test.go` — covers PQ-01 through PQ-08 (new file)
  - `TestCreatePromptFlag` — verifies `--prompt` is `StringArrayVar`, repeatable
  - `TestResolvePrompts` — `@file` read, `@@` escape, missing-file error
  - `TestCreatePromptDockerReject` — `--prompt + --docker` hard-fail
  - `TestPushQueueFiles` — mock SSM, verify base64 content + meta.json
  - `TestCreatePromptWait` — mock SSM returning `done` statuses
  - `TestCreatePromptWaitFail` — mock SSM returning `failed`
  - `TestQueueRunnerStateMachine` — table-driven test of runner state transitions
- [ ] Add `TestAgentListQueue` to existing `internal/app/cmd/agent_test.go` — covers PQ-07

No new framework installs needed.

---

## Sources

### Primary (HIGH confidence)
- Direct read: `/Users/khundeck/working/klankrmkr/internal/app/cmd/agent.go` — full file, all patterns
- Direct read: `/Users/khundeck/working/klankrmkr/internal/app/cmd/create.go` — lines 1-1325 (full create flow)
- Direct read: `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` — sections 2845-2904 (configFiles), 1075-1270 (systemd units), 1831-1890 (mail-poller/presence patterns)
- Direct read: `/Users/khundeck/working/klankrmkr/docs/superpowers/specs/2026-05-19-km-create-prompt-queue-design.md` — complete spec
- Direct read: `/Users/khundeck/working/klankrmkr/internal/app/cmd/agent_test.go` — test pattern reference

### Secondary (MEDIUM confidence)
- Direct read: `/Users/khundeck/working/klankrmkr/go.mod` — `cobra v1.9.1`, `pflag v1.0.7` (confirmed `StringArrayVar` available)
- Direct read: `/Users/khundeck/working/klankrmkr/.planning/config.json` — Nyquist enabled

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries directly verified in source
- Architecture: HIGH — all patterns read from live source files
- Pitfalls: HIGH — derived from reading actual code paths that would be hit
- Validation: HIGH — framework confirmed via existing test files

**Research date:** 2026-05-19
**Valid until:** 2026-06-19 (stable domain — no external API churn expected)

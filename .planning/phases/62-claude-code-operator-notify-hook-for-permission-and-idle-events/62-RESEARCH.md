# Phase 62: Claude Code Operator-Notify Hook — Research

**Researched:** 2026-04-26
**Domain:** Claude Code hooks, Go profile/compiler extension, SSM env var injection, bash hook scripting
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Profile schema:** Four new fields under `spec.cli`:
- `notifyOnPermission` (bool, default false) — enable email on Notification hook events
- `notifyOnIdle` (bool, default false) — enable email on Stop hook events
- `notifyCooldownSeconds` (int, default 0) — suppress emails within N seconds of last send
- `notificationEmailAddress` (string, optional) — override recipient

**CLI flags** on `km shell` and `km agent run`:
- `--notify-on-permission` / `--no-notify-on-permission`
- `--notify-on-idle` / `--no-notify-on-idle`
- Cooldown and recipient are profile-only in v1

**Compile-time:** Hook script and settings.json entries written unconditionally (every sandbox). `/etc/environment` entries written only when corresponding profile field is set.

**Runtime:** CLI flag overrides injected as env vars into SSM-launched Claude process.

**Hook script:** `/opt/km/bin/km-notify-hook` bash, ~60 lines. Uses `km-send --body <file>` (not stdin). Always exits 0. Cooldown state in `/tmp/km-notify.last`.

**Email format:** Plain text, signed. Subject `[<sandbox-id>] needs permission` or `[<sandbox-id>] idle`.

**No new infra, no new sidecars, no SES changes.** No retroactive installation on existing sandboxes.

### Claude's Discretion

- Hook script storage: inline in `userdata.go` (following pre-push hook precedent) vs. `pkg/compiler/assets/` file with Go embed. Planner should follow whichever pattern the pre-push hook used.
- Test framework: bats vs. Go-shell-out for hook script tests.
- Plan breakdown and wave assignment.
- Requirement IDs to assign (REQUIREMENTS.md has no HOOK or NOTIFY category yet).

### Deferred Ideas (OUT OF SCOPE)

- Closed-loop reply ingestion (v2)
- Slack/webhook delivery
- Filtering by tool name
- Rich HTML email
- Per-run correlation IDs
- CLI override for cooldown/recipient
- Multiple recipients or separate `replyEmailAddress`
- Retroactive hook installation
</user_constraints>

<phase_requirements>
## Phase Requirements

The roadmap lists "Requirements: TBD." Based on the spec and REQUIREMENTS.md taxonomy, the following new requirement IDs are recommended:

| Recommended ID | Description | Research Support |
|----------------|-------------|-----------------|
| HOOK-01 | Compiler writes `/opt/km/bin/km-notify-hook` bash script unconditionally at provision time | Pre-push hook pattern in `userdata.go` is the precedent; inline heredoc approach |
| HOOK-02 | Compiler merges km-notify-hook entries into `~/.claude/settings.json`, preserving user-supplied hooks | ConfigFiles mechanism + new JSON merge logic; `encoding/json` is the right tool |
| HOOK-03 | Profile fields `spec.cli.notifyOnPermission`, `notifyOnIdle`, `notifyCooldownSeconds`, `notificationEmailAddress` gate behavior via `/etc/environment` env vars | Four new CLISpec fields; `/etc/environment` is new ground in userdata.go |
| HOOK-04 | `km shell` and `km agent run` gain `--notify-on-permission` / `--no-notify-on-permission` / `--notify-on-idle` / `--no-notify-on-idle` flags that inject env vars into SSM-launched Claude process | NoBedrock flag and AgentRunOptions are the precedent |
| HOOK-05 | Hook exits 0 always; uses `km-send --body <file>`; subject/body format matches spec; cooldown is per-sandbox shared across event types | `km-send` already wired; `--body <file>` required per CLAUDE.md |

These slot naturally under a new **Hook** category in REQUIREMENTS.md (following the MAIL-*, OPER-* pattern). They satisfy the following existing categories implicitly:
- MAIL-04 (operator receives notifications for sandbox events) — this extends it to agent events
- CONF-05 (km shell abstracts substrate) — flags extend the shell command
</phase_requirements>

---

## Summary

Phase 62 adds a one-way email notification mechanism for Claude Code agents. When an agent hits a permission prompt (`Notification` hook) or finishes a turn (`Stop` hook), it emails the operator via the existing `km-send` infrastructure. The mechanism is entirely profile-driven with per-invocation CLI flag overrides.

The implementation touches five areas: (1) Go profile schema — four new `CLISpec` fields; (2) JSON schema file — four new `cli` properties; (3) compiler/userdata.go — unconditional hook script drop + conditional `/etc/environment` writes + JSON merge of `settings.json`; (4) CLI commands shell.go and agent.go — two new flag pairs with SSM env var injection; (5) tests — compiler unit tests, hook script bash tests, CLI flag unit tests.

All upstream dependencies are complete: `km-send` (Phase 45), `km agent run` / tmux (Phase 50/51), and `spec.execution.configFiles` (already in production). No new AWS infrastructure, no SES changes, no new sidecars.

**Primary recommendation:** Follow the pre-push hook inline heredoc pattern for the hook script. Inject env vars into `km agent run` by extending `AgentRunOptions` with two new bool fields and prepending `export KM_NOTIFY_ON_PERMISSION=1 KM_NOTIFY_ON_IDLE=0` to the generated script, mirroring the `noBedrockLines` stanza. For `km shell` (interactive), use the SSM SendCommand approach (same as the existing `noBedrock` shell path) to write a `zz-km-notify.sh` profile.d file before the session starts, cleaned up on exit.

---

## Standard Stack

### Core (all already in the codebase)

| Library / Tool | Version | Purpose | Why Standard |
|----------------|---------|---------|--------------|
| `encoding/json` | stdlib | Parse and merge settings.json | Already used throughout; no external deps |
| `text/template` | stdlib | userdata.go template execution | Existing pattern for all user-data generation |
| `cobra` | current | CLI flag definition | All commands use Cobra |
| `km-send` | Phase 45 | Signed email delivery from sandbox | Already wired, `--body <file>` required |
| `jq` | system (sandbox) | Extract transcript text in hook script | Already present in sandbox AMI; used in other hooks |
| `bash` | system (sandbox) | Hook script runtime | All existing hook scripts use bash |

### No New Dependencies

This phase introduces no new Go packages, no new sidecar binaries, no new Terraform modules.

---

## Architecture Patterns

### Pattern 1: Pre-Push Hook — Inline Heredoc in userdata.go Template

**What:** A bash script is written inline in the Go template using a heredoc delimited by a unique string. The script is conditional on a profile field (`HasAllowedRefs`); for this phase the script is **unconditional** (always written).

**Location in source:** `pkg/compiler/userdata.go` lines 305–336 (the `{{- if .HasAllowedRefs }}` block).

**Exact pattern:**
```bash
# In the Go template (userdata.go):
cat > /opt/km/hooks/pre-push << 'PREPUSH'
#!/bin/bash
ALLOWED_REFS="${KM_ALLOWED_REFS:-}"
if [ -z "$ALLOWED_REFS" ]; then exit 0; fi
...
exit 0
PREPUSH
chmod +x /opt/km/hooks/pre-push
```

**Key observations:**
- Script body uses `<< 'PREPUSH'` (single-quoted heredoc delimiter) so no shell variable expansion occurs at write time; the script's own `${VAR}` references are literal.
- File is `chmod +x` immediately after writing.
- The directory (`/opt/km/hooks`) is created separately before the cat.

**For this phase:** Follow the same `cat > /opt/km/bin/km-notify-hook << 'KMHOOKEOF'` pattern. The `/opt/km/bin/` directory is already created unconditionally at the top of the user-data (line ~343: `mkdir -p /opt/km/bin`). No new directory creation needed.

**No `pkg/compiler/assets/` directory exists** — the project does not use Go `embed`; hook scripts are inlined in the template string. Follow this existing pattern.

### Pattern 2: ConfigFiles — Inline Cat Heredoc After initCommands

**What:** `spec.execution.configFiles` writes arbitrary files during bootstrap. Each file is written via `cat > 'path' << 'KM_CONFIG_EOF'`.

**Location in source:** `pkg/compiler/userdata.go` lines 1672–1686.

**Exact template block:**
```
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

**Implication for this phase:** The hook script must write and merge `~/.claude/settings.json` AFTER the configFiles block, so that user-supplied settings.json content is already on disk when the merge happens. The merge block runs after line 1686 in the template sequence.

**Merge logic is new Go code** (not shell): at compile time, in `generateUserData()`, read any user-supplied `settings.json` content from `params.ConfigFiles["/home/sandbox/.claude/settings.json"]`, parse it as JSON with `encoding/json`, append km-notify-hook entries to `hooks.Notification` and `hooks.Stop` arrays, serialize back to JSON, then set `params.ClaudeSettings` (a new string param) for a new template block that writes the merged content.

**Alternative approach (shell-side merge at boot time):** The hook script block runs in user-data and could use `jq` to merge. This is simpler to implement but requires `jq` to be available before Claude Code is installed. Since `jq` is typically installed separately, the compile-time Go merge is safer and testable without a real sandbox. Use the Go merge approach.

### Pattern 3: ProfileEnv / `/etc/profile.d/` — Existing Env Var Delivery

**What:** `spec.execution.env` key-value pairs are written to `/etc/profile.d/km-profile-env.sh` and sourced by all login shells.

**Location in source:** `pkg/compiler/userdata.go` lines 172–191.

**NOT `/etc/environment`:** The spec mandates `/etc/environment` for the notify env vars. `/etc/environment` is NOT currently used anywhere in the codebase (confirmed: zero grep hits). It is read by PAM on SSH/console login and by systemd units. SSM sessions source profile.d scripts, so for SSM-launched Claude processes, `/etc/environment` is NOT automatically sourced.

**Critical finding:** The spec says "SSM-launched env vars take precedence over `/etc/environment`." This implies `/etc/environment` is used as a persist-across-sessions default for _interactive_ Claude sessions that source it. But SSM sessions via KM-Sandbox-Session run as sandbox user via `runAsDefaultUser`, and the session document sources `/etc/profile.d/` via `bash -l`. `/etc/environment` is sourced by PAM, not by bash login scripts. **Therefore `/etc/environment` may not be automatically picked up by SSM-launched bash sessions on Amazon Linux 2.**

**Recommendation:** Write the notify env vars to `/etc/profile.d/km-notify-env.sh` (following the existing `/etc/profile.d/` pattern) rather than `/etc/environment`. This is consistent with all other env var delivery in the codebase, and is guaranteed to be sourced by bash login shells in SSM sessions. The spec says `/etc/environment` — the planner should validate whether the SSM session doc sources PAM or just `/etc/profile.d/`. If the existing pattern already uses `/etc/profile.d/`, use that. The hook script should read from env vars that are guaranteed to be present.

**If the spec is strictly followed for `/etc/environment`:** Amazon Linux 2 systemd-based logins do read `/etc/environment` via PAM (`pam_env`). However, SSM agent sessions may or may not trigger PAM depending on the session configuration. The safest approach is to write to BOTH: `/etc/profile.d/km-notify-env.sh` (for SSM sessions) AND `/etc/environment` (for systemd/PAM compatibility). Alternatively, write only to `/etc/profile.d/` and note the deviation in a code comment.

**Planner decision required:** Confirm whether SSM sessions source `/etc/environment` on the specific Amazon Linux 2 AMI used. The safest is to use `/etc/profile.d/km-notify-env.sh` mirroring the existing pattern.

### Pattern 4: NoBedrock CLI Flag — Two Distinct Injection Paths

**Interactive shell (`km shell` / `km agent --claude`):** Uses `ssmClient.SendCommand` to write `/etc/profile.d/zz-km-no-bedrock.sh` before the session starts, then cleans it up via `defer`. This is a **pre-session file write** approach because `start-session` cannot pass arbitrary env vars.

**Non-interactive agent (`km agent run`):** Uses `AgentRunOptions.NoBedrock` → `noBedrockLines` string prepended to the generated bash script. The script runs as `sudo -u sandbox bash -c "..."`.

**For `--notify-on-permission` / `--notify-on-idle`:**

- **`km shell` path:** Follow the `noBedrock` shell pattern — SendCommand a `zz-km-notify.sh` profile.d file with `export KM_NOTIFY_ON_PERMISSION=1` before the session, defer cleanup. Use a separate file (e.g., `zz-km-notify.sh`) to avoid collision with `zz-km-no-bedrock.sh`.

- **`km agent run` path:** Add `NotifyOnPermission bool` and `NotifyOnIdle bool` to `AgentRunOptions`. In `BuildAgentShellCommands`, generate an `envLines` stanza (similar to `noBedrockLines`) that exports `KM_NOTIFY_ON_PERMISSION` and `KM_NOTIFY_ON_IDLE` at the top of the script.

**`loadProfileCLI` pattern:** Both commands already call `loadProfileCLI(ctx, cfg, sandboxID)` to check `p.NoBedrock`. Add `loadProfileCLINotify(ctx, cfg, sandboxID)` helper (or extend the existing `loadProfileCLI` call to also return the four new fields). Follow the `!cmd.Flags().Changed("no-bedrock")` guard pattern to only apply profile defaults when the flag was not explicitly set.

### Pattern 5: Claude Code Hook Configuration Schema

**Verified from official docs (code.claude.com/docs/en/hooks):** The settings.json hook format for `Notification` and `Stop` is:

```json
{
  "hooks": {
    "Notification": [
      {
        "hooks": [
          { "type": "command", "command": "/opt/km/bin/km-notify-hook Notification" }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "/opt/km/bin/km-notify-hook Stop" }
        ]
      }
    ]
  }
}
```

Note the nested structure: `hooks.Notification` is an array of **hook groups**, each of which has a `hooks` array of **hook entries**. The outer element may optionally have a `matcher` field; for `Notification`, the spec does not restrict by `notification_type` (fires on all Notification events). This matches the spec's design (all permission events fire if gate is on).

**Confirmed field names:** The hook command receives the event name as `hook_event_name` in the JSON payload on stdin, NOT as a CLI argument from settings.json. The command in settings.json is invoked as a subprocess; the event-specific JSON is piped to its stdin. The spec chooses to pass the event name as the first CLI argument (`km-notify-hook Notification`) as a convenience so the script does not need to parse stdin just to know the event type — this is a valid design choice.

### Pattern 6: Hook Payload Schemas (Verified from Official Docs)

**Notification event stdin JSON:**
```json
{
  "session_id": "abc123",
  "transcript_path": "/home/sandbox/.claude/projects/.../transcript.jsonl",
  "cwd": "/workspace",
  "hook_event_name": "Notification",
  "notification_type": "permission_prompt|idle_prompt|auth_success|elicitation_dialog",
  "message": "Claude needs your permission to use Bash"
}
```

**Important:** The official docs include `notification_type` as a field. The spec does not restrict by `notification_type` in v1. The `message` field is present for all notification types. The hook script reads `.message` from the payload — this is correct.

**Stop event stdin JSON:**
```json
{
  "session_id": "abc123",
  "transcript_path": "/home/sandbox/.claude/projects/.../transcript.jsonl",
  "cwd": "/workspace",
  "hook_event_name": "Stop",
  "stop_reason": "end_turn",
  "stop_hook_active": true,
  "model": "claude-sonnet-4-6",
  "input_tokens": 1234,
  "output_tokens": 567
}
```

**Important:** `stop_hook_active` is present. The spec's hook pseudocode reads `.transcript_path` from the payload for the body. The hook should NOT return any JSON decision payload (just exit 0) — it is notification-only.

**Hook invocation:** Claude Code calls the command string from settings.json as a shell command, pipes the JSON payload to its stdin. The event name is NOT passed as a CLI argument by Claude itself — the command string in settings.json is `"/opt/km/bin/km-notify-hook Notification"` which includes the argument directly. This is the correct pattern from the spec.

### Anti-Patterns to Avoid

- **Writing to `/etc/environment` without verifying PAM sourcing in SSM:** Use `/etc/profile.d/km-notify-env.sh` to be safe.
- **Using stdin for km-send:** CLAUDE.md mandates `--body <file>` for OpenSSL 3.5+ signing reliability.
- **Blocking Claude on hook failure:** Hook MUST always exit 0.
- **JSON merge via shell `jq`:** Do at compile time in Go for testability; shell jq risks ordering issues and missing-jq errors.
- **Using `cmd.Flags().Changed()` guard only on one flag:** Apply the `!Changed("--notify-on-permission")` guard independently for each flag so they can be independently overridden.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| JSON merge for settings.json | Custom string concatenation / regex | `encoding/json` Marshal/Unmarshal | Handles edge cases; testable; already in stdlib |
| Email delivery from hook script | Direct SES API call in bash | `km-send --body <file>` | Already signs with Ed25519; handles SES auth; battle-tested |
| Transcript parsing | Custom JSONL reader in hook | `jq` with `select()` | Already on sandbox AMI; handles malformed lines gracefully |
| Cooldown state | Complex distributed locking | `/tmp/km-notify.last` file with `date +%s` | Per-sandbox is sufficient; tmpfs is fast; spec explicitly requires this |

---

## Common Pitfalls

### Pitfall 1: `/etc/environment` Not Sourced by SSM Sessions

**What goes wrong:** The spec says to write notify env vars to `/etc/environment`. On Amazon Linux 2, `/etc/environment` is read by PAM (`pam_env` module). SSM sessions launched via `aws ssm start-session` may not invoke full PAM stack, meaning `/etc/environment` vars are not exported into the sandbox user's shell.

**Why it happens:** The existing codebase uses `/etc/profile.d/` for all env var delivery. The spec diverges here.

**How to avoid:** Write to `/etc/profile.d/km-notify-env.sh` following the existing pattern. The hook script uses `${KM_NOTIFY_ON_PERMISSION:-0}` fallback so unset vars default to 0 (no-op). Alternatively, test `/etc/environment` sourcing in the E2E UAT scenario before relying on it.

**Warning signs:** Hook fires but `KM_NOTIFY_ON_PERMISSION` is empty/unset even though profile has `notifyOnPermission: true`.

### Pitfall 2: settings.json Overwrite Instead of Merge

**What goes wrong:** If the compiler writes `~/.claude/settings.json` via the ConfigFiles block AND also writes it for the hook, the second write clobbers the first.

**Why it happens:** Both the user-supplied configFiles and the hook merge need to touch the same file.

**How to avoid:** Perform the merge entirely in Go at compile time: read `ConfigFiles["/home/sandbox/.claude/settings.json"]` if present, parse, append hook entries, replace the entry in the map. The template then writes the single merged file via the ConfigFiles block. This means the separate "write hook entries" template block is not needed — the Go `generateUserData()` function handles the merge before template execution.

**Warning signs:** User-supplied hooks disappear from settings.json on sandbox provisioned with both configFiles and notify profile fields.

### Pitfall 3: SSM Env Injection Race for Interactive Shell

**What goes wrong:** For `km shell --notify-on-idle`, the SendCommand approach writes a profile.d file then immediately starts the session. If the SendCommand hasn't completed before the session opens, the env var isn't set.

**Why it happens:** The existing `noBedrock` shell path has `time.Sleep(2 * time.Second)` after SendCommand before starting the session (line 235 in shell.go). This is already the pattern.

**How to avoid:** Follow the same sleep pattern. The 2-second wait is already established precedent.

### Pitfall 4: Heredoc Delimiter Collision in userdata.go Template

**What goes wrong:** If the hook script body contains the delimiter string used for the heredoc (e.g., `KMHOOKEOF`), the heredoc terminates early.

**Why it happens:** The pre-push hook uses `PREPUSH` as its delimiter. Using a sufficiently unique delimiter for the notify hook avoids collision.

**How to avoid:** Use `KM_NOTIFY_HOOK_EOF` or similar. Ensure the hook script body doesn't contain this string.

### Pitfall 5: `jq` Absent When Stop Transcript Parsing Runs

**What goes wrong:** The hook script uses `jq` to parse the transcript JSONL. If `jq` is not installed on the sandbox AMI, the Stop event body is always "(no recent assistant text)".

**Why it happens:** `jq` is a separate package not guaranteed on all AMIs.

**How to avoid:** The hook script already falls back to `[[ -z "$body_text" ]] && body_text="(no recent assistant text)"`. Additionally, the init commands in profiles that install Claude Code likely install jq. Document this dependency in the hook script header comment.

### Pitfall 6: CLISpec Fields Are Operator-Side Only — No Sandbox Provisioning Effect (By Design)

**What goes wrong:** Confusion between fields in `spec.cli` (operator-side defaults, not written to user-data as profile env) and fields in `spec.execution.env` (sandbox env vars). The four new fields are `spec.cli` — they affect what the compiler writes to `/etc/profile.d/km-notify-env.sh` (compile-time env) and what the CLI injects (run-time env), not the CLISpec values themselves.

**Why it happens:** `spec.cli.noBedrock` does NOT write anything to user-data — it only controls CLI behavior. But the new notify fields DO write to user-data (the env vars). This is a behavioral difference from the `noBedrock` precedent.

**How to avoid:** Clearly document in code that unlike `noBedrock`, the notify fields affect both user-data generation AND CLI behavior.

---

## Code Examples

### Example 1: CLISpec Extension (pkg/profile/types.go)

```go
// Source: existing CLISpec at types.go:354
type CLISpec struct {
    NoBedrock   bool     `yaml:"noBedrock,omitempty"`
    ClaudeArgs  []string `yaml:"claudeArgs,omitempty"`
    CodexArgs   []string `yaml:"codexArgs,omitempty"`
    // New fields (Phase 62):
    NotifyOnPermission      bool   `yaml:"notifyOnPermission,omitempty"`
    NotifyOnIdle            bool   `yaml:"notifyOnIdle,omitempty"`
    NotifyCooldownSeconds   int    `yaml:"notifyCooldownSeconds,omitempty"`
    NotificationEmailAddress string `yaml:"notificationEmailAddress,omitempty"`
}
```

### Example 2: AgentRunOptions Extension (internal/app/cmd/agent.go)

```go
// Source: existing AgentRunOptions at agent.go:1102
type AgentRunOptions struct {
    AgentType              string
    NoBedrock              bool
    ClaudeArgs             []string
    CodexArgs              []string
    ArtifactsBucket        string
    // New (Phase 62):
    NotifyOnPermission     bool
    NotifyOnIdle           bool
}
```

In `BuildAgentShellCommands`, after the `noBedrockLines` stanza:
```go
var notifyEnvLines string
if opts.NotifyOnPermission || opts.NotifyOnIdle {
    perm := "0"
    if opts.NotifyOnPermission { perm = "1" }
    idle := "0"
    if opts.NotifyOnIdle { idle = "1" }
    notifyEnvLines = fmt.Sprintf(
        "export KM_NOTIFY_ON_PERMISSION=%s\nexport KM_NOTIFY_ON_IDLE=%s", perm, idle)
}
```

### Example 3: settings.json Merge Logic (pkg/compiler/userdata.go generateUserData)

```go
// Compile-time settings.json merge (new Go code, not shell)
// Runs inside generateUserData() before template execution.
baseSettings := map[string]interface{}{}
if existing, ok := p.Spec.Execution.ConfigFiles["/home/sandbox/.claude/settings.json"]; ok && existing != "" {
    if err := json.Unmarshal([]byte(existing), &baseSettings); err != nil {
        return "", fmt.Errorf("invalid JSON in configFiles[\"/home/sandbox/.claude/settings.json\"]: %w", err)
    }
}
// Merge hook entries into hooks.Notification and hooks.Stop arrays.
// [merge logic using map[string]interface{} navigation]
merged, _ := json.MarshalIndent(baseSettings, "", "  ")
// Store merged JSON in params for template to write.
if params.ConfigFiles == nil {
    params.ConfigFiles = map[string]string{}
}
params.ConfigFiles["/home/sandbox/.claude/settings.json"] = string(merged)
```

### Example 4: km shell Notify Flag Injection (internal/app/cmd/shell.go)

```go
// Follow noBedrock pattern in execSSMSession (shell.go:219-246):
if notifyOnPermission || notifyOnIdle {
    perm, idle := "0", "0"
    if notifyOnPermission { perm = "1" }
    if notifyOnIdle { idle = "1" }
    awsCfg, ssmErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
    if ssmErr == nil {
        ssmClient := ssm.NewFromConfig(awsCfg)
        _, _ = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
            InstanceIds:  []string{instanceID},
            DocumentName: awssdk.String("AWS-RunShellScript"),
            Parameters: map[string][]string{
                "commands": {
                    fmt.Sprintf(`echo 'export KM_NOTIFY_ON_PERMISSION=%s\nexport KM_NOTIFY_ON_IDLE=%s' > /etc/profile.d/zz-km-notify.sh`, perm, idle),
                    `chmod 644 /etc/profile.d/zz-km-notify.sh`,
                },
            },
        })
        time.Sleep(2 * time.Second)
        defer func() {
            _, _ = ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
                InstanceIds:  []string{instanceID},
                DocumentName: awssdk.String("AWS-RunShellScript"),
                Parameters:   map[string][]string{"commands": {"rm -f /etc/profile.d/zz-km-notify.sh"}},
            })
        }()
    }
}
```

### Example 5: userdata.go Template Block — Hook Script and Env Vars

```bash
# In userdata.go template:
# ============================================================
# N. Claude Code operator-notify hook (Phase 62)
# Always installed unconditionally; gated by env vars at runtime.
# ============================================================
echo "[km-bootstrap] Installing km-notify-hook..."
cat > /opt/km/bin/km-notify-hook << 'KM_NOTIFY_HOOK_EOF'
#!/bin/bash
set -euo pipefail
event="${1:?event-name required}"
# ... [full hook script body] ...
KM_NOTIFY_HOOK_EOF
chmod +x /opt/km/bin/km-notify-hook
echo "[km-bootstrap] km-notify-hook installed"

{{- if .NotifyEnv }}
# Notify env var defaults from profile spec.cli fields
cat >> /etc/profile.d/km-notify-env.sh << 'KM_NOTIFY_ENV'
{{- range $key, $value := .NotifyEnv }}
export {{ $key }}="{{ $value }}"
{{- end }}
KM_NOTIFY_ENV
chmod 644 /etc/profile.d/km-notify-env.sh
echo "[km-bootstrap] Notify env vars written"
{{- end }}
```

---

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| Single `/etc/environment` for per-session env | `/etc/profile.d/*.sh` sourced by bash login shells | Profile.d is the established pattern in this codebase; use it |
| Inline hook commands in settings.json | Named hook script + settings.json reference | Named script is testable independently |
| Operator manually polls for agent completion | Agent pushes notification email on Stop | Async operator awareness without polling |

**Deprecated/outdated:**
- The `stop_hook_active` field in older Claude Code docs was named differently; current schema uses `stop_hook_active` (confirmed from official docs, April 2026).

---

## Open Questions

1. **`/etc/environment` vs `/etc/profile.d/`**
   - What we know: Spec says `/etc/environment`; codebase uses `/etc/profile.d/` exclusively.
   - What's unclear: Whether SSM sessions on the project's Amazon Linux 2 AMI source `/etc/environment` via PAM.
   - Recommendation: Use `/etc/profile.d/km-notify-env.sh` for safety. Add a comment referencing the spec's intended `/etc/environment`. The hook's `${VAR:-0}` defaults make this functionally equivalent when CLI flags are used.

2. **Notification event granularity**
   - What we know: The `Notification` event fires with `notification_type` field (`permission_prompt`, `idle_prompt`, `auth_success`, `elicitation_dialog`). The spec does not filter by `notification_type`.
   - What's unclear: Whether operators will want idle_prompt notifications (which also fires the Notification event, not just Stop). In v1, all Notification events trigger email if `notifyOnPermission=true`.
   - Recommendation: Implement as specified (no filtering). Document in hook script header that `notification_type` filtering is a v2 feature.

3. **settings.json location precedence**
   - What we know: `~/.claude/settings.json` applies to all projects. `.claude/settings.json` in the project dir applies to one project.
   - What's unclear: Whether km agents are launched with a specific `--project-dir` that might shadow user-level settings.
   - Recommendation: Write to `~/.claude/settings.json` (user-level) as specified. All km agent runs go to `/workspace` which won't have a `.claude/settings.json` by default.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go test (`testing` stdlib), `package compiler` and `package compiler_test` |
| Config file | No separate config — standard `go test ./...` |
| Quick run command | `go test ./pkg/compiler/... ./internal/app/cmd/... -run TestNotify -v` |
| Full suite command | `go test ./pkg/compiler/... ./internal/app/cmd/... -v` |
| Hook script tests | Go test that shells out (follow existing pattern; bats not present in repo) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|--------------|
| HOOK-01 | Hook script written to `/opt/km/bin/km-notify-hook` unconditionally | unit | `go test ./pkg/compiler/... -run TestUserDataNotifyHook` | Wave 0 |
| HOOK-02 | settings.json hook entries merged, user hooks preserved | unit | `go test ./pkg/compiler/... -run TestUserDataNotifySettingsJSON` | Wave 0 |
| HOOK-03 | `/etc/profile.d/km-notify-env.sh` written when profile fields set | unit | `go test ./pkg/compiler/... -run TestUserDataNotifyEnvVars` | Wave 0 |
| HOOK-04a | `--notify-on-permission` / `--no-notify-on-permission` flags on `km shell` | unit | `go test ./internal/app/cmd/... -run TestShellCmd_NotifyFlags` | Wave 0 |
| HOOK-04b | `--notify-on-idle` flag injects env in `BuildAgentShellCommands` | unit | `go test ./internal/app/cmd/... -run TestBuildAgentShellCommands_Notify` | Wave 0 |
| HOOK-05a | Hook exits 0 when gate var is 0 | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_GatedOff` | Wave 0 |
| HOOK-05b | Hook calls km-send with expected subject/body when gated on | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_Notification` | Wave 0 |
| HOOK-05c | Hook parses Stop transcript JSONL for body text | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_Stop` | Wave 0 |
| HOOK-05d | Cooldown prevents second email within window | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_Cooldown` | Wave 0 |
| HOOK-05e | km-send failure does not cause hook to exit non-zero | hook script (bash shelled out) | `go test ./pkg/compiler/... -run TestNotifyHook_SendFailure` | Wave 0 |

**Hook script test pattern:** Shell out from Go test using `exec.Command("bash", hookScriptPath, "Notification")` with a stub `km-send` (PATH override to a temp script) and synthetic stdin/environment. This follows the Go-shell-out approach used in the smoke-test scripts. No bats framework needed.

### Sampling Rate

- **Per task commit:** `go test ./pkg/compiler/... ./internal/app/cmd/... -run "TestNotify|TestUserDataNotify|TestBuildAgentShellCommands_Notify|TestShellCmd_Notify" -v`
- **Per wave merge:** `go test ./pkg/compiler/... ./internal/app/cmd/... -v`
- **Phase gate:** Full suite green (`go test ./...`) before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/compiler/userdata_notify_test.go` — covers HOOK-01, HOOK-02, HOOK-03 (new file following `userdata_test.go` pattern)
- [ ] `internal/app/cmd/shell_notify_test.go` — covers HOOK-04a (or extend `shell_test.go`)
- [ ] `internal/app/cmd/agent_notify_test.go` — covers HOOK-04b (or extend `agent_test.go`)
- [ ] `pkg/compiler/testdata/notify-hook-test.sh` — stub km-send for hook script tests
- [ ] No framework install needed — `go test` is already in CI

---

## Sources

### Primary (HIGH confidence)

- `pkg/compiler/userdata.go` (read directly) — pre-push hook pattern (lines 305–336), configFiles block (lines 1672–1686), generateUserData() signature and param construction (lines 1896–2027), userDataParams struct (lines 1735–1810)
- `pkg/profile/types.go` (read directly) — CLISpec struct (lines 354–372), ConfigFiles field (lines 203–212)
- `internal/app/cmd/shell.go` (read directly) — noBedrock shell.go pattern (lines 108–246), execSSMSession function
- `internal/app/cmd/agent.go` (read directly) — AgentRunOptions struct (lines 1102–1108), BuildAgentShellCommands (lines 1116–1196), loadProfileCLI (lines 1227–1253), runAgent noBedrock prefix (lines 294–307)
- `internal/app/cmd/agent_test.go` (read directly) — TestAgentNonInteractive_NoBedrock (lines 198–236)
- `pkg/compiler/compiler_test.go` (read directly) — TestUserDataPrePushHookPresent/Absent pattern (lines 950–1010)
- `pkg/compiler/userdata_test.go` (read directly) — baseProfile() and generateUserData() test pattern (lines 1–50)
- `docs/superpowers/specs/2026-04-26-operator-notify-hook-design.md` (read directly) — full hook script pseudocode, email format, test surface
- `.planning/phases/62-.../62-CONTEXT.md` (read directly) — locked decisions, implementation footprint
- Official Claude Code docs (code.claude.com/docs/en/hooks, fetched 2026-04-26) — Notification/Stop payload schemas, settings.json format

### Secondary (MEDIUM confidence)

- `pkg/compiler/bedrock.go` (read directly) — mergeBedrockEnv() pattern for env map construction, confirms encoding/json is not used there (just map operations)
- `pkg/profile/schemas/sandbox_profile.schema.json` (read directly) — existing cli schema structure (lines 477–497)
- WebSearch for Claude Code hook schemas — cross-verified with official docs fetch

### Tertiary (LOW confidence)

- `/etc/environment` vs `/etc/profile.d/` sourcing behavior in SSM sessions — not tested empirically; recommended to use `/etc/profile.d/` based on codebase convention

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries are already in use; no new dependencies
- Architecture patterns: HIGH — pre-push hook and noBedrock patterns read directly from source
- Hook payload schemas: HIGH — verified from official docs (code.claude.com/docs/en/hooks), April 2026
- `/etc/environment` sourcing: LOW — not empirically verified; use `/etc/profile.d/` to be safe
- Pitfalls: HIGH — derived from direct code reading

**Research date:** 2026-04-26
**Valid until:** 2026-05-26 (Claude Code hook API is fairly stable; settings.json schema may evolve)

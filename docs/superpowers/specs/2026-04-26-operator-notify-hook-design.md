# Operator-Notify Hook — Design

**Date:** 2026-04-26
**Status:** Approved for implementation (v1 = notification-only)

## Goal

When a Claude Code agent running on a km sandbox needs operator attention,
email a designated address. Two distinct triggers:

1. **Permission prompt** — Claude is mid-turn and waiting for permission to
   use a tool. Maps to Claude Code's `Notification` hook event.
2. **Idle after turn** — Claude finished generating a response and is now
   waiting for the next instruction. Maps to Claude Code's `Stop` hook event.

v1 is one-way notification. v2 (out of scope here, but the design must
remain compatible) closes the loop: operator emails back, the agent picks
up the reply, resumes.

## Non-Goals

- Closed-loop reply ingestion. Designed-around but not built in v1.
- Bedrock/`noBedrock` interactions — orthogonal.
- Throttling beyond a simple cooldown.
- Per-event recipient routing (one address per sandbox).

## Profile Schema Changes

New optional fields under `spec.cli`:

```yaml
spec:
  cli:
    noBedrock: true                            # existing
    notifyOnPermission: true                   # new
    notifyOnIdle: true                         # new
    notifyCooldownSeconds: 60                  # new (default 0 = no cooldown)
    notificationEmailAddress: "team@example.com"  # new (optional override)
```

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifyOnPermission` | bool | `false` | Enable email on `Notification` hook events |
| `notifyOnIdle` | bool | `false` | Enable email on `Stop` hook events |
| `notifyCooldownSeconds` | int | `0` | Suppress emails within N seconds of the last send (per-sandbox) |
| `notificationEmailAddress` | string | unset → operator | Override recipient (e.g., team inbox) |

All fields are independently togglable. Schema additions go in
`pkg/profile/types.go` and `pkg/profile/schemas/sandbox_profile.schema.json`.

## CLI Overrides

`km shell` and `km agent run` gain four flags that override the
profile-baked defaults for a single invocation:

- `--notify-on-permission` / `--no-notify-on-permission`
- `--notify-on-idle` / `--no-notify-on-idle`

Cooldown and recipient address are **profile-only** in v1 (no CLI flag).

## Mechanics

### Compile-Time (always, every sandbox)

The compiler (`pkg/compiler/userdata.go`) writes the hook script and
`settings.json` entries unconditionally during sandbox provisioning:

1. Drops `/opt/km/bin/km-notify-hook` (a bash script — see below).
2. Merges hook entries into `~/.claude/settings.json` for the sandbox user:
   ```json
   {
     "hooks": {
       "Notification": [
         { "hooks": [{ "type": "command", "command": "/opt/km/bin/km-notify-hook Notification" }] }
       ],
       "Stop": [
         { "hooks": [{ "type": "command", "command": "/opt/km/bin/km-notify-hook Stop" }] }
       ]
     }
   }
   ```
   **Merge semantics:** if `spec.execution.configFiles` already supplies a
   `settings.json`, the compiler parses it as JSON and *appends* the
   `km-notify-hook` entry to any existing `hooks.Notification` and
   `hooks.Stop` arrays (creating them if absent). User-supplied hooks are
   preserved; the km hook runs alongside them. If the user-supplied file is
   not valid JSON, the build fails fast.

3. Writes profile-derived defaults into `/etc/environment`:
   ```
   KM_NOTIFY_ON_PERMISSION=1
   KM_NOTIFY_ON_IDLE=1
   KM_NOTIFY_COOLDOWN_SECONDS=60
   KM_NOTIFY_EMAIL=team@example.com   # only if notificationEmailAddress is set
   ```
   Each variable is written iff its corresponding profile field is set
   (true *or* false). Unset profile fields produce no env var, so the hook
   script's `${VAR:-0}` defaults take effect. CLI flags override at
   SSM-launch time by exporting the variable in the launched process'
   environment, which takes precedence over `/etc/environment`.

### Run-Time (per `km shell` / `km agent run` invocation)

The operator-side CLI computes the effective values:

```
effective_on_permission = cli_flag ?? profile.notifyOnPermission ?? false
effective_on_idle       = cli_flag ?? profile.notifyOnIdle       ?? false
```

It passes them as env vars over SSM when launching Claude:

```
KM_NOTIFY_ON_PERMISSION=1 KM_NOTIFY_ON_IDLE=0 claude ...
```

The env vars set on the Claude process propagate to its hook subprocesses.
If neither flag is given, `/etc/environment` defaults take effect.

### Hook Script: `/opt/km/bin/km-notify-hook`

One bash script, ~60 lines. Pseudocode:

```bash
#!/bin/bash
set -euo pipefail
event="${1:?event-name required}"   # Notification | Stop

# 1. Gate check
case "$event" in
  Notification) [[ "${KM_NOTIFY_ON_PERMISSION:-0}" == "1" ]] || exit 0 ;;
  Stop)         [[ "${KM_NOTIFY_ON_IDLE:-0}"       == "1" ]] || exit 0 ;;
  *)            exit 0 ;;
esac

# 2. Cooldown check
cooldown="${KM_NOTIFY_COOLDOWN_SECONDS:-0}"
last_file="/tmp/km-notify.last"
if [[ "$cooldown" -gt 0 && -f "$last_file" ]]; then
  last=$(cat "$last_file")
  now=$(date +%s)
  (( now - last < cooldown )) && exit 0
fi

# 3. Read hook payload from stdin
payload=$(cat)

# 4. Build subject + body file
sandbox_id="${KM_SANDBOX_ID:-unknown}"
case "$event" in
  Notification)
    subject="[$sandbox_id] needs permission"
    msg=$(jq -r '.message // "(no message)"' <<<"$payload")
    body_text="$msg"
    ;;
  Stop)
    subject="[$sandbox_id] idle"
    transcript=$(jq -r '.transcript_path' <<<"$payload")
    # Last assistant message text
    body_text=$(tail -n 50 "$transcript" \
      | jq -r 'select(.type=="assistant") | .message.content[]? | select(.type=="text") | .text' \
      | tail -n 1)
    [[ -z "$body_text" ]] && body_text="(no recent assistant text)"
    ;;
esac

body_file=$(mktemp)
{
  echo "$body_text"
  echo
  echo "---"
  echo "Attach:  km agent attach $sandbox_id"
  echo "Results: km agent results $sandbox_id"
} > "$body_file"

# 5. Send
to_args=()
[[ -n "${KM_NOTIFY_EMAIL:-}" ]] && to_args=(--to "$KM_NOTIFY_EMAIL")
/opt/km/bin/km-send "${to_args[@]}" --subject "$subject" --body "$body_file" || {
  rm -f "$body_file"
  exit 0   # never block Claude on notification failure
}
rm -f "$body_file"

# 6. Update cooldown timestamp
date +%s > "$last_file"
```

Key invariants:

- **Never blocks Claude.** Hook always exits 0 even on send failure.
- **Body via file**, not stdin — required for OpenSSL 3.5+ signing per
  CLAUDE.md (`docs/multi-agent-email.md`).
- **Cooldown is per-sandbox, not per-event.** `/tmp/km-notify.last` is shared
  across both event types — a Notification followed by a Stop within 60s
  emits one email, by design.
- **Default recipient** falls back to km-send's operator default when
  `KM_NOTIFY_EMAIL` is unset.

## Email Format

### Permission event

```
Subject: [sb-abc123] needs permission
From: sb-abc123@sandboxes.klankermaker.ai

Claude needs your permission to use Bash

---
Attach:  km agent attach sb-abc123
Results: km agent results sb-abc123
```

### Idle event

```
Subject: [sb-abc123] idle
From: sb-abc123@sandboxes.klankermaker.ai

I've finished refactoring the auth middleware. Let me know what you'd like
to test next.

---
Attach:  km agent attach sb-abc123
Results: km agent results sb-abc123
```

Subject line format `[<sandbox-id>] <event-keyword>` is intentional — v2's
closed-loop receiver will match operator replies back to the originating
sandbox by parsing the `In-Reply-To`/`References` headers, but the subject
prefix gives a human-readable secondary key.

## Test Surface

### Compiler unit tests (`pkg/compiler/compiler_test.go`)

- Profile with both bools `false` → no `KM_NOTIFY_ON_*` lines in
  `/etc/environment`, but hook entries still in `settings.json`.
- Profile with `notifyOnPermission: true` only → `KM_NOTIFY_ON_PERMISSION=1`
  written, idle var absent.
- Profile with `notifyCooldownSeconds: 30` → variable present.
- Profile with `notificationEmailAddress: "x@y"` → `KM_NOTIFY_EMAIL=x@y`
  written.
- `spec.execution.configFiles` user-supplied `settings.json` → hook entries
  merged in alongside, not clobbered.

### Hook script tests

Stand up the script with fixture stdin payloads:

- Notification with `KM_NOTIFY_ON_PERMISSION=0` → exits 0, no `km-send` call.
- Notification with `KM_NOTIFY_ON_PERMISSION=1` → `km-send` invoked with
  expected subject and body file containing message text.
- Stop with synthetic transcript JSONL → body contains last assistant text.
- Cooldown: two back-to-back invocations with `KM_NOTIFY_COOLDOWN_SECONDS=10`
  → second call exits 0 without calling km-send.
- `km-send` failure → hook still exits 0.

km-send itself is stubbed (PATH override) so no real email is sent.

### CLI flag tests (`internal/app/cmd/`)

- `km shell --notify-on-idle` overrides profile default `false` → env var
  set in SSM session.
- `km agent run --no-notify-on-permission` overrides profile default `true`
  → env var set to 0.

### E2E (manual / opt-in CI)

- Create a sandbox from a profile with `notifyOnPermission: true`, run
  `km agent run --prompt "run rm -rf /"` (or any prompt that triggers a
  permission prompt), confirm operator inbox receives a signed email.
- Same with `notifyOnIdle: true` and a benign prompt; confirm idle email
  arrives after Claude finishes.
- Confirm `notificationEmailAddress: "alt@example.com"` routes to the
  override address.

## v2 Closed-Loop Compatibility

Out of scope to build, but the v1 design choices below are deliberate:

- Hook-emitted email subject prefix `[<sandbox-id>]` lets a future poller
  filter relevant replies.
- v2 will likely add a "wait for reply" hook variant that polls
  `km-recv --json` and feeds matching replies back to Claude. The cooldown
  state file is irrelevant in that mode and will be bypassed.
- `notificationEmailAddress` may evolve to support multiple recipients or
  a separate `replyEmailAddress`; v1 uses one field for both send and
  (implicit) reply target.

## Out of Scope (Explicitly)

- Slack / webhook delivery. Email only.
- Filtering by tool name (e.g., "only notify on Bash permissions"). All
  Notification events fire if the gate is on.
- Rich HTML email. Plain text only, signed via existing km-send protocol.
- Per-run correlation IDs. Subject prefix is enough for v1.

## Implementation Footprint

Files touched:

- `pkg/profile/types.go` — add four `cli` fields.
- `pkg/profile/schemas/sandbox_profile.schema.json` — schema additions.
- `pkg/compiler/userdata.go` — write `/etc/environment` entries, drop
  `/opt/km/bin/km-notify-hook`, merge `settings.json` hook entries.
- `pkg/compiler/compiler_test.go` — coverage per matrix above.
- `internal/app/cmd/shell.go` — `--notify-on-{permission,idle}` flags +
  env var injection.
- `internal/app/cmd/agent.go` (or `agent_run.go`) — same flags + injection.
- New: `pkg/compiler/assets/km-notify-hook.sh` (or inlined string in
  `userdata.go`, matching the pattern used for the pre-push hook).
- New: `pkg/compiler/assets/km-notify-hook_test.sh` (bats or shellcheck +
  Go test that shells out).

No new infra modules, no new sidecars, no SES policy changes (km-send is
already wired). Existing sandboxes will *not* gain hook support
retroactively — the hook script and `/etc/environment` entries are written
into user-data at provision time. To enable on a pre-existing sandbox,
`km destroy && km create` it from a profile with the new fields set.
(`km init --sidecars` rebakes management Lambdas, not sandbox user-data.)

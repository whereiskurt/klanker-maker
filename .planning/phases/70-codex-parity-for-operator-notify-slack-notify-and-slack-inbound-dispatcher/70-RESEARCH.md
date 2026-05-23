# Phase 70: Codex parity + Slack prefix routing & cross-agent thread switching — Research

**Researched:** 2026-05-22
**Domain:** Go monorepo (km CLI), bash userdata heredocs, Codex CLI hook system, Slack Web API
**Confidence:** HIGH (all findings verified against source code; Codex hook shape verified against SPEC.md + codebase evidence)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Profile schema:**
- New field `spec.cli.agent: claude | codex`, default `claude`. Absence ≡ `claude`. Any other value is a validation error.
- No new cross-field rules. Existing inbound/notify combinatorics validate unchanged.
- No new fields specifically for prefix routing. `spec.cli.agent` is the profile default; prefix routing is runtime poller behavior only.
- Schema lives in `pkg/profile/types.go` as `Agent string \`yaml:"agent,omitempty"\`` on `CLISpec`, mirrored in `schemas/sandbox_profile.schema.json` as an enum.

**Codex hook configuration:**
- `~/.codex/config.toml` is written to every sandbox regardless of `spec.cli.agent`. Claude-default sandboxes never start Codex; the file has no runtime effect for them.
- `[features] codex_hooks = true` set unconditionally (provisioned at create time, not feature-flag-gated at runtime).
- Two hook entries only: `PermissionRequest` (matcher = `".*"`) and `Stop` (no matcher). No `PostToolUse` (Tier 3 will add it).
- Both hooks point at `/opt/km/bin/km-notify-hook` with the event name as the first positional argument (matches Claude convention).
- Hook timeout 30s.

**km-notify-hook:**
- Single bash file remains (no fork into per-agent scripts). New `PermissionRequest` branch reuses the existing "needs permission" path verbatim (tool name extracted from `tool_name` field, exits 0 with no stdout so Codex auto-approves).
- `Stop` payload prefers `last_assistant_message` when present (Codex path); falls back to existing transcript-tail logic (Claude path).
- Cooldown logic, env-var gates (`KM_NOTIFY_ON_PERMISSION`, `KM_NOTIFY_ON_IDLE`, `KM_NOTIFY_COOLDOWN_SECONDS`), and downstream `km-send` / `km-slack post` calls are agent-agnostic and reused.

**Inbound poller dispatch fork:**
- Boot-time read of `KM_AGENT` from the env file. Runtime override per turn from prefix matching.
- Claude dispatch unchanged: `claude -p "$PROMPT" --output-format json --dangerously-skip-permissions [--resume $SESSION]`.
- Codex first-turn: `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT" > $OUT_FILE`.
- Codex resume: `codex exec resume $SESSION "$PROMPT" --json --dangerously-bypass-approvals-and-sandbox > $OUT_FILE` (subcommand form, not legacy `--resume` flag).
- Codex session-ID extraction via hook-file approach: the `Stop` hook writes the session ID to a known per-run file (e.g. `/tmp/km-codex-session.$RUN_ID`) before exiting. Spike (Plan 70-00) confirms the exact filename + permission model before planning Plan 70-05.
- `KM_SLACK_THREAD_TS` injection works for both agents (already generic; no changes).

**DDB schema additions (`km-slack-threads`):**
- Two new String attributes: `agent_type` (`claude`|`codex`) and `last_assistant_msg` (truncated to ~2000 chars).
- Writer sets `agent_type` on every successful turn; reader defaults to `claude` when the attribute is absent (backward compat with pre-existing rows).
- `claude_session_id` is reused as the agent-agnostic session-ID column. Column-name hangover documented in CLAUDE.md and `docs/slack-notifications.md`. No migration.
- Both new attributes inherit the existing 30-day TTL via `ttl_expiry`. No schema or module changes (DynamoDB is schemaless on attributes).

**Slack prefix routing:**
- Strict grammar — case-insensitive `claude:` or `codex:` at message start, exactly one optional space after the colon, no leading whitespace tolerated. Regex `^([Cc]laude|[Cc]odex|CLAUDE|CODEX):[[:space:]]?`.
- Per-thread state model: each Slack thread maps 1:1 to one agent (the one that ran the first turn). Stored in `agent_type` on the DDB row keyed by `(channel_id, thread_ts)`.
- Same-agent prefix is a no-op switch: strip the prefix, dispatch the same agent in the same thread, no new thread, no new DDB row, no handoff post.
- Prefix in a brand-new top-level message picks the agent for that fresh thread, overriding the profile default for this thread only. The profile's compiled `KM_AGENT` on disk is unchanged.

**Cross-agent mid-thread switch:**
- Spawns a new top-level message in the same `#sb-{id}` channel (NOT a thread reply, NOT a thread-broadcast). The new top-level becomes the parent of the new agent's thread.
- Bot posts in the old thread: `Switching to {new_agent} → continuing in this thread.` followed by a Slack permalink to the new top-level.
- New thread's first body line: `Continuing from <permalink-to-old-thread>`. Then a truncated excerpt (~500 chars) of the prior agent's `last_assistant_msg` prefixed with `Previous assistant ({old_agent}) said:`.
- New agent's prompt is composed as: `{stripped_prompt}\n\n--- Context from prior thread (agent: {old_agent}) ---\n{full last_assistant_msg, up to 2000 chars}`.
- Switch ordering: post new top-level FIRST (so we know its `ts`/permalink), THEN post the handoff in the old thread with the permalink already embedded. This avoids the Slack `chat.update` 10-minute window risk. Planner MAY use `chat.update` for cleaner UX if they prefer, but the safer ordering is the spec's recommended path.
- Old DDB row is unchanged. Old session/process is NOT killed. Operator replies in the old thread continue to resume the old agent.
- New DDB row written keyed on `(channel_id, new_thread_ts)` with `agent_type={new_agent}`, the new session ID extracted post-run, and the new `last_assistant_msg` after the new agent completes.

**Failure modes (locked behaviors):**
- Permalink retrieval fails → embed `(unavailable)` placeholder, journald log, continue with both posts.
- DDB row write fails after agent dispatch → next reply in the new thread is treated as a first turn; degrades to two-turn-loss of continuity. Acceptable.
- Operator replies in old thread after switch → old DDB row resumes old session. Two threads run in parallel. Working as intended.
- `last_assistant_msg` absent on old row (pre-existing rows): switch proceeds with no context excerpt. New rows always carry the attribute.

**km-slack sidecar additions:**
- New flag `--new-message` on `km-slack post`: omits `thread_ts` from `chat.postMessage` and returns the new message's `ts` to stdout.
- New subcommand `km-slack permalink --channel X --ts Y` wrapping `chat.getPermalink`.
- New subcommand `km-slack update --channel X --ts Y --text Z` wrapping `chat.update`.
- All three are thin REST wrappers; no business logic.

**km doctor:**
- `codex_hook_config_present` — for sandboxes with `spec.cli.agent: codex`, SSM-RunCommand check confirming `~/.codex/config.toml` exists, contains `codex_hooks = true`, and the two expected hook entries point at `/opt/km/bin/km-notify-hook`. WARN on drift.
- `agent_type_consistency` — for each `km-slack-threads` row with `agent_type` set, confirm the sandbox's profile (via `sandbox_id` → S3 profile fetch using the existing `downloadProfileFromS3` helper) still declares the same agent. WARN on drift.
- Both checks honor `--all-regions` and follow Phase 67/68 doctor-check conventions.

**Operator-side CLI:**
- No new CLI commands. `km agent run --claude` / `--codex` flags (Phase 58) remain the per-invocation override.
- `km validate` adds the enum check for `agent` (auto-derived from the schema addition).
- `km shell` defaults to the profile's agent unless `--claude` / `--codex` is passed.

### Claude's Discretion

None explicitly designated. All surface areas are locked.

### Deferred Ideas (OUT OF SCOPE)

- **Tier 3: Phase 68 transcript streaming parity for Codex.** Adds `[[hooks.PostToolUse]]` to `~/.codex/config.toml` + JSONL stream parser.
- **Slack-driven approve/deny on Codex `PermissionRequest`.** Would require Slack interactivity webhook into the bridge Lambda.
- **Goose / other-agent parity.** `agent_type` extends to additional enum values.
- **`agent_session_id` column rename.** Cosmetic; column-name hangover documented.
- **Auto-routing by content heuristics.** No "looks like code → codex" classifier.
- **Thread merge / rejoin after switch.** No mechanism to fold a switched thread back into its origin.
- **More than two-agent prefix routing.** Phase 70 ships claude + codex only.
- **Prefix in `km agent run --prompt`.** CLI side keeps `--claude`/`--codex` flags; poller is the only place prefix parsing runs.
- **Slack thread-broadcast** as alternative to spawning a new top-level message.
</user_constraints>

---

## Summary

Phase 70 is primarily a bash userdata extension + Go sidecar extension + Go doctor extension. The bulk of the work lives in three files: `pkg/compiler/userdata.go` (the km-notify-hook heredoc and the km-slack-inbound-poller heredoc), `cmd/km-slack/main.go` (three new surface elements on the existing `post/upload/record-mapping` sidecar), and `internal/app/cmd/doctor_slack.go` (two new check functions). Profile schema changes are additive (one new `Agent string` field on `CLISpec`, one enum in JSON Schema). DynamoDB is schemaless — no Terraform changes.

The only external unknown is whether `codex exec --json` fires hooks identically to the interactive TUI. SPEC.md rates this as strong-prior-probability-yes, and the Plan 70-00 spike eliminates that risk before any code is written. All other surfaces are fully understood from code inspection.

The cross-agent switch sequence is the most complex flow. The safe implementation order is: (1) post new top-level first to capture its `ts`, (2) call `km-slack permalink` to get the link, (3) post handoff in old thread with permalink embedded. This ordering avoids the 10-minute `chat.update` window entirely. The planner may optionally use `chat.update` on step 3 if preferred for UX cleanliness, but must document the 10-minute window as a deployment risk.

**Primary recommendation:** Implement the spike (Plan 70-00) first, using its confirmed session-ID extraction path as the contract for Plan 70-05. All other plans are unblocked in parallel once that path is confirmed.

---

## Standard Stack

### Core
| Library / Tool | Version / Path | Purpose | Why Standard |
|----------------|---------------|---------|--------------|
| Go stdlib (`flag`, `encoding/json`, `net/http`) | Go 1.21+ | `km-slack` sidecar flag parsing + HTTP | Existing pattern in `cmd/km-slack/main.go` |
| `github.com/whereiskurt/klanker-maker/pkg/slack` | in-tree | Envelope construction, signing, bridge POST | All km-slack subcommands use this package |
| bash (poller script, hook script) | bash 5.x | Inlined userdata heredocs | Existing pattern for km-notify-hook and km-slack-inbound-poller |
| AWS CLI v2 (`aws dynamodb get-item/put-item`, `aws sqs`) | bundled on AMI | DDB reads/writes + SQS ops inside poller | Existing pattern throughout poller |
| `jq` | 1.6+ (on AMI) | JSON field extraction in bash scripts | Already used throughout km-notify-hook and poller |
| Codex CLI | Phase 58 baked in AMI | The agent being dispatched | Phase 58 ships this; Phase 70 assumes it's on PATH |

### Supporting
| Library / Tool | Version / Path | Purpose | When to Use |
|----------------|---------------|---------|-------------|
| `pkg/slack/bridge` (interfaces + handler) | in-tree | Bridge-side dispatch for permalink/update | Only if permalink/update go through the bridge; in Phase 70 they go directly from the sandbox-side sidecar to Slack (same bot token path) |
| `internal/app/cmd/destroy.go::downloadProfileFromS3` | in-tree helper | S3 profile fetch for doctor check | `agent_type_consistency` doctor check |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Hook-file session-ID extraction (Stop hook writes `/tmp/km-codex-session.$RUN_ID`) | Tail JSONL output.json for `session_id` field | Hook-file is more robust; avoids JSONL parsing edge cases when Codex emits partial lines before exit. Spike confirms exact mechanics. |
| Post new-top-level FIRST, then handoff in old thread | Post handoff first, then `chat.update` with permalink | New-top-level-first avoids the 10-minute `chat.update` expiry window. Planner may choose update path if preferred. |

**Installation:** No new Go dependencies. New Slack API methods (`chat.getPermalink`, `chat.update`) are plain JSON REST calls using the existing `pkg/slack.Client.callJSON` helper pattern.

---

## Architecture Patterns

### File Locations
```
pkg/profile/types.go                     ← Add Agent string to CLISpec
pkg/profile/schemas/sandbox_profile.schema.json ← Add agent enum under cli.properties
pkg/compiler/userdata.go                 ← 4 edit sites (see below)
cmd/km-slack/main.go                     ← Add 3 new surface elements
pkg/slack/client.go                      ← Add GetPermalink + UpdateMessage methods
internal/app/cmd/doctor_slack.go         ← Add 2 new check functions
docs/codex-parity.md                     ← New file
docs/slack-notifications.md              ← Extend with prefix routing section
CLAUDE.md                                ← Document agent: codex field + prefix syntax
```

### Pattern 1: CLISpec Field Addition (pkg/profile/types.go)

**What:** Add `Agent string \`yaml:"agent,omitempty"\`` to `CLISpec`. No validation logic in the struct — JSON Schema enum handles it.

**Where:** Line ~394 in `CLISpec`, alongside `CodexArgs []string` and `ClaudeArgs []string`. The field comes before the notify fields for grouping.

**JSON Schema:** Under `spec.cli.properties` (line 514+), add:
```json
"agent": {
  "type": "string",
  "enum": ["claude", "codex"],
  "description": "Default agent for Slack inbound dispatch and km agent run. Default claude. Absence equivalent to claude."
}
```

### Pattern 2: NotifyEnv Extension (pkg/compiler/userdata.go ~line 3580)

**What:** Add `KM_AGENT` to the `notifyEnv` map inside the `if p.Spec.CLI != nil` block. Always emitted (like `KM_NOTIFY_ON_PERMISSION`), not gated on any flag.

**Exact location:** After line 3587 (`"KM_RESOURCE_PREFIX": params.ResourcePrefix,`):
```go
agent := "claude"
if p.Spec.CLI.Agent == "codex" {
    agent = "codex"
}
notifyEnv["KM_AGENT"] = agent
```

This writes into both `/etc/profile.d/km-notify-env.sh` AND `/etc/km/notify.env` (the two-file pattern from Phase 67's gotcha, already handled by the dual `range` over `.NotifyEnv` at lines 865 and 881).

### Pattern 3: Codex config.toml Writer (pkg/compiler/userdata.go — new heredoc block)

**What:** New section in the userdata template that unconditionally writes `~/.codex/config.toml`. Placed immediately after the `~/.claude/settings.json` merge block (which is already present from Phase 62/63/67). The existing `WriteConfigFiles` template block handles user `configFiles`; the codex config is compiler-emitted separately (like `km-notify-hook`), always present regardless of `spec.execution.configFiles`.

**Exact TOML shape** (verbatim from CONTEXT.md — locked):
```toml
[features]
codex_hooks = true

[[hooks.PermissionRequest]]
matcher = ".*"

[[hooks.PermissionRequest.hooks]]
type = "command"
command = "/opt/km/bin/km-notify-hook PermissionRequest"
timeout = 30
statusMessage = "km: notifying operator"

[[hooks.Stop]]

[[hooks.Stop.hooks]]
type = "command"
command = "/opt/km/bin/km-notify-hook Stop"
timeout = 30
```

**Template invocation:** Add a new boolean template param `WriteCodexHooks bool` (always `true` when userdata is generated for EC2; used for conditional test assertions). Unconditional for now — no profile gate.

### Pattern 4: km-notify-hook heredoc edits (lines 467-850)

**What:** Two small edits inside the existing `KM_NOTIFY_HOOK_EOF` heredoc.

**Edit A — PermissionRequest branch (~line 485):** The existing `case "$event" in` block currently handles `PostToolUse`, `Notification`, `Stop`. Add `PermissionRequest` as a gate-pass alongside `Notification`:

Current:
```bash
case "$event" in
  PostToolUse)
    ...
  Notification)
    [[ "${KM_NOTIFY_ON_PERMISSION:-0}" == "1" ]] || exit 0
    ;;
  Stop)
    ...
  *)
    exit 0
    ;;
esac
```

After (add before `Notification`):
```bash
  PermissionRequest)
    [[ "${KM_NOTIFY_ON_PERMISSION:-0}" == "1" ]] || exit 0
    ;;
```

Then in the body-building section (step 5, around line 697), add a `PermissionRequest` case that mirrors `Notification`:
```bash
    PermissionRequest)
      subject="[$sandbox_id] needs permission"
      msg=$(echo "$payload" | jq -r '.tool_name // .message // "(permission request)"' 2>/dev/null || echo "(payload parse failed)")
      body_text="$msg"
      ;;
```

And in the `do_email_branch` gate (line 677):
```bash
if [[ "$event" == "Notification" || "$event" == "PermissionRequest" ]]; then
  do_email_branch=1
```

Exit 0 with no stdout (Codex auto-approves) is guaranteed because the hook always exits 0 at line 849.

**Edit B — Stop `last_assistant_message` fast-path (~line 705):** In the `Stop` case of step 5:

Current:
```bash
Stop)
  subject="[$sandbox_id] idle"
  transcript=$(echo "$payload" | jq -r '.transcript_path // ""' 2>/dev/null || echo "")
  body_text=""
  if [[ -n "$transcript" && -f "$transcript" ]]; then
    body_text=$(tail -n 50 "$transcript" ...)
  fi
  [[ -z "$body_text" ]] && body_text="(no recent assistant text)"
  ;;
```

After:
```bash
Stop)
  subject="[$sandbox_id] idle"
  # Codex fast-path: last_assistant_message is a direct field in the payload.
  # Claude path: falls back to transcript-tail logic.
  body_text=$(echo "$payload" | jq -r '.last_assistant_message // ""' 2>/dev/null || echo "")
  if [[ -z "$body_text" ]]; then
    transcript=$(echo "$payload" | jq -r '.transcript_path // ""' 2>/dev/null || echo "")
    if [[ -n "$transcript" && -f "$transcript" ]]; then
      body_text=$(tail -n 50 "$transcript" ...)
    fi
  fi
  [[ -z "$body_text" ]] && body_text="(no recent assistant text)"
  ;;
```

### Pattern 5: Poller Dispatch Fork (km-slack-inbound-poller heredoc, lines 1278-1585)

**What:** The poller currently dispatches only `claude -p ... --dangerously-skip-permissions`. Phase 70 adds:
1. Boot-time `KM_AGENT` read from env (already in `/etc/km/notify.env`).
2. DDB lookup extended to also read `agent_type` and `last_assistant_msg` attributes.
3. Dispatch fork branching on effective agent.
4. Codex session-ID extraction via hook-file.
5. DDB writeback always sets `agent_type` and `last_assistant_msg`.
6. Prefix parser front-end.
7. Cross-agent switch sequence.

**Boot-time read** (after line 1291, THREADS_TABLE assignment):
```bash
AGENT="${KM_AGENT:-claude}"  # resolved from /etc/km/notify.env at systemd start
```

**DDB lookup extension** (around line 1413):
```bash
DDB_ITEM=$(aws dynamodb get-item ...)
CLAUDE_SESSION=$(echo "$DDB_ITEM" | jq -r '.Item.claude_session_id.S // empty')
CURRENT_AGENT=$(echo "$DDB_ITEM" | jq -r '.Item.agent_type.S // empty')
LAST_ASSISTANT_MSG=$(echo "$DDB_ITEM" | jq -r '.Item.last_assistant_msg.S // empty')
[ -z "$CURRENT_AGENT" ] && CURRENT_AGENT="$AGENT"  # default from profile KM_AGENT
```

**Prefix parser** (new block before dispatch, after PROMPT_FILE creation):
```bash
# Prefix parser: ^([Cc]laude|[Cc]odex|CLAUDE|CODEX):[[:space:]]?
REQUESTED_AGENT=""
STRIPPED_TEXT="$TEXT"
if [[ "$TEXT" =~ ^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]? ]]; then
  PREFIX="${BASH_REMATCH[1],,}"  # lowercase
  REQUESTED_AGENT="$PREFIX"
  STRIPPED_TEXT="${TEXT#*:}"
  STRIPPED_TEXT="${STRIPPED_TEXT# }"  # strip one optional leading space
fi
```

Note: The regex in CONTEXT.md uses `[[:space:]]?` but bash ERE requires `[[:space:]]` without `?` in a character class. The portable form for "zero or one space" in bash `[[ =~ ]]` is `[[:space:]]?` when `?` is a quantifier on the class — this is standard ERE and bash supports it. Confirmed by the existing `[[ ... =~ ... ]]` usages in the poller.

**Dispatch decision tree** (after prefix parser):
```bash
EFFECTIVE_AGENT="$CURRENT_AGENT"
DO_SWITCH=0
if [ -n "$REQUESTED_AGENT" ] && [ "$REQUESTED_AGENT" != "$CURRENT_AGENT" ] && [ -n "$CLAUDE_SESSION" ]; then
  DO_SWITCH=1
elif [ -n "$REQUESTED_AGENT" ]; then
  EFFECTIVE_AGENT="$REQUESTED_AGENT"
  TEXT="$STRIPPED_TEXT"  # strip prefix for dispatch
fi
```

**Codex dispatch:**
```bash
if [ "$EFFECTIVE_AGENT" = "codex" ]; then
  RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
  RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
  mkdir -p "$RUN_DIR"; chown sandbox:sandbox "$RUN_DIR"
  if [ -n "$CLAUDE_SESSION" ]; then
    sudo -u sandbox bash -lc "
      ... 
      codex exec resume '$CLAUDE_SESSION' \"\$(cat '$PROMPT_FILE')\" \
        --json --dangerously-bypass-approvals-and-sandbox \
        > '$RUN_DIR/output.json' 2>'$RUN_DIR/stderr.log'
      echo \$? > '$RUN_DIR/exit_code'
    " || true
  else
    sudo -u sandbox bash -lc "
      ...
      codex exec --json --dangerously-bypass-approvals-and-sandbox \"\$(cat '$PROMPT_FILE')\" \
        > '$RUN_DIR/output.json' 2>'$RUN_DIR/stderr.log'
      echo \$? > '$RUN_DIR/exit_code'
    " || true
  fi
fi
```

**Codex session-ID extraction via hook-file** (the Stop hook writes to `/tmp/km-codex-session.$RUN_ID`):

The Stop hook script will receive `session_id` in its stdin payload. For Codex, the `Stop` hook fires before the `codex exec` process exits. Add to the Stop branch in km-notify-hook:
```bash
# If invoked in a poller context (RUN_ID is set), write session_id for the poller to read.
if [[ -n "${KM_CODEX_RUN_ID:-}" ]]; then
  codex_sid=$(echo "$payload" | jq -r '.session_id // ""' 2>/dev/null || echo "")
  [[ -n "$codex_sid" ]] && echo "$codex_sid" > "/tmp/km-codex-session.${KM_CODEX_RUN_ID}"
fi
```

The poller injects `KM_CODEX_RUN_ID="$RUN_ID"` into the `sudo -u sandbox` environment for Codex runs. After the dispatch completes:
```bash
NEW_SESSION=""
if [ "$EFFECTIVE_AGENT" = "codex" ]; then
  SESSION_FILE="/tmp/km-codex-session.$RUN_ID"
  for _w in 1 2 3 4 5; do
    [ -f "$SESSION_FILE" ] && break
    sleep 1
  done
  NEW_SESSION=$(cat "$SESSION_FILE" 2>/dev/null || true)
  rm -f "$SESSION_FILE"
else
  # existing claude path
  NEW_SESSION=$(jq -r '.session_id // empty' "$RUN_DIR/output.json" 2>/dev/null || true)
fi
```

**Note:** The spike (Plan 70-00) must confirm: (a) `session_id` is in the Codex `Stop` hook payload, (b) `$KM_CODEX_RUN_ID` is visible inside the hook subprocess. If the hook runs as sandbox user but the poller writes the file as root, a `chmod 666` on the temp file or a `/tmp/km-codex-session.$RUN_ID` world-writable path avoids permission issues.

**DDB writeback with new attributes** (extend the existing `aws dynamodb put-item` call around line 1515):
```bash
aws dynamodb put-item \
  --table-name "$THREADS_TABLE" \
  --region "$REGION" \
  --item "{
    \"channel_id\":{\"S\":\"$CHANNEL\"},
    \"thread_ts\":{\"S\":\"$THREAD_TS\"},
    \"claude_session_id\":{\"S\":\"$NEW_SESSION\"},
    \"agent_type\":{\"S\":\"$EFFECTIVE_AGENT\"},
    \"last_assistant_msg\":{\"S\":\"$(echo "$RESULT_TEXT" | head -c 2000 | jq -Rs .)\"},
    \"sandbox_id\":{\"S\":\"$SANDBOX_ID\"},
    \"last_turn_ts\":{\"S\":\"$NOW\"},
    \"ttl_expiry\":{\"N\":\"$TTL_EXPIRY\"}
  }" 2>/dev/null || true
```

**Caution on `last_assistant_msg` JSON escaping:** `RESULT_TEXT` may contain embedded quotes, newlines, or backslashes. Use `jq -Rs .` to JSON-encode the truncated string: `head -c 2000 | jq -Rs .` produces a properly-escaped JSON string (including the surrounding double quotes). The outer DDB JSON must NOT double-quote the result. Pattern: `\"last_assistant_msg\":{\"S\":$(echo "$RESULT_TEXT" | head -c 2000 | jq -Rs .)}`.

### Pattern 6: Cross-Agent Switch Sequence

**Full sequence (safe ordering — new top-level first):**

```bash
if [ "$DO_SWITCH" -eq 1 ]; then
  NEW_AGENT="$REQUESTED_AGENT"
  TEXT="$STRIPPED_TEXT"

  # 1. Get permalink for OLD thread (for new thread's header).
  OLD_PERMALINK=$(/opt/km/bin/km-slack permalink \
    --channel "$KM_SLACK_CHANNEL_ID" --ts "$THREAD_TS" 2>/dev/null || echo "(unavailable)")

  # 2. Post new top-level message (captures new_thread_ts).
  NEW_MSG_BODY=$(mktemp /tmp/km-new-thread.XXXXXX)
  {
    echo "Continuing from $OLD_PERMALINK"
    echo ""
    if [ -n "$LAST_ASSISTANT_MSG" ]; then
      echo "Previous assistant ($CURRENT_AGENT) said:"
      echo "> $(echo "$LAST_ASSISTANT_MSG" | head -c 500)"
    fi
  } > "$NEW_MSG_BODY"
  NEW_TOP_TS=$(/opt/km/bin/km-slack post \
    --channel "$KM_SLACK_CHANNEL_ID" \
    --new-message \
    --body "$NEW_MSG_BODY" 2>&1 | grep -oE 'ts=[0-9.]+' | head -n1 | sed 's/^ts=//')
  rm -f "$NEW_MSG_BODY"

  # 3. Get permalink for NEW thread top-level.
  NEW_PERMALINK="(unavailable)"
  if [ -n "$NEW_TOP_TS" ]; then
    NEW_PERMALINK=$(/opt/km/bin/km-slack permalink \
      --channel "$KM_SLACK_CHANNEL_ID" --ts "$NEW_TOP_TS" 2>/dev/null || echo "(unavailable)")
  fi

  # 4. Post handoff in OLD thread (permalink already known).
  HANDOFF_BODY=$(mktemp /tmp/km-handoff.XXXXXX)
  printf 'Switching to %s → continuing in this thread.\n%s\n' "$NEW_AGENT" "$NEW_PERMALINK" > "$HANDOFF_BODY"
  /opt/km/bin/km-slack post \
    --channel "$KM_SLACK_CHANNEL_ID" \
    --thread "$THREAD_TS" \
    --body "$HANDOFF_BODY" 2>/dev/null || true
  rm -f "$HANDOFF_BODY"

  # 5. Compose seeded prompt.
  SEED_PROMPT="$TEXT"
  if [ -n "$LAST_ASSISTANT_MSG" ]; then
    SEED_PROMPT="$TEXT

--- Context from prior thread (agent: $CURRENT_AGENT) ---
$(echo "$LAST_ASSISTANT_MSG" | head -c 2000)"
  fi

  # 6. Dispatch NEW agent as first turn (no CLAUDE_SESSION) into NEW_TOP_TS thread.
  #    Set THREAD_TS to NEW_TOP_TS for the rest of the turn handling.
  CLAUDE_SESSION=""
  THREAD_TS="${NEW_TOP_TS:-$THREAD_TS}"
  EFFECTIVE_AGENT="$NEW_AGENT"
  TEXT="$SEED_PROMPT"
  # ... fall through to normal dispatch block ...
fi
```

### Pattern 7: km-slack sidecar additions (cmd/km-slack/main.go)

**`--new-message` flag on `post`:** Add to `runPost`'s `flag.FlagSet`:
```go
var newMessage bool
fs.BoolVar(&newMessage, "new-message", false, "Post as new top-level message (omits thread_ts)")
```

When `newMessage` is true, pass `thread=""` to `runWith`. The `runWith` function already passes `thread` through to `slack.BuildEnvelope`; the bridge already omits `thread_ts` from `chat.postMessage` when it is the empty string (see `pkg/slack/client.go` line 133: `if threadTS != "" { payload["thread_ts"] = threadTS }`).

The `--new-message` flag returns `ts=...` on stderr (like the existing post path — see `fmt.Fprintf(os.Stderr, "km-slack: posted ts=%s\n", resp.TS)` at line 379). The poller captures it with `grep -oE 'ts=[0-9.]+'`.

**`km-slack permalink` subcommand:** New `runPermalink` function + `"permalink"` case in `dispatch`. The bridge does NOT need a new action — `chat.getPermalink` is called directly from the sandbox-side sidecar using the bot token fetched from SSM. This requires adding a `GetPermalink` method to `pkg/slack/client.go`.

Wait — the sandbox-side km-slack currently signs envelopes and sends them to the bridge Lambda. The bridge calls Slack using its cached bot token. The sandbox itself does NOT have direct Slack API access (no bot token). Therefore `permalink` and `update` MUST go through the bridge like `post`.

**Revised approach for `permalink` and `update`:** Both need new action types on the bridge (`ActionPermalink`, `ActionUpdate`). The sandbox-side sidecar builds and signs the envelope, the bridge calls `chat.getPermalink` or `chat.update` directly with the bot token.

**Bridge extension required:**
- `pkg/slack/payload.go`: add `ActionPermalink = "permalink"` and `ActionUpdate = "update"` constants. Extend `SlackEnvelope` field validation in `Handle` to accept these two new actions.
- `pkg/slack/bridge/handler.go`: add dispatch cases for `ActionPermalink` and `ActionUpdate` that call new `SlackPoster` interface methods.
- `pkg/slack/bridge/interfaces.go`: extend `SlackPoster` with:
  ```go
  GetPermalink(ctx context.Context, channel, messageTS string) (string, error)
  UpdateMessage(ctx context.Context, channel, ts, text string) (string, error)
  ```
- `pkg/slack/client.go`: add `GetPermalink` and `UpdateMessage` methods (thin `callJSON` wrappers).
- `cmd/km-slack/main.go`: add `runPermalink` and `runUpdate` functions with `--channel`, `--ts`, `--text` flags.

**Alternative simpler design:** Since `chat.getPermalink` only needs the channel + ts (not a body-signed payload), and the Slack bot token is already in SSM accessible to the sandbox's IAM role (it IS accessible via SSM — the poller already reads `/km/slack/bridge-url` from SSM), the sidecar could call SSM for the token and call Slack directly. However, this pattern is inconsistent with how all other Slack calls work (everything goes through the bridge). The bridge-mediated approach is architecturally consistent and avoids giving the sandbox a raw Slack token.

**Recommendation:** Route `permalink` and `update` through the bridge (new `ActionPermalink` + `ActionUpdate`). This is 4 additional small files/edits but maintains the security boundary. The planner should note this is slightly more work than "thin REST wrappers" but architecturally correct.

**Bridge action permission gate:** `chat.update` modifies messages — should only be allowed for the sandbox's own channel (same ownership check as `post`). `chat.getPermalink` is read-only; the sandbox can only request a permalink for a message in its own channel.

### Pattern 8: km doctor additions (internal/app/cmd/doctor_slack.go)

**Mirrors existing `checkSlackInboundQueueExists` / `checkSlackAppEventsScopes` pattern:**

```go
// checkCodexHookConfigPresent — mirrors checkSlackInboundQueueExists.
// For each sandbox with spec.cli.agent: codex, run SSM RunCommand to verify
// ~/.codex/config.toml contains codex_hooks = true and both hook entries.
func checkCodexHookConfigPresent(
    ctx context.Context,
    listCodexSandboxes func(context.Context) ([]sandboxRef, error),
    ssmRunner SSMCommandRunner,
) CheckResult { ... }

// checkAgentTypeConsistency — for each km-slack-threads row with agent_type set,
// confirm the sandbox's profile declares the same agent.
func checkAgentTypeConsistency(
    ctx context.Context,
    listThreadRows func(context.Context) ([]threadRow, error),
    getProfile func(context.Context, string) (*profile.SandboxProfile, error),
) CheckResult { ... }
```

`listCodexSandboxes` → scan `km-sandboxes` DDB for rows where a profile attribute indicates `agent: codex`. Since DDB doesn't have a `spec_cli_agent` column, this requires either scanning all sandboxes and checking the S3 profile, or a convention where `km create` writes an `agent_type` attribute to the `km-sandboxes` row. The simpler path for Phase 70: scan all sandboxes, fetch profile from S3 via `downloadProfileFromS3`, check `profile.Spec.CLI.Agent == "codex"`. This is the same approach `checkAgentTypeConsistency` uses. Accept the higher AWS API cost; both checks are `km doctor` (not hot path).

`listThreadRows` → new helper that scans `km-slack-threads` via `Scan` (full scan; table is small in practice). Returns rows where `agent_type` attribute is present.

### Anti-Patterns to Avoid

- **Reading bot token from SSM in the sandbox-side sidecar directly.** Token belongs to the bridge. Sandbox-side calls go through signed envelopes.
- **Using `chat.update` as the primary handoff mechanism.** The 10-minute window makes it unreliable for long agent runs. Post-new-top-level-first is the safe ordering.
- **Writing `last_assistant_msg` using shell string interpolation directly into DDB JSON.** Must use `jq -Rs .` to escape embedded quotes and newlines.
- **Using `--resume` flag form for Codex resume.** Use `codex exec resume $SESSION "$PROMPT"` (subcommand form). The `--resume` flag appeared in early 2025 docs but the canonical 2026 form is the subcommand. Plan 70-00 spike verifies the installed form.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| JSON-encoding `last_assistant_msg` for DDB `put-item` CLI | Manual shell escaping | `jq -Rs .` | Handles embedded quotes, newlines, backslashes, unicode |
| Slack Web API calls from sandbox side | Direct HTTP with raw token | Signed envelope → bridge | Maintains security boundary; consistent with all other km-slack calls |
| Per-agent config file writers | Separate Go functions per agent | Single template param + unconditional heredoc | codex config.toml is always written (per CONTEXT.md locked decision) |
| Session-ID extraction via JSONL parsing | Custom JSONL line iterator | Hook-file approach | Hook fires before process exit; avoids partial-line/buffering issues |

---

## Common Pitfalls

### Pitfall 1: `chat.update` 10-Minute Window

**What goes wrong:** If you post the handoff in the old thread first (mentioning "switching to codex"), then dispatch the new agent (which may take 10+ minutes), then try to edit the handoff post to add the permalink — Slack rejects `chat.update` after 10 minutes.

**Why it happens:** Slack's edit window for bot messages is ~10 minutes (enforced server-side).

**How to avoid:** Post the new top-level FIRST (captures its `ts` and permalink immediately), then post the handoff in the old thread with the permalink already in the message body. `chat.update` becomes optional cosmetic polish.

**Warning signs:** Unit tests pass (they run in <1 second) but E2E fails on slow agent runs.

### Pitfall 2: `last_assistant_msg` DDB JSON Injection

**What goes wrong:** `RESULT_TEXT` from `output.json` often contains double quotes, backslashes, and newlines. Embedding it directly in the `aws dynamodb put-item --item '{...}'` JSON string causes parse errors or data corruption.

**Why it happens:** Shell heredoc / string substitution does not JSON-escape values.

**How to avoid:** `ESCAPED=$(echo "$RESULT_TEXT" | head -c 2000 | jq -Rs .)` produces a properly-quoted JSON string. Then `"last_assistant_msg":{\"S\":$ESCAPED}` (note: no extra quotes around `$ESCAPED`).

### Pitfall 3: Codex `sudo -u sandbox` env isolation

**What goes wrong:** `KM_CODEX_RUN_ID` is set in the root-side poller, but `sudo -u sandbox bash -lc "..."` drops non-exported shell variables. The hook file is written as root in `/tmp/`, but the Codex process (running as sandbox user) writes it — so the hook can only write to paths the sandbox user can write.

**Why it happens:** `sudo -lc` with a string argument creates a fresh environment; only exported variables and the explicit variable assignments in the string are visible.

**How to avoid:** Export `KM_CODEX_RUN_ID` before the `sudo` call, or pass it as a `VAR=val` prefix in the `sudo -u sandbox bash -lc` command string (same pattern as `export KM_SLACK_THREAD_TS='$THREAD_TS'` at line 1498). Then in the hook: the file is written to `/tmp/km-codex-session.$RUN_ID` — the hook runs as sandbox user, `/tmp` is world-writable, no permission issue.

### Pitfall 4: Bash ERE prefix regex matching mid-sentence occurrences

**What goes wrong:** An operator asks the agent "what does claude: mean?" — without the `^` anchor, the regex would match `claude:` mid-sentence.

**Why it happens:** Regex without `^` matches anywhere in the string.

**How to avoid:** The spec's regex `^([Cc]laude|[Cc]odex|CLAUDE|CODEX):[[:space:]]?` uses `^` which anchors to message start. The bash `[[ "$TEXT" =~ $REGEX ]]` check with this pattern is safe.

**Test case:** `TEXT="explain what claude: means"` should NOT match. `TEXT="claude: do something"` SHOULD match.

### Pitfall 5: `codex exec resume` syntax

**What goes wrong:** Using `codex exec --resume $SESSION "$PROMPT"` (Phase 58 flag form) instead of `codex exec resume $SESSION "$PROMPT"` (subcommand form).

**Why it happens:** Early Codex docs showed `--resume` flag; 2026 canonical form is subcommand.

**How to avoid:** Plan 70-00 spike confirms installed syntax. Use `codex --help` on the learn sandbox to verify the resume subcommand's exact syntax before writing Plan 70-05.

### Pitfall 6: Permalink response field name

**What goes wrong:** `chat.getPermalink` returns `{"ok":true,"channel":"C...","permalink":"https://..."}` — the bridge's `SlackAPIResponse` struct only has `TS` and `Channel` sub-struct. The `permalink` field needs to be captured separately.

**How to avoid:** Add a `Permalink string \`json:"permalink,omitempty"\`` field to `SlackAPIResponse` in `pkg/slack/client.go`. The existing `callJSON` generic decoder will pick it up.

### Pitfall 7: DynamoDB Scan for doctor checks

**What goes wrong:** `km doctor` scanning the full `km-slack-threads` table with `Scan` is slow if there are thousands of rows (Phase 67 operators with many threads).

**Why it happens:** No GSI on `agent_type`.

**How to avoid:** Limit the `Scan` to pages of 100 items with `Limit: 100` + pagination. Document the O(n) cost in the check's godoc. No new infrastructure needed (this is a doctor check, not a hot path). Add a `LastEvaluatedKey` loop to ensure full coverage but with page-by-page processing.

---

## Code Examples

### Verified: CLISpec field addition

```go
// pkg/profile/types.go — alongside CodexArgs (line ~394)
// Agent is the default agent for Slack inbound dispatch. One of "claude" or
// "codex"; absence or "" treated as "claude".
// Validated by the JSON Schema enum; no extra validator logic needed.
Agent string `yaml:"agent,omitempty"`
```

### Verified: NotifyEnv KM_AGENT emission

```go
// pkg/compiler/userdata.go ~line 3587
agent := "claude"
if p.Spec.CLI.Agent == "codex" {
    agent = "codex"
}
notifyEnv["KM_AGENT"] = agent
```

### Verified: km-slack `--new-message` flag

```go
// cmd/km-slack/main.go — in runPost(), flag.FlagSet definition
var newMessage bool
fs.BoolVar(&newMessage, "new-message", false, "Post as new top-level (omits thread_ts)")
// ...
threadArg := thread
if newMessage {
    threadArg = ""
}
if err := run(channel, subject, bodyPath, threadArg, renderMode); err != nil {
```

### Verified: SlackAPIResponse permalink capture

```go
// pkg/slack/client.go — extend SlackAPIResponse
type SlackAPIResponse struct {
    OK        bool   `json:"ok"`
    Error     string `json:"error,omitempty"`
    TS        string `json:"ts,omitempty"`
    Permalink string `json:"permalink,omitempty"`  // NEW — chat.getPermalink response
    // ... existing fields
}

// New GetPermalink method
func (c *Client) GetPermalink(ctx context.Context, channel, messageTS string) (string, error) {
    payload := map[string]any{
        "channel":    channel,
        "message_ts": messageTS,
    }
    resp, err := c.callJSON(ctx, "chat.getPermalink", payload)
    if err != nil {
        return "", err
    }
    return resp.Permalink, nil
}
```

### Verified: UpdateMessage method

```go
// pkg/slack/client.go
func (c *Client) UpdateMessage(ctx context.Context, channel, ts, text string) (string, error) {
    payload := map[string]any{
        "channel": channel,
        "ts":      ts,
        "text":    text,
    }
    resp, err := c.callJSON(ctx, "chat.update", payload)
    if err != nil {
        return "", err
    }
    return resp.TS, nil
}
```

### Verified: Doctor check function signature pattern

```go
// internal/app/cmd/doctor_slack.go — mirrors checkSlackInboundQueueExists signature
func checkCodexHookConfigPresent(
    ctx context.Context,
    listCodexSandboxes func(context.Context) ([]sandboxIDRef, error),
    runSSMCommand func(ctx context.Context, instanceID, cmd string) (string, error),
) CheckResult {
    name := "Codex hook config present"
    if listCodexSandboxes == nil || runSSMCommand == nil {
        return CheckResult{Name: name, Status: CheckSkipped, Message: "Codex deps not configured"}
    }
    // ...
}
```

---

## Plan 70-00 Spike Specification

The spike is a critical risk-elimination step (30 minutes, one sandbox). Exact verifier sequence:

1. On the learn sandbox (Codex already auth'd via AMI), write a minimal `~/.codex/config.toml`:
   ```toml
   [features]
   codex_hooks = true

   [[hooks.Stop]]

   [[hooks.Stop.hooks]]
   type = "command"
   command = "/bin/bash -c 'echo $KM_CODEX_RUN_ID > /tmp/km-codex-session.${KM_CODEX_RUN_ID:-test}; jq -r .session_id // empty >> /tmp/km-codex-session.${KM_CODEX_RUN_ID:-test}'"
   timeout = 30
   ```
2. Set `export KM_CODEX_RUN_ID=spike01`.
3. Run: `codex exec --json --dangerously-bypass-approvals-and-sandbox "What is 2+2?"`.
4. Verify:
   - `/tmp/km-codex-session.spike01` exists.
   - Contains a non-empty line (the `session_id`).
   - The `last_assistant_message` field is present in the hook's stdin: add `cat >> /tmp/codex-hook-payload-test` to the hook command and inspect after run.
5. Also verify `PermissionRequest` fires by running: `codex exec --json --dangerously-bypass-approvals-and-sandbox "write a file to /etc/test"` (should trigger a permission request) and confirming the hook fires (even with `--dangerously-bypass-approvals-and-sandbox`, the hook fires before approval bypass).
6. Record exact `session_id` format (UUID? opaque string?), exact `last_assistant_message` field name (vs `last_assistant_text` or similar), exact `tool_name` field name in `PermissionRequest` payload.
7. Discard the sandbox after the spike.

**Spike output contract for Plan 70-05:** The exact session-ID extraction mechanism and payload field names confirmed by the spike replace any remaining unknowns in the dispatch fork plan.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Claude-only notify hook (Phase 62/63) | Agent-agnostic hook that handles Codex `PermissionRequest` + `Stop.last_assistant_message` | Phase 70 | Codex operators get operator-notify parity |
| Claude-only inbound dispatch (`claude -p`) | Agent-aware fork: `codex exec` or `claude -p` based on `KM_AGENT` / prefix | Phase 70 | Codex operators get Slack inbound parity |
| Single-agent threads in `km-slack-threads` | Multi-agent: `agent_type` + `last_assistant_msg` on every row | Phase 70 | Cross-agent thread switching enabled |
| Slack thread = fixed agent for lifetime | Per-message prefix routing, cross-agent switch spawns new top-level | Phase 70 | Operators can switch between claude and codex mid-conversation |
| `codex exec --resume` flag form | `codex exec resume` subcommand form | Phase 58 (confirmed) | Must use subcommand form in poller dispatch |

**Deprecated/outdated:**
- `codex exec --resume $SESSION` flag form: replaced by `codex exec resume $SESSION` (subcommand). The poller MUST use subcommand form.

---

## Open Questions

1. **Does `codex exec --json` fire the Stop hook with `last_assistant_message` in the payload?**
   - What we know: Codex hook system documentation says `Stop` payload mirrors Claude's shape; SPEC.md rates this as strong-prior-probability-yes.
   - What's unclear: Whether `--json` (non-interactive) mode and interactive TUI mode both fire hooks; whether `last_assistant_message` is the exact field name.
   - Recommendation: Plan 70-00 spike confirms before any code is written. No code in Plans 70-01 through 70-05 should be written until spike result is committed.

2. **Does `PermissionRequest` fire for every tool request even with `--dangerously-bypass-approvals-and-sandbox`?**
   - What we know: SPEC.md and CONTEXT.md both assume the hook fires. The flag bypasses Codex's approval UI but documentation does not explicitly confirm hook firing.
   - Recommendation: Spike verifies this explicitly (step 5 of spike sequence above).

3. **Is `KM_CODEX_RUN_ID` visible inside the hook subprocess when exported from the poller?**
   - What we know: `sudo -u sandbox bash -lc` creates a fresh env; the poller currently uses `export KM_SLACK_THREAD_TS='$THREAD_TS'` inside the command string (line 1498). Same pattern should work for `KM_CODEX_RUN_ID`.
   - Recommendation: Confirm during spike (step 4 above).

4. **Does `km-slack post` currently write `ts=...` to stderr or stdout?**
   - What we know: `fmt.Fprintf(os.Stderr, "km-slack: posted ts=%s\n", resp.TS)` at line 379. The poller must capture stderr (or make `--new-message` write to stdout). The existing inbound poller uses `km-slack post ... 2>>"$RUN_DIR/stderr.log"` which discards stderr to a file. For `--new-message`, the ts must be captured.
   - Recommendation: Change `--new-message` path to print `ts=...` to stdout (not stderr), or pipe `2>&1` in the capture call. The planner picks; stdout is cleaner. This is a small design choice for Plan 70-04.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (built-in) |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./pkg/compiler/... ./cmd/km-slack/... ./pkg/slack/... ./internal/app/cmd/... -run TestPhase70 -v` (once tests are named) |
| Full suite command | `go test ./...` |

### Success Criteria → Test Map

| SC# | Behavior | Test Type | Automated Command | File |
|-----|----------|-----------|-------------------|------|
| SC-01 | `~/.codex/config.toml` present with correct content after `km create` | Compiler snapshot | `go test ./pkg/compiler/... -run TestUserdata_CodexConfigToml` | `pkg/compiler/userdata_codex_test.go` — Wave 0 gap |
| SC-01 | `KM_AGENT` emitted in notify.env | Compiler unit | `go test ./pkg/compiler/... -run TestUserDataNotifyEnv_AgentCodex` | `pkg/compiler/userdata_notify_test.go` — extend existing |
| SC-02 | `Stop` hook uses `last_assistant_message` field when present | Hook script smoke | `go test ./pkg/compiler/... -run TestNotifyHookStop_LastAssistantMessage` | `pkg/compiler/notify_hook_script_test.go` — Wave 0 gap |
| SC-02 | `Stop` hook falls back to transcript-tail when field absent | Hook script smoke | `go test ./pkg/compiler/... -run TestNotifyHookStop_TranscriptFallback` | existing pattern, extend |
| SC-03 | `PermissionRequest` fires hook, exits 0, no stdout | Hook script smoke | `go test ./pkg/compiler/... -run TestNotifyHookPermissionRequest` | `pkg/compiler/notify_hook_script_test.go` — Wave 0 gap |
| SC-04 | Poller writes `agent_type=codex` + session ID to DDB on first turn | Poller script unit (bash-in-Go) | `go test ./pkg/compiler/... -run TestPollerDispatch_CodexFirstTurn` | `pkg/compiler/userdata_slack_inbound_test.go` — Wave 0 gap |
| SC-05 | Poller uses `codex exec resume` for follow-up turns | Poller script unit | `go test ./pkg/compiler/... -run TestPollerDispatch_CodexResume` | same file |
| SC-06 | Stop hook Slack branch silent when `KM_SLACK_THREAD_TS` set (Codex path) | Hook script smoke | `go test ./pkg/compiler/... -run TestNotifyHookStop_SlackSuppressedWhenThreadTSSet` | existing test, verify Codex payload triggers same path |
| SC-07 | `km doctor` checks green on codex sandbox; no false positive on claude sandbox | Doctor unit | `go test ./internal/app/cmd/... -run TestCheckCodexHookConfigPresent` | `internal/app/cmd/doctor_slack_test.go` — Wave 0 gap |
| SC-08 | Prefix `codex:` on claude-default profile dispatches codex, new DDB row | Poller script unit | `go test ./pkg/compiler/... -run TestPollerPrefix_CodexOnClaudeProfile` | `pkg/compiler/userdata_slack_inbound_test.go` — Wave 0 gap |
| SC-09 | Same-agent prefix is no-op (no new thread, no handoff) | Poller script unit | `go test ./pkg/compiler/... -run TestPollerPrefix_SameAgentNoOp` | same file |
| SC-10 | Cross-agent switch: new top-level, handoff post, seeded prompt, two DDB rows | E2E UAT (Plan 70-09) | Manual — real EC2 sandboxes | `70-VERIFY.md` |
| SC-01 (km-slack) | `--new-message` omits thread_ts | km-slack unit | `go test ./cmd/km-slack/... -run TestRunPost_NewMessage` | `cmd/km-slack/main_test.go` — Wave 0 gap |
| SC-01 (km-slack) | `permalink` subcommand calls `chat.getPermalink` | km-slack unit | `go test ./cmd/km-slack/... -run TestRunPermalink` | `cmd/km-slack/main_test.go` — Wave 0 gap |
| SC-01 (km-slack) | `update` subcommand calls `chat.update` | km-slack unit | `go test ./cmd/km-slack/... -run TestRunUpdate` | `cmd/km-slack/main_test.go` — Wave 0 gap |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... ./cmd/km-slack/... -run TestPhase70 -count=1`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/compiler/userdata_codex_test.go` — SC-01 compiler test for `config.toml` emission
- [ ] `pkg/compiler/notify_hook_script_test.go` additions — SC-02 `last_assistant_message` fast-path, SC-03 `PermissionRequest` branch
- [ ] `pkg/compiler/userdata_slack_inbound_test.go` additions — SC-04/05/08/09 poller dispatch fork tests
- [ ] `cmd/km-slack/main_test.go` additions — `--new-message`, `permalink`, `update` surface tests
- [ ] `internal/app/cmd/doctor_slack_test.go` additions — SC-07 `codex_hook_config_present` + `agent_type_consistency`
- [ ] No new framework installs needed — existing `go test` infrastructure covers all cases.

*(SC-10 cross-agent switch is manual-only E2E UAT — requires live Slack workspace and real EC2 sandboxes.)*

---

## Sources

### Primary (HIGH confidence — verified against source code)
- `pkg/compiler/userdata.go` lines 402-850 (km-notify-hook heredoc — verified full shape)
- `pkg/compiler/userdata.go` lines 1278-1585 (km-slack-inbound-poller heredoc — verified full shape)
- `pkg/compiler/userdata.go` lines 3573-3670 (NotifyEnv compiler logic — verified)
- `pkg/profile/types.go` lines 377-480 (CLISpec struct — verified field locations)
- `pkg/profile/schemas/sandbox_profile.schema.json` lines 510-580 (cli.properties — verified)
- `cmd/km-slack/main.go` lines 1-400 (full sidecar source — verified dispatch, runPost, runWith)
- `pkg/slack/client.go` lines 1-250 (Client + callJSON + PostMessage — verified)
- `pkg/slack/payload.go` lines 1-100 (SlackEnvelope + actions — verified)
- `pkg/slack/bridge/handler.go` lines 1-340 (Handler.Handle + dispatch — verified)
- `pkg/slack/bridge/interfaces.go` (SlackPoster + BlockPoster — verified)
- `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` (DDB table schema — verified schemaless)
- `internal/app/cmd/doctor_slack.go` lines 200-510 (existing check patterns — verified)
- `internal/app/cmd/agent.go` lines 1260-1310 (BuildAgentShellCommands — verified Codex dispatch form)

### Secondary (MEDIUM confidence)
- SPEC.md (full design narrative including Codex hook payload shape — verified against Codex CLI 2026 documentation via SPEC.md's own research notes)
- CONTEXT.md (locked decisions — authoritative for Phase 70 implementation contracts)

### Tertiary (LOW confidence)
- Codex `--dangerously-bypass-approvals-and-sandbox` still hooks fire: strong prior, not yet code-confirmed (confirmed by Plan 70-00 spike)
- `chat.getPermalink` rate limits: Slack Tier-3 API (50 req/min); not a concern at operator interaction frequency (LOW confidence on exact tier, but near-zero risk at expected volume)

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries are in-tree; no new external dependencies
- Architecture: HIGH — all integration points verified against source code
- Codex hook payload shape: MEDIUM — spike required; strong prior from SPEC.md's research
- Pitfalls: HIGH — verified from existing code patterns and known gaps documented in SPEC.md

**Research date:** 2026-05-22
**Valid until:** 2026-06-22 (stable monorepo; Codex hook API could change — check Codex changelog before planning if more than 30 days elapse)

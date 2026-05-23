# Phase 70 — Codex parity for Phases 62/63/67 (Tier 2: notify + Slack inbound)

**Status:** draft (pre-plan). Hand to `/gsd:plan-phase` once the operator approves the brief.
**Author:** brainstormed 2026-05-05; extended 2026-05-22 with prefix routing + cross-agent thread switching.
**Depends on:** Phase 58 (Codex CLI agent-run support + `spec.cli.codexArgs`), Phase 62 (operator-notify hook script), Phase 63 (Slack notify hook + km-slack sidecar + bridge Lambda), Phase 67 (Slack inbound poller + DDB threads table)

## Notation

`spec.cli.agent` is the new profile field; `KM_AGENT` is the runtime env var compiled from it. `~/.codex/config.toml` is the per-sandbox Codex hook configuration file written by `km create`. "Tier 2" refers to the scope chosen in brainstorming on 2026-05-05 — operator email + Slack notify + Slack inbound dispatcher parity. Tier 3 (transcript streaming, Phase 68 parity) is explicitly deferred.

A **prefix** is the leading `claude:` / `codex:` token (case-insensitive, with one optional trailing space) on a Slack inbound message; it selects the agent for that turn and may trigger a cross-agent **thread switch**. A **thread switch** spawns a fresh top-level message in the same `#sb-{id}` channel, with a "Switching to {agent} → continuing in this thread." handoff post (containing a Slack permalink) in the originating thread. Each Slack thread maps 1:1 to an agent session row in `km-slack-threads`, keyed by `(channel_id, thread_ts)`; switching writes a new row, leaving the old row resumable.

## Goal

A SandboxProfile can declare `spec.cli.agent: codex` and the resulting sandbox gets the same operator-notify (Phase 62) and Slack notify (Phase 63) experience that Claude sandboxes get today, plus working Slack inbound bidirectional chat (Phase 67) — including multi-turn session resume — driven by Codex CLI instead of Claude Code. The hook plumbing is unified: a single `km-notify-hook` script handles both agents' payloads. The DDB schema gains an `agent_type` attribute so the same `km-slack-threads` row can carry either a Claude or a Codex session.

Beyond agent selection, the Slack inbound dispatcher learns to **per-message prefix routing**: a Slack message starting with `claude:` or `codex:` routes the turn to the named agent, overriding the profile default. Inside an existing thread, a prefix that names the *other* agent triggers a clean handoff — the bot posts a "Switching to {agent} → continuing in this thread." message (with a Slack permalink) in the originating thread, spawns a new top-level thread in the same channel, and seeds the new agent with the prior agent's last assistant message. Threads remain 1:1 with sessions; agents stay siloed; operators can hop between Claude and Codex inside one Slack channel without changing the profile.

Phase 68 (per-turn transcript streaming + final JSONL upload) is intentionally left for a follow-up phase.

## Why

Phase 58 shipped `km agent run --codex` for one-shot non-interactive Codex execution. Everything the platform built around Claude Code — operator notifications when Claude pauses for permission or completes an idle turn, parallel Slack delivery of those same events, bidirectional Slack chat that drives `claude -p` at SQS dispatch — is currently Claude-exclusive. Operators who pick Codex (because of OpenAI model fit, billing, or organizational tooling preferences) get a stripped-down sandbox that runs but doesn't surface its activity through any of the same channels.

Crucially, Codex CLI now ships a hook system (stable since 2026) that mirrors Claude Code's almost field-for-field: same configuration shape (TOML/JSON files in `~/.codex/`), same stdin payload (`session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`), same exit-code semantics for blocking, even the same `Stop` event name. The previous concern that parity would require a wrapper sidecar parsing `codex exec --json` JSONL stdout is gone. Tier 2 parity becomes mostly a config-writer + a small bash branch + a poller dispatch fork.

## Success criteria

A reviewer can verify each as TRUE end-to-end on a real EC2 sandbox.

1. A profile with `spec.cli.agent: codex` and the existing notify/Slack toggles enabled, after `km create`, contains both `~/.claude/settings.json` (already true) AND a fresh `~/.codex/config.toml` with `[features] codex_hooks = true` and command hooks for `PermissionRequest` and `Stop` pointing at `/opt/km/bin/km-notify-hook`. No `PostToolUse` entry is registered (Tier 2 explicitly defers transcript streaming).
2. `km agent run --codex --prompt "What model are you?"` against that sandbox emits a `Stop` event whose payload contains `last_assistant_message`. The notify hook reads that field directly, sends an operator email, and posts the same body to the Slack channel — with no transcript-tailing fallback path executed.
3. A Codex `PermissionRequest` event fires the notify hook, the hook sends an operator + Slack ping containing the requested tool name, the hook exits 0 with no JSON body, and Codex's `--dangerously-bypass-approvals-and-sandbox` flag continues to auto-approve so the agent run completes.
4. An operator types a message in `#sb-{sandbox-id}` Slack against an inbound-enabled sandbox with `agent: codex`. The poller dispatches `codex exec --json --dangerously-bypass-approvals-and-sandbox "<prompt>"`, captures the resulting session ID, and writes a DDB row to `km-slack-threads` with `agent_type=codex` and the session ID stored in `claude_session_id` (column-name hangover documented).
5. A follow-up Slack message in the same thread causes the poller to dispatch `codex exec resume <session-id> "<new prompt>"`, with the session ID read from DDB. The conversation continues in Codex's session context (resumed transcript, plan history, approvals).
6. Stop hook gating on `KM_SLACK_THREAD_TS` works for Codex: when the env var is set (poller-driven run), the Stop hook's Slack branch stays silent; the poller posts the assistant reply via km-slack. When the env var is not set (operator's `km agent run --codex`), the Stop hook posts as usual.
7. `km doctor` runs `codex_hook_config_present` and `agent_type_consistency` checks green on a sandbox using `agent: codex` and yields no false positives on a Claude-default sandbox.
8. **Top-level prefix routing.** On a sandbox where `spec.cli.agent: claude` (profile default), operator posts a new top-level message `codex: list workspace files` in `#sb-x`. The poller strips the prefix, dispatches `codex exec --json --dangerously-bypass-approvals-and-sandbox "list workspace files"`, and writes a new `km-slack-threads` row keyed on the new top-level `thread_ts` with `agent_type=codex`. Codex's reply lands in that thread. The profile's compiled `KM_AGENT` env var is unchanged on disk; the override is per-thread only. The symmetric case `claude: ...` on a `agent: codex` profile produces the same shape with agents reversed.
9. **Same-agent prefix is a no-op switch.** Inside an existing claude-rooted thread, operator posts `claude: do another thing`. The poller strips the prefix, dispatches `claude -p --resume <session> "do another thing"`, and the reply lands in the same thread. No new thread, no new DDB row, no handoff post. The symmetric case for codex behaves identically.
10. **Cross-agent mid-thread switch.** Inside a running claude thread (DDB row has `agent_type=claude`, `claude_session_id=<id>`, `last_assistant_msg` cached from the prior turn), operator posts `codex: check this`. The bot posts in the old thread: `Switching to codex → continuing in this thread.` followed by a Slack permalink to the new thread. A new top-level message appears in `#sb-x` containing `Continuing from <permalink-to-old-thread>` plus a truncated excerpt of the prior claude assistant message; Codex's reply to "check this" (seeded with the prior assistant message as context) posts as the first thread reply. DDB has two rows after the switch: the original claude row (unchanged, still resumable) and a new codex row keyed on the new `thread_ts`. The old claude session/process is NOT killed.

## Approach

### Profile schema additions

New field under `spec.cli`:

```yaml
spec:
  cli:
    agent: claude | codex          # default claude; absence ≡ claude
    codexArgs: [...]                # Phase 58, unchanged
    claudeArgs: [...]               # unchanged
    notifyEmailEnabled: true|false  # Phase 62, unchanged (agent-agnostic)
    notifySlackEnabled: true|false  # Phase 63, unchanged
    notifySlackPerSandbox: true|false
    notifySlackInboundEnabled: true|false
    notifyOnPermission: true|false
    notifyOnIdle: true|false
    # ... existing notify/Slack fields all stay agent-agnostic
```

Validation rules:
- `agent` enum: `claude`, `codex`. Absence ≡ `claude`. Any other value is a validation error.
- No new cross-field rules. The existing inbound/notify combinatorics already validate.

Schema lives in `pkg/profile/types.go` (new field on `CLISpec`) and the embedded `schemas/sandbox_profile.schema.json`.

### Compiler: write both config files; emit `KM_AGENT` env var

`pkg/compiler/userdata.go` already writes `~/.claude/settings.json` (Phase 62/63/67/68). Extend it to also write `~/.codex/config.toml` for every sandbox, regardless of `spec.cli.agent` value:

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

Decisions baked in:
- **No `PostToolUse` entry** in Codex config under Tier 2. Phase 68 parity adds it later. The Codex CLI fires the hook on every tool use; we're not paying that fork+exec cost for nothing.
- **`codex_hooks` feature flag** is set unconditionally because the file is provisioned at create time, not feature-flag-gated at runtime. Operators who don't use Codex never see the file's effects (their agent never starts Codex).
- **`matcher = ".*"`** on `PermissionRequest` because the platform doesn't differentiate by tool — all permission requests trigger the same operator ping. PostToolUse-style fine-grained matchers are a Phase 68+ concern.

A new env var `KM_AGENT` (values `claude` | `codex`, sourced from `spec.cli.agent`) is added to `/etc/profile.d/km-notify-env.sh` AND the systemd-friendly `/etc/km/notify.env` (the dual-file pattern from Phase 67's userdata gotcha).

`km init --sidecars` is required after this phase ships so management Lambdas pick up the schema addition.

### km-notify-hook becomes agent-aware

The bash script at `/opt/km/bin/km-notify-hook` (inlined in userdata.go via heredoc, lines ~402–491) gains two small branches:

- **`PermissionRequest` event** (Codex-only): treated as a synonym of Claude's `Notification`. Reuse the existing "needs permission" path verbatim — extract the tool name from `tool_name` field, compose the same email/Slack body shape, exit 0 with no stdout body so Codex's `--dangerously-bypass-approvals-and-sandbox` continues to auto-approve.
- **`Stop` event** payload-shape detection: Codex's `Stop` payload ships `last_assistant_message` directly; Claude's payload requires tail-parsing the JSONL transcript at `transcript_path`. Hook prefers `last_assistant_message` when present; falls back to existing transcript-tail logic when absent. This makes the Codex idle path strictly simpler than Claude.

The script remains a single bash file. Existing cooldown logic, env-var gates (`KM_NOTIFY_ON_PERMISSION`, `KM_NOTIFY_ON_IDLE`, `KM_NOTIFY_COOLDOWN_SECONDS`), and downstream invocations (`km-send`, `km-slack post`) are agent-agnostic and reused as-is. Approximately +30 lines under `if [[ "$1" == "PermissionRequest" ]]` and a `last_assistant_message` fallback in the existing `Stop` clause.

### Phase 67 Slack inbound poller: dispatch fork + session-ID handling

The systemd-managed `km-slack-inbound-poller` (lines ~1150–1400 of userdata.go) gains:

1. **Boot-time read** of `KM_AGENT` from the env file.
2. **DDB lookup** at message receipt: row may carry `agent_type` attribute. Default `claude` for backward compat with rows written before this phase.
3. **Dispatch builder branches:**
   - `agent: claude` (existing path, unchanged):
     ```
     claude -p "$PROMPT" --output-format json --dangerously-skip-permissions [--resume $SESSION]
     ```
   - `agent: codex` (new):
     - First turn: `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT" > $OUT_FILE`
     - Resume: `codex exec resume $SESSION "$PROMPT" --json --dangerously-bypass-approvals-and-sandbox > $OUT_FILE`
4. **Session-ID extraction** post-run:
   - claude path (existing): parse `output.json` (single JSON document).
   - codex path (new): the `Stop` hook fires before the dispatch process exits. The hook can write the session ID to a known per-run file (e.g., `/tmp/km-codex-session.$RUN_ID`) before exiting, OR the poller can tail the JSONL stream for the last `session_id`-bearing event. Pick one at planning time after a tiny spike — the hook-file approach is more robust because it avoids JSONL parsing edge cases.
5. **DDB writeback:** always set `agent_type` on writes; store the session ID in `claude_session_id` (column-name hangover, documented). `last_turn_ts`, `ttl_expiry` (30 days) carry forward.
6. **`KM_SLACK_THREAD_TS` injection** into the agent process: works for both — already generic, no changes.

### DDB schema: `agent_type` + `last_assistant_msg` attributes on `km-slack-threads`

Two additive attributes:

- **`agent_type`** (S, values `claude` | `codex`). DynamoDB is schemaless, so the change is purely on the writer/reader sides:
  - **Writer** (poller, after a successful turn): always sets `agent_type` matching the agent that ran the turn.
  - **Reader** (poller, on inbound): if `agent_type` attribute is absent (rows pre-dating this phase), default to `claude`.
- **`last_assistant_msg`** (S, optional, truncated to ~2000 chars). Written after every successful turn by the poller using the same source the Stop hook reads (`last_assistant_message` for Codex; transcript tail / poller-extracted `.result` for Claude). Used by the cross-agent switch path to seed the new agent's first turn with context. Absent on pre-existing rows; missing → switching path falls back to "no context" (still functional, less helpful).

The `claude_session_id` attribute is reused as the agent-agnostic session-ID column. The column-name hangover is documented in CLAUDE.md and `docs/slack-notifications.md`. Future agents (Goose, etc.) slot in as additional `agent_type` values without further schema work. No migration job, no rename. Both new attributes inherit the existing 30-day TTL via `ttl_expiry`.

The `km-slack-stream-messages` table (Phase 68's deferred-for-Tier-2 surface) is unchanged in this phase. When Phase 68 parity lands, it gets the same `agent_type` treatment.

### Slack prefix routing & mid-thread agent switching

The Slack inbound poller gains a small parser front-end that runs before the dispatch fork. All of this lives in the same systemd-managed bash poller as the dispatch fork; no new sidecar.

**Prefix grammar.** Match the regex `^([Cc]laude|[Cc]odex|CLAUDE|CODEX):[[:space:]]?` against the inbound message body (post-Slack-mention-strip; post-bridge-dispatch). Case-insensitive on the agent name; exactly one optional space allowed after the colon; nothing else is tolerated (leading whitespace before the prefix → no match; `claude :` with space before colon → no match). If matched, capture:

- `requested_agent` = the named agent (`claude` or `codex`, lowercased).
- `stripped_prompt` = the message body with the prefix and the optional trailing space removed.

If no match, `requested_agent` is empty and `stripped_prompt` is the full body.

**Per-thread state.** Each Slack thread maps to exactly one agent — the agent that ran the *first* turn in that thread. The poller looks up the DDB row by `(channel_id, thread_ts)`:

- **Row exists** → `current_agent` = `agent_type` from the row (default `claude` if attribute absent). `current_session_id` = `claude_session_id`.
- **Row absent** (first turn in this thread) → `current_agent` = `requested_agent` if a prefix was matched, else the profile's `KM_AGENT`. `current_session_id` is empty (the upcoming dispatch is a fresh session for this thread).

**Routing decision tree.**

```
requested_agent matched?
├─ no → dispatch to `current_agent`, resume `current_session_id` if set
├─ yes, requested_agent == current_agent → strip + dispatch as above (no-op switch)
└─ yes, requested_agent != current_agent → cross-agent switch (see below)
```

**Cross-agent switch sequence** (in the originating thread):

1. **Read context.** Load `last_assistant_msg` from the existing DDB row. Truncate to ~500 chars for the new-thread excerpt; keep the full 2000-char version for seeding the new agent.
2. **Post handoff in old thread** via `km-slack post` (existing sidecar): body `Switching to {new_agent} → continuing in this thread.` *Footnote:* the permalink for the new thread is not yet known at this point — see ordering below.
3. **Spawn new top-level message** via `km-slack post --new-message` (a new flag on the sidecar; today everything is thread-replied). Body:
   ```
   Continuing from <permalink-to-old-thread>

   Previous assistant ({old_agent}) said:
   > {truncated last_assistant_msg}
   ```
   Capture the response's `ts` field → this is the new `thread_ts`.
4. **Update the old-thread handoff post** to include the new thread permalink. Approaches:
   - **Edit the old-thread message via `chat.update`** (preferred) — Slack API allows the bot to amend its own messages within 10 minutes.
   - **Or, post a second message** with just the permalink (avoids the edit dependency, slightly noisier).

   Planner picks at slice time; prefer edit for cleaner UX.
5. **Compose the new agent's prompt** by concatenating:
   ```
   {stripped_prompt}

   --- Context from prior thread (agent: {old_agent}) ---
   {full last_assistant_msg, up to 2000 chars}
   ```
6. **Dispatch the new agent** as a *first turn* (no `--resume` / no session ID) with the composed prompt.
7. **Write a new DDB row** keyed on `(channel_id, new_thread_ts)` with `agent_type={new_agent}`, the new session ID extracted post-run, and the new `last_assistant_msg` after the agent completes.
8. **Old DDB row is unchanged.** The original agent's session is still resumable: if the operator replies in the old thread (with or without prefix), the poller picks up where it left off.

**Failure modes.**

- **Permalink retrieval fails** (Slack API hiccup): post the handoff and new thread anyway; the permalink reference becomes a literal "(unavailable)" placeholder. Log to journald. The new thread still works; the cross-link is just degraded.
- **`km-slack-threads` row write fails after agent dispatch**: the new agent ran, but no row exists. On the next reply in the new thread, the poller treats it as a first turn (no row → fresh `current_agent` from `KM_AGENT` or new prefix). This degrades to "two-turn-loss of continuity" — acceptable failure mode for a P2 race; mitigated by single-row writes (no multi-attribute consistency).
- **Operator posts in old thread after a switch**: works fine. Old DDB row resumes the old agent's session. The two threads run in parallel.
- **`last_assistant_msg` attribute absent on old row** (pre-existing row from before this phase): switch still proceeds; the new agent gets no context, only the stripped prompt. New rows always carry the attribute going forward.

**`km-slack` sidecar additions.** New flag `--new-message` on `km-slack post` that omits `thread_ts` from the `chat.postMessage` call and returns the new message's `ts` to stdout. New subcommand `km-slack permalink --channel X --ts Y` wrapping `chat.getPermalink`. New subcommand `km-slack update --channel X --ts Y --text Z` wrapping `chat.update`. All three are thin REST wrappers; no business logic.

**No schema changes for prefix routing.** `spec.cli.agent` remains the profile default; prefix routing is purely runtime poller behavior. Phase 70's profile additions are unchanged.

### km doctor checks

Two new checks under the existing doctor framework, mirroring the Phase 67 inbound checks (`slack_inbound_queue_exists`, `slack_inbound_stale_queues`):

- **`codex_hook_config_present`** — for each running sandbox with `spec.cli.agent: codex`, SSM-RunCommand check confirming `~/.codex/config.toml` exists, contains `codex_hooks = true`, and the two expected hook entries (`PermissionRequest`, `Stop`) point at `/opt/km/bin/km-notify-hook`. WARN on drift (operator may have hand-edited the file).
- **`agent_type_consistency`** — for each row in `km-slack-threads`, if `agent_type` is set, confirm the corresponding sandbox's profile (resolved via `sandbox_id` attribute → S3 profile fetch using the existing `downloadProfileFromS3` helper from `internal/app/cmd/destroy.go`) still declares the same agent. WARN on drift; this catches profiles that flipped from `agent: claude` to `agent: codex` (or vice versa) without recreating affected sandboxes.

Both checks honor `--all-regions` and follow the Phase 67/68 doctor-check conventions.

### Operator-side CLI: no new commands

`km agent run --claude` / `--codex` flags (Phase 58) are unchanged. They still override per-invocation. The new `spec.cli.agent` field sets the *default* — the value the poller uses for inbound dispatch and the value `km agent run` uses when no flag is passed. Existing `km shell` similarly defaults to the profile's agent unless `--claude` / `--codex` is passed.

`km validate` adds the small enum check for `agent`. No other CLI surface changes.

## Out of scope

- **Phase 68 transcript streaming for Codex.** Tier 3 work — separate phase. Requires Codex JSONL transcript schema parsing (different from Claude's), and Codex `PostToolUse` hook registration. The schema additions in this phase are forward-compatible: when Tier 3 lands, it adds a third `[[hooks.PostToolUse]]` entry to the same `~/.codex/config.toml` and extends the existing `km-slack-stream-messages` table with `agent_type`.
- **Slack-driven approve/deny on Codex `PermissionRequest`.** Would require Slack interactivity webhook into the bridge Lambda, a long-lived hook process awaiting button-click responses, and timeout/fallback semantics. Genuine ops feature; phase of its own. Note: once it exists for Codex, it should backport to Claude via Slack-inbound-driven message responses (Claude's `Notification` is non-blocking, but a "reply with /approve to grant the permission" workflow would be feasible).
- **Goose / other-agent parity.** The `agent_type` attribute is now extensible — adding Goose later means a new enum value, a new config-file template, and a new dispatch branch in the poller. Schema doesn't change.
- **Migrating existing `claude_session_id` column to `agent_session_id`.** Cosmetic; documented hangover; no plan to migrate.
- **Hot-reload of `spec.cli.agent`.** Profile changes require `km destroy` + `km create`, matching the existing pattern for every other profile field. The poller reads `KM_AGENT` once at boot.
- **Codex CLI install / version pinning.** Phase 58 already handles this in the AMI bake.
- **Auto-routing by content heuristics.** No "this question looks like code → route to codex" classifier. Routing is operator-explicit (profile default or prefix).
- **Thread merge / rejoin after switch.** No mechanism to fold a switched thread back into its origin. The two threads run independently after the switch.
- **Carrying tool, file, or working-directory state between agents.** Only the last assistant message text travels. Each agent's `cwd`, MCP servers, approved tools, and session transcript are isolated.
- **More than two agents on prefix routing.** Goose / other-agent prefixes are deferred until those agents have first-turn parity. The dispatch fork and prefix matcher are written so a third option is a one-branch addition, but Phase 70 ships claude + codex only.
- **Prefix in `km agent run --prompt`.** The `--prompt` content is plumbed verbatim to the chosen agent (selected by `--claude`/`--codex` flags); the poller's prefix parser does not run on operator-driven invocations. Operators who want the equivalent of prefix routing on the CLI use the existing flags.
- **Slack "thread broadcast" toggle** as an alternative to spawning a new top-level message. The new-top-level pattern is locked.

## Demo storyboard

For a Phase 70 readout, run these flows in sequence on a single sandbox profile with `spec.cli.agent: codex`, `notifySlackEnabled: true`, `notifySlackPerSandbox: true`, `notifySlackInboundEnabled: true`, `notifyOnPermission: true`, `notifyOnIdle: true`:

1. **Provisioning.** `km create profile.yaml` → sandbox boots. Show both files exist on the sandbox: `cat ~/.claude/settings.json && cat ~/.codex/config.toml`. Both have hook entries pointing at `/opt/km/bin/km-notify-hook`.
2. **Operator-side Codex run.** `km agent run --codex --prompt "What model are you?"` from operator workstation. Codex runs to completion. Operator email arrives with subject `[sb-x] idle` and body containing the assistant's reply (read from `last_assistant_message`). Slack channel `#sb-x` shows the same body.
3. **Permission notification.** A Codex turn triggers a `PermissionRequest` (e.g., agent attempts `apply_patch` outside cwd). Operator email + Slack ping fire ("Codex is requesting permission for apply_patch"). Codex auto-approves due to `--dangerously-bypass-approvals-and-sandbox`. Run completes.
4. **Slack inbound, first turn.** Operator types "list workspace files" in `#sb-x`. Poller dispatches `codex exec --json --dangerously-bypass-approvals-and-sandbox "list workspace files"`, captures session ID, writes DDB row with `agent_type=codex`. Codex's reply posts to the same Slack thread.
5. **Slack inbound, multi-turn resume.** Operator follows up in the same thread: "now show README.md". Poller reads DDB → finds `agent_type=codex` + session ID. Dispatches `codex exec resume <id> "now show README.md" --json --dangerously-bypass-approvals-and-sandbox`. Codex resumes (its transcript and approvals carry forward). Reply posts to the same thread. DDB row updated with new `last_turn_ts`.
6. **`km doctor`.** Run `km doctor`. Both new checks return green: `codex_hook_config_present` confirms the on-sandbox config, `agent_type_consistency` confirms the DDB row matches the profile.
7. **Prefix routes a fresh top-level thread.** On a *second* sandbox where `spec.cli.agent: claude` (the platform default), operator posts new top-level `codex: list workspace files` in its `#sb-y` channel. Codex thread spawns; codex reply lands; DDB row carries `agent_type=codex` even though the profile is claude-default. Operator posts a follow-up reply in that thread (no prefix); codex resumes with its session ID.
8. **Same-agent prefix is invisible.** On the codex-default sandbox `#sb-x` thread, operator posts `codex: another question` mid-thread. Same thread, same session, response posts inline. No handoff, no new thread.
9. **Cross-agent switch with handoff.** Inside an active claude thread on `#sb-y`, operator posts `codex: check the code change`. Old thread receives `Switching to codex → continuing in this thread.` followed by (or amended to include) a permalink to a brand-new top-level message. The new message reads `Continuing from <permalink>\n\nPrevious assistant (claude) said:\n> {excerpt}`. Codex's reply to "check the code change" — seeded with the prior assistant message as context — appears as the first thread reply. DDB now has two rows for `#sb-y`. Operator replies in the **old** thread without a prefix: Claude resumes its prior session (proves the old session was not killed).

## Implementation slice estimate

Roughly 9-11 plans. The planner should refine.

1. **Spike: confirm `codex exec --json` fires hooks.** ~30-min Plan 70-00. Write a minimal `~/.codex/config.toml` with a Stop hook that touches `/tmp/codex-hook-fired` on a learn-derived sandbox with Codex auth'd. Run `codex exec --json "hello"`. Confirm the file appears and `last_assistant_message` is in the payload. Discard the sandbox.
2. **Schema + validation.** `pkg/profile/types.go` (new `Agent string` field on `CLISpec`), embedded JSON Schema, validator unit tests.
3. **Compiler config-file writer.** `pkg/compiler/userdata.go` writes `~/.codex/config.toml` for every sandbox; emits `KM_AGENT` to both env files. Depends on (2).
4. **km-notify-hook agent-aware branches.** `pkg/compiler/userdata.go` heredoc edit for the `PermissionRequest` branch and `last_assistant_message` fallback in the `Stop` branch. ~30 LOC. Independent of (3).
5. **km-slack sidecar: `--new-message`, `permalink`, `update` surface.** New flags/subcommands in the `sidecars/km-slack` Go binary. Pure REST wrappers; no business logic. Independent of (3), (4); blocks (7).
6. **Phase 67 poller dispatch fork + DDB attribute writeback.** `pkg/compiler/userdata.go` poller-script section. Codex session-ID extraction via hook-file approach (the spike from slice 1 informs the exact mechanism). DDB writer always sets `agent_type` and `last_assistant_msg`; reader defaults `agent_type=claude` on absence. Depends on (3).
7. **Slack prefix routing & cross-agent switch.** New section of the poller script: prefix parser, per-thread state lookup (with `last_assistant_msg` retrieval), switch sequence orchestrating handoff post + new top-level + `chat.update` of the handoff with the new permalink + seeded first-turn dispatch + new DDB row write. Uses the km-slack flags from (5). Depends on (5) and (6).
8. **km doctor checks.** Two new checks in `internal/app/cmd/doctor*.go`: `codex_hook_config_present`, `agent_type_consistency`. Independent of (6), (7).
9. **Documentation.** `docs/codex-parity.md` (new operator guide with the prefix routing + switching examples); CLAUDE.md additions noting `agent: codex` and the `claude:` / `codex:` prefix; `docs/slack-notifications.md` adds a "Prefix routing & agent switching" section with the demo storyboard's flows.
10. **End-to-end manual UAT.** The nine demo flows on real EC2 sandboxes (at least one `agent: claude` and one `agent: codex`, both with `notifySlackInboundEnabled: true`). UAT uses the operator's existing learn-derived sandbox (Codex already auth'd) for the Codex side; a fresh claude sandbox for the claude side and for cross-agent switch flows. Captured in `70-VERIFY.md`.

The eBPF surface from Phase 69 is unrelated; this phase touches the userdata template, profile schema, the km-slack sidecar, and the `internal/app/cmd` doctor framework only.

## Risks & open research items

- **Does `codex exec --json` (non-interactive) fire hooks identically to the interactive TUI?** Documentation doesn't differentiate — strong prior probability yes (the OpenAI engineering issue thread treats hooks as a CLI-wide feature), but the Plan 70-00 spike (first slice in the implementation estimate) eliminates the entire phase's risk surface before any code is written. Spike runbook: write a minimal `~/.codex/config.toml` with a Stop hook that touches `/tmp/codex-hook-fired`, run `codex exec --json "hello"`, confirm the file appears and `last_assistant_message` is in the payload. Single learn-derived sandbox, ~30 minutes.
- **Codex resume syntax form.** Web-confirmed `codex exec resume <id> "<prompt>"` (subcommand form, not `--resume` flag). The 2024-era `--resume` flag still appears in some community docs but the canonical 2026 form is the subcommand. The dispatcher must use the subcommand form; planner verifies against `codex --help` from the AMI's installed version.
- **Session-ID extraction strategy.** Two viable paths — read from the Stop hook's stdin payload (recommended; the hook fires before process exit and `session_id` is a documented field), or tail the JSONL stream from `output.json`. Recommend the hook-file approach because it avoids JSONL parser edge cases; planner picks at slice time after the spike.
- **`codex_hooks` feature flag stability.** The flag is new in 2026 and still gated. If OpenAI removes the gate (auto-on), our config file's `[features] codex_hooks = true` line becomes a no-op — harmless. If they add new gating around it, we may need to revisit. Low risk; documented as something to track in CLAUDE.md so the next time someone touches this code they know to check the Codex changelog.
- **Permission-hook semantic if `--dangerously-bypass-approvals-and-sandbox` is removed by OpenAI.** Today, our Codex runs auto-approve so the notify-only hook is fine. If OpenAI removes that flag (or renames it), Codex would block on permission requests with no operator response, which would hang `codex exec`. Document the dependency; track Codex's changelog.
- **`km-notify-hook` regression risk.** The script handles three Claude event names today; adding a fourth (Codex `PermissionRequest`) and a payload-shape detection in `Stop` introduces small regression surface. Mitigation: add unit-style smoke tests that pipe canonical Codex and Claude payload JSON into the script in a sandbox VM and assert the right downstream calls fire. Existing test patterns in `internal/app/cmd/shell_learn_test.go` style apply.
- **Slack `chat.update` 10-minute window.** Slack only allows bot users to edit their own messages within ~10 minutes of posting. If the agent dispatch for the new thread takes longer than that (e.g., a 15-minute codex run before the first reply), the planned "edit the handoff post with the new permalink" approach will fail. Mitigation: post the new top-level FIRST, capture its `ts`/permalink, THEN post the handoff in the old thread with the permalink already embedded. This inverts the order from the spec's narrative but eliminates the dependency on `chat.update`. Planner picks at slice time.
- **Permalink race for the handoff post.** The new top-level message must exist before the handoff text in the old thread can reference its permalink. If posting the new top-level fails after the handoff is up, the operator sees a "Switching to codex →" message with no destination. Mitigation: same as above — post new-top-level first; only post the handoff after the permalink is in hand; on permalink-fetch failure, embed `(new thread spawn failed — see journald)` in the handoff and abort the switch (the user's prompt is then echoed back in the old thread with an error reaction).
- **Bash prefix parser fragility.** A Slack message body containing `claude:` mid-sentence (e.g., a user asking the agent to explain "Claude:" as a notation) must NOT trigger routing. The `^` anchor in the regex prevents that — `claude:` only matches at message start. Mitigation: covered by the strict grammar; add poller unit tests that pipe synthetic Slack payloads through the parser and assert the right `requested_agent` (or none) is selected.
- **`last_assistant_msg` truncation losing critical context.** The 2000-char cap for the seeded prompt and 500-char cap for the Slack excerpt are first-pass guesses. If they're too tight, the new agent gets a degraded handoff. Mitigation: ship with the caps as named bash constants at the top of the poller script so they're tunable without re-architecting; revisit in a quick task if UAT shows persistent truncation pain.
- **Per-thread DDB row count growth.** Each cross-agent switch adds a row. Long-running operators flipping back and forth could accumulate 10–20 rows per channel. The 30-day TTL handles cleanup; `km status` thread count column reflects active threads only (`thread_ts` newer than some recency window). Low-impact at expected usage volumes.

## Out-of-band notes

- Phase number 70 assumes Phase 69 (AWS allowlist) lands first. If a quick task or insertion lands at 69.x, renumber accordingly.
- This phase has no dependency on Phase 69 (AWS allowlist) — could ship in either order. The two phases touch different files and different concerns.
- The Phase 68 transcript-streaming parity for Codex (Tier 3) should land as Phase 70.x or a numbered follow-up. Once it ships, the Codex `~/.codex/config.toml` adds a `[[hooks.PostToolUse]]` entry; everything else is the JSONL parser.
- The "Goose / Codex sandbox profile" from Phase 34 is a placeholder profile YAML; this phase uses it as the starting point for the demo profile but doesn't otherwise touch it.
- The current operator workstation has a sandbox (alias `learn`) with Codex authenticated. This sandbox is the natural foundation for the Plan 70-00 spike and for the codex side of UAT — the operator can `km destroy` and `km create` it fresh each time without re-doing the Codex auth dance (the auth lives in the AMI / mounted volume, not in the sandbox FS). The phase plan should assume that fixture is available rather than building auth onboarding into the test plan.

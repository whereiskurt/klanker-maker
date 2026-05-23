# Phase 70: Codex parity + Slack prefix routing & cross-agent thread switching - Context

**Gathered:** 2026-05-22
**Status:** Ready for planning
**Source:** Brainstorming sessions 2026-05-05 (Tier 2 Codex parity) + 2026-05-22 (prefix routing + cross-agent switch) → SPEC.md

For the full design narrative, demo storyboard, failure modes, and risk register, read `SPEC.md` in the same directory. This document is the locked-decisions surface for the planner.

<domain>
## Phase Boundary

This phase delivers two intertwined capabilities on top of Phase 67's Slack inbound chat:

1. **Codex parity for Tier 2** (Phases 62/63/67 hook + Slack surfaces): `spec.cli.agent: codex` profiles get operator-notify hook events, Slack notify posts, and Slack inbound bidirectional chat with multi-turn resume, driven by the Codex CLI's stable hook system (PermissionRequest + Stop, same payload shape as Claude Code).
2. **Slack prefix routing + cross-agent mid-thread switching**: a Slack message starting with `claude:` or `codex:` selects the agent for that turn. Inside an existing thread, a prefix that names the *other* agent triggers a clean handoff — the bot posts a "Switching to {agent} → continuing in this thread." message with a Slack permalink in the originating thread, spawns a new top-level thread in the same `#sb-{id}` channel, and seeds the new agent with the prior agent's last assistant message.

**In scope:**
- New profile field `spec.cli.agent` (enum `claude` | `codex`, default `claude`) under `CLISpec`
- Compile-time emission of `KM_AGENT` env var to `/etc/profile.d/km-notify-env.sh` + `/etc/km/notify.env`
- Provisioning `~/.codex/config.toml` on every sandbox (not gated on `agent: codex`) with `[features] codex_hooks = true` + `PermissionRequest` + `Stop` hooks pointing at `/opt/km/bin/km-notify-hook`
- `km-notify-hook` bash script gains a `PermissionRequest` branch (Codex synonym for Claude `Notification`) + `last_assistant_message` fast-path fallback in the `Stop` clause
- Phase 67 inbound poller learns to fork dispatch by `KM_AGENT`: `codex exec --json --dangerously-bypass-approvals-and-sandbox` for first turns, `codex exec resume <id>` for follow-ups; Codex session-ID extraction via hook-file approach
- Two new attributes on `km-slack-threads`: `agent_type` (S, `claude`|`codex`, defaults to `claude` on absence) and `last_assistant_msg` (S, truncated to ~2000 chars)
- Poller prefix parser: `^([Cc]laude|[Cc]odex|CLAUDE|CODEX):[[:space:]]?` against the inbound message body; case-insensitive on agent name; exactly one optional space after the colon; nothing else tolerated
- Cross-agent switch sequence (new top-level first, then handoff in old thread with permalink; planner picks chat.update vs second-message ordering)
- `km-slack` sidecar gains `--new-message` flag on `post`, plus `permalink` and `update` subcommands (thin REST wrappers over chat.getPermalink and chat.update)
- Two new `km doctor` checks: `codex_hook_config_present`, `agent_type_consistency`
- Documentation: `docs/codex-parity.md` (new), CLAUDE.md, `docs/slack-notifications.md`

**Out of scope (deferred to follow-up phases or explicit non-goals):**
- Phase 68 transcript streaming for Codex (Tier 3 — separate phase). When it lands, it adds `[[hooks.PostToolUse]]` to the same `~/.codex/config.toml`.
- Slack-driven approve/deny on Codex `PermissionRequest` (requires Slack interactivity webhook; phase of its own)
- Goose / other-agent parity (`agent_type` extends cleanly; dispatch fork is a one-branch addition)
- Migrating `claude_session_id` column to `agent_session_id` (cosmetic; column-name hangover documented)
- Hot-reload of `spec.cli.agent` (requires `km destroy` + `km create`, matches every other profile field)
- Codex CLI install / version pinning (Phase 58 handles this in the AMI bake)
- Auto-routing by content heuristics (no "looks like code → codex" classifier)
- Thread merge / rejoin after switch
- Carrying tool, file, or cwd state between agents (only the last assistant message text travels)
- More than two agents on prefix routing (claude + codex only in Phase 70)
- Prefix in `km agent run --prompt` (existing `--claude`/`--codex` flags handle CLI-side selection; the prefix parser is poller-only)
- Slack thread-broadcast pattern as alternative to spawning a new top-level

**Touchpoints:**
- `pkg/profile/types.go` (CLISpec.Agent field) + embedded `schemas/sandbox_profile.schema.json`
- `pkg/compiler/userdata.go` (codex config writer, env file emission, km-notify-hook heredoc, poller dispatch fork, prefix parser + switch sequence)
- `sidecars/km-slack/` (new `--new-message`, `permalink`, `update` surface)
- `internal/app/cmd/doctor*.go` (two new check functions)
- `docs/codex-parity.md` (new), `docs/slack-notifications.md`, `CLAUDE.md`

</domain>

<decisions>
## Implementation Decisions (locked)

### Profile schema
- **LOCKED:** New field `spec.cli.agent: claude | codex`, default `claude`. Absence ≡ `claude`. Any other value is a validation error.
- **LOCKED:** No new cross-field rules. Existing inbound/notify combinatorics validate unchanged.
- **LOCKED:** No new fields specifically for prefix routing. `spec.cli.agent` is the profile default; prefix routing is runtime poller behavior only.
- **LOCKED:** Schema lives in `pkg/profile/types.go` as `Agent string \`yaml:"agent,omitempty"\`` on `CLISpec`, mirrored in `schemas/sandbox_profile.schema.json` as an enum.

### Codex hook configuration
- **LOCKED:** `~/.codex/config.toml` is written to every sandbox regardless of `spec.cli.agent`. Claude-default sandboxes never start Codex; the file has no runtime effect for them.
- **LOCKED:** `[features] codex_hooks = true` set unconditionally (provisioned at create time, not feature-flag-gated at runtime).
- **LOCKED:** Two hook entries only: `PermissionRequest` (matcher = `".*"`) and `Stop` (no matcher). No `PostToolUse` (Tier 3 will add it).
- **LOCKED:** Both hooks point at `/opt/km/bin/km-notify-hook` with the event name as the first positional argument (matches Claude convention).
- **LOCKED:** Hook timeout 30s.

### km-notify-hook
- **LOCKED:** Single bash file remains (no fork into per-agent scripts). New `PermissionRequest` branch reuses the existing "needs permission" path verbatim (tool name extracted from `tool_name` field, exits 0 with no stdout so Codex auto-approves).
- **LOCKED:** `Stop` payload prefers `last_assistant_message` when present (Codex path); falls back to existing transcript-tail logic (Claude path).
- **LOCKED:** Cooldown logic, env-var gates (`KM_NOTIFY_ON_PERMISSION`, `KM_NOTIFY_ON_IDLE`, `KM_NOTIFY_COOLDOWN_SECONDS`), and downstream `km-send` / `km-slack post` calls are agent-agnostic and reused.

### Inbound poller dispatch fork
- **LOCKED:** Boot-time read of `KM_AGENT` from the env file. Runtime override per turn from prefix matching (see below).
- **LOCKED:** Claude dispatch unchanged: `claude -p "$PROMPT" --output-format json --dangerously-skip-permissions [--resume $SESSION]`.
- **LOCKED:** Codex first-turn: `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT" > $OUT_FILE`.
- **LOCKED:** Codex resume: `codex exec resume $SESSION "$PROMPT" --json --dangerously-bypass-approvals-and-sandbox > $OUT_FILE` (subcommand form, not legacy `--resume` flag).
- **LOCKED:** Codex session-ID extraction via hook-file approach: the `Stop` hook writes the session ID to a known per-run file (e.g. `/tmp/km-codex-session.$RUN_ID`) before exiting. Spike (Plan 70-00) confirms the exact filename + permission model before planning Plan 70-05.
- **LOCKED:** `KM_SLACK_THREAD_TS` injection works for both agents (already generic; no changes).

### DDB schema additions (`km-slack-threads`)
- **LOCKED:** Two new String attributes: `agent_type` (`claude`|`codex`) and `last_assistant_msg` (truncated to ~2000 chars).
- **LOCKED:** Writer sets `agent_type` on every successful turn; reader defaults to `claude` when the attribute is absent (backward compat with pre-existing rows).
- **LOCKED:** `claude_session_id` is reused as the agent-agnostic session-ID column. Column-name hangover documented in CLAUDE.md and `docs/slack-notifications.md`. No migration.
- **LOCKED:** Both new attributes inherit the existing 30-day TTL via `ttl_expiry`. No schema or module changes (DynamoDB is schemaless on attributes).

### Slack prefix routing
- **LOCKED:** Strict grammar — case-insensitive `claude:` or `codex:` at message start, exactly one optional space after the colon, no leading whitespace tolerated. Regex `^([Cc]laude|[Cc]odex|CLAUDE|CODEX):[[:space:]]?`.
- **LOCKED:** Per-thread state model: each Slack thread maps 1:1 to one agent (the one that ran the first turn). Stored in `agent_type` on the DDB row keyed by `(channel_id, thread_ts)`.
- **LOCKED:** Same-agent prefix is a no-op switch: strip the prefix, dispatch the same agent in the same thread, no new thread, no new DDB row, no handoff post.
- **LOCKED:** Prefix in a brand-new top-level message picks the agent for that fresh thread, overriding the profile default for this thread only. The profile's compiled `KM_AGENT` on disk is unchanged.

### Cross-agent mid-thread switch
- **LOCKED:** Spawns a new top-level message in the same `#sb-{id}` channel (NOT a thread reply, NOT a thread-broadcast). The new top-level becomes the parent of the new agent's thread.
- **LOCKED:** Bot posts in the old thread: `Switching to {new_agent} → continuing in this thread.` followed by a Slack permalink to the new top-level.
- **LOCKED:** New thread's first body line: `Continuing from <permalink-to-old-thread>`. Then a truncated excerpt (~500 chars) of the prior agent's `last_assistant_msg` prefixed with `Previous assistant ({old_agent}) said:`.
- **LOCKED:** New agent's prompt is composed as: `{stripped_prompt}\n\n--- Context from prior thread (agent: {old_agent}) ---\n{full last_assistant_msg, up to 2000 chars}`.
- **LOCKED:** Switch ordering: post new top-level FIRST (so we know its `ts`/permalink), THEN post the handoff in the old thread with the permalink already embedded. This avoids the Slack `chat.update` 10-minute window risk. Planner MAY use `chat.update` for cleaner UX if they prefer, but the safer ordering is the spec's recommended path.
- **LOCKED:** Old DDB row is unchanged. Old session/process is NOT killed. Operator replies in the old thread continue to resume the old agent.
- **LOCKED:** New DDB row written keyed on `(channel_id, new_thread_ts)` with `agent_type={new_agent}`, the new session ID extracted post-run, and the new `last_assistant_msg` after the new agent completes.

### Failure modes (locked behaviors)
- **LOCKED:** Permalink retrieval fails → embed `(unavailable)` placeholder, journald log, continue with both posts.
- **LOCKED:** DDB row write fails after agent dispatch → next reply in the new thread is treated as a first turn; degrades to two-turn-loss of continuity. Acceptable.
- **LOCKED:** Operator replies in old thread after switch → old DDB row resumes old session. Two threads run in parallel. Working as intended.
- **LOCKED:** `last_assistant_msg` absent on old row (pre-existing rows): switch proceeds with no context excerpt. New rows always carry the attribute.

### km-slack sidecar additions
- **LOCKED:** New flag `--new-message` on `km-slack post`: omits `thread_ts` from `chat.postMessage` and returns the new message's `ts` to stdout.
- **LOCKED:** New subcommand `km-slack permalink --channel X --ts Y` wrapping `chat.getPermalink`.
- **LOCKED:** New subcommand `km-slack update --channel X --ts Y --text Z` wrapping `chat.update`.
- **LOCKED:** All three are thin REST wrappers; no business logic.

### km doctor
- **LOCKED:** `codex_hook_config_present` — for sandboxes with `spec.cli.agent: codex`, SSM-RunCommand check confirming `~/.codex/config.toml` exists, contains `codex_hooks = true`, and the two expected hook entries point at `/opt/km/bin/km-notify-hook`. WARN on drift.
- **LOCKED:** `agent_type_consistency` — for each `km-slack-threads` row with `agent_type` set, confirm the sandbox's profile (via `sandbox_id` → S3 profile fetch using the existing `downloadProfileFromS3` helper) still declares the same agent. WARN on drift.
- **LOCKED:** Both checks honor `--all-regions` and follow Phase 67/68 doctor-check conventions.

### Operator-side CLI
- **LOCKED:** No new CLI commands. `km agent run --claude` / `--codex` flags (Phase 58) remain the per-invocation override.
- **LOCKED:** `km validate` adds the enum check for `agent` (auto-derived from the schema addition).
- **LOCKED:** `km shell` defaults to the profile's agent unless `--claude` / `--codex` is passed.

</decisions>

<specifics>
## Specific code references and concrete examples

- `pkg/profile/types.go:371` — current location of `ClaudeArgs` (added in commit 4dbbe63); new `Agent` field goes alongside.
- `pkg/profile/schemas/sandbox_profile.schema.json:488` — current `codexArgs` entry; `agent` enum slots in nearby.
- `pkg/compiler/userdata.go:402-491` — `km-notify-hook` heredoc location (existing Phase 62/63 implementation).
- `pkg/compiler/userdata.go:1150-1400` — `km-slack-inbound-poller` script section (existing Phase 67 dispatch path).
- `pkg/compiler/userdata.go:689` — current `KM_SLACK_THREAD_TS` gate on the Stop hook's Slack branch (Phase 67 Gap A fix).
- `pkg/compiler/userdata.go:1280` — current `SlackInboundEnabled` conditional poller emission.
- `internal/app/cmd/doctor_slack.go:210, 462` — existing Phase 67 inbound check patterns to mirror.
- `internal/app/cmd/destroy.go` — `downloadProfileFromS3` helper used by the new `agent_type_consistency` check.
- `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` — current `(channel_id, thread_ts)` composite key. No changes; DynamoDB is schemaless on attributes.

## Concrete example: codex config.toml emitted by the compiler

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

## Concrete example: cross-agent switch artifacts

In the old (claude) thread, the bot posts:
```
Switching to codex → continuing in this thread.
https://workspace.slack.com/archives/C12345/p1716393742000300
```

The new top-level message reads:
```
Continuing from https://workspace.slack.com/archives/C12345/p1716393640000200

Previous assistant (claude) said:
> The auth middleware tests are passing; I refactored the token validation to use the new jose helpers. The integration tests for SSO are still red — I think the mock OIDC provider needs reconfiguring.
```

The codex dispatch prompt is:
```
check the code change

--- Context from prior thread (agent: claude) ---
The auth middleware tests are passing; I refactored the token validation to use the new jose helpers. The integration tests for SSO are still red — I think the mock OIDC provider needs reconfiguring.
```

## Test fixture: operator's learn sandbox

The operator's existing `learn` sandbox (with Codex auth'd via the standard `codex login` flow on the AMI / mounted volume) is the fixture for:
- Plan 70-00 spike (confirm hooks fire)
- Codex side of UAT in Plan 70-09

Codex auth persists across `km destroy` + `km create` (lives in the AMI), so the planner can assume the fixture without rebuilding auth.

</specifics>

<deferred>
## Deferred Ideas (acknowledged, not in Phase 70)

- **Tier 3: Phase 68 transcript streaming parity for Codex.** Adds `[[hooks.PostToolUse]]` to `~/.codex/config.toml` + JSONL stream parser. Schema additions in this phase are forward-compatible.
- **Slack-driven approve/deny on Codex `PermissionRequest`.** Would require Slack interactivity webhook into the bridge Lambda; long-lived hook process; phase of its own. Once it exists for Codex, it should backport to Claude.
- **Goose / other-agent parity.** `agent_type` extends to additional enum values; dispatch fork is a one-branch addition.
- **`agent_session_id` column rename.** Cosmetic; column-name hangover documented; no plan to migrate.
- **Auto-routing by content heuristics.** No "this looks like code → codex" classifier. Routing stays operator-explicit.
- **Thread merge / rejoin after switch.** No mechanism to fold a switched thread back into its origin.
- **More than two-agent prefix routing.** Phase 70 ships claude + codex only.
- **Prefix in `km agent run --prompt`.** CLI side keeps `--claude`/`--codex` flags; poller is the only place prefix parsing runs.
- **Slack thread-broadcast** as alternative to spawning a new top-level message. New-top-level pattern is locked.

</deferred>

---

*Phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher*
*Context gathered: 2026-05-22 from SPEC.md (brainstormed 2026-05-05 + extended 2026-05-22)*

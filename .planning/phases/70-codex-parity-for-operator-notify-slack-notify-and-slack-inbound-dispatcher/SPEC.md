# Phase 70 — Codex parity for Phases 62/63/67 (Tier 2: notify + Slack inbound)

**Status:** draft (pre-plan). Hand to `/gsd:plan-phase` once the operator approves the brief.
**Author:** brainstormed 2026-05-05
**Depends on:** Phase 58 (Codex CLI agent-run support + `spec.cli.codexArgs`), Phase 62 (operator-notify hook script), Phase 63 (Slack notify hook + km-slack sidecar + bridge Lambda), Phase 67 (Slack inbound poller + DDB threads table)

## Notation

`spec.cli.agent` is the new profile field; `KM_AGENT` is the runtime env var compiled from it. `~/.codex/config.toml` is the per-sandbox Codex hook configuration file written by `km create`. "Tier 2" refers to the scope chosen in brainstorming on 2026-05-05 — operator email + Slack notify + Slack inbound dispatcher parity. Tier 3 (transcript streaming, Phase 68 parity) is explicitly deferred.

## Goal

A SandboxProfile can declare `spec.cli.agent: codex` and the resulting sandbox gets the same operator-notify (Phase 62) and Slack notify (Phase 63) experience that Claude sandboxes get today, plus working Slack inbound bidirectional chat (Phase 67) — including multi-turn session resume — driven by Codex CLI instead of Claude Code. The hook plumbing is unified: a single `km-notify-hook` script handles both agents' payloads. The DDB schema gains an `agent_type` attribute so the same `km-slack-threads` row can carry either a Claude or a Codex session. Phase 68 (per-turn transcript streaming + final JSONL upload) is intentionally left for a follow-up phase.

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

### DDB schema: `agent_type` attribute on `km-slack-threads`

Single change: add `agent_type` String attribute (values `claude` | `codex`). DynamoDB is schemaless, so the change is purely on the writer/reader sides:

- **Writer** (poller, after a successful turn): always sets `agent_type` matching the sandbox's `KM_AGENT`.
- **Reader** (poller, on inbound): if `agent_type` attribute is absent (rows pre-dating this phase), default to `claude`.

The `claude_session_id` attribute is reused as the agent-agnostic session-ID column. The column-name hangover is documented in CLAUDE.md and `docs/slack-notifications.md`. Future agents (Goose, etc.) slot in as additional `agent_type` values without further schema work. No migration job, no rename.

The `km-slack-stream-messages` table (Phase 68's deferred-for-Tier-2 surface) is unchanged in this phase. When Phase 68 parity lands, it gets the same `agent_type` treatment.

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

## Demo storyboard

For a Phase 70 readout, run these flows in sequence on a single sandbox profile with `spec.cli.agent: codex`, `notifySlackEnabled: true`, `notifySlackPerSandbox: true`, `notifySlackInboundEnabled: true`, `notifyOnPermission: true`, `notifyOnIdle: true`:

1. **Provisioning.** `km create profile.yaml` → sandbox boots. Show both files exist on the sandbox: `cat ~/.claude/settings.json && cat ~/.codex/config.toml`. Both have hook entries pointing at `/opt/km/bin/km-notify-hook`.
2. **Operator-side Codex run.** `km agent run --codex --prompt "What model are you?"` from operator workstation. Codex runs to completion. Operator email arrives with subject `[sb-x] idle` and body containing the assistant's reply (read from `last_assistant_message`). Slack channel `#sb-x` shows the same body.
3. **Permission notification.** A Codex turn triggers a `PermissionRequest` (e.g., agent attempts `apply_patch` outside cwd). Operator email + Slack ping fire ("Codex is requesting permission for apply_patch"). Codex auto-approves due to `--dangerously-bypass-approvals-and-sandbox`. Run completes.
4. **Slack inbound, first turn.** Operator types "list workspace files" in `#sb-x`. Poller dispatches `codex exec --json --dangerously-bypass-approvals-and-sandbox "list workspace files"`, captures session ID, writes DDB row with `agent_type=codex`. Codex's reply posts to the same Slack thread.
5. **Slack inbound, multi-turn resume.** Operator follows up in the same thread: "now show README.md". Poller reads DDB → finds `agent_type=codex` + session ID. Dispatches `codex exec resume <id> "now show README.md" --json --dangerously-bypass-approvals-and-sandbox`. Codex resumes (its transcript and approvals carry forward). Reply posts to the same thread. DDB row updated with new `last_turn_ts`.
6. **`km doctor`.** Run `km doctor`. Both new checks return green: `codex_hook_config_present` confirms the on-sandbox config, `agent_type_consistency` confirms the DDB row matches the profile.

## Implementation slice estimate

Roughly 6-8 plans. The planner should refine.

1. **Schema + validation.** `pkg/profile/types.go` (new field on `CLISpec`), embedded JSON Schema, validator unit tests.
2. **Compiler config-file writer.** `pkg/compiler/userdata.go` writes `~/.codex/config.toml` for every sandbox; emits `KM_AGENT` to both env files. Independent of (1) for code, depends on schema.
3. **km-notify-hook agent-aware branches.** `pkg/compiler/userdata.go` heredoc edit. ~30 LOC. Independent of (2).
4. **Phase 67 poller dispatch fork.** `pkg/compiler/userdata.go` poller-script section. New session-ID extraction helper for Codex (hook-file approach recommended after a small spike). DDB reader/writer changes.
5. **DDB attribute writeback.** Could be merged into (4) — tiny additive change.
6. **km doctor checks.** Two new checks in `internal/app/cmd/doctor*.go`.
7. **Documentation.** `docs/codex-parity.md` (new operator guide); CLAUDE.md additions to the Slack Notifications section noting `agent: codex` semantics; `docs/slack-notifications.md` updates.
8. **End-to-end manual UAT.** The six demo flows on a real EC2 sandbox with both agents available. Captures live in a `70-VERIFY.md` artifact.

The eBPF surface from Phase 69 is unrelated; this phase touches the userdata template, profile schema, and the `internal/app/cmd` doctor framework only.

## Risks & open research items

- **Does `codex exec --json` (non-interactive) fire hooks identically to the interactive TUI?** Documentation doesn't differentiate — strong prior probability yes (the OpenAI engineering issue thread treats hooks as a CLI-wide feature), but a 30-minute spike before the dispatcher slice (Plan 70-04 or earlier) eliminates the entire phase's risk surface. Recommended Plan 70-00 spike: write a minimal `~/.codex/config.toml` with a Stop hook that touches `/tmp/codex-hook-fired`, run `codex exec --json "hello"`, confirm the file appears. Single sandbox, ~30 minutes.
- **Codex resume syntax form.** Web-confirmed `codex exec resume <id> "<prompt>"` (subcommand form, not `--resume` flag). The 2024-era `--resume` flag still appears in some community docs but the canonical 2026 form is the subcommand. The dispatcher must use the subcommand form; planner verifies against `codex --help` from the AMI's installed version.
- **Session-ID extraction strategy.** Two viable paths — read from the Stop hook's stdin payload (recommended; the hook fires before process exit and `session_id` is a documented field), or tail the JSONL stream from `output.json`. Recommend the hook-file approach because it avoids JSONL parser edge cases; planner picks at slice time after the spike.
- **`codex_hooks` feature flag stability.** The flag is new in 2026 and still gated. If OpenAI removes the gate (auto-on), our config file's `[features] codex_hooks = true` line becomes a no-op — harmless. If they add new gating around it, we may need to revisit. Low risk; documented as something to track in CLAUDE.md so the next time someone touches this code they know to check the Codex changelog.
- **Permission-hook semantic if `--dangerously-bypass-approvals-and-sandbox` is removed by OpenAI.** Today, our Codex runs auto-approve so the notify-only hook is fine. If OpenAI removes that flag (or renames it), Codex would block on permission requests with no operator response, which would hang `codex exec`. Document the dependency; track Codex's changelog.
- **`km-notify-hook` regression risk.** The script handles three Claude event names today; adding a fourth (Codex `PermissionRequest`) and a payload-shape detection in `Stop` introduces small regression surface. Mitigation: add unit-style smoke tests that pipe canonical Codex and Claude payload JSON into the script in a sandbox VM and assert the right downstream calls fire. Existing test patterns in `internal/app/cmd/shell_learn_test.go` style apply.

## Out-of-band notes

- Phase number 70 assumes Phase 69 (AWS allowlist) lands first. If a quick task or insertion lands at 69.x, renumber accordingly.
- This phase has no dependency on Phase 69 (AWS allowlist) — could ship in either order. The two phases touch different files and different concerns.
- The Phase 68 transcript-streaming parity for Codex (Tier 3) should land as Phase 70.x or a numbered follow-up. Once it ships, the Codex `~/.codex/config.toml` adds a `[[hooks.PostToolUse]]` entry; everything else is the JSONL parser.
- The "Goose / Codex sandbox profile" from Phase 34 is a placeholder profile YAML; this phase uses it as the starting point for the demo profile but doesn't otherwise touch it.

---
name: 91-CONTEXT
description: Phase 91 context — Slack inbound @-mention-only mode (polite-bot) for shared/override channels
metadata:
  phase: "91"
  source: PRD Express Path
  prd: .planning/todos/pending/2026-05-30-slack-inbound-mention-only-mode.md
  gathered: 2026-05-30
---

# Phase 91: Slack inbound @-mention-only mode (polite-bot) — Context

**Gathered:** 2026-05-30
**Status:** Ready for planning
**Source:** PRD Express Path (.planning/todos/pending/2026-05-30-slack-inbound-mention-only-mode.md + ROADMAP.md Phase 91 entry)

<domain>
## Phase Boundary

**What this phase delivers:**

Make the km-slack bridge "polite" in shared (Mode 1) and operator-controlled override (Mode 3) channels by only acting on messages that explicitly @-mention the bot. Per-sandbox `#sb-{id}` channels (Mode 2) keep current every-message behaviour. A new opt-in/opt-out profile knob lets operators override the smart per-mode default.

**Components touched:**

- `sidecars/slack-bridge/` — events handler must scan `event.text` for `<@{bot_user_id}>` and skip the 👀 reaction + downstream dispatch when mention-only mode is active and the message doesn't mention the bot.
- `pkg/profile/types.go` — new `CLISpec.NotifySlackInboundMentionOnly *bool` field (tri-state: nil = mode-derived default, true = force on, false = force off).
- `pkg/profile/schemas/sandbox_profile.schema.json` — schema addition for the new field.
- `pkg/profile/validate.go` — accept the new field; no semantic validation needed (any bool ok).
- `pkg/compiler/` (or wherever bridge config env is emitted) — resolve the effective mention-only boolean at compile time based on channel mode + override, then emit `KM_SLACK_MENTION_ONLY` env var into the bridge config / Lambda env.
- `internal/app/cmd/slack.go` (or `slack_init.go`) — `km slack init` must verify and cache `bot_user_id` in SSM at `{prefix}slack/bot-user-id` (the `auth.test` call already returns it; this phase confirms persistence and adds it if missing).
- `internal/app/cmd/doctor.go` — `km doctor` adds a sanity check for the `bot_user_id` cache when at least one resolved profile has mention-only enabled.

**Bridge dispatch contract:**

`KM_SLACK_MENTION_ONLY` env var, derived per channel mode from the resolved profile field. The bridge events handler reads this on each message event; when true, it scans for `<@{bot_user_id}>` before reacting/dispatching.

</domain>

<decisions>
## Implementation Decisions

### Channel-mode defaults (locked)
- **Mode 1 (shared, e.g. `#km-notifications`)**: default `true` → mention-only.
- **Mode 2 (per-sandbox `#sb-{id}`)**: default `false` → every-message (current behaviour). The channel is single-purpose; humans expect direct conversation with the bot.
- **Mode 3 (operator override channel)**: default `true` → mention-only.
- Operator can flip any mode by setting `cli.notifySlackInboundMentionOnly` explicitly to `true` or `false`.

### Profile field shape (locked)
- Name: `cli.notifySlackInboundMentionOnly` (camelCase, matches sibling `notifySlackEnabled`/`notifySlackPerSandbox`/`notifySlackChannelOverride`).
- Type: `*bool` (tri-state pointer: nil = inherit mode-derived default; true = force polite; false = force chatty).
- Default: nil — let the channel mode drive the effective value.
- Schema: optional `boolean` (no enum, no default in JSON schema — Go side handles the tri-state).

### Mention detection (locked)
- Slack canonicalises @-mentions on send to the `<@U...>` form. The bridge events handler scans `event.text` (substring match) for `<@{bot_user_id}>`.
- Display-name typing (`@klankermaker` without the `<@U...>` rendering) is **out of scope**.

### `bot_user_id` persistence (locked)
- SSM parameter path: `{prefix}slack/bot-user-id` (follows the existing `{prefix}slack/...` namespacing).
- Captured during `km slack init` from the `auth.test` response (already called; the value may already be cached — Phase 91 must **verify** and add caching if missing).
- Bridge Lambda env var or runtime fetch — TBD (Claude's Discretion): prefer compile-time injection via terragrunt module so the Lambda has no extra SSM round-trip on cold-start.

### Compiler env var (locked)
- Env var name: `KM_SLACK_MENTION_ONLY` (string `"true"`/`"false"`).
- Emitted into the bridge config from the resolved profile field after mode-derived default is applied.
- Path: same place the existing bridge env vars are written (Claude's Discretion to locate).

### `km doctor` check (locked)
- Add a new check: `slack_bot_user_id_cached` (or similar naming consistent with existing doctor checks).
- Trigger condition: WARN if at least one local profile resolves to `mention_only=true` AND `{prefix}slack/bot-user-id` SSM parameter is missing/empty.
- Severity: WARN (consistent with `slack_users_read_email_scope` added in Phase 72).
- Suggested fix in the doctor output: `km slack init --force` or a more targeted re-cache subcommand if convenient.

### Reuse from Phase 72 (locked)
- Reuse the channel-mode dispatch (`notifySlackEnabled` / `notifySlackPerSandbox` / `notifySlackChannelOverride`) established in `create_slack.go` to identify the mode each profile resolves to.
- Reuse the `auth.test` call shape from 72-01 — the `bot_user_id` is in that response.

### `km init --sidecars` (locked)
- Existing pattern: schema additions require `km init --sidecars` for management Lambdas to pick up the change. Document this in CONTEXT/PLAN.
- Existing sandboxes do **not** retroactively pick up the new field — they need `km destroy && km create`.

### Documentation (locked)
- Add a Phase 91 section to `docs/slack-notifications.md` describing the new field, the per-mode defaults, and operator override examples.
- Update `CLAUDE.md` § Slack to mention the polite-bot mode and `KM_SLACK_MENTION_ONLY`.
- Update `OPERATOR-GUIDE.md` where appropriate.

### Tests (locked — derive specifics during planning)
- Unit test: profile resolution of `mention_only` effective bool given (mode, override) combinations — exhaustive (Mode 1/2/3 × nil/true/false = 9 cases).
- Compiler test: `KM_SLACK_MENTION_ONLY` correctly emitted into bridge config from each (mode, override) combination.
- Bridge handler test: `<@{bot_user_id}>` substring scan — matches with surrounding text, matches at start/end, doesn't match when bot_user_id differs, no false positives on `<@OTHER>` mentions.

### Claude's Discretion
- File-level breakdown of plans (recommended: schema/types → compiler → bridge handler → SSM caching → doctor check → docs/tests).
- Specific Go struct/function names — follow existing conventions in `pkg/profile/types.go` and `pkg/compiler/`.
- Exact location of `KM_SLACK_MENTION_ONLY` emission (alongside existing bridge env vars).
- Whether bot_user_id is injected at compile-time vs Lambda-runtime read — pick the simpler/cheaper path that doesn't bake the ID into Terragrunt state.
- Naming of the new `km doctor` check (must be consistent with neighbours).
- Plan/wave granularity — Claude decides whether this is 4 plans, 6 plans, or 8 plans.

</decisions>

<specifics>
## Specific Ideas

- **Existing precedent in roadmap entry:** `KM_SLACK_MENTION_ONLY` env var name is locked.
- **Existing precedent in roadmap entry:** SSM path `{prefix}slack/bot-user-id` is locked.
- **Existing precedent:** Phase 72 added `slack_users_read_email_scope` to `km doctor` — Phase 91's new check should follow the same shape.
- **Existing precedent:** Phase 70/72 require `km init --sidecars` for schema additions to flow into the management Lambda.
- **PRD originator:** raised during Phase 72 UAT (2026-05-30) — corporate-workspace install where shared `#km-notifications` would be too noisy if the bot 👀-reacted to every team message.
- **Bridge dispatch path:** `notifySlackChannelOverride` (Mode 3) was added in Phase 72; the channel-mode resolution logic in `create_slack.go` is the source of truth for which mode a profile is in.

</specifics>

<deferred>
## Deferred Ideas

- **Per-channel runtime overrides via slash command** (e.g. `/km mention-only on`) — explicitly out of scope.
- **Display-name mention detection** (`@klankermaker` typed without `<@U...>` form) — explicitly out of scope; Slack canonicalises on send so it shouldn't matter.
- **Reactions-as-actions integration** — explicitly out of scope (different phase).
- **Backward-compat shim for existing sandboxes** — none; existing sandboxes need `km destroy && km create` to pick up the field.

</deferred>

---

*Phase: 91-slack-inbound-mention-only-mode-for-shared-and-override-channels-polite-bot*
*Context gathered: 2026-05-30 via PRD Express Path*

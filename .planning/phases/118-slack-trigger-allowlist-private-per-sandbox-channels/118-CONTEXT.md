# Phase 118: Slack trigger allowlist + private per-sandbox channels - Context

**Gathered:** 2026-06-24
**Status:** Ready for planning
**Source:** Approved design spec (`docs/superpowers/specs/2026-06-24-slack-trigger-allowlist-private-channels-design.md`)

<domain>
## Phase Boundary

Two independent, composable additions to the Slack integration:

- **Feature A — Private per-sandbox channel.** A per-sandbox profile knob `notification.slack.private` (bool, default `false`) creates the per-sandbox Slack channel as private (`is_private:true`) instead of the current hardcoded public at `pkg/slack/client.go:606`. Invites unchanged; channel membership becomes the access boundary (read + trigger).
- **Feature B — Uxxxx trigger allowlist (`allow`).** Gate *who can trigger the bot* by Slack user ID. Configurable install-wide (`km-config.yaml slack.allow`) and per-sandbox (`profile notification.slack.inbound.allow`). In a public channel: everyone reads, only listed `Uxxxx` trigger an agent turn.

The two features mix freely. This is an **additive, backward-compatible** change — no apiVersion bump, no behavior change when neither knob is set.

**Out of scope (parked):**
- Email/handle-based allowlist resolution (`users.info`) — Uxxxx only.
- Converting an existing public channel to private in place.
- Ephemeral "not authorized" nudges to rejected senders — silent by decision.
- Per-user gating finer than trigger/no-trigger.

</domain>

<decisions>
## Implementation Decisions (all LOCKED by the approved spec)

### Naming
- The switch is named **`allow`** in both YAMLs — single lowercase token, byte-identical across km-config.yaml snake_case and profile camelCase conventions. Matches existing GitHub bridge (`github.repos[].allow`) and H1 bridge (`programs[].allow`).

### Feature A — Private channel
- Field: `notification.slack.private` (bool, default `false`).
- At `km create`, when `perSandbox:true` AND `private:true`, `CreateChannel` passes `is_private:true` instead of the hardcoded `false` at `pkg/slack/client.go:606`.
- Invites unchanged — `conversations.invite` works identically on private channels; operator + `notification.slack.invites.emails` still invited.
- **No new OAuth scopes** — the App already requests `groups:read/history/write` in `slack_manifest_template.json`.
- `private:true` takes effect at **channel creation only**. A reused channel (Phase 104: `archiveOnDestroy:false` + alias reuse) is NOT converted public→private (Slack conversion is admin-gated). First-create wins; flip via `km destroy && km create`. Documented, not enforced.
- `private:true` with `perSandbox:false` is a no-op → `km validate` **warns** (does not error).

### Feature B — Trigger allowlist
- Install-level: `slack.allow` in `km-config.yaml` → `KM_SLACK_ALLOW` env var on the slack bridge Lambda.
- Per-sandbox: `notification.slack.inbound.allow` in the profile → written to the `km-sandboxes` DDB row at `km create` (alongside `slack_react_always`) → read by the bridge's `FetchByChannel`.
- **Resolution (in order):** non-empty per-sandbox `allow` replaces install-level; else non-empty `slack.allow`; else (both empty/absent) **everyone allowed** (backward-compatible). Empty/absent and explicitly-empty treated identically (both = "no per-sandbox override") to avoid the empty-list footgun.
- Enforced in `pkg/slack/bridge/events_handler.go` on the dispatch path, gating on `event.User` (the `Uxxxx` already in the payload — no `users.info` call, no `users:read.email` dependency).
- Reject = **silent ignore**: no 👀 reaction, no reply, no dispatch (mirrors GitHub bridge `200 silent`).
- **Always enforced**, independent of mention-only mode (allowlist = strict outer gate; mention-only = inner content filter; both must pass when both active) and the Phase 91.3 thread-bypass (a non-allowlisted user cannot hijack an already-active thread).
- **Handler order:** channel-ownership → allowlist (new) → mention/thread logic → dedup → dispatch. The allowlist sits BEFORE the mention check so a non-listed user is dropped regardless of mention/thread state.

### Repo plumbing (known-gotcha checklist — MUST address)
- **Config merge-list:** `slack.allow` must be added to the v2→v merge-list in `config.Load()` (not just struct + getter), or the YAML value is silently ignored. (memory: config-key merge-list)
- **DDB round-trip:** the per-sandbox `allow` attribute must round-trip through the `SandboxMetadata` struct + marshal/unmarshal, or `resume`/`extend`/ttl-handler strip it on the next full-row `PutItem`. (memory: SandboxMetadata lossy round-trip)
- **Bridge read:** `FetchByChannel` must read the new `slack_allow` attribute from the `km-sandboxes` row — verify the actual DDB attribute name against a real row, not the test mock. (memory: status-vs-state attr bug)
- **Validation:** `km validate` warns (does not error) when `private:true` or per-sandbox `allow` is set while `perSandbox:false`.

### Deploy surface
- Install-level `allow` → `KM_SLACK_ALLOW` is a bridge env-block change → `km init --dry-run=false` (NOT `--sidecars`; `km init --slack` scoped apply also covers env+IAM).
- Per-sandbox `allow` + `private` → profile schema field + create-handler (writes DDB row / sets `is_private`) + bridge (reads + enforces) → `make build-lambdas` + `km init --dry-run=false`. Existing sandboxes need `km destroy && km create` to gain per-sandbox attributes; install-level allowlist takes effect immediately on the next webhook.
- No SandboxProfile apiVersion bump (additive fields).

### Claude's Discretion
- Test structure and table-driven test layout for resolution-order and enforcement.
- Exact DDB attribute name (`slack_allow` proposed; verify against the actual row-writer convention used for `slack_react_always`).
- Doc placement (`docs/slack-notifications.md` § Phase 118) and CLAUDE.md phase summary wording.
- Whether `km validate` warning text/format follows existing warn conventions.

</decisions>

<specifics>
## Specific Ideas

- Reference enforcement pattern: GitHub bridge `allow` deny-by-default per-sender (silent 200). Mirror its silent-ignore semantics.
- Reference field-plumbing pattern: `slack_react_always` (Phase 91.5) is the precedent for a per-sandbox bool/list written at `km create`, round-tripped through `SandboxMetadata`, and read by `FetchByChannel`. Follow it exactly for `slack_allow`.
- The hardcoded public-channel site is `pkg/slack/client.go:606`.
- Enforcement site is `pkg/slack/bridge/events_handler.go`.

</specifics>

<deferred>
## Deferred Ideas

- Email/handle-based allowlist resolution (`users.info`).
- In-place public→private channel conversion.
- Ephemeral "not authorized" nudges (silent by decision; possible future opt-in).
- Per-user gating finer than trigger/no-trigger (read-only vs command subsets).

</deferred>

---

*Phase: 118-slack-trigger-allowlist-private-per-sandbox-channels*
*Context gathered: 2026-06-24 from approved design spec*

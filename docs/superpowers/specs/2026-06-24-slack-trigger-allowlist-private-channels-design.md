# Slack trigger allowlist + private per-sandbox channels — Design

**Date:** 2026-06-24
**Status:** Approved (design); pending GSD phase plan
**Author:** brainstormed with operator

## Summary

Two independent, composable additions to the Slack integration:

- **Feature A — Private per-sandbox channel.** A per-sandbox profile knob `notification.slack.private` creates the per-sandbox Slack channel as *private* (`is_private:true`) instead of the current hardcoded public. Invites are unchanged. Channel membership becomes the access boundary (read + trigger).
- **Feature B — Uxxxx trigger allowlist (`allow`).** Gate *who can trigger the bot* by Slack user ID. In a public channel this lets everyone read while only listed users trigger an agent turn. Configurable install-wide (`km-config.yaml slack.allow`) and per-sandbox (`profile notification.slack.inbound.allow`).

The two features mix freely: private channel = membership gates everything; allowlist = in a (typically public) channel, everyone reads but only listed `Uxxxx` trigger.

## Motivation

Today the Slack bridge has **no per-user authorization**. Any human in a connected channel can trigger the bot. Gating exists only at the channel level (channel must be registered) and message level (mention-only mode, bot-loop filter). The GitHub and H1 bridges, by contrast, are deny-by-default per-sender via an `allow` list.

Operators want:
1. A *private* channel option for sensitive sandboxes (access = invited members).
2. A *read-public / trigger-restricted* mode: a public channel anyone can observe, where only specific people can actually drive the agent.

## Naming

The switch is named **`allow`** in both YAMLs.

- It is a single lowercase token, so it is byte-identical across km-config.yaml's snake_case convention (`mention_only`, `react_always`) and profiles' camelCase convention (`mentionOnly`, `reactAlways`) — no case divergence.
- It matches the existing GitHub bridge (`github.repos[].allow`) and H1 bridge (`programs[].allow`), giving cross-bridge consistency.

| Scope | Location | Type |
|-------|----------|------|
| Install-level | `km-config.yaml` → `slack.allow` | `[]string` (Uxxxx) |
| Per-sandbox | profile → `notification.slack.inbound.allow` | `[]string` (Uxxxx) |
| Private channel | profile → `notification.slack.private` | `bool` (default `false`) |

## Feature A — Private per-sandbox channel

**Behavior**
- Field: `notification.slack.private` (bool, default `false`).
- At `km create`, when `perSandbox: true` **and** `private: true`, `CreateChannel` passes `is_private: true` instead of the hardcoded `false` at `pkg/slack/client.go:606`.
- Invites are **unchanged**: `conversations.invite` works identically on private channels, so the primary operator + `notification.slack.invites.emails` are all still invited.
- **No new OAuth scopes** — the App already requests `groups:read/history/write` in `slack_manifest_template.json`.
- Default `false` ⇒ byte-identical to today (public).

**Edges**
- `private:true` takes effect at **channel creation only**. A reused channel (Phase 104: `archiveOnDestroy:false` + alias reuse) is **not** converted public→private (Slack's conversion is admin-gated). First-create wins; to flip, `km destroy && km create`. Documented, not enforced.
- `private:true` with `perSandbox:false` is a no-op (no per-sandbox channel created) → `km validate` **warns**.

## Feature B — Uxxxx trigger allowlist

**Sources & composition**
- Install-level: `slack.allow` in `km-config.yaml` → `KM_SLACK_ALLOW` env var on the slack bridge Lambda.
- Per-sandbox: `notification.slack.inbound.allow` in the profile → written to the `km-sandboxes` DDB row at `km create` (alongside `slack_react_always`) → read by the bridge's `FetchByChannel`.
- **Resolution (in order):** if the per-sandbox `allow` list is **non-empty**, it is the effective list (replaces install-level). Else if `slack.allow` is **non-empty**, that is the effective list. Else (both empty/absent) **everyone is allowed** — backward-compatible, no behavior change for existing installs. Empty/absent and explicitly-empty are treated identically (both = "no per-sandbox override"), avoiding an empty-list footgun.

**Enforcement**
- Enforced in `pkg/slack/bridge/events_handler.go` on the dispatch path, gating on the message event's `event.User` (the `Uxxxx` ID already present in the payload — no `users.info` call, no `users:read.email` dependency).
- Reject = **silent ignore**: no 👀 reaction, no reply, no dispatch (mirrors the GitHub bridge's `200 silent`).
- **Always enforced**, independent of:
  - mention-only mode (allowlist is the strict outer gate; mention-only is the inner content filter — both must pass when both active), and
  - the Phase 91.3 thread-bypass (a non-allowlisted user cannot hijack an already-active thread).
- Order in the handler: channel-ownership → allowlist (new) → mention/thread logic → dedup → dispatch. The allowlist sits before the mention check so a non-listed user is dropped regardless of mention/thread state.

## Repo plumbing (known-gotcha checklist)

- **Config merge-list:** `slack.allow` must be added to the v2→v merge-list in `config.Load()` (not just the struct + getter), or the YAML value is silently ignored. (See memory: config-key merge-list.)
- **DDB round-trip:** the per-sandbox `allow` attribute must round-trip through the `SandboxMetadata` struct + marshal/unmarshal, or `resume`/`extend`/ttl-handler strip it on the next full-row `PutItem`. (See memory: SandboxMetadata lossy round-trip.)
- **Bridge read:** `FetchByChannel` must read the new `slack_allow` attribute from the `km-sandboxes` row (verify the actual DDB attribute name against a real row, not the test mock — see memory: status vs state attr bug).
- **Validation:** `km validate` warns (does not error) when `private:true` or per-sandbox `allow` is set while `perSandbox:false`.

## Deploy surface

- **Install-level `allow`** → `KM_SLACK_ALLOW` is a bridge env-block change → `km init --dry-run=false` (NOT `--sidecars`, which doesn't update the env block; `km init --slack` scoped apply also covers env+IAM).
- **Per-sandbox `allow` + `private`** → profile schema field + create-handler (writes DDB row / sets `is_private`) + bridge (reads + enforces) → `make build-lambdas` + `km init --dry-run=false`. Existing sandboxes need `km destroy && km create` to gain the per-sandbox attributes; install-level allowlist takes effect immediately on the next webhook.
- No SandboxProfile apiVersion bump (additive fields).

## Out of scope (parked)

- Email/handle-based allowlist resolution (`users.info`) — Uxxxx only, by decision.
- Converting an existing public channel to private in place.
- Ephemeral "not authorized" nudges to rejected senders — silent by decision (could be a future opt-in).
- Per-user gating finer than trigger/no-trigger (e.g., read-only vs command subsets).

## Acceptance criteria

1. A profile with `notification.slack.perSandbox:true` + `private:true` creates a private Slack channel; the operator + `invites.emails` are members; the bot posts/streams normally.
2. With `slack.allow:[U_OP]` install-wide, a message from `U_OP` triggers the bot; a message from any other user in the same channel is silently ignored (no reaction, no reply, no run).
3. A profile with a **non-empty** `notification.slack.inbound.allow:[U_OP,U_X]` overrides the install-level list for that sandbox's channel; users not in the per-sandbox list are ignored there even if present in `slack.allow`. An empty/absent per-sandbox list falls back to `slack.allow`.
4. Empty/unset `allow` at both levels ⇒ current behavior (everyone can trigger).
5. Allowlist is enforced on thread replies (Phase 91.3 bypass does not exempt a non-listed user) and regardless of mention-only mode.
6. `km validate` warns on `private`/`allow` set with `perSandbox:false`.
7. Per-sandbox `allow` survives `km pause`/`resume`/`extend` (round-trips through `SandboxMetadata`).
8. Existing installs with no `allow`/`private` configured produce byte-identical behavior.

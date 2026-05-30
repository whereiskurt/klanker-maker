---
created: 2026-05-30T00:00:00.000Z
title: Slack inbound — only react to messages that @-mention klankermaker
area: slack
files:
  - sidecars/slack-bridge/... (events handler — @-mention detection)
  - internal/app/cmd/slack.go (bridge config persistence)
  - pkg/profile/types.go (CLISpec field)
  - pkg/profile/schemas/sandbox_profile.schema.json
  - pkg/profile/validate.go
---

## Problem

The km-slack bridge currently forwards every `message.channels` / `message.groups`
event in subscribed channels to the create-handler / agent-run flow, and the bot
adds a 👀 (`:eyes:`) reaction on every message it processes. This is fine in
per-sandbox `#sb-{id}` channels where the bot is the primary participant — but
in shared (Mode 1) or operator-controlled (Mode 3) channels, humans are also
chatting with each other and don't want the bot reacting to every line. Existing
Slack-bot convention is "polite mode" — only act when explicitly @-mentioned.

## Proposed solution

Add a profile field `cli.notifySlackInboundMentionOnly` with smart defaults:

- **Mode 2 (per-sandbox `#sb-{id}`)**: default `false` — keep current
  every-message behaviour. The channel is single-purpose and humans
  expect to type to the bot directly.
- **Mode 1 (shared) / Mode 3 (override)**: default `true` — only react
  when the message text contains `<@{bot_user_id}>`.

Operator can force `true` everywhere (polite mode in per-sandbox channels too)
or `false` everywhere (current behaviour in shared channels) by setting the
field explicitly.

## Implementation sketch

- Detect @-mention in the bridge's events handler by scanning `event.text` for
  `<@{bot_user_id}>` (Slack canonicalises mentions to that form). Bot user ID
  is already cached in SSM at `{prefix}slack/bot-user-id` after `km slack init`
  (verify — may need to add).
- Drop `:eyes:` reaction and downstream dispatch on non-mention events when
  mention-only mode is active for the channel.
- Defaults applied at compile-time based on the profile's channel mode so the
  Lambda doesn't have to re-derive at runtime.

## Out of scope

- Per-channel overrides at runtime (could add later as a slash command).
- Mentioning by display name (`@klankermaker` typed without the Slack-rendered
  `<@U...>` form). Slack canonicalises on send so this should not be required.

## Origin

Raised by operator during Phase 72 UAT (2026-05-30) — corporate-workspace
install where shared `#km-notifications` would be too noisy if the bot
reacted to every team message.

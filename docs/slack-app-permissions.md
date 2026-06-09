# Slack App permissions — what each scope is for

> Reference for the Slack App that backs km's Slack notifications + inbound bridge
> (Phases 63, 67, 72, 91, 95, 96). Explains every bot token scope and event km
> requests, **why** it needs it, and **what breaks** without it.
>
> The authoritative source is the manifest template
> `internal/app/cmd/slack_manifest_template.json` (rendered by `km slack manifest`)
> and the `km doctor` scope checks (`doctor_slack*.go`). Keep this doc in sync.

## TL;DR

```bash
km slack manifest > app.json   # render a deployment-specific manifest
# Slack → Your Apps → Create New App → "From an app manifest" → paste app.json
```

Bot token scopes (15) + bot events (2) declared by the manifest:

| Scope | One-liner |
|-------|-----------|
| `chat:write` | post messages (notifications, replies, transcript) |
| `channels:manage` | create / archive **public** channels (`#sb-{id}`) |
| `channels:join` | join public channels so the bot can post |
| `channels:read` | look up a public channel by name (reuse path) |
| `channels:history` | read public-channel messages (inbound trigger) |
| `groups:write` | create / manage **private** channels |
| `groups:read` | look up a private channel |
| `groups:history` | read private-channel messages (inbound trigger) |
| `conversations.connect:write` | Slack Connect invites to external people |
| `reactions:write` | add the 👀 ACK reaction on inbound |
| `reactions:read` | read reaction state (read-counterpart; future reactions-as-actions) |
| `files:write` | upload attachments + transcript files |
| `files:read` | read files referenced in inbound messages |
| `users:read` | base user-read; **required companion** of `users:read.email` |
| `users:read.email` | resolve an email → Slack user id for invites |
| **event** `message.channels` | messages in public channels the bot is in |
| **event** `message.groups` | messages in private channels the bot is in |

---

## Why each scope

### Posting & messaging

**`chat:write`** — the core outbound scope. Every message km sends uses it: the
`#sb-{id}` "sandbox ready"/"destroyed" notes, on-permission / on-idle notifications,
threaded transcript streaming, inbound replies, and the Phase 96 default-router
orphan-channel reply. **Without it:** km is mute — no notifications, no replies.

### Channel lifecycle (public)

**`channels:manage`** — create the per-sandbox public channel at `km create`
(`conversations.create`) and **archive** it at `km destroy` when
`notification.slack.archiveOnDestroy` is on (`destroy_slack.go`). **Without it:**
`km create` can't make `#sb-{id}` and `km destroy` can't archive it.

**`channels:join`** — a bot can only post to a public channel it is a member of; km
joins as needed (e.g. a `channelOverride` channel, or after reuse). **Without it:**
posting to a channel the bot isn't in fails with `not_in_channel`.

**`channels:read`** — the O(1) reuse path looks up an existing `#sb-{id}` by name
before creating, so a recreate reuses the same channel. Also used by `km slack init`
`--shared-channel`. **Without it:** channel-by-name lookup fails (km surfaces a
"grant `channels:read`" error).

**`channels:history`** — required by the **Events API** to deliver `message.channels`
events and for the bridge to read the triggering message. This is an *inbound* scope
(Phase 67). **Without it:** inbound message events don't arrive / can't be read — the
bot can notify but never *listens*.

### Channel lifecycle (private) — `groups:*`

`groups:write`, `groups:read`, `groups:history` are the **private-channel** mirrors of
the `channels:*` trio above (Slack models private channels as "groups"). They let km
create/look-up/read **private** per-sandbox channels and receive `message.groups`
events. **Without them:** km works only in public channels; private-channel
sandboxes/inbound break.

### Invites

**`conversations.connect:write`** — sends a **Slack Connect** invite to an *external*
person (a different workspace), used by `km slack invite --external` and the
`notification.slack.invites` auto-invite loop when `useConnect` is true (Phase 72).
Requires a Pro/Business Slack workspace. **Without it:** external/Connect invites fail;
native-workspace invites (same workspace) still work.

**`users:read.email`** — resolves an **email address → Slack user id** via
`users.lookupByEmail` (`pkg/slack/client.go`), which Slack's invite APIs require. Used
by `km slack invite <email>` and `km create` auto-invite (Phase 72). `km doctor` warns
when it's missing (`checkSlackUsersReadEmailScope`). **Without it:** every email-based
invite fails with `missing_scope`.

**`users:read`** — the **base user-read scope** and a **required companion** of
`users:read.email`. Slack treats the `.email` variant as an *add-on* to `users:read`:
the email field is only readable when both are granted, and a manifest that lists
`users:read.email` alone is incomplete (Slack's own OAuth UI auto-pairs them, but a
manifest must list both explicitly). **Without it:** `users:read.email` cannot function
— `users.lookupByEmail` / user reads fail even though the email scope appears granted.

### Reactions

**`reactions:write`** — the bridge posts the **👀 ACK** on an inbound message so the
user sees it was received before the agent finishes (Phase 67.1). Honors the
`react_always` / per-sandbox `reactAlways` toggles (Phase 91.4/91.5). **Without it:**
no ACK reaction; users get no immediate "heard you" signal.

**`reactions:read`** — the read counterpart, declared for reading reaction state
(reactions-as-actions was a deferred Phase 68 idea; the active ACK path only needs
`reactions:write`). Kept in the manifest so the scope set is forward-compatible.
**Without it:** no impact on today's flows.

### Files

**`files:write`** — upload **attachments** and stream **transcript** files via the
bridge `ActionUpload` (Phase 67 transcript streaming, `km-slack` attachments,
Block-Kit-rendered output). `km doctor` warns when missing
(`checkSlackTranscriptScope`). **Without it:** transcript upload / attachments 400 at
the bridge.

**`files:read`** — read files a user attaches in an inbound message so the agent can
act on them. Part of the Events API inbound scope set (`VerifyEventsAPIScopes`).
**Without it:** inbound file handling fails.

---

## Bot events: `message.channels` + `message.groups`

km subscribes to messages in **public** (`message.channels`) and **private**
(`message.groups`) channels the bot belongs to. These are the inbound triggers: a user
message in a sandbox/shared channel → Events API delivery → bridge → polite-bot
@-mention filter (Phase 91) → dispatch to the sandbox agent.

**Why only these two?** km reacts to channel messages it can already see (member-only),
keeping the surface minimal. App-mention-in-non-member-channels (`app_mention` +
`chat:write.public`) and DM fallback (`im:write`) were explicitly **deferred** (see
`docs/slack-notifications.md` § Phase 96 "Deferred").

**Without these events:** no inbound — the bot notifies but never responds.

---

## How scopes map to features

| Feature | Scopes |
|---------|--------|
| Outbound notifications | `chat:write` |
| Per-sandbox channel create/archive | `channels:manage` / `groups:write`, `channels:read` / `groups:read`, `channels:join` |
| Inbound (listen + reply) | `channels:history`, `groups:history`, `chat:write` + events `message.channels`/`message.groups` |
| 👀 ACK reaction | `reactions:write` |
| Transcript streaming / attachments | `files:write`, `files:read` |
| Invites (native + Connect) | `users:read` + `users:read.email`, `conversations.connect:write` |

---

## Verifying & updating

- **Check live scopes:** `km doctor` validates the inbound set (`channels:history`,
  `groups:history`, `reactions:write`, `files:read`), `files:write`, and
  `users:read.email`, with exact remediation lines for any that are missing.
- **Add a scope:** `km slack manifest > app.json`, update the App's Bot Token Scopes
  from it (Slack Admin → your app → OAuth & Permissions → Bot Token Scopes), then
  **reinstall** to the workspace. Adding scopes does **not** change the bot token, so no
  `km slack rotate-token` is needed — just reinstall and re-run `km doctor`.
- **Status:** `km slack status` prints the SSM-backed config.

See `docs/slack-notifications.md` for the full Slack runbook, polite-bot mode,
federation relay (Phase 95), and the default router (Phase 96).

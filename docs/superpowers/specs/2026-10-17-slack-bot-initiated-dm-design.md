# Slack bot-initiated DM — design

**Date:** 2026-10-17. **Status:** approved in chat by the operator; bounded follow-up to
`2026-10-16-slack-default-route-design.md`. **Branch:** `feat/slack-default-route`.

## 1. Goal

In any thread the bot is already answering in, the agent can send the person it is talking
to a direct message from the bot:

```
km-slack dm --to U0ABC123 --text "Here is the summary you asked for …"
```

The person receives a top-level DM from the bot. A reply they type **under** that DM message
continues with the same sandbox and session. A fresh top-level DM from them keeps going
through the default route exactly as today.

Motivating ask: "In a public thread, say *hey, can you DM me about this?* and the bot does."

## 2. What exists today, and why it refuses

- The manifest requests `im:history` and `message.im` only. The route design chose "the bot
  answers a DM, it never opens one" (`docs/slack-default-route.md` §4 "Reach" row).
  `conversations.open` needs **`im:write`**.
- `km-slack` has `post`, `reply`, `upload`, `permalink`, `update`, `record-mapping`. Nothing
  opens a conversation.
- The signed-envelope handler (`pkg/slack/bridge/handler.go`, step 6) lets a sandbox post
  outside its own `#sb-` channel only when the envelope carries a `thread_ts` whose
  `km-slack-threads` row names that sandbox, and only for `post`/`upload`. A fresh DM has no
  row, so it is `403 channel_mismatch` by construction.

So the agent either says it cannot, or guesses a channel and gets the 403.

## 3. Decisions

| Decision | Choice | Why |
|---|---|---|
| Who may be DM'd | **Only a user whose email passes the default route's `allow_emails`.** | The thread row (`channel_id`, `thread_ts`, `sandbox_id`) never records the human, so "the person in your thread" would need a new attribute and a write per turn. The allowlist already defines who may converse with the bot; the bot opening the conversation first is no new privilege. |
| No default route configured | `403 dm_disabled`. | Mapped-only installs have no email allowlist. Honouring the Phase 118 user-id `allow` there is a possible later extension, not built now. |
| Target identity | `users.info` → email, lowercased, via the existing `CachedSenderEmailFetcher`; deny on API error, no email, `deleted`, `is_bot`. Denies are never cached. | Same gate, same code as inbound. |
| Names | Rejected at parse time; ids only (`U…`/`W…`). | Same rule as `--mention`; the bot never enumerates the directory. The agent takes the id from the `[Slack] From:` preamble. |
| Delivery | `conversations.open(users=<id>)` → `D…` channel; `chat.postMessage` into it, **top-level**. Block Kit rendering reused from `post`. | A top-level post writes no "already posted" marker (`postedMarkerPath` returns "" for an empty thread), so the originating thread's automatic final reply still lands. |
| Continuity | On a successful post the bridge writes a thread row `(D…, ts) → posting sandbox` via `Threads.Upsert`, **after** the post, never before. | A threaded human reply under the DM then takes the existing continuity branch to that sandbox, email gate included, and that sandbox's reply (`km-slack post --channel D… --thread ts`) passes `threadRoutedToSender`. A failed post leaves no row. |
| `--from-thread` | Optional. Envelope carries the originating `channel` + `thread_ts`; the bridge verifies that row names the sender (or the channel is the sender's own), mints `chat.getPermalink`, and appends `\n\nFrom: <permalink>` to the DM text. Verification failure → `403 channel_mismatch`, no DM sent. | The sandbox cannot mint it (`permalink` stays 403 in foreign channels); the bridge holds the bot token. |
| Quota | Counted as `slack_post` through the existing `checkQuota`. | No new action class. |
| Rate limiting | None new. | Targets are bounded by the allowlist and volume by the quota gate. YAGNI. |
| Envelope field | `TargetUser string \`json:"target_user,omitempty"\`` on `SlackEnvelope`. | **Load-bearing:** the bridge re-canonicalises the struct before verifying. A non-`omitempty` field would make every pre-existing sandbox's `km-slack` (which does not know the field) fail `bad_signature` the moment the new bridge deploys. With `omitempty`, old envelopes canonicalise identically on both sides; a new `dm` envelope reaching an **old** bridge fails `bad_signature`, which is the correct "unsupported" outcome. |
| Infrastructure | None. | The bridge already holds `PutItem` on the threads table and the bot token. No table, IAM, or env var change. Dormant when `slack.default_route` is absent. |

## 4. Components

### 4.1 Scope and checks
- `internal/app/cmd/slack_manifest_template.json` + `testdata/slack_manifest_golden.json`: add
  `im:write` to `oauth_config.scopes.bot` (after `mpim:history`).
- `internal/app/cmd/slack.go` `VerifyEventsAPIScopes`: add `im:write` to `required`.
- `internal/app/cmd/doctor_slack.go` `checkSlackDefaultRoute` step 3: check
  `im:history`, `mpim:history`, **`im:write`**; message names the missing one.
- `docs/slack-app-permissions.md`: one row for `im:write` ("the bot opens a DM when a sandbox
  agent runs `km-slack dm`; never used to read").

### 4.2 Envelope and client
- `pkg/slack/payload.go`: `ActionDM = "dm"`; `TargetUser` field (omitempty); `BuildEnvelope`
  unchanged (callers set `TargetUser` after).
- `pkg/slack/client.go`: `OpenDM(ctx, userID string) (channelID string, err error)` wrapping
  `conversations.open` (`users=<id>`, returns `channel.id`). Errors carry the Slack error
  string.
- `pkg/slack/bridge/interfaces.go`: `type DMOpener interface { OpenDM(ctx, userID) (string, error) }`.
  The handler type-asserts `h.Slack.(DMOpener)` like `BlockPoster`, so existing fakes keep
  compiling; a poster without it → `500 dm_unsupported`.

### 4.3 Bridge handler (`pkg/slack/bridge/handler.go`)
Handler gains `DefaultRoute *DefaultRoute` and `SenderEmails SenderEmailFetcher`, both set by
`wireDefaultRoute` in `cmd/km-slack-bridge/main.go` (shared instances with the events
handler; nil when the route is off).

Step 6 authz: `ActionDM` skips the owned-channel check (the DM channel does not exist yet).
Operators (`sender_id == operator`) may not send `dm` (`403 sandbox_action_forbidden` mirror:
`operator_action_forbidden`) — there is no operator use case and it keeps the gate single.

Dispatch, in order, each fail-closed:
1. `h.DefaultRoute == nil || h.SenderEmails == nil` → `403 dm_disabled`.
2. `env.TargetUser` must match `^[UW][A-Z0-9]+$` → else `400 bad_target`.
3. `SenderEmails.SenderEmail(ctx, target)` → error → `403 dm_not_allowed` (log reason);
   `!DefaultRoute.Allows(email)` → `403 dm_not_allowed`.
4. `--from-thread` present (`env.Channel != "" && env.ThreadTS != ""`): row must name the
   sender, or `env.Channel` must equal the sender's owned channel → else `403 channel_mismatch`.
   Then `GetPermalink`; a permalink error is logged and the DM goes without the link
   (fail-soft: the link is a courtesy).
5. `checkQuota(slack_post)` → `429 quota_exceeded`.
6. `OpenDM(target)` → `D…`; error → `502 slack_error` with the Slack string.
7. Post (blocks path when `env.Blocks != ""`, else text) into `D…`, no `thread_ts`.
8. On success: `Threads.Upsert(ctx, D…, ts, env.SenderID)` (error logged, response still 200);
   respond `{ok:true, ts, channel: D…}`.
Logs: `bridge: dm` with sender, target, channel, ts; every refusal names its step.

### 4.4 `km-slack dm` (`cmd/km-slack/`)
`km-slack dm --to <id> (--text <s> | --body <file>) [--render plain|mrkdwn|blocks|blocks-rich] [--from-thread]`
- `--to` validated with the same id regex as `--mention` (names rejected at parse time, exit 2).
- `--from-thread` reads `KM_SLACK_CHANNEL_ID` and `KM_SLACK_THREAD_TS` from the environment
  (the poller exports both per turn); both empty → exit 2 with a message.
- Renders like `post` (same tiers and fallback), builds `ActionDM` envelope with `TargetUser`,
  `Channel`/`ThreadTS` only when `--from-thread`. Prints `channel=<D…> ts=<ts>` to stdout.
- Writes no posted marker (top-level).
- `--help` lists it.

### 4.5 Preamble and skill
- `pkg/compiler/userdata.go` sender preamble gains one sentence: "To send this person a
  direct message run `km-slack dm --to <@id-without-brackets> --text '…'`; it does not
  replace your reply here." Frozen pre-92 golden hand-patched with exactly that hunk.
- `skills/slack/SKILL.md`: new subsection "Sending someone a DM" under "Inbound Slack
  Messages" (verb, allowlist rule, `--from-thread`, what a refusal means). Plugin `0.4.19` in
  `plugin.json` + `marketplace.json`.

## 5. Tests
- `pkg/slack`: `OpenDM` against an httptest server (ok, `cannot_dm_bot`, `user_not_found`);
  envelope round-trip: an envelope without `TargetUser` canonicalises to the pre-change bytes
  (pin the exact string).
- `pkg/slack/bridge` handler matrix: allowed target → `conversations.open` + post + row written
  once, 200 with channel; unlisted email → 403 and no Slack call; route nil → `dm_disabled`;
  lookup error → `dm_not_allowed`, no Slack call; post failure → no row; `--from-thread` with
  a row naming the sender → permalink appended; with a row naming another sandbox → 403, no
  DM; operator sender → 403; `update`/`permalink` behaviour unchanged (existing tests).
- `cmd/km-slack`: `dm` arg parsing (name rejected, missing text, `--from-thread` without env),
  envelope shape, no marker file written.
- `internal/app/cmd`: manifest golden + scope-check tests updated; doctor result names `im:write`.
- `pkg/compiler`: rendered-bash preamble test asserts the sentence; frozen golden diff is one hunk.

## 6. Deploy
`make build` → `make build-lambdas` → `km init --dry-run=false` (bridge zip, `km-slack`
sidecar, create-handler poller preamble) → `km slack manifest` → paste, reinstall →
`km slack rotate-token --bot-token <new>` → `km doctor` (default route scopes green) →
`km destroy && km create` of the default sandbox (or let the next mention cold-create it), so
its `km-slack` carries the verb. Half-deploys: new sidecar + old bridge → `dm` gets
`bad_signature`, nothing else affected; new bridge + old sidecar → no change at all (omitempty);
scope not yet granted → `conversations.open` fails `missing_scope`, surfaced as `502 slack_error`.

## 7. Live UAT
1. In a public thread, ask the bot to DM a summary → DM arrives, thread reply still lands once.
2. Reply under the DM message → same sandbox answers in the DM thread; bridge log shows the
   continuity branch.
3. Ask it to DM an id not on the allowlist → agent reports the refusal; bridge log `dm_not_allowed`.
4. `--from-thread` → the DM ends with a working permalink.
5. `km doctor` → `Slack default route scopes` green with `im:write`.

## 8. Out of scope
Group DMs (`mpim`), DMs from mapped-only installs, DM-by-name, a per-target cooldown, and
recording thread participants on the row.

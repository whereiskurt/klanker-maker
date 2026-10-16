# Slack default route: @-mention the bot from any channel or DM

**Date:** 2026-10-16
**Status:** Design approved in conversation; implementation plan pending
**Scope:** `km-slack-bridge` events handler and outbound authorizer, `km-config.yaml`,
`km init` / `km doctor`, the Slack inbound poller userdata, `cmd/create-handler`,
`infra/modules/lambda-slack-bridge`, the Slack App manifest.

## 1. Problem

Today the bridge dispatches a Slack message only when the channel it arrived in is
bound to a sandbox (`FetchByChannel` on the `slack_channel_id-index` GSI). A message
in any other channel reaches, at most, the Phase 96 orphan reply: a threaded hint
naming the `#sb-{alias}-{profile}` convention. There is no way for a person to open a
new channel, invite the bot, and start working with it there, and no way to DM it.

The GitHub bridge already has the opposite posture for one source: `github.events`
rules send any matching webhook to a named alias, cold-creating it if absent. This
design gives the Slack bridge the same thing for unmapped conversations, gated on a
named list of people.

## 2. Decisions taken in the design conversation

| Decision | Choice | Why |
|---|---|---|
| Reach | Public channels, private channels, DMs, group DMs | Operator's call. DMs cost two scopes and two events. |
| Empty allowlist | Route is OFF | A bot reachable from every channel in an org is deny-by-default. Phase 118's allow-by-default stays for mapped channels. |
| Target | `alias` + `profile`, full GitHub semantics (warm / resume / cold-create) | Same vocabulary as `github.events` and `webhooks:`. |
| Non-listed sender | Silent drop | Matches Phase 118; never confirms who is listed. |
| Thread follow-ups | Allowlist still enforced | Reach into an org-wide bot is by named person, including inside an established thread. |
| Identity | Email, resolved at event time via `users.info` | Deactivation and list edits take effect without a `km init`; a lookup failure is a deny, not a hole. |
| Rejected: wildcard sandbox row | — | Lies to `km list`, `km doctor`, the running-channels lister, and the Phase 118 per-sandbox allowlist; has no place for an email gate. |
| Rejected: resolve emails at `km init` | — | Stale until re-init; a failed lookup at init must be a hard error or a silent hole. |

## 3. Configuration

New block in `km-config.yaml`, meaningful only on the front-door install (the one that
receives raw Slack events; a peer that sets it is a no-op because peers only ever see
relayed requests, which are terminal):

```yaml
slack:
  default_route:
    alias: ir-bot                     # required
    profile: profiles/ir-bot.yaml     # required; cold-create source
    allow_emails:                     # required, non-empty, or the route is off
      - kurt@example.com
      - analyst@example.com
```

- `internal/app/config.SlackConfig` gains `DefaultRoute *SlackDefaultRoute` with
  `Alias`, `Profile`, `AllowEmails []string`.
- Three new entries in the v2→v merge list (`slack.default_route.alias`,
  `slack.default_route.profile`, `slack.default_route.allow_emails`) with the existing
  merge-list regression guard extended. This footgun has bitten twice; the guard is
  not optional.
- `ExportTerragruntEnvVars` exports `KM_SLACK_DEFAULT_ROUTE_ALIAS`,
  `KM_SLACK_DEFAULT_ROUTE_PROFILE`, and `KM_SLACK_DEFAULT_ROUTE_ALLOW` (comma-joined,
  lowercased, trimmed). All three are exported or none: a block with an empty
  `allow_emails` fails `km init` with a message naming the key and the deny-by-default
  rule. A block whose profile does not exist, or whose profile lacks
  `notification.slack.enabled` and `notification.slack.inbound.enabled`, fails the same
  way, because a cold-created box without an inbound queue can never answer.
- `slack.allow` (Phase 118, Slack user IDs, allow-by-default) is deliberately not reused.
  Two meanings under one key is a trap.

## 4. Identity

The bridge resolves the sender with `users.info` (new `pkg/slack.Client.UserInfo`,
form-encoded like `users.lookupByEmail`, same `callForm` path). It reads
`user.profile.email`, lowercases it, and checks membership in the allowlist.

- Deny on: API error, missing `email` field, `deleted: true`, `is_bot: true`, empty
  allowlist.
- In-memory cache keyed by user id, 15 minute TTL, storing allows only. A deny is
  never cached, so a transient Slack error costs one event and cannot pin a person
  out.
- Requires `users:read` and `users:read.email`, both already in the manifest.
- Removing an email takes effect on the next `km init` plus at most one cache TTL on a
  warm Lambda. `km doctor` says so in the route group.

## 5. Events handler flow

Nothing before the `FetchByChannel` miss changes, and nothing on the owned path
changes. The miss branch (`events_handler.go` step 5) becomes a ladder:

1. **Thread continuity.** If `msg.ThreadTS != ""`, `Threads.LookupSandbox(channel,
   thread_ts)`. A hit means the bridge routed this thread before: fetch routing info by
   sandbox id (new `SandboxByIDFetcher.FetchBySandboxID`, a `GetItem` on the sandboxes
   table returning the same `SandboxRoutingInfo`) and continue at step 4 with the
   mention check skipped. Runs before the relay so a follow-up never fans out to peers.
   If the row's sandbox id no longer matches the alias's current sandbox (alias
   recreated), refresh the row rather than enqueueing to a dead queue, mirroring the
   GitHub bridge's `InvalidateStaleSession`.
2. **Relay, unchanged.** `x-km-relayed` present and no owner: terminal
   `{claimed:false, channels:[...]}`. Front door with a `Relayer`: broadcast and tally;
   any `claimed:true` ends processing.
3. **Route selection.** No route configured: `maybePostOrphanReply` exactly as today.
   Route configured: the orphan reply is retired on this install and the ladder
   continues. (Replying to a non-listed sender would contradict the silent-drop
   decision, and a listed sender gets a dispatch, so there is nobody left to hint.)
4. **Gate.** Mention required when `channel_type` is `channel`, `group`, or `mpim`;
   not required for `im`. `slackMessageEvent` gains a `ChannelType` field read from
   the event's `channel_type`. Then the email check from §4. Either failure: 200, debug
   log, nothing else.
5. **Resolve the alias** with a Slack-side `DynamoAliasResolver` (copy of the GitHub
   one: `alias-index` query, then read `slack_inbound_queue_url`, status,
   `action_frozen`, `frozen_reason`). Three-way split:
   - absent: `PutSandboxCreate(alias, profile, slackEnvelopeJSON)` where the envelope
     is the `InboundQueueBody` the running path would have enqueued, carried in a new
     `SandboxCreateDetail.SlackEnvelope` (`slack_envelope`)
   - stopped or paused: `Resumer.StartSandbox` then enqueue, with Phase 114's
     `ErrNoResumableInstance` handling intact
   - running: enqueue
   - frozen: post the existing frozen notice in the originating thread and stop
6. **Tail, shared with the owned path.** Dedup on `event_id`; `Threads.Upsert(channel,
   thread_ts, sandbox_id)` so step 1 works next time; enqueue `InboundQueueBody` with
   the originating `channel`; 👀 on `msg.TS`. Files go through the existing download
   fork untouched.

Not in scope: per-channel rules (one route was the ask; `github.events` is the pattern
if that changes), a "booting, hold on" message on cold-create (GitHub posts none; 👀 is
the acknowledgement), cooldown or rate limiting on the route (the Phase 121 action
quota is the backstop).

## 6. Reply routing

Two halves that ship together.

### 6.1 Poller (`pkg/compiler/userdata.go`, `km-slack-inbound-poller`)

- `REPLY_CHANNEL="$CHANNEL"` immediately after the envelope parse.
- Every post site inside the turn uses `$REPLY_CHANNEL` instead of the boot-time
  `$KM_SLACK_CHANNEL_ID`.
- The three dispatch blocks (codex resume, codex first, claude) export
  `KM_SLACK_CHANNEL_ID='$REPLY_CHANNEL'` alongside `KM_SLACK_THREAD_TS` and
  `KM_SLACK_SENDER_ID`, so the notify-hook's streaming posts and `km-slack reply` inside
  the agent's turn land in the right place with no change to those binaries.
- The sender preamble becomes `[Slack] From: <@U…> in <#C…>`.
- The Phase 70 cross-agent handoff creates a new top-level message, which §6.2 refuses
  in a routed channel. In a routed thread the poller skips the handoff, keeps the
  current agent, and logs why. Agent switching is unchanged in mapped channels.
- For a mapped channel `$REPLY_CHANNEL == $KM_SLACK_CHANNEL_ID`, so behaviour there is
  unchanged. Profiles without Slack inbound render byte-identically. The frozen pre-92
  `learn.v2` golden carries the poller and is hand-patched for exactly these hunks,
  never re-captured. `userdata_additional_volume_only.golden.sh` regenerates normally.

### 6.2 Bridge outbound authorizer (`pkg/slack/bridge/handler.go` step 6)

A non-operator envelope is authorized when either:

- `env.Channel == OwnedChannel(env.SenderID)` (today's rule), or
- `env.ThreadTS != ""` and `Threads.LookupSandbox(env.Channel, env.ThreadTS) ==
  env.SenderID`.

The thread row is written only by the events handler at dispatch, so a sandbox can
post only into threads the bridge routed to it. The sandbox-never-reads-DDB boundary
holds. A top-level post into a routed channel, and `permalink` or `update` there
(neither carries a thread), stay `403 channel_mismatch`.

## 7. Infrastructure

- **`infra/modules/lambda-slack-bridge/v1.0.0`**, edited in place (additive, empty
  defaults, the Phase 100 precedent): variables `slack_default_route_alias`,
  `slack_default_route_profile`, `slack_default_route_allow`; env
  `KM_SLACK_DEFAULT_ROUTE_*`; IAM `dynamodb:Query` on
  `${sandboxes_table_arn}/index/alias-index` and `events:PutEvents` on the default bus,
  both copied from the GitHub bridge module. Already granted: `GetItem` on the sandboxes
  table, `Query` on the threads table and its index, SQS send on
  `{prefix}-slack-inbound-*.fifo`, EC2 describe/start.
- **`infra/live/use1/lambda-slack-bridge/terragrunt.hcl`**: three `get_env` inputs.
- **`cmd/create-handler`**: `SlackEnvelope` on `SandboxCreateDetail`; a third drain
  into `awspkg.SlackInboundQueueName(prefix, id)` next to the GitHub and H1 drains.
  Verify the create-handler role's SQS send grant covers `slack-inbound-*`; add it if
  the grant is source-specific.
- **Manifest** (`slack_manifest_template.json`): scopes `im:history`, `mpim:history`;
  events `message.im`, `message.mpim`. No `im:write` (the bot never opens a DM). One-time
  reinstall plus `km slack rotate-token` on the front-door install, the Phase 72 shape.
  `km slack init`'s scope warning list learns `im:history`.
- **`km doctor`**, new group `Slack default route`, skipped silently when the block is
  absent: profile exists and has Slack inbound; `allow_emails` non-empty and each
  resolves via `users.lookupByEmail` (WARN per miss); bot scopes cache contains
  `im:history` (WARN); alias resolves or is reported "absent, will cold-create".
- No new tables, queues, or Lambdas.

## 8. Deploy and rollout

1. Edit `km-config.yaml` on the front-door install; confirm the profile has Slack
   inbound enabled.
2. `make build` (config keys, init export and validation, doctor). A stale binary
   exports nothing and the route never turns on.
3. `make build-lambdas` (bridge: ladder, email gate, thread-scoped authz;
   create-handler: `slack_envelope` drain, poller userdata).
4. `km init --dry-run=false`. Not `--sidecars` (rebuilds neither zip). Not `--slack`
   alone (bridge env only, not the create-handler).
5. `km slack manifest` → paste → reinstall → `km slack rotate-token --bot-token <new>`.
   Until this step channels work and DMs are never delivered.
6. Default sandbox: `km destroy && km create` if it exists, so it boots with the new
   poller. Otherwise the first listed mention cold-creates it with a few minutes'
   latency on the first reply.
7. `km doctor`, then the live UAT in §10.

Half-deploy outcomes (all quiet, none dangerous): new bridge + old poller → replies
land in the sandbox's own channel; old bridge + new poller → route never fires; new
bridge + old create-handler → box is created, `slack_envelope` ignored, first prompt
lost, sender re-mentions.

Docs: `docs/slack-notifications.md` § Default route; `CLAUDE.md` phase block. No
`klanker:slack` change and no plugin bump: the agent already replies via the per-turn
`KM_SLACK_CHANNEL_ID` / `KM_SLACK_THREAD_TS`.

## 9. Tests

**Events handler** (`pkg/slack/bridge`, existing fakes):
- unmapped channel + mention + listed email → enqueued to the alias's queue, thread row
  written, 👀
- same, email not listed → 200, no enqueue, no reaction, no thread row, no orphan reply
- same, no mention → dropped; same text with `channel_type: im` → dispatched
- thread reply with a thread row → dispatched without mention, relay not called;
  with the row but a non-listed email → dropped
- route not configured → Phase 96 orphan reply fires exactly as the current test asserts
- relay returns a claim → route never evaluated
- alias absent / stopped / running / frozen → cold-create event / resume + enqueue /
  enqueue / frozen notice
- `users.info` error, missing email, deleted user → deny; a cached allow is reused; a
  deny is never cached

**Outbound handler:** own channel allowed; foreign channel with matching thread row
allowed; foreign channel with no thread, mismatched row, `permalink`, `update` → 403.

**Config/init:** the three keys survive the v2→v merge (existing guard extended);
empty `allow_emails` fails the export with the stated message; a profile without Slack
inbound fails it.

**Poller userdata:** rendered bash executed under `bash` with a fake `km-slack` that
records arguments, asserting the reply post names the envelope's channel and the
dispatch blocks export the per-turn channel; the handoff skip in a routed thread the
same way. Byte-identity for non-Slack profiles stays pinned by the existing goldens.

**Infrastructure guards:** `pkg/terragrunt` pairs each new module env var with a
`km init` export; the IAM test asserts `alias-index` and `events:PutEvents` and is
verified to fail with either removed; the manifest test asserts the two scopes and two
events.

## 10. Live UAT checklist

1. New public channel, `/invite` the bot, mention with a task → 👀, reply in thread in
   that channel.
2. Reply in that thread without a mention → same sandbox, same agent session.
3. A non-listed user mentions the bot in the same channel → nothing.
4. A non-listed user replies inside the established thread → nothing.
5. DM the bot without a mention → dispatched, reply in the DM.
6. Private channel: invite, mention → dispatched.
7. Stop the default sandbox, mention again → auto-resume, reply arrives.
8. Destroy the default sandbox, mention again → cold-create, first prompt answered on
   boot.
9. Recreate under the same alias, reply in an old thread → new sandbox answers, no
   dead-queue enqueue.
10. Mention in a channel a peer install owns → peer handles it, front door's route
    never fires.
11. `claude:` / `codex:` prefix inside a routed thread → current agent continues, log
    line names the skipped handoff.

## 11. Open items to settle during implementation

- Whether the create-handler's SQS send grant already covers `slack-inbound-*` queues.
- Whether `km-slack update` is used by any in-turn path that would now target a routed
  channel; if so it needs `thread_ts` on its envelope to pass §6.2.
- Slack Connect channels from another workspace: `users.info` returns the external
  member's email so the gate applies; reaction and reply behaviour there is unverified
  and is a UAT addition if an operator has such a channel.

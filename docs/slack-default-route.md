# Slack default route and strict mention-only threads

This document is the standalone account of the change that landed on
`feat/slack-default-route` on 2026-10-16. It explains what an operator can now do, why
each design choice was made, how a message travels end to end, what changed in the code,
what the change does and does not protect, how to deploy it, and how to operate it. The
operator runbook lives in `docs/slack-notifications.md` § Default route and § Phase 91
Strict mode; this document links to it rather than repeating its tables, and goes further
on rationale, code and security posture.

## 1. Summary

A person on a named list can now invite the km bot to any Slack channel, public or
private, or DM it, @-mention it with a task, and get the answer **in a thread in that same
channel**. Follow-ups in the thread continue the same conversation with the same sandbox
and the same agent session. Every such conversation lands on one designated sandbox, the
"default route", which km keeps warm when it is running, resumes when it is stopped, and
cold-creates from a named profile when it does not exist. The second half of the change
adds an optional strict mode, `notification.slack.inbound.threadBypass: false`, which makes
the bot require an @-mention inside threads too, and hands it the human conversation it
missed since its last turn when it is mentioned again.

It was built because the bridge could only dispatch a message whose channel was already
bound to a sandbox. A team wanting to pull the bot into an incident channel, or a person
wanting to DM it a quick question, had no path at all; the best the bridge could do was the
Phase 96 orphan reply naming the `#sb-{alias}-{profile}` convention. The GitHub bridge
already had the opposite posture for one source, sending matching webhooks to a named alias
and cold-creating it if absent. This change gives the Slack bridge the same thing for
unmapped conversations, gated on a named list of email addresses. Strict mode exists
because Phase 91.3 had made the thread bypass unconditional, so in a shared channel the bot
joined every human-to-human exchange in any thread it had been pulled into once.

**Deploy surface:** `make build` + `make build-lambdas` + `km init --dry-run=false`, then a
Slack App manifest update with reinstall and `km slack rotate-token` for DMs, then a
recreate of the default sandbox. Dormant by default: with no `slack.default_route` block
and no `threadBypass` field, the bridge and every sandbox behave as before. Section 8 has
the exact sequence.

## 2. Motivation and prior behaviour

**What the bridge did on an unmapped channel.** The events handler resolves the channel a
message arrived in with `FetchByChannel` against the `slack_channel_id-index` GSI on the
sandboxes table. On a miss, two older mechanisms applied. Phase 95's federated relay let
the front-door install broadcast the raw event to `slack.peer_bridges`, so a channel owned
by a sibling install still reached its owner; a relayed request is terminal and is never
re-relayed. Phase 96 made that relay claim-aware: each peer answers `{claimed, channels}`,
and when nobody claims the channel and `slack.default_router: true` is set on the front
door, the bridge posts one threaded hint listing running sandbox channels, with a one-hour
per-channel cooldown. Nothing was dispatched. DMs were never delivered at all, because the
manifest requested neither the `im:history`/`mpim:history` scopes nor the `message.im` and
`message.mpim` events.

**What the GitHub bridge already did.** `github.events` rules (Phase 115) match any
non-comment webhook and dispatch an expanded prompt to a named `alias`: a FIFO enqueue when
the box is running, a resume then enqueue when it is stopped, and an EventBridge
`SandboxCreate` from a pre-staged profile when it is absent. The webhook ingress bridge
(Phase 127) uses the same vocabulary. The Slack default route copies that shape so an
operator learns one dialect for three bridges.

**Two findings from the design pass shaped the work beyond the bridge.** First, the inbound
poller replied to the box's own channel. Every post site in `km-slack-inbound-poller` used
the boot-time `KM_SLACK_CHANNEL_ID`, so even a correctly routed turn would have answered in
`#sb-…` instead of the channel the person wrote in. Second, the bridge refused posts to
foreign channels. The outbound authorizer in `pkg/slack/bridge/handler.go` admitted a
sandbox envelope only when its channel equalled the sandbox's owned channel, so a reply into
an invited channel would have come back `403 channel_mismatch`. Both had to change in
lockstep with the events handler, which is why the poller and the authorizer are part of
this change rather than follow-ups.

## 3. Design decisions

Each row is a decision taken in the design conversation or ruled during implementation.
The spec carries the original table for Part A (§2) and Part B (§B2); the implementation
ledger records the rulings.

| Decision | Choice | Rationale |
|---|---|---|
| Reach | Public channels, private channels, DMs, group DMs | The operator's call. DMs cost two scopes and two events. `im:write` was added on 2026-10-17 for `km-slack dm` (bot-initiated DM, allowlist-gated; see `docs/slack-notifications.md` § Bot-initiated DMs). |
| Empty allowlist | The route is OFF | A bot reachable from every channel in an org must be deny-by-default. Phase 118's allow-by-default empty list stays for mapped channels, which is why `slack.allow` was not reused: one key with two opposite empty-list meanings is a trap. Since 2026-10-17 "empty" means neither the route profile's `allowEmails` nor `allow_emails` lists anyone. |
| Allowlist entry forms (2026-10-17) | An exact address or exactly `*@<domain>`; any other `*` fails `km init` and `km validate`; subdomains do not match | Admits a whole domain without listing people, with one unambiguous form and no glob engine. A typo cannot widen a list. |
| Per-sandbox email list (2026-10-17) | `notification.slack.inbound.allowEmails` REPLACES the install list for that sandbox on every path: its own `#sb-…` channel, routed threads, DM threads, strict-context trust, and `km-slack dm` targets it sends. Absent means the install list on route paths and no email gate on the owned path. Explicitly empty is a `km validate` error | An override is simpler to reason about than an intersection, and lets one klanker be stricter or differently scoped than the install. "Empty means everyone" is the Phase 118 rule and was deliberately not reused. The Phase 118 user-id list still applies in addition (AND). |
| The route's two lists (2026-10-17) | `km init` exports `allow_emails` as `KM_SLACK_DEFAULT_ROUTE_ALLOW` (the install list: DM targets, non-route boxes) and the route profile's `allowEmails` as `KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW` (cold-create and the alias's box, falling back to the install list); neither stops `km init` and `km slack init` | The mention that cold-creates the box is gated by the same list the box enforces afterwards, so nobody the profile does not name can start it, and narrowing the route profile narrows nothing else. The cost: changing the profile list needs `km init` (env) and a recreate (row). |
| Reaction quiet period (2026-10-17) | A thread reply reacts 👀 only after more than N seconds since the thread's `last_dispatch_ts`; top-level always reacts; `react_always: false` wins; default 180 s, `0` disables | The 👀 on every reply of a live exchange is noise. The boundary is already stored and advanced on every delivery, and a missing row or read error reacts, because a missing reaction is preferable to a silent bot. |
| Identity | Sender email resolved at event time via `users.info` | Deactivation and list edits take effect without a `km init` of Slack ids, and a lookup failure is a deny rather than a hole. Resolving ids at `km init` was rejected because it goes stale until re-init and a failed lookup at init must be either a hard error or a silent hole. |
| Rejected: a wildcard sandbox row | Not built | A fake row owning every channel would lie to `km list`, `km doctor`, the running-channels lister and the Phase 118 per-sandbox allowlist, and has no place for an email gate. |
| Target semantics | `alias` + `profile`: warm, resume, cold-create | The same vocabulary as `github.events` and `webhooks:`. |
| Non-listed sender | Silent drop | Matches Phase 118. A reply, a reaction or a hint would confirm who is and is not on the list. |
| Thread follow-ups | The email gate still applies | Reach into an org-wide bot is by named person, including inside a thread a listed person started. |
| Phase 96 reply when a route exists | Retired on that install | A listed sender gets a dispatch and an unlisted one a silent drop, so nobody is left to hint. The orphan reply is unchanged when no route is configured. |
| One spec, one plan for Parts A and B | Merged | Both touch the same handler step, queue body, thread store, row parser and poller preamble. Strict mode and context are separate commits so either half can be reverted. |
| Strict mode in routed threads | Strict governs routed threads too | Part A's thread continuity is itself a thread bypass. One knob with one meaning: a strict default sandbox needs a mention for routed follow-ups as well, and the email gate stays on top. |
| Context trust predicate | Whichever gate governs the thread | In a mapped channel, the Phase 118 user-id list (empty means every human). In a routed thread, the email resolver per unique author. Authors who fail the gate are counted, and their text and files never leave the bridge. |
| `MarkDispatched` timing | After every successful delivery, enqueue or cold-create publish, in every mode | A sandbox later switched to strict already has a correct boundary. A failed delivery leaves the boundary unchanged so context is never lost. |
| Outbound authorization verbs | Only `post` and `upload` qualify for the routed-thread rule | The bot token is shared across sandboxes. `update` and `permalink` are not anchored to a thread, so admitting them would let one sandbox edit another's post in a shared routed channel. A repo-wide search found no in-turn caller of `km-slack update`. |
| Cold-create sandbox id | Minted by the bridge before the files fork | Attachments are stored under `slack-inbound/<sandbox>/<thread_ts>/`. Minting in the EventBridge adapter left the key with an empty sandbox segment. The publisher now takes the id as an argument. |
| Thread row write | Only after a successful delivery, on every path | A row for a turn that never dispatched would open the Phase 91.3 bypass on a thread the bot never answered. This also changes the owned-channel path: after an SQS failure the sender re-mentions, which they would do anyway. |
| Continuity to a gone box | The bypass decision comes from the alias's current box; strict when the alias is absent too | Otherwise a recreated strict box would inherit a bypass from the row of a box that no longer exists. |
| Rebind timing | Rebind the thread row to the alias's current box only after delivery succeeds | A frozen box or a failed delivery must never rewrite the row. |
| Context collection placement | After `event_id` dedup, budgeted against the Lambda deadline | Dedup first means a Slack retry is absorbed before any `conversations.replies` call. Once the nonce is consumed a timed-out turn is lost silently, so collection is capped at 20 s and shrunk to leave 35 s of the 60 s Lambda timeout for delivery, resume and reaction; it is skipped when nothing is left. |
| Context predicate signature | `func(ctx, userID) bool`, called with the context sub-deadline | The plan's `func(string) bool` let per-author `users.info` calls escape the 20 s budget. The spec's error-handling table wins over the plan text. |
| Pre-staged profile shape | Flattened when the profile uses `extends:` | The create-handler Lambda has no `profiles/base/**` fragments, so a raw child profile fails cold-create with "profile base/... not found". A monolithic profile is uploaded verbatim. |
| Merge-list entry | One `slack.default_route` block entry, loaded with `UnmarshalKey` | The `github` block precedent. The spec originally said three leaf entries; one block entry shipped and the spec was corrected. |

## 4. How it works, end to end

### 4.1 The routing ladder

Everything up to the channel lookup is unchanged: signature verification, the url
verification challenge, the bot-loop filter. When `FetchByChannel` finds an owner, the
owned path runs as before. When it misses, the handler walks a six-step ladder.

The first step is thread continuity. If the message is a thread reply, the handler looks
the `(channel, thread_ts)` pair up in `km-slack-threads`. A hit means the front door routed
this thread before, so it fetches the routing info for that row's sandbox by id and skips
the relay entirely; a follow-up never fans out to peers. Whether the mention is skipped
depends on the receiving box's `threadBypass` (Part B). If the row's box is gone, the
decision comes from the alias's current box, and defaults to strict when the alias is absent
as well. If the alias now points at a different box, the turn goes to the current one and
the row is rebound after delivery.

The second step is the relay, unchanged. A relayed request with no owner returns the Phase
96 claim response and stops. The front door broadcasts to its peers and stops if any peer
claims the channel, so an install that owns a channel always wins over the route.

The third step is route selection. With no route configured, the Phase 96 orphan reply runs
exactly as before. With a route configured, the orphan reply is retired on this install and
the ladder continues.

The fourth step is the gate. A mention is required when the event's `channel_type` is
`channel`, `group` or `mpim`, and not for `im`. Then the sender's email is resolved through
the cached `users.info` lookup and checked against the route's effective list (the route
profile's `allowEmails` when it has one, else `allow_emails`). Either failure returns 200 with
an Info log line and nothing else. On the enqueue and resume branches the target box's own
`allowEmails`, when its row carries one, decides instead; the cold-create branch is gated by
the route list, which is that same profile's list when it sets one.

The fifth step resolves the alias through the `alias-index` GSI. An absent alias produces a
cold-create: the bridge mints the `sl-…` id, downloads any attached files under it, and
publishes `SandboxCreate` with the queue body carried as `slack_envelope`. A stopped or
paused box is resumed through the Phase 114 path, with its `ErrNoResumableInstance` handling
intact, and then enqueued. A running box is enqueued. A frozen box (Phase 121) gets the
frozen notice in the originating thread and no dispatch. An ambiguous alias is a resolve
error and a drop.

The sixth step is the tail shared with owned channels: `event_id` dedup, strict-mode context
collection when it applies, the files fork, delivery with the originating channel on the
queue body, 👀 on the message, then the thread write and `MarkDispatched` on success. A
failed delivery writes no row, advances no boundary and adds no reaction. Since 2026-10-17 the
👀 on a thread reply is subject to the quiet period, decided before delivery because delivery
advances the boundary it is measured from.

```
 Slack            bridge Lambda               DynamoDB / SQS / EventBridge       create-handler      sandbox poller
   |  event (C0…)       |                                |                              |                   |
   |------------------->| FetchByChannel(C0…) ---------->| miss                         |                   |
   |                    | LookupSandbox(C0…, thread_ts)->| (continuity, if a reply)     |                   |
   |                    | relay to peers (no claim)      |                              |                   |
   |  users.info(U0…)   |                                |                              |                   |
   |<-------------------| email in allow_emails?         |                              |                   |
   |                    | ResolveAlias(ir-bot) --------->| alias-index                  |                   |
   |                    | nonce(event_id) -------------->| nonces table                 |                   |
   |                    |-- running/resumed: SendMessage>| slack-inbound-<id>.fifo ------------------------>| turn
   |                    |-- absent: PutEvents ---------->| SandboxCreate{slack_envelope}>| provision box     |
   |                    |                                |<---- drain envelope ---------|                   |
   |                    |                                | slack-inbound-<id>.fifo ------------------------>| turn on boot
   |  reactions.add 👀   |                                |                              |                   |
   |<-------------------| Upsert/Rebind + MarkDispatched>| km-slack-threads             |                   |
   |  chat.postMessage(C0…, thread_ts) via bridge outbound authorizer <---------------------------------------|
```

### 4.2 The strict-mode path

On an owned channel, step 5b of the handler is the Phase 91 mention-only check. It now
computes `bypassAllowed` from the sandbox row's `slack_thread_bypass` (absent means `true`).
Only when the bypass is allowed does the existing engaged-thread lookup let an unmentioned
reply through. A strict, unmentioned reply returns before dedup, before the thread write and
before enqueue: no 👀, no turn, no nonce. On a routed thread the continuity step uses the
same predicate, so strict continuity still finds the sandbox but does not skip the mention.

When a strict thread reply does carry a mention, the handler collects context after dedup.
`DispatchBoundary` reads the row's `last_dispatch_ts`; without one the window starts at the
thread root, root included. `SlackThreadReader.Replies` calls `conversations.replies` with
`limit=200` and an exclusive `oldest`, a single page. `BuildThreadContext` drops the
trigger, anything with `bot_id`, subtype `bot_message`, or the bot's own user id, and
authors who fail the trust predicate, which are counted into `omitted_non_allow`. It keeps
at most 100 messages and 32 KiB of text, chosen newest-first and emitted chronologically,
with the dropped count in `truncated_older`. Files are capped at 10 and 50 MiB in total,
newest-first, with overflow names in `files_omitted`; they are downloaded through the
existing `S3FileDownloader` under the same per-thread prefix as trigger attachments. A queue
body over 240 KiB is trimmed oldest-first, and the trigger is never dropped. The result
rides on `InboundQueueBody.thread_context`, which old pollers ignore.

Context never blocks the turn. A boundary read failure falls back to the root; a
`conversations.replies` failure or `ratelimited` dispatches with `thread_context.error`; a
file failure becomes a per-file entry in `file_errors`; collection skipped for lack of time
dispatches with an error reason. The poller renders a header above the sender preamble, or a
single "Thread context unavailable" line when the error field is set. The rendered shape is
shown in `docs/slack-notifications.md` § Strict mode.

### 4.3 The reply path

The poller sets `REPLY_CHANNEL` from the queue body's `channel` right after it parses the
envelope, and all six in-turn post sites use it instead of the boot-time channel. The three
dispatch blocks (codex resume, codex first turn, claude) export
`KM_SLACK_CHANNEL_ID='$REPLY_CHANNEL'` per turn alongside `KM_SLACK_THREAD_TS` and
`KM_SLACK_SENDER_ID`, so the notify-hook's streaming and an agent's own `km-slack reply` land
in the right thread with no change to either binary. The sender preamble now names the
channel, `[Slack] From: <@U0…> in <#C0…>`. In a mapped channel the two ids are equal, so
nothing changes there. The Phase 70 `claude:`/`codex:` handoff posts a new top-level
message, which the authorizer refuses in a foreign channel, so in a routed thread the poller
keeps the current agent and logs why.

On the bridge, a sandbox envelope whose channel is not its owned channel now gets a second
chance through `threadRoutedToSender`: the action must be `post` or `upload`, the envelope
must carry `thread_ts`, and the `km-slack-threads` row for that pair must name the sending
sandbox. A pass logs `bridge: authz_routed_thread`. Everything else stays
`403 channel_mismatch`, including a top-level post into a routed channel. `km-slack dm
--from-thread` (2026-10-17) reuses the same check for its originating thread, probing it as a
`post`. The per-verb rules are in `docs/slack-notifications.md` § km-slack verb reference.

## 5. What changed in the code

The full file list is `git diff --stat 13ff3de3..HEAD`. This table groups the load-bearing
changes.

| Area | File / function | What it does now |
|---|---|---|
| Config & init | `internal/app/config/config.go` `SlackDefaultRoute`, `NormalizedEmails`, `Validate` | Parses `slack.default_route`; trims and lowercases emails; enforces alias, profile and a non-empty, `@`-bearing email list. One merge-list entry. |
| Config & init | `internal/app/cmd/init.go` `exportSlackDefaultRouteEnv` | Exports `KM_SLACK_DEFAULT_ROUTE_{ALIAS,PROFILE,ALLOW}`, all three or none, with the env-wins drift WARN. |
| Config & init | `internal/app/cmd/init.go` `ValidateSlackDefaultRouteProfile` | Resolves the profile (search paths and `extends:` included) and fails `km init` unless it enables Slack and Slack inbound. |
| Config & init | `internal/app/cmd/init.go` `PreStageSlackDefaultRouteProfile` | Uploads the profile to `slack-profiles/{slug}/.km-profile.yaml`, flattened when it uses `extends:`; a missing profile errors and uploads nothing. |
| Config & init | `internal/app/cmd/create_slack_inbound.go` | Writes `slack_thread_bypass` on the sandbox row when the profile sets it; `makeUpsertSlackThread` widened to the update-item interface. |
| Profile & metadata | `pkg/profile/types.go`, schema, `validate.go` | New `notification.slack.inbound.threadBypass` (`*bool`) with two semantic warnings. |
| Profile & metadata | `pkg/aws/metadata.go`, `sandbox_dynamo.go` | `SlackThreadBypass` round-trips through `SandboxMetadata` so pause, resume, extend and TTL never strip it. |
| Bridge events handler | `pkg/slack/bridge/events_handler.go` | The six-step ladder on the channel-miss path; strict predicate in step 5b and in continuity; context collection after dedup with the deadline budget; thread write, rebind and `MarkDispatched` after delivery on every path. |
| Bridge events handler | `pkg/slack/bridge/default_route.go` | `DefaultRoute.Allows`, `requiresMention` by `channel_type`, and `CachedSenderEmailFetcher`, which caches allows for 15 minutes and never caches a deny. |
| Bridge events handler | `pkg/slack/bridge/thread_context.go`, `thread_reader.go` | `BuildThreadContext`, `TrimContextToFit` and the cap constants; the single-page `conversations.replies` reader. |
| Bridge events handler | `events_types.go`, `events_interfaces.go` | `channel_type` on the message event, `thread_context` on the queue body, and the new interfaces: by-id fetcher, alias resolver, create publisher, sender-email fetcher, thread reader, and `Rebind`/`DispatchBoundary`/`MarkDispatched` on the thread store. |
| Bridge adapters | `pkg/slack/bridge/aws_adapters.go` | `DDBSandboxByID` (same field parse as by-channel, plus `ThreadBypass`); `DDBAliasResolver` (absent, found, ambiguous); `EventBridgeAdapter` publishing `slack_envelope` with the caller's id; `SlackSenderEmailAdapter`; the thread store's conditional, monotonic `MarkDispatched`. |
| Bridge adapters | `pkg/slack/client.go` `UserInfo` | `users.info` returning email, `deleted` and `is_bot`. |
| Bridge adapters | `cmd/km-slack-bridge/main.go` `wireDefaultRoute` | Wires the route from env only when all three vars are set; a partial set logs a WARN and stays OFF. The thread reader is wired unconditionally and stays dormant until a row is strict. |
| Bridge outbound authorizer | `pkg/slack/bridge/handler.go` `threadRoutedToSender` | Admits `post` and `upload` into a foreign channel only when the thread row names the sender. |
| create-handler | `cmd/create-handler/main.go` | `SlackEnvelope` on the create detail, drained into `{prefix}-slack-inbound-{id}.fifo` after provisioning; a failure is a non-fatal WARN. |
| create-handler | `pkg/aws/eventbridge.go` | `slack_envelope` on `SandboxCreateDetail`. |
| Poller userdata | `pkg/compiler/userdata.go` | `REPLY_CHANNEL` on every post site and per-turn `KM_SLACK_CHANNEL_ID`; channel-naming preamble; handoff skip in routed threads; the context header and context attachment mirroring. The frozen pre-92 golden is hand-patched for these hunks only. |
| Terraform & IAM | `infra/modules/lambda-slack-bridge/v1.0.0` | Three variables and env vars; `dynamodb:Query` on `alias-index`; `events:PutEvents` on the default bus; `dynamodb:UpdateItem` on `km-slack-threads`. Edited in place, additive. |
| Terraform & IAM | `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | Three `get_env` inputs. |
| Terraform & IAM | `infra/modules/km-operator-policy/v1.0.0/main.tf` | Create-handler `sqs:SendMessage` on `{prefix}-slack-inbound-*.fifo`, which it lacked. |
| Manifest | `slack_manifest_template.json`, `slack.go` | `im:history`, `mpim:history`, `message.im`, `message.mpim`, and `features.app_home` with the Messages tab enabled and writable (without it Slack refuses DMs client-side); `km slack init`'s scope check requires both history scopes. |
| `km doctor` | `internal/app/cmd/doctor_slack.go` `checkSlackDefaultRoute` | The `Slack default route` group: profile, allowlist and scopes results from one evaluation. |
| Profile | `profiles/ir-bot.yaml` | A shipped default-route target: Bedrock on both knobs so a cold-created box needs no login, per-sandbox Slack with inbound, mention-only, three concurrent threads. |

### 5.1 Tests and what they pin

| Test file | What it pins |
|---|---|
| `internal/app/config/config_slack_default_route_test.go` | `TestLoadSlackDefaultRoute_Set` is the merge-list guard: it fails with a message naming `slack.default_route` if the entry is removed. `TestSlackDefaultRoute_Validate` pins the deny-by-default rule. |
| `internal/app/cmd/init_slack_default_route_test.go` | All-three-or-none export, profile validation, flattening of `extends:`, search-path lookup, and that a missing profile errors and uploads nothing. |
| `internal/app/cmd/doctor_slack_default_route_test.go` | Skipped when absent, all green, the strict-profile note, and each WARN. |
| `internal/app/cmd/slack_manifest_test.go` | The two scopes and two events; the init scope check requires `im:history`. |
| `cmd/create-handler/main_test.go` | A Slack envelope is enqueued to the slack-inbound queue; an empty one enqueues nothing. |
| `cmd/km-slack-bridge/default_route_wiring_test.go` | `TestWireDefaultRoute_AllThreeOrNothing`. |
| `pkg/slack/bridge/events_handler_default_route_test.go` | The ladder matrix: listed and unlisted senders, DM without mention, continuity without relay, email gate on continuity, rebind on alias move, Phase 96 unchanged, peer claim wins, cold-create carries the envelope and keys files on the minted id, resume then enqueue, frozen notice, lookup failure is deny, relayed stays terminal, `MarkDispatched` exactly once, no row or reaction on failure, the gone-box bypass rules, and no rebind on frozen or failed send. |
| `pkg/slack/bridge/events_handler_strict_test.go` | The dispatch matrix across `threadBypass` values; context respects the allowlist; context runs after dedup; reader error still dispatches; context files; the files-sync branch carries context; routed threads; the bounded predicate context; deadline budgeting; file downloads stop near the deadline; per-author memoisation; `FileError` wire keys. |
| `pkg/slack/bridge/handler_thread_authz_test.go` | The outbound matrix: routed thread allowed, wrong sandbox row, no thread, nil store, and only `post` and `upload`. |
| `pkg/slack/bridge/thread_context_test.go`, `thread_reader_test.go` | Filtering and counts, caps that keep the newest in chronological order, the file budget, oldest-first trimming that keeps the trigger; the reader's request shape and error mapping. |
| `pkg/slack/bridge/aws_adapters_*_test.go`, `default_route_test.go`, `routing_info_internal_test.go` | By-id parse parity with by-channel, alias found, absent and ambiguous, the publisher, the email adapter, conditional monotonic `MarkDispatched`, case-insensitive allow, nil route on empty emails, `requiresMention`, cache-success-only, `ThreadBypass` parsing. |
| `pkg/compiler/userdata_slack_reply_channel_test.go`, `userdata_slack_thread_context_test.go` | These execute the rendered poller bash under `bash` with a fake `km-slack` that records arguments: the reply channel comes from the envelope, every post uses it, dispatch exports it, the preamble names it, the handoff is skipped in a routed channel, and the context header renders with messages, files, counts, error and absence. Profiles without Slack inbound render byte-identically. |
| `pkg/terragrunt/slack_default_route_module_test.go` | Each `KM_SLACK_DEFAULT_ROUTE_*` env var is set from a module variable that the live unit feeds from `get_env`; the bridge IAM carries `alias-index` and `events:PutEvents`; the create-handler can send to slack-inbound. |
| `pkg/terragrunt/slack_threads_sandbox_role_guard_test.go` | `TestSandboxRole_ThreadsWriteGrantExcludesSandboxID` reads the pinned ec2spot module and fails on any threads-table Allow lacking a `dynamodb:Attributes` condition, on an attribute set containing `sandbox_id`, on a missing ReturnValues/Select pin, and on any wildcard DynamoDB write. See §6.4. |
| `pkg/profile/profile_thread_bypass_test.go`, `pkg/aws/sandbox_dynamo_thread_bypass_test.go`, `internal/app/cmd/create_slack_inbound_test.go`, `pkg/slack/client_userinfo_test.go` | Schema acceptance through YAML and the semantic warnings; the metadata round-trip; the create-time row write; `users.info` parsing and error return. |

## 6. Security posture

### 6.1 What the email gate bounds, and what it does not

The gate decides who can make the default sandbox run a turn from an unmapped conversation.
It applies to every turn, thread follow-ups included, and it fails closed: an API error, a
profile with no email, a deleted user, a bot user, or an empty list all deny, and a deny is
never cached. A listed person removed from the list stops being admitted after the next
`km init --dry-run=false` plus at most one 15-minute cache TTL on a warm Lambda; a Slack
deactivation stops them after at most the same TTL.

The gate is exactly as strong as the workspace's control of profile emails. In an SSO- or
admin-managed workspace that is strong. Where members can edit their own email, it rests on
Slack's verification of the new address. The gate does not authenticate a person beyond what
Slack asserts, and it does not limit what an admitted person can ask the agent to do; the
profile's own controls (egress allowlist, `privileged: false`, budget, the Phase 121 action
quota) are what bound that. Everyone on the list shares one sandbox and its `/workspace`,
so one listed person can see what another left there.

### 6.2 The shared bot token

Every sandbox's outbound Slack call goes through the bridge, which holds one bot token for
the whole install. A sandbox never handles that token, but anything the bridge admits is
posted as the same bot. That is why authorization is the bridge's job, why it keys on the
sandbox id in a signed envelope, and why verbs not anchored to a thread are refused in a
foreign channel: with a shared token, an unscoped `update` could edit another sandbox's
message in a shared routed channel.

### 6.3 The outbound authorization and its honest limit

The rule a sandbox's envelope must pass is either "this is my channel" or "this is a thread
the bridge routed to me, and the action is `post` or `upload`". The second half trusts a
`km-slack-threads` row as proof. That trust is sound only while nothing running on a sandbox
can write the table, because DynamoDB IAM cannot scope a grant by the `sandbox_id` attribute
of a row: any write grant lets every sandbox write any row. Rows are meant to be written by
bridge-side components only: the events handler at dispatch, and the create path's
ready announcement, which `km create` posts and the create-handler runs for remote creates.

### 6.4 The `ec2spot_region_lock` finding — closed in v1.8.0 (2026-10-17)

The final review found that the premise in §6.3 did not hold on most profiles. In
`infra/modules/ec2spot/v1.7.0/main.tf`, `aws_iam_role_policy.ec2spot_region_lock` is an
`Allow` of `Action "*"` on `Resource "*"` conditioned only on `aws:RequestedRegion`. It is
created whenever a profile sets `iam.allowedRegions`, and `profiles/base/platform.yaml` sets
it, so it is present on most shipped profiles. It is an allow, not a deny, so on those boxes
the sandbox role can reach `km-slack-threads` and every other DynamoDB table in the region,
plus nearly everything else the account's SCPs do not deny.

For this feature that means a sandbox on such a profile could write a forged thread row
naming itself and then post into any thread in any channel the bot is a member of, and
`threadRoutedToSender` would honour it. On those profiles the outbound gate bounds posting
to channels the bot is in, not to threads the bridge routed. On a profile without
`allowedRegions`, where the region lock is absent, a sandbox can post only into threads the
bridge routed to it.

The existing inbound poller already reads and writes `km-slack-threads` from the box with
`aws dynamodb get-item`, `put-item` and `update-item` for session persistence, Phase 119
idempotency and agent switching. Those calls work only because of the region lock; on a box
without it they would fail quietly, since each is `2>/dev/null || true`. The ledger records
this as a pre-existing item to verify in live UAT. It also means that narrowing the region
lock will break the poller's session writes unless they move behind the bridge first.

**How it closed.** `infra/modules/ec2spot/v1.8.0` replaced the `Allow */*` with a region Deny and explicit per-concern grants. The threads grant carries a `dynamodb:Attributes` condition that excludes `sandbox_id`, and the pollers no longer SET it. `LookupSandbox` treats a row without `sandbox_id` as unrouted, so a row the box creates authorizes nothing. `TestSandboxRole_ThreadsWriteGrantExcludesSandboxID` pins the grant. Residual, stated honestly: any box can still `UpdateItem` another thread's session attributes if it knows that thread's `(channel_id, thread_ts)`. Two of those attributes matter. A poisoned `last_assistant_msg` is pasted into the victim's handoff seed prompt, which is cross-sandbox prompt injection. A large `last_processed_event_ts` makes the victim's poller skip every later message as already processed. Both were possible under the Allow-star, and the hole is strictly narrower now, but it is not closed. Moving the session writes behind the bridge (the §11 follow-up) is what removes it, and v1.9.0 did exactly that (below). Handoff threads: the poller writes the row for a Phase 70 handoff thread itself and cannot write `sandbox_id`, so the bridge fills `sandbox_id` on the first turn it dispatches into that thread. In a mention-only channel the first reply in a handoff thread therefore still needs an @-mention. The role is created per sandbox, so existing boxes keep the old shape until recreate. See `docs/operational-gotchas.md` § The sandbox role is least-privilege.

**Residual closed in v1.9.0 (2026-10-18).** The pollers keep session state in local files and record only the session id and agent type through the signed `session` action, which writes an existing row only if it names the caller. A handoff's new thread is claimed by the bridge (`post --new-message --claim-thread`), so the poller never creates a row and the first-reply @-mention caveat is gone. The sandbox role has no threads-table grant. See `docs/operational-gotchas.md` § Thread session state behind the bridge.

### 6.5 Two smaller limits

A cold-created routed box gets no thread context on its first turn, because its sandbox row
and its `threadBypass` value do not exist when the bridge builds that turn. Later turns
behave normally, and the first reply has no prior bot turn to bound anyway. Strict-mode
context is a single `conversations.replies` page with `limit=200` that ignores `has_more`,
so a thread with more than 200 replies since the boundary yields the oldest page rather than
the newest.

## 7. Configuration reference

### 7.1 `slack.default_route` in `km-config.yaml`

Set it on the front-door install only, the one whose bridge URL is the Slack App's Request
URL. A peer that sets it is a no-op, because peers only ever see relayed requests and a
relayed request is terminal.

```yaml
slack:
  default_route:
    alias: ir-bot                    # required
    profile: profiles/ir-bot.yaml    # required; the cold-create source
    allow_emails:                    # required unless the profile sets allowEmails
      - operator@example.com
      - "*@example.com"              # a whole domain; subdomains do not match
```

| Rule | Where enforced | Failure |
|---|---|---|
| `alias` and `profile` set | `SlackDefaultRoute.Validate`, at `km init` | `km init` fails naming the key |
| The route profile's `allowEmails` or `allow_emails` lists at least one entry | `ValidateSlackDefaultRouteProfile` | `km init` and `km slack init` fail naming both places |
| Each entry is an exact address or exactly `*@<domain>` | `Validate`, and `ValidateEmailEntry` for the profile list | `km init` fails naming the entry |
| The profile's `allowEmails` wins over `allow_emails` | `effectiveSlackDefaultRoute` | `[info] slack.default_route allowlist: from <source> (N entries)` |
| Profile resolves, including `extends:` | `ValidateSlackDefaultRouteProfile` | `km init` fails |
| Profile sets `notification.slack.enabled` and `notification.slack.inbound.enabled` | `ValidateSlackDefaultRouteProfile` | `km init` fails; a box without an inbound poller can never answer |
| Env and yaml agree | `exportSlackDefaultRouteEnv` | WARN, env wins |
| Bridge sees all three env vars | `wireDefaultRoute` | A partial set logs a WARN and the route stays OFF |

Editing the profile needs a `km init --dry-run=false` before the next cold-create sees it,
because the create-handler reads the pre-staged copy. `slack.allow` (Phase 118) is
independent: it gates mapped channels by Slack user id and is allow-by-default when empty.

### 7.2 `notification.slack.inbound.threadBypass` in a SandboxProfile

| Value | Meaning |
|---|---|
| unset or `true` | Phase 91.3: replies in an engaged thread dispatch without a mention |
| `false` | Strict: every dispatch needs a mention, thread replies included, in mapped channels and in routed threads; DMs never need one |

The field matters only when mention-only is in effect: the profile's `mentionOnly`, the
install-level `slack.mention_only`, or a routed thread, where mention-only always applies.
`km validate` WARNs when `threadBypass: false` is set with `mentionOnly: false` (a no-op), and
prints an info-level WARN when `mentionOnly` is unset, naming the install default as the
deciding factor. The value is written to the sandbox row as `slack_thread_bypass` at create
time. See `docs/profile-reference.md`.

### 7.3 `km doctor` results

| Result | OK | WARN | SKIPPED |
|---|---|---|---|
| `Slack default route` | n/a | n/a | `slack.default_route not set — route off` |
| `Slack default route profile` | Profile resolves with Slack inbound; notes strict mode when `threadBypass: false` | Missing, unresolvable, or lacking inbound | n/a |
| `Slack default route allowlist` | Every exact email in the effective list resolves via `users.lookupByEmail`; domain wildcards are counted separately and never looked up | Neither list names anyone (route OFF), or exact emails are not found in the workspace | Bot token unavailable |
| `Slack default route scopes` | `im:history`, `mpim:history` and `im:write` present in the live `auth.test` scopes | Any missing: DMs are never delivered, or `km-slack dm` fails | `auth.test` scopes unavailable |

No alias-resolution result shipped, although spec §7 originally listed one.

## 8. Deploy surface

The steps run on the front-door install, in this order.

1. Edit `km-config.yaml` and confirm the profile enables Slack inbound.
2. `make build`. The operator binary carries the config key, the export and validation, the
   pre-stage, and the doctor group. A stale binary exports nothing and the route never turns
   on.
3. `make build-lambdas`. The bridge zip carries the ladder, the email gate, strict mode and
   the thread-scoped authorization; the create-handler zip carries the `slack_envelope` drain
   and the new poller userdata.
4. `km init --dry-run=false`. Not `--sidecars`, which rebuilds neither zip, and not `--slack`
   alone, which refreshes the bridge but not the create-handler. This applies the bridge env,
   the three bridge IAM additions and the create-handler send grant, and pre-stages the
   profile.
5. `km slack manifest`, paste it into the Slack App, save, and reinstall. **Human only.** Confirm the App's **App Home → Messages Tab** shows "Allow users to send … messages" enabled; the manifest sets it, but an App created from an older manifest keeps the old value until re-pasted.
6. `km slack rotate-token --bot-token <new>`. **Human only.** Until steps 5 and 6 are done,
   invited channels work and DMs are never delivered. `docs/slack-app-permissions.md` notes
   that adding scopes does not change the bot token; the rotate step is the Phase 72 shape
   and is harmless if the token is unchanged.
7. `km destroy <default-sandbox-id> --remote --yes && km create profiles/ir-bot.yaml ir-bot`
   if the default sandbox exists, so it boots the new poller; otherwise the first listed
   mention cold-creates it. Then `km doctor` and the live UAT in §10.

Strict mode for any other sandbox reaches it only on `km destroy && km create`, because both
the row attribute and the poller header ride in the create-handler path.

| Deployed | Not deployed | Result |
|---|---|---|
| New bridge | Old poller on the default box | Replies land in the sandbox's own `#sb-…` channel instead of the originating thread |
| New poller | Old bridge | The route never fires |
| New bridge | Old create-handler | The box is created, `slack_envelope` is ignored, the first prompt is lost, and the sender re-mentions |

All three are quiet and none is dangerous.

## 9. Operating it

A second "Reply sent to …" line under every answer was the pre-existing double post, not the
route: the agent answered with `km-slack reply` and the poller then posted its closing
sentence. A marker file now makes the poller skip its final post when the agent already
posted in the thread (see `docs/slack-notifications.md` § One reply per turn). It ships with
`make build` + `km init --dry-run=false`, because the `km-slack` sidecar and the
create-handler-rendered poller both change, and a box gains it only on recreate.

### 9.1 Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| A mention in an invited channel is ignored | The sender's Slack profile email is not on `allow_emails` | Add it and run `km init --dry-run=false`; `km doctor` names emails that do not resolve |
| A mention in an invited channel is ignored | The bot is not a member of the channel | `/invite` the bot |
| A message in a channel is ignored | It has no @-mention; only one-to-one DMs dispatch without one | Mention the bot |
| A thread reply without a mention is ignored | The receiving box is strict | Mention the bot, or unset `threadBypass` |
| Nothing routes at all | The route env is not on the bridge: stale binary, init not run, or a partial env set | `make build`, `km init --dry-run=false`; check `KM_SLACK_DEFAULT_ROUTE_ALLOW` on the Lambda |
| DMs are ignored | The DM scopes or events are missing | Manifest, reinstall, `km slack rotate-token`; `km doctor` reports `Slack default route scopes` |
| The reply lands in `#sb-…` | The default box runs a pre-route poller | Recreate the default sandbox |
| The bridge logs `channel_mismatch` for a routed channel | A top-level post, or `permalink` or `update`, into a foreign channel | Expected; only `post` and `upload` with the routed `thread_ts` pass |
| A cold-created box never answers | Old create-handler, missing create-handler send grant, or a profile without inbound | `make build-lambdas` + `km init --dry-run=false`, then re-mention |
| The first reply takes minutes | The alias was absent and the mention cold-created it | Expected: the box boots and runs profile init first, and the poller waits for it. Later turns are warm. A reply ~30 min late means a box from before the init gate; see `docs/operational-gotchas.md` § Cold-create first turn and the init gate |
| A `claude:` or `codex:` prefix in a routed thread changes nothing | The handoff is skipped in routed threads | Expected; switch agents in a mapped channel |
| The agent says thread context was unavailable | `conversations.replies` failed or was rate-limited, or the turn ran out of time before collection | Check the bridge log; confirm the App is internal (Tier 3) |
| Every mention fails with the alias resolve error | Two sandbox rows share the alias | Destroy the stale one |

### 9.2 Log lines

The bridge logs to the CloudWatch group `/aws/lambda/{prefix}-slack-bridge`, for example
`/aws/lambda/km-slack-bridge` on the default install. The poller logs to the box's journal
for `km-slack-inbound-poller`.

| Log line | Meaning |
|---|---|
| `km-slack-bridge: default route enabled` | Cold start found all three env vars; carries alias, profile and the email count |
| `km-slack-bridge: default route partially configured; staying OFF` | One or two env vars set |
| `events: route: no mention` | Gate step 4 dropped a mention-less message (Info) |
| `events: route: sender email unavailable (deny)` | `users.info` failed or returned no usable email (Info) |
| `events: route: sender not allowlisted (silent drop)` | The email is not on the route list (Info) |
| `events: sender not in sandbox allowEmails (silent drop)` | The target sandbox's own `allowEmails` refused the sender, on any path (Info) |
| `events: reaction skipped (quiet period)` | A thread reply inside the quiet period got no 👀 (Info) |
| `events: route: alias resolve failed` | DynamoDB error or an ambiguous alias |
| `events: route: cold-create published` | `SandboxCreate` sent with the envelope |
| `events: route: alias moved; rebinding thread after delivery` | Continuity found an old box; the row is rebound on success |
| `events: route: continuity fetch failed; treating as strict` and siblings | Continuity could not read the box; the mention is required |
| `events: context collected` | Strict context built, with counts |
| `events: context: skipped, insufficient time before the Lambda deadline` | Collection skipped; the turn still dispatches |
| `events: context: replies fetch failed` | `conversations.replies` error; dispatched with the unavailable header |
| `events: threads mark-dispatched` | The boundary write failed (WARN); the next context may repeat messages |
| `bridge: authz_routed_thread` | An outbound post or upload was admitted into a routed thread |
| `bridge: channel_mismatch` | An outbound envelope was refused (403) |
| `agent switch requested in a routed channel (C0…)` | Poller-side: the handoff was skipped |

### 9.3 Telling which gate dropped a mention

The route-gate drops log at Info, so they reach CloudWatch at the Lambda's default level.
Grep the bridge log group for these lines:

- `events: route: no mention`: the channel is not a DM and the message did not mention the bot.
- `events: route: sender email unavailable (deny)`: the sender's email lookup failed, so the gate denied.
- `events: route: sender not allowlisted (silent drop)`: the sender's email is not on the route's list.

The sender sees nothing in any of these cases. No `events:` line at all for the channel means
the event never reached the bridge: check bot membership and, for DMs, the scopes. An
`events: router:` line means no route is configured and Phase 96 ran. A dispatch that reached
the box but whose reply never appeared shows as `bridge: channel_mismatch`. The owned-path
drops (Phase 118 allowlist, mention-only skip, bypass, the `react_always: false` reaction
skip) still log at Debug. A sandbox `allowEmails` drop and a quiet-period reaction skip log at
Info on every path.

## 10. Live UAT

The two checklists below come from spec §10 and §B9. The Result column is filled in by the
controller after the deploy.

### 10.1 Default route (spec §10)

| # | Step | Expected | Result |
|---|---|---|---|
| 1 | New public channel, `/invite` the bot, mention with a task | 👀, reply in thread in that channel | **PASS** (2026-10-16 23:44–23:50Z). Bridge: `route: cold-create published alias=ir-bot sandbox=sl-…`; 👀 on the anchor; create-handler received the event; the box answered `READY #C0… ir-bot` in the originating channel's thread, not in its own `#sb-ir-bot`. The very first turn hit a pre-existing boot race (poller up before `initCommands` installed the agent, exit 127, message invisible for the 1800 s FIFO timeout); a second top-level mention on the warm box answered in 25 s. |
| 2 | Reply in that thread without a mention | Same sandbox, same agent session | **PASS**: `CONTINUED` from the same box; bridge took the continuity branch before the relay. |
| 3 | A non-listed user mentions the bot in the same channel | Nothing | **PASS**: no enqueue, no 👀; after the Info-level change the bridge logs `route: sender email unavailable (deny) user=… err="users.info: user_not_found"`. |
| 4 | A non-listed user replies inside the established thread | Nothing | **PASS** with and without a mention: denied at the email gate, nothing enqueued. |
| 5 | DM the bot without a mention | Dispatched, reply in the DM | **PASS** (2026-10-17): the first DM after the manifest paste, reinstall and `km slack rotate-token` was refused by Slack itself ("Sending messages to this app has been turned off") even though `km doctor` reported `im:history and mpim:history present` — the App Home Messages tab was disabled, a setting the generated manifest did not carry. Fixed in the template (`features.app_home`); once the operator toggled the tab the DM was dispatched and answered in the DM. A genuine human `@`-mention from the shared channel the same morning was answered in the thread in 27 s. The same morning also surfaced the double reply (one answer plus "Reply sent to …" under it), fixed in 16e03319 (see §9). |
| 6 | Private channel: invite, mention | Dispatched | **PASS**: `PRIVATE-OK` reply in the private channel's thread (the agent guessed a channel *name*; the preamble carries only the id). |
| 7 | Stop the default sandbox, mention again | Auto-resume, reply arrives | **PASS**: `km stop` → mention → EC2 stopped→running → `RESUMED-OK` in 25 s. No "waking up" hint on a routed resume (the hinter resolves the row by channel). |
| 8 | Destroy the default sandbox, mention again | Cold-create, first prompt answered on boot | **PASS** (cold-create half): `km destroy` archived the box's own channel and left the routed rows; the next mention cold-created `sl-…` under the same alias. |
| 9 | Recreate under the same alias, reply in an old thread | New sandbox answers, no dead-queue enqueue | **PASS** (rebind half): the old thread's row (named the dead box) took the gone-box path — strict mention required, alias absent ⇒ cold-create — and after delivery the row was rebound to the new id with its session cleared (`last_dispatch_ts` advanced). The new box answered `RECREATED-OK sl-…` in that old thread at 00:39Z, ~32 minutes after the mention: the boot race above cost the full 1800 s visibility timeout, then the poller processed the retry on the first attempt. |
| 10 | Mention in a channel a peer install owns | Peer handles it, the front door's route never fires | **N/A**: this install has no `slack.peer_bridges`; the relay-before-route order is pinned by `TestDefaultRoute_PeerClaim_RouteNotEvaluated`. |
| 11 | `claude:` or `codex:` prefix inside a routed thread | Current agent continues, log line names the skipped handoff | **NOT RUN** (pinned by the rendered-bash test on the handoff gate). |
| 12 | One reply per turn after 16e03319 (recreated box `sl-648f21e9`) | Exactly one bot reply per turn; the poller skips its post when the agent posted | **PASS** (2026-10-17 09:11–09:21Z): plain turn → one reply (the poller's). `km-slack post` as the sandbox user into a routed thread → posted, but the marker write failed with `permission denied` on a root-owned marker the poller's own post had left in sticky `/tmp`; fixed by the opt-out on the poller's post; re-verified on the next recreate (`sl-33ae9a96`, 09:59Z): after the poller's reply no marker remained, and a sandbox-user `km-slack post` into that thread wrote a sandbox-owned marker. Marker present at turn end → `agent already posted in thread=…; skipping the final post`, marker consumed, zero bot replies in that thread. The agent declined two prompts asking it to post via `km-slack reply` (preamble steering, then "prompt injection"), so the skip branch was driven by placing the marker during a 40 s turn. The cold box's first turn hit the boot race again (exit 127, `claude` not yet on PATH). |
| 13 | A human mention in the box's own `#sb-ir-bot` channel ("Yo!! @bot you around?") | A normal answer | **FOUND AND FIXED** (2026-10-17 09:33Z): the agent replied "I'm Claude Code … not the person you're looking for. I can't ping other users for you." The preamble told it to use `<@U…>` tokens from the message to notify people, and the only token in the message was the bot's own id, which the agent could not recognise as itself. The bridge now puts `bot_user_id` in the SQS envelope and the preamble adds "You are the Slack bot `<@U…>`". Rendered-bash and bridge tests pin it. Deployed (apply #5) and re-verified on `sl-33ae9a96` at 09:59Z: "Yo!! @bot you around?" → "Hey @KPH! Yeah, I'm here. What's up?" in 6 s, one reply. |

Test method: the bridge drops bot-authored messages, so a human mention cannot be scripted. Each step posted a real anchor message into a real channel with the bot token (so the reply had a thread to land in), then POSTed an HMAC-signed `event_callback` naming the operator's user id to the bridge. Everything after the bridge — `users.info`, the alias resolve, EventBridge, the create-handler, the EC2 box, the box's reply through the signed-envelope handler — was the real path.

### 10.2 Strict mode (spec §B9)

| # | Step | Expected | Result |
|---|---|---|---|
| 0 | Confirm the Slack App is classified internal | Tier 3 on `conversations.replies` | **NOT VERIFIED** (needs the App settings page). The single `conversations.replies` call per strict mention succeeded during the run. |
| 1 | Top-level mention | Reply in thread | **PASS** by construction: the strict box (`profiles/ir-bot-strict.yaml`, its own `#sb-ir-bot-strict` channel, `slack_thread_bypass=false` on its row) was created with `km create`; its ready announcement is the engaged thread used below. |
| 2 | Two humans reply without a mention, one with a screenshot | No 👀, no turn | **PASS** (text half): an unmentioned reply in the engaged thread enqueued nothing (queue 0/0). The screenshot half cannot be scripted (see method note). |
| 3 | Mention in the thread | Reply reflects both messages and the screenshot | **PASS** (mechanics): bridge logged `context collected … messages=0 omitted=0 truncated=0 files=0` and enqueued; the box answered `STRICT-OK` in the thread. Content assertions need real human posts, which a bot token cannot produce (they are excluded as bot-authored by design). |
| 4 | A non-allowlisted user posts, then an allowlisted user mentions | Omitted count shown, content absent | **NOT RUN** (same limitation; pinned by `TestStrictMode_ContextRespectsAllowlist`). |
| 5 | `km pause` and `km resume` | Strict still in effect | **NOT RUN** (pinned by `TestSlackThreadBypass_RoundTrip`). |
| 6 | Profile without `threadBypass` | Phase 91.3 unchanged | **PASS**: the default-route box (no `threadBypass`) accepted the unmentioned follow-up in step 2 of §10.1. |
| 7 | Routed thread on a strict default sandbox: follow-up without and then with a mention | Nothing; then a reply with context from the invited channel's thread | **NOT RUN** (the default-route box was not strict; pinned by `TestStrictMode_RoutedThread`). |

Test method for this table: the same signed-event driver, aimed at the strict box's own per-sandbox channel (the mapped path). Human context messages cannot be fabricated with the bot token because `BuildThreadContext` excludes bot-authored messages, so the content rows above are covered by unit tests only and should be re-run by a person with two real accounts.

Two additions are worth running if the setup allows. Verify whether the poller's session
`get-item`/`put-item` on `km-slack-threads` succeeds on the default box (§6.4). Exercise a
Slack Connect channel from another workspace, whose reaction and reply behaviour is
unverified.

## 11. Known limits and follow-ups

### Fix soon

- ~~**Narrow `ec2spot_region_lock`.**~~ **Fixed 2026-10-17 (ec2spot v1.8.0).** Region Deny plus explicit grants; see §6.4.
- ~~**Move the poller's thread-table session writes behind the bridge.**~~ **Fixed 2026-10-18 (ec2spot v1.9.0).** The `session` action and `--claim-thread`; the sandbox role has no threads-table grant.
- ~~**Confirm the poller's thread-table access in UAT.**~~ Moot since v1.9.0: pollers make no DynamoDB call.
- **Paginate or surface `has_more` in the context reader**, so a long thread yields its
  newest messages rather than its oldest page.
- **Render `file_errors` and `files_omitted` in the poller header.** Both are on the wire;
  the agent sees neither.
- ~~**Close the cold-create boot race.**~~ **Fixed 2026-10-17.** The inbound pollers now wait
  (bounded at 1200 s) for `/var/lib/km/init-done`, which the bootstrap writes after profile
  init on every path, and an exit-127 turn makes its message visible again in 60 s instead of
  1800 s. Existing boxes get it on recreate. See `docs/operational-gotchas.md` § Cold-create
  first turn and the init gate. The `km doctor` WARN idea was not built.
- **Post the wake hint on a routed resume.** `DDBPauseHinter` resolves the sandbox row by
  channel, so a mention that auto-resumes the box from a channel it does not own gets no
  "Sandbox is waking up" line; the reply still arrives.

### Can wait

- Shipped 2026-10-17: bot-initiated DMs (`km-slack dm`).
- Shipped 2026-10-17: the reaction quiet period (`slack.react_quiet_seconds`,
  `reactQuietSeconds`). See `docs/slack-notifications.md` § Quiet period.
- Shipped 2026-10-17: `*@<domain>` allowlist entries and the per-sandbox `allowEmails` list,
  with the route profile's list gating the cold-create mention. See
  `docs/slack-notifications.md` § Allowlists.
- The guard's exception is keyed by policy name, its write regex misses glob actions such as
  `dynamodb:Put*`, and its table regex also matches the GitHub and HackerOne threads tables.
- No alias-resolution result in `km doctor`, and the profile result reads scopes from
  `auth.test` but a SKIPPED message once said "cache".
- `ResolveAlias` treats any two rows for the alias as ambiguous; a filter on live status
  would tolerate a stale row.
- The profile slug rule is duplicated across the Slack, GitHub and webhook bridge packages
  and its case handling is untested across them.
- `PreStageGitHubProfiles`, the HackerOne and webhook pre-stages still upload raw bytes and
  carry the same latent `extends:` bug the Slack pre-stage fixed.
- Any thread row counts as continuity, so a thread in a destroyed sandbox's own channel is
  rebound to the alias box (still email-gated).
- The routed authorizer maps a thread-lookup error to 403 while an owned-channel lookup error
  maps to 500, and the lookup-error branch is untested.
- `NormalizedEmails` does not deduplicate. (`Validate` now checks each entry's full shape.)
- The email cache never evicts expired entries (bounded per user id) and has no single-flight
  on concurrent misses.
- `BotUserID.Fetch` in context collection has no nil guard; production always wires it.
- `km slack init`'s scope note still lists only `channels:history` and `groups:history`.
- Slack Connect channels from another workspace are unverified.
- Per-channel rules, a "booting" message on cold-create, and a cooldown on the route are out
  of scope; one route was the ask, 👀 is the acknowledgement, and the Phase 121 action quota
  is the backstop.

### Noted during the km-slack docs audit (2026-10-17), code untouched

- The header comment in `cmd/km-slack/main.go` predates `reply`, `update`, `permalink` and `dm`.
- Missing `KM_SANDBOX_ID`/`KM_SLACK_BRIDGE_URL` exits 1 on `post`, `reply` and `dm` but 2 on the other verbs.
- `km-slack dm` does not validate `--render`; an unknown value renders plain (same fallback as `post`, without the warning line).

## 12. Where things live

| What | Where |
|---|---|
| Design spec, Parts A and B, including §11 follow-ups | `docs/superpowers/specs/2026-10-16-slack-default-route-design.md` |
| Implementation plan | `docs/superpowers/plans/2026-10-16-slack-default-route.md` |
| Operator runbook for the route | `docs/slack-notifications.md` § Default route |
| Operator runbook for strict mode and context | `docs/slack-notifications.md` § Phase 91, Strict mode |
| Every `km-slack` verb, its flags and the bridge rule it meets | `docs/slack-notifications.md` § km-slack verb reference |
| Phase summary | `CLAUDE.md`, "Slack default route + strict mention-only threads (2026-10-16)" |
| Profile field reference | `docs/profile-reference.md`, `threadBypass` |
| Slack App scopes and events | `docs/slack-app-permissions.md` |
| The threads-table IAM rule | `docs/operational-gotchas.md` § The sandbox role is least-privilege since ec2spot v1.8.0 |
| This document | `docs/slack-default-route.md` |

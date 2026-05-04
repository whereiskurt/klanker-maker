---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: 03
subsystem: slack-bridge
tags: [slack, events-api, handler, sqs, dedup, bot-loop-filter, hmac, unit-tests]
dependency_graph:
  requires: [67-00]
  provides: [events_handler, events_interfaces, events_types]
  affects: [pkg/slack/bridge]
tech_stack:
  added: []
  patterns:
    - EventsHandler with 7 interface fields (dependency injection, no global state)
    - EventNonceStore interface distinct from existing NonceStore (CheckAndStore returns bool vs Reserve returns error)
    - 200-on-internal-error invariant for all non-auth/non-parse failures
    - PauseHintPoster goroutine using context.Background() + 5s timeout (not request ctx)
    - Table-driven bot-loop filter with 6 cases (bot_id, 3 subtypes, user==bot_uid, empty_user)
key_files:
  created:
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/events_interfaces.go
    - pkg/slack/bridge/events_types.go
    - pkg/slack/bridge/events_handler_test.go
  modified: []
decisions:
  - "EventNonceStore interface defined separately from existing NonceStore: existing NonceStore.Reserve returns only error (using ErrNonceReplayed sentinel), but EventsHandler needs CheckAndStore returning (bool, error) for clean dedup branch logic. New interface in events_interfaces.go rather than modifying existing interfaces.go to preserve backward compat with handler.go and aws_adapters.go"
  - "6 bot-loop filter modes implemented: bot_id non-empty, subtype bot_message, subtype message_changed, subtype message_deleted, user==bot_uid, empty user (no user field = non-human message). Empty-user case added per plan's isBotLoop implementation"
  - "url_verification bypasses signature check entirely — Slack docs require this since signing secret may not yet be configured at URL setup time"
  - "SigningSecretFetchFailure returns 200 not 500 — consistent with all other internal errors per RESEARCH.md Pitfall 2"
metrics:
  duration: 231s
  completed_date: "2026-05-02"
  tasks: 2
  files: 4
---

# Phase 67 Plan 03: EventsHandler Pure-Go Skeleton Summary

Pure-Go EventsHandler for Slack Events API `/events` route with HMAC signing-secret verification, url_verification challenge echo, bot-loop filtering, event_id deduplication, and SQS dispatch — fully tested with 20 table-driven unit tests and zero AWS calls.

## What Was Built

### Three new source files

**`pkg/slack/bridge/events_interfaces.go`** defines 7 interfaces:
- `SQSSender` — sends a single message to a FIFO SQS queue
- `SlackThreadStore` — reads/writes `km-slack-threads` DDB table (Get + Upsert)
- `SandboxByChannelFetcher` — resolves channel_id to `SandboxRoutingInfo` (sandboxID, queueURL, paused flag)
- `SigningSecretFetcher` — returns Slack signing secret (cached implementation in Plan 67-05)
- `BotUserIDFetcher` — returns bot's own user_id for bot-loop filter (auth.test cache in Plan 67-05)
- `PauseHintPoster` — posts one-time "sandbox paused" hint (cooldown enforcement in adapter)
- `EventNonceStore` — CheckAndStore(ctx, id, ttl) returning (bool, error) for event_id dedup

**`pkg/slack/bridge/events_types.go`** defines DTOs:
- `slackEnvelope`, `slackMessageEvent` — Slack Events API payload shapes
- `InboundQueueBody` — SQS message body written by bridge, parsed by poller
- `EventsRequest` / `EventsResponse` — framework-agnostic handler I/O

**`pkg/slack/bridge/events_handler.go`** implements `EventsHandler.Handle`:
1. Parse body → url_verification short-circuit (bypasses sig check)
2. Fetch signing secret → HMAC-SHA256 verify (±300s window)
3. Dispatch event_callback only
4. Bot-loop filter (6 modes, before any DDB/SQS work)
5. Event_id dedup via `EventNonceStore` (24h TTL, `"event:"` prefix)
6. Channel→sandbox resolution (returns 200 on failure)
7. Thread anchor upsert (best-effort, DDB failure does not block SQS)
8. SQS SendMessage (returns 200 on failure, not 500)
9. PauseHinter.PostIfCooldownExpired in `go func()` with `context.Background()` + 5s timeout

### Test suite (`events_handler_test.go`)

20 tests replacing Wave 0 stubs — all PASS, zero skips:

| Test | What it proves |
|------|---------------|
| `URLVerification` | 200 + challenge echo, no SQS write, no sig check |
| `BadSigningSecret` | 401, no SQS |
| `StaleTimestamp` | 401 at -10min skew |
| `FutureTimestamp` | 401 at +10min skew |
| `BotSelfMessageFiltered` (6 subtests) | bot_id, 3 subtypes, user==bot_uid, empty_user all return 200 with zero SQS/threads |
| `ReplayedEventID` | 200, zero SQS on second delivery |
| `UnknownChannel` | 200, zero SQS when sandbox routing is empty |
| `TopLevelPost_UsesTSAsThreadTS` | thread_ts=msg.TS in SQS body + upsert |
| `InThreadReply_PreservesThreadTS` | thread_ts=msg.thread_ts in SQS body |
| `ValidMessage_HappyPath` | 200, group=sandbox-id, dedup=event_id, threads upsert |
| `SQSWriteFailure_Returns200` | 200 + "ok" on AccessDeniedException |
| `DDBUpsertFailure_Returns200` | 200, SQS write still attempted |
| `SandboxLookupFailure_Returns200` | 200, zero SQS |
| `SigningSecretFetchFailure_Returns200` | 200 + "ok", zero SQS |
| `PausedSandbox_FirstMessage` | 200, SQS write, PauseHinter invoked once |
| `PausedSandbox_WithinCooldown` | 200, SQS write, PauseHinter invoked (adapter enforces cooldown) |
| `NotPaused_NoHint` | 200, SQS write, PauseHinter NOT invoked |
| `PausedSandbox_NilHinter_IsNoop` | 200, SQS write, no panic |

## Key Architectural Decisions

### EventNonceStore vs existing NonceStore

The existing `NonceStore.Reserve` returns only `error` and uses `ErrNonceReplayed` as a sentinel — this is the outbound bridge pattern where a replay is a security violation. For the events handler, a replayed event_id is a normal Slack retry (not a security issue) so `CheckAndStore` returning `(bool, error)` is cleaner. The two interfaces coexist in the package; Plan 67-05's AWS adapter implements `EventNonceStore` by wrapping `DynamoNonceStore` with an `errors.Is(err, ErrNonceReplayed)` check.

### 200-on-internal-error invariant

Returning 5xx causes Slack to retry with a NEW `event_id` every ~30s, bypassing the `event_id`-based dedup. During SQS/DDB outages this creates a retry storm: the same human message arrives 4 times when downstream recovers. The only safe behavior is logging + 200. The 4 `_Returns200` tests encode this invariant explicitly.

### PauseHinter goroutine

The `PostIfCooldownExpired` call fires in a goroutine using `context.Background()` with a 5-second timeout because:
1. The request context is canceled when the Lambda handler returns (after the 200 response is written)
2. The hint post calls back through the bridge's own `post` action which may take 1-2s
3. We never want to delay the Slack ack beyond the 3s window

The `fakePauseHinter` in tests uses `sync.Mutex` to safely observe goroutine calls with a 500ms polling deadline.

## Interface Contract for Plan 67-05

Plan 67-05 implements all 7 interfaces with real AWS adapters:

| Interface | Plan 67-05 Implementation |
|-----------|--------------------------|
| `SQSSender` | `SQSSendMessageAdapter` wrapping `*sqs.Client` |
| `SlackThreadStore` | `DynamoSlackThreadStore` with `attribute_not_exists` PutItem |
| `SandboxByChannelFetcher` | `DynamoSandboxByChannelFetcher` querying `slack_channel_id-index` GSI |
| `SigningSecretFetcher` | `SSMSigningSecretFetcher` (mirror of `SSMBotTokenFetcher`, 15-min cache) |
| `BotUserIDFetcher` | `SlackAuthTestFetcher` calling `auth.test`, cached for Lambda lifetime |
| `PauseHintPoster` | `DynamoPauseHintPoster` with 1h conditional DDB write + bridge `post` call |
| `EventNonceStore` | Adapter wrapping `DynamoNonceStore` converting `ErrNonceReplayed` to `(true, nil)` |

The `EventsHandler` struct in `events_handler.go` requires zero changes to wire Plan 67-05's adapters.

## Deviations from Plan

None — plan executed exactly as written. The only implementation decision was introducing `EventNonceStore` as a separate interface (rather than reusing `NonceStore.Reserve`) to match the `CheckAndStore` API the plan's handler code required — documented as a decision above.

## Self-Check: PASSED

Files exist:
- FOUND: pkg/slack/bridge/events_handler.go (262 lines, min 120)
- FOUND: pkg/slack/bridge/events_interfaces.go (71 lines, min 36)
- FOUND: pkg/slack/bridge/events_types.go (48 lines, min 40)
- FOUND: pkg/slack/bridge/events_handler_test.go (533 lines, min 320)

Commits exist:
- 53049c8 feat(67-03): add EventsHandler pure-Go skeleton with interfaces and types
- 1f127c6 test(67-03): implement comprehensive EventsHandler tests replacing Wave 0 stubs

Test run: `go test ./pkg/slack/bridge/... -count=1 -run TestEventsHandler`: 20/20 PASS
Build: `go build ./pkg/slack/bridge/...`: CLEAN
Vet: `go vet ./pkg/slack/bridge/...`: CLEAN

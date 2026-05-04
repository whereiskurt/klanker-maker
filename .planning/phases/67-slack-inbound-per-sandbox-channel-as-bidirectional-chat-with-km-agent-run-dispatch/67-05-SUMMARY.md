---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: "05"
subsystem: slack-bridge
tags:
  - aws-adapters
  - sqs
  - dynamodb
  - ssm
  - lambda
  - iam
  - pause-hint
dependency_graph:
  requires:
    - 67-03  # EventsHandler interfaces and pure-Go handler
    - 67-02  # km-slack-threads DDB module + slack_channel_id-index GSI
  provides:
    - Production-wired EventsHandler via real AWS SDK adapters
    - Path-based Lambda dispatch (/ vs /events)
    - IAM policy extensions for SQS + DDB threads + DDB channel GSI + SSM signing secret
  affects:
    - cmd/km-slack-bridge  # new adapter wiring + dispatch logic
    - pkg/slack/bridge     # six new adapter types + adapter tests
    - infra/modules/lambda-slack-bridge/v1.0.0  # IAM + env vars
tech_stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/sqs (promoted from indirect to direct)
  patterns:
    - DynamoDB conditional write (attribute_not_exists) for idempotent Upsert
    - DynamoDB conditional UpdateItem (LWT) for bridge cold-start race absorption
    - SSM SecureString 15-min in-process cache (mirrors SSMBotTokenFetcher pattern)
    - SlackAuthTestAPI thin HTTP adapter for bot user_id resolution
    - nonceStoreAdapter bridging NonceStore.Reserve to EventNonceStore.CheckAndStore
key_files:
  created: []
  modified:
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/aws_adapters_test.go
    - cmd/km-slack-bridge/main.go
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/variables.tf
decisions:
  - "slackAuthTestAdapter implemented inline in main.go rather than extending pkg/slack.Client because Client.AuthTest doesn't return user_id and we don't want to change that public API"
  - "nonceStoreAdapter wraps existing DynamoNonceStore to bridge Reserve/ErrNonceReplayed to EventNonceStore.CheckAndStore bool interface — avoids duplicating nonce table logic"
  - "PostHintFunc is a closure posting via SlackPosterAdapter.PostMessage with empty subject — hint text is self-contained and needs no bold header"
  - "SSM signing secret uses same tokenCache struct as bot token (reuse over new type)"
  - "DDBUpdateItemAPI extends DDBQueryGetPutAPI so a single *dynamodb.Client satisfies both interfaces; threads adapter never needs UpdateItem"
metrics:
  duration: "467s"
  completed_date: "2026-05-03"
  tasks_completed: 2
  files_modified: 5
---

# Phase 67 Plan 05: AWS Adapters and Lambda Dispatch Summary

Six production AWS adapters wiring EventsHandler to real AWS resources: SQS FIFO delivery, DDB thread tracking, DDB channel GSI resolution, SSM signing secret cache, bot user_id resolution, and DDB-backed 1h pause-hint cooldown (LWT).

## What Was Built

### Task 1: Six AWS Adapters (pkg/slack/bridge/aws_adapters.go)

| Adapter | Interface | Key Design |
|---------|-----------|------------|
| `SQSAdapter` | `SQSSender` | `SendMessage` with MessageGroupId=sandbox_id, MessageDeduplicationId=event_id; empty queue URL rejected before SDK call |
| `DDBThreadStore` | `SlackThreadStore` | `PutItem` with `attribute_not_exists(channel_id)` condition; `ConditionalCheckFailed` = idempotent nil (row exists = poller already wrote claude_session_id) |
| `DDBSandboxByChannel` | `SandboxByChannelFetcher` | `Query` on `slack_channel_id-index` GSI; `state=paused` or `state=stopped` → `Paused=true` |
| `SSMSigningSecretFetcher` | `SigningSecretFetcher` | 15-min `tokenCache` pattern identical to `SSMBotTokenFetcher`; reuses `BotTokenSSMClient` interface |
| `CachedBotUserIDFetcher` | `BotUserIDFetcher` | 1h TTL cache; delegates to `SlackAuthTestAPI.AuthTest(ctx, token)` |
| `DDBPauseHinter` | `PauseHintPoster` | `GetItem` reads `last_pause_hint_ts`; cooldown check; conditional `UpdateItem` (LWT) absorbs cold-start race; `ConditionalCheckFailed` = silent nil (lost race); `PostHintFunc` closure called only on LWT win |

New interfaces introduced:
- `SQSSendMessageAPI` — narrow `*sqs.Client` subset for `SQSAdapter`
- `DDBQueryGetPutAPI` — GetItem/PutItem/Query for thread store and channel fetcher
- `DDBUpdateItemAPI` — extends `DDBQueryGetPutAPI` with UpdateItem for pause hinter
- `SlackAuthTestAPI` — `AuthTest(ctx, token) (userID, error)`
- `PostHintFunc` — `func(ctx, channelID, threadTS, text) error`

### Adapter Tests (19 new tests in aws_adapters_test.go)

New mock helpers added (all hand-written, matching existing file pattern):
- `mockSQSSendMessage` with `callCount`
- `mockDDBQueryGetPut` with `putCalls`
- `mockDDBUpdateItem` extending above with `updateCalled`
- `fakeSandboxFetcher` for `SandboxByChannelFetcher`

All 79 tests in `pkg/slack/bridge` pass.

### Task 2: Lambda Dispatch + IAM

**cmd/km-slack-bridge/main.go** — path-based dispatch:
- `POST /events` → `eventsHandler.Handle` (Phase 67)
- `POST /` (default) → existing `handler.Handle` (Phase 63, no behavior change)
- `IsBase64Encoded` body decoded before dispatch
- `lowercaseHeaders` normalizes Lambda headers before passing to EventsHandler
- `slackAuthTestAdapter` — direct HTTP `auth.test` call; extracts `user_id` from response
- `nonceStoreAdapter` — bridges `DynamoNonceStore.Reserve/ErrNonceReplayed` to `EventNonceStore.CheckAndStore`
- `DDBPauseHinter` constructed at cold start; wired to `eventsHandler.PauseHinter`
- `PostHintFunc` closure delegates to `poster.PostMessage(ctx, channelID, "", text, threadTS)`
- Missing `KM_SLACK_THREADS_TABLE` logs a warning but does NOT crash — Phase 63 `/` path continues

**infra/modules/lambda-slack-bridge/v1.0.0/main.tf** — five new IAM policies:

| Policy Resource | Sid | Permissions |
|----------------|-----|-------------|
| `sqs_send_inbound` | `SQSSendInbound` | `sqs:SendMessage`, `GetQueueAttributes`, `GetQueueUrl` on `{prefix}-slack-inbound-*.fifo` |
| `dynamodb_slack_threads` | `DDBSlackThreads` | `GetItem`, `PutItem`, `Query` on km-slack-threads table + index/* |
| `dynamodb_slack_threads` | `DDBSandboxesChannelGSI` | `Query` on km-sandboxes/slack_channel_id-index |
| `dynamodb_sandboxes_pause_hint` | `DDBSandboxesUpdateLastPauseHint` | `UpdateItem` on km-sandboxes |
| `ssm_signing_secret` | `SSMSigningSecret` | `GetParameter`, `GetParameters` on /km/slack/signing-secret |

New variables: `resource_prefix` (default: "km"), `slack_threads_table_name` (default: "km-slack-threads"), `signing_secret_path` (default: "/km/slack/signing-secret").

New Lambda env vars: `KM_SIGNING_SECRET_PATH`, `KM_SLACK_THREADS_TABLE`, `KM_RESOURCE_PREFIX`.

`replace_triggered_by = [aws_iam_role.slack_bridge]` was already in place from Phase 63 — confirmed, no change needed.

## Verification Results

- `go build ./...` — clean
- `go test ./pkg/slack/bridge/... -count=1` — 79 tests pass
- `terraform fmt -check` — clean (fmt applied once, re-checked clean)
- `terraform validate` — Success (2 pre-existing deprecation warnings on `aws_region.current.name` from Phase 63 code, not introduced by this plan)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Functionality] Added nonceStoreAdapter for EventNonceStore interface**
- **Found during:** Task 2 — EventsHandler.Nonces requires `EventNonceStore.CheckAndStore(bool)` but existing `DynamoNonceStore` implements `NonceStore.Reserve/ErrNonceReplayed`
- **Fix:** Added `nonceStoreAdapter` struct in main.go bridging the two interfaces; no new DDB logic needed
- **Files modified:** cmd/km-slack-bridge/main.go

**2. [Rule 2 - Missing Functionality] slackAuthTestAdapter implemented in main.go rather than extending pkg/slack.Client**
- **Found during:** Task 2 — `pkg/slack.Client.AuthTest` does not return the bot's `user_id` (it only validates the token); `CachedBotUserIDFetcher` requires `SlackAuthTestAPI.AuthTest(ctx, token) (userID, error)`
- **Fix:** Added `slackAuthTestAdapter` with direct HTTP call to `auth.test` extracting `user_id` from JSON response; did not modify pkg/slack.Client public API
- **Files modified:** cmd/km-slack-bridge/main.go

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | 38d0b9a | feat(67-05): implement six AWS adapters for EventsHandler wiring |
| 2 | 2f5d8ac | feat(67-05): wire EventsHandler path dispatch and extend Lambda IAM policy |

## Self-Check: PASSED

All artifacts verified present:
- pkg/slack/bridge/aws_adapters.go (6 adapters: SQSAdapter, DDBThreadStore, DDBSandboxByChannel, SSMSigningSecretFetcher, CachedBotUserIDFetcher, DDBPauseHinter)
- pkg/slack/bridge/aws_adapters_test.go (19 new tests)
- cmd/km-slack-bridge/main.go (/events dispatch + PauseHinter wiring)
- infra/modules/lambda-slack-bridge/v1.0.0/main.tf (sqs:SendMessage + DDBSandboxesUpdateLastPauseHint IAM)
- Commits 38d0b9a, 2f5d8ac verified in git log

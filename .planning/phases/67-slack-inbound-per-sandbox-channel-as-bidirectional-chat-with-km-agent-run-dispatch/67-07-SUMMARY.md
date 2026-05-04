---
phase: 67-slack-inbound-per-sandbox-channel-as-bidirectional-chat-with-km-agent-run-dispatch
plan: "07"
subsystem: slack-inbound-lifecycle
tags: [slack, sqs, dynamodb, km-create, km-destroy, lifecycle]
dependency_graph:
  requires: [67-05, 67-06]
  provides: [ready-announcement, drain-sequence, thread-cleanup]
  affects: [create.go, destroy.go, create_slack_inbound.go, destroy_slack_inbound.go]
tech_stack:
  added: []
  patterns:
    - Operator-signed bridge post reused for km create ready announcement
    - DDBThreadStore.Upsert reused from pkg/slack/bridge for thread anchoring
    - makeStopPoller + makeWaitForAgentRunIdle: SSM-backed concrete factories
    - DDBQueryDeleteAPI interface for testable drain
key_files:
  created:
    - internal/app/cmd/destroy_slack_inbound.go
  modified:
    - internal/app/cmd/create_slack_inbound.go
    - internal/app/cmd/create_slack_inbound_test.go
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - internal/app/cmd/destroy_slack_inbound_test.go
    - pkg/aws/metadata.go
    - pkg/aws/sandbox_dynamo.go
decisions:
  - "Used DDBThreadStore.Upsert from pkg/slack/bridge (not reimplementing) for consistent schema"
  - "Drain placed in Step 12 of destroy (after Terraform destroy) — instance may be gone but StopPoller/Wait fail gracefully; all steps best-effort"
  - "PostOperatorSigned and UpsertSlackThread added as optional func fields on slackInboundDeps (nil = skip) for clean test injection without compile-time mock overhead"
  - "SlackInboundQueueURL added to SandboxMetadata + unmarshal/sandbox_dynamo so destroy can read it from existing DDB row"
metrics:
  duration: "783s"
  completed: "2026-05-03"
  tasks: 2
  files_modified: 8
---

# Phase 67 Plan 07: Slack Inbound Lifecycle Round-Out Summary

**One-liner:** Ready announcement via operator-signed bridge post after km create + bounded drain (stop poller → wait 30s → delete queue → cascade thread rows) on km destroy for Slack-inbound sandboxes.

## What Was Built

### Task 1: postReadyAnnouncement + create.go wiring

`postReadyAnnouncement` in `create_slack_inbound.go` posts a "Sandbox `sb-abc123` ready. Reply here or in any thread to give it a task." message via the existing Phase 63 operator-signed bridge `post` action. The returned `ts` is written to `km-slack-threads` as a thread anchor with empty `claude_session_id` (intentional — first reply starts a fresh session).

Two new injectable fields were added to `slackInboundDeps`:
- `PostOperatorSigned func(ctx, channelID, body) (ts, error)` — signs with operator Ed25519 key and POSTs to bridge
- `UpsertSlackThread func(ctx, channelID, threadTS, sandboxID) error` — wraps `DDBThreadStore.Upsert`

Production factories `makePostOperatorSigned(ssmClient, bridgeURL)` and `makeUpsertSlackThread(ddbClient, tableName)` wire the real implementations. The announcement is non-fatal: bridge failures emit a WARN but never abort `km create`.

`create.go` Step 11e was extended to populate both callbacks and call `postReadyAnnouncement` after `provisionSlackInboundQueue` succeeds, when `slackChannelID != "" && slackPerSandbox`.

`SandboxMetadata` gained `SlackInboundQueueURL string` field + unmarshal in `sandbox_dynamo.go` so `km destroy` can read it.

### Task 2: drainSlackInbound + destroy.go wiring

`destroy_slack_inbound.go` provides:

- `drainSlackInbound(ctx, destroyInboundDeps)` — orchestrates the 4-step drain:
  1. Stop `km-slack-inbound-poller` systemd unit via SSM SendCommand (10s timeout)
  2. Wait up to 30s for `km-agent` tmux session to exit (polling 2s intervals)
  3. Delete SQS queue via `awspkg.DeleteSlackInboundQueue`
  4. Query + individually delete all `km-slack-threads` rows for `channel_id`

- `cleanupSlackThreads(ctx, destroyInboundDeps)` — DDB Query → per-row DeleteItem loop
- `makeStopPoller(ssmClient)` — concrete `StopPoller` callback using `productionSSMRunner`
- `makeWaitForAgentRunIdle(ssmClient)` — polls sandbox via SSM; returns nil when no `km-agent` session found
- `DDBQueryDeleteAPI` interface (Query + DeleteItem) satisfied by `*dynamodb.Client`

`destroy.go` Step 12 calls `drainSlackInbound` before the existing `runSlackTeardown` (Phase 63 final-post + channel archive). This preserves the invariant: final "destroyed" message lands while the channel exists, then it gets archived.

Drain is gated on `existingMeta.SlackInboundQueueURL != ""` — sandboxes without inbound (or pre-Phase 67 sandboxes) skip the drain entirely, preserving identical destroy behavior.

## Verification

- `go test ./internal/app/cmd/... -run "TestCreate_SlackInbound|TestDestroy_SlackInbound" -count=1` — 10 tests, all PASS
- `go build ./...` — clean

## Tests Added

| Test | File | Validates |
|------|------|-----------|
| TestCreate_SlackInboundReadyAnnouncement | create_slack_inbound_test.go | Happy path: post called, ts returned, thread upserted |
| TestCreate_SlackInboundReadyAnnouncement_Disabled | create_slack_inbound_test.go | inbound=false → silent no-op |
| TestCreate_SlackInboundReadyAnnouncement_PostFailureNonFatal | create_slack_inbound_test.go | Bridge error does not bubble up |
| TestDestroy_SlackInboundDrain | destroy_slack_inbound_test.go | Full drain: stop + wait + queue + 2 thread rows |
| TestDestroy_SlackInboundQueueDeleted | destroy_slack_inbound_test.go | Queue deleted even with nil optional deps |
| TestDestroy_SlackInboundThreadsCleanedUp_OnQueueErr | destroy_slack_inbound_test.go | Thread cleanup runs even if queue delete fails |
| TestDestroy_SlackInboundDrain_NoOp_WhenNoQueueURL | destroy_slack_inbound_test.go | Empty QueueURL → no-op |

## Deviations from Plan

None — plan executed exactly as written, with one minor adaptation:

The plan listed `PostOperatorSigned` / `UpsertSlackThread` with return type mismatches for `PostOperatorSigned` (plan said `error` return; implementation returns `(messageTS, error)` per bridge API shape). The final signature `func(ctx, channelID, body string) (string, error)` correctly returns the Slack message `ts` needed to key the thread anchor row.

## Self-Check: PASSED

- destroy_slack_inbound.go: FOUND
- create_slack_inbound.go: FOUND
- destroy_slack_inbound_test.go: FOUND
- create_slack_inbound_test.go: FOUND
- commit 7568baa (Task 1): FOUND
- commit 9459eac (Task 2): FOUND
- drainSlackInbound in destroy.go: FOUND (1 call site)
- `go build ./...`: CLEAN
- All 10 target tests: PASS

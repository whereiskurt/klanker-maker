---
phase: 119-slack-inbound-per-thread-parallelism
plan: "02"
subsystem: slack-bridge
tags: [slack, sqs, fifo, threading, parallelism, queue-timeout]
dependency_graph:
  requires: ["119-01"]
  provides: ["P119-A", "P119-E"]
  affects: ["pkg/slack/bridge", "pkg/aws"]
tech_stack:
  added: []
  patterns:
    - "FIFO MessageGroupId == threadTS (per-thread ordering, parallel-across-threads)"
    - "VisibilityTimeout const extracted (slackInboundVisibilityTimeout = 1800s)"
key_files:
  created: []
  modified:
    - pkg/slack/bridge/events_handler.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/aws/sqs.go
    - pkg/aws/sqs_dlq_test.go
    - pkg/aws/github_inbound_test.go
    - pkg/slack/bridge/events_handler_test.go
decisions:
  - "Unconditional rollout of threadTS grouping — no env flag; safe for cap=1 sandboxes (per-thread ordering preserved; global FIFO ordering was never meaningful)"
  - "slackInboundVisibilityTimeout const added parallel to h1InboundVisibilityTimeout; inboundQueueAttrs shared by Slack+GitHub (both inherit 1800s as intended)"
  - "Three stale test assertions updated (Rule 1 auto-fix) as direct consequence of the two functional changes"
metrics:
  duration: "219s"
  completed_date: "2026-06-25"
  tasks_completed: 2
  files_modified: 6
---

# Phase 119 Plan 02: Layer 1 Bridge + Queue Timeout Raise Summary

Bridge-level FIFO grouping swapped from sandboxID to threadTS and Slack inbound queue
base VisibilityTimeout raised from 30s to 1800s (matching H1 precedent), turning the
Wave-0 RED tests GREEN for P119-A and P119-E.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Bridge — group by threadTS at both Send sites + fix stale doc comment | 900a93c8 | events_handler.go, aws_adapters.go |
| 2 | Raise Slack inbound queue base VisibilityTimeout to 1800s | a1f06eb6 | sqs.go |

## What Was Built

### Task 1: Bridge MessageGroupId swap (P119-A)

Both `h.SQS.Send(...)` calls in `events_handler.go` had their 4th positional arg
(`groupID`) changed from `info.SandboxID` to `threadTS`:

- Files path (~line 470): `Send(bgCtx, info.QueueURL, string(sqsBodyBytes), threadTS, dedupID)`
- No-files path (~line 490): `Send(ctx, info.QueueURL, string(bodyBytes), threadTS, dedupID)`

`threadTS` is already computed at line 403 with the `msg.TS` fallback (guarantees non-empty,
satisfying FIFO's non-empty MessageGroupId requirement for top-level posts).

Effect: FIFO now gives parallel-across-threads / serial-within-thread for free. Turns from
different Slack threads on the same sandbox queue in parallel; turns within the same thread
are strictly ordered. This is the prerequisite for P119 concurrency goals.

The stale doc comment on `SQSAdapter` in `aws_adapters.go` was updated from "MessageGroupId
is the sandboxID" to reflect Phase 119 per-thread FIFO grouping semantics.

### Task 2: Queue base VisibilityTimeout raise (P119-E)

Added `slackInboundVisibilityTimeout = "1800"` const in `pkg/aws/sqs.go` (parallel to the
existing `h1InboundVisibilityTimeout = "1800"`), and switched `inboundQueueAttrs()` from the
literal `"30"` to the const. Updated `CreateSlackInboundQueue` doc comment.

`inboundQueueAttrs` is shared by the Slack and GitHub inbound FIFO queues — both inherit the
new 1800s base. GitHub turns also run minutes so this is correct for both. The H1 queue has
its own `CreateH1InboundQueue` path and is unaffected.

**Important deployment note:** the base raise only helps NEWLY created queues. Pre-Phase-119
sandboxes keep 30s until `km destroy && km create`. The poller heartbeat (Plan 04) is the
complementary mechanism for already-provisioned queues.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Three stale test assertions pinning pre-Phase-119 values**

- **Found during:** Post-task verification (`go test ./pkg/slack/bridge/ ./pkg/aws/ -count=1`)
- **Issue:** After swapping MessageGroupId to threadTS and raising VisibilityTimeout to 1800s,
  three existing tests still asserted the old values:
  - `assertByteIdenticalInboundAttrs` in `sqs_dlq_test.go`: `"VisibilityTimeout": "30"`
  - `TestCreateGitHubInboundQueue_FIFO` in `github_inbound_test.go`: `want "30"`
  - `TestEventsHandler_ValidMessage_HappyPath` in `events_handler_test.go`: `want "sb-abc123"`
- **Fix:** Updated all three to assert the correct Phase 119 values (1800s / threadTS).
- **Files modified:** `pkg/aws/sqs_dlq_test.go`, `pkg/aws/github_inbound_test.go`,
  `pkg/slack/bridge/events_handler_test.go`
- **Commits:** `8d2dbf9a`, `f3afd46d`

## Verification

All Wave-0 Phase 119 tests are GREEN:

```
go test ./pkg/slack/bridge/ ./pkg/aws/ -count=1
ok  github.com/whereiskurt/klanker-maker/pkg/slack/bridge
ok  github.com/whereiskurt/klanker-maker/pkg/aws
```

Specific tests:
- `TestEventsHandler_GroupID_IsThreadTS_NoFiles` — PASS
- `TestEventsHandler_GroupID_IsMsgTS_TopLevel` — PASS
- `TestEventsHandler_GroupID_IsThreadTS_Files` — PASS
- `TestInboundQueueAttrs_VisibilityTimeout` — PASS (was checking "1800")

`go build ./...` — PASS

No new bridge env var introduced (unconditional rollout per CONTEXT.md).

## Deploy Notes

- Bridge change: `make build-lambdas` + `km init --slack` (env+IAM apply re-uploads the
  freshly built zip; verify code SHA via `aws lambda get-function`).
- Queue base raise: applies to newly created per-sandbox queues only. Existing sandboxes
  keep 30s base until `km destroy && km create`. Plan 04 poller heartbeat handles
  pre-Phase-119 queues.
- No SandboxProfile schema change. No new DDB table. No new IAM grant.

## Self-Check: PASSED

Files verified:
- pkg/slack/bridge/events_handler.go — FOUND (threadTS on both Send paths)
- pkg/slack/bridge/aws_adapters.go — FOUND (updated doc comment)
- pkg/aws/sqs.go — FOUND (slackInboundVisibilityTimeout const + inboundQueueAttrs updated)

Commits verified:
- 900a93c8 (Task 1) — FOUND
- a1f06eb6 (Task 2) — FOUND
- 8d2dbf9a (test fix) — FOUND
- f3afd46d (test fix) — FOUND

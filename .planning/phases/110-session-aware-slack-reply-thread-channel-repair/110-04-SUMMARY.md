---
phase: "110"
plan: "04"
subsystem: operator-cli
tags: [slack, operator, reply, session-index, gsi, dynamodb]
dependency_graph:
  requires: [110-01, 110-02]
  provides: [km-slack-reply-operator-cmd]
  affects: [internal/app/cmd/slack.go, pkg/slack/bridge/aws_adapters.go]
tech_stack:
  added: []
  patterns: [cobra-subcommand, injectable-deps, tdd-red-green, narrow-interface]
key_files:
  created:
    - internal/app/cmd/slack_reply.go
    - internal/app/cmd/slack_reply_test.go
  modified:
    - internal/app/cmd/slack.go
    - pkg/slack/bridge/aws_adapters.go
decisions:
  - "SlackPostAPI and SlackThreadLookupAPI defined as narrow interfaces in slack_reply.go (not added to SlackAPI) for minimal surface and testability"
  - "deps.ThreadLookup preferred over inline DDB construction; fallback to cfg-based build for partial-injection compatibility"
  - "LookupBySession sandboxID='' operator mode: bypass ownership filter to return first matching row (operator has no sandbox boundary)"
metrics:
  duration: "834s"
  completed_date: "2026-06-13"
  tasks_completed: 2
  files_changed: 4
---

# Phase 110 Plan 04: Operator km slack reply Summary

Operator-side `km slack reply` with session-index GSI resolution, verbatim thread post, and sandbox channel fallback. Posts via `chat.postMessage` with the bot-token Slack client (same `*kmslack.Client` as `km slack test`/`invite`).

## What Was Built

### RunSlackReply (slack_reply.go)

Exported testable function with three resolution modes (first-hit wins):
1. `--thread` + `--channel` → verbatim post into that thread
2. `--session <id>` → Query `session-index` GSI directly (operator AWS creds, no bridge) → post to `(channel_id, thread_ts)` from matching row
3. `SandboxChannel` (from `--sandbox`/`--alias`) → top-level post to sandbox's bound channel
4. None resolved → `"no thread or channel resolved"` error

Narrow interfaces: `SlackPostAPI` (PostMessage) and `SlackThreadLookupAPI` (LookupBySession) defined in `slack_reply.go` for testability without coupling to full `SlackAPI`.

### SlackCmdDeps extension (slack.go)

`ThreadLookup SlackThreadLookupAPI` field added to `SlackCmdDeps`. `buildSlackCmdDeps` wires a `*slackbridge.DDBThreadStore` against `cfg.GetSlackThreadsTableName()` with operator AWS creds. `newSlackReplyCmd` is registered in `newSlackCmdInternal` via `AddCommand`.

### TestRunSlackReply suite (slack_reply_test.go)

5 test cases (all green):
- `TestRunSlackReply_VerbatimThreadChannel` — direct post via --thread+--channel
- `TestRunSlackReply_SessionGSIHit` — GSI returns row, posts to session's thread
- `TestRunSlackReply_SessionGSIMiss_FallbackSandboxChannel` — GSI miss, falls to channel root
- `TestRunSlackReply_NoResolution` — error returned when nothing resolves
- `TestRunSlackReply_VerbatimRequiresBothFlags` — error when --thread without --channel

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] DDBThreadStore.LookupBySession operator mode returned no rows**

- **Found during:** Task 1 implementation review
- **Issue:** `LookupBySession` filters by `sbSV.Value != sandboxID`. For the operator, `sandboxID = ""`. Since no real sandbox has an empty `sandbox_id`, the condition `sbSV.Value != ""` was always true — ALL rows were skipped. The operator could never resolve any session via the GSI.
- **Fix:** Added operator-mode bypass in `DDBThreadStore.LookupBySession`: when `sandboxID == ""`, skip the ownership filter and return the first matching row. When `sandboxID != ""` (sandbox-side), the boundary filter is unchanged.
- **Files modified:** `pkg/slack/bridge/aws_adapters.go`
- **Commit:** `324904c1`

## Self-Check: PASSED

- FOUND: internal/app/cmd/slack_reply.go
- FOUND: internal/app/cmd/slack_reply_test.go
- FOUND: commit 324904c1 (Task 1 TDD RED+GREEN)
- FOUND: commit c5289e6a (Task 2 wire + register)

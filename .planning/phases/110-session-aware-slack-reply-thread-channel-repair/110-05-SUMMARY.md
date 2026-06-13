---
phase: "110"
plan: "05"
subsystem: operator-cli
tags: [slack, operator, repair, dynamodb, threads, prune, forget]
dependency_graph:
  requires: [110-01, 110-04]
  provides: [km-slack-repair-cmds]
  affects: [internal/app/cmd/slack.go, internal/app/cmd/slack_repair.go]
tech_stack:
  added: []
  patterns: [cobra-subcommand, injectable-deps, tdd-red-green, narrow-interface, scan-filter-expression]
key_files:
  created:
    - internal/app/cmd/slack_repair.go
    - internal/app/cmd/slack_repair_test.go
  modified:
    - internal/app/cmd/slack.go
decisions:
  - "DDBRepairAPI interface defined in slack_repair.go (not added to existing pkg/slack/bridge interfaces) — operator-only, no bridge use"
  - "SlackChannelChecker interface wraps IsChannelDead: transient errors return (false, err) so caller never deletes on ambiguous Slack responses"
  - "prune-threads uses per-channel dedup map to avoid calling conversations.info multiple times for the same channel_id"
  - "forget-thread --session uses same GSI Query + GetItem base-table pattern as DDBThreadStore.LookupBySession (KEYS_ONLY projection)"
  - "ThreadRow exported type for both threads list and prune-threads dead-row output (shared return type)"
metrics:
  duration: "847s"
  completed_date: "2026-06-13"
  tasks_completed: 2
  files_changed: 3
---

# Phase 110 Plan 05: Slack Repair Commands Summary

Four operator-side cleanup/repair commands for stale Slack thread and channel DDB mappings: `km slack threads`, `km slack forget-thread`, `km slack prune-threads`, `km slack forget-channel`. All use operator AWS creds (local profile, direct DDB) — no bridge IAM change.

## What Was Built

### slack_repair.go

Four exported testable functions + four cobra commands:

**RunSlackThreads** — Scan `km-slack-threads` with FilterExpression `sandbox_id = :sid`. sandboxID empty → list all rows. Returns `[]ThreadRow` (channel_id, thread_ts, session_id, agent_type, sandbox_id, last_turn_ts). O(n) Scan documented in command long-help.

**RunSlackForgetThread** — DeleteItem on `km-slack-threads` keyed by (channel_id, thread_ts). Two resolution modes (mutually exclusive):
- `--session`: Query `session-index` GSI → GSI KEYS_ONLY row → GetItem base table → (channel_id, thread_ts) → DeleteItem. Returns error when session not found (not silent no-op).
- `--thread + --channel`: DeleteItem directly on the exact key.

**RunSlackPruneThreads** — Scan all rows (optionally filtered by sandbox), collect unique channel_ids, call `SlackChannelChecker.IsChannelDead` per unique channel. Only `channel_not_found` marks a channel dead; all other errors are transient (skip, never delete). `--dry-run` lists dead rows and makes zero DeleteItem calls; without `--dry-run` deletes each dead row.

**RunSlackForgetChannel** — DeleteItem on `km-slack-channels` keyed by `alias`. Inverse of `km slack adopt`.

### DDBRepairAPI + SlackChannelChecker interfaces

Narrow interfaces defined in `slack_repair.go` for testability:
- `DDBRepairAPI`: Scan + Query + GetItem + DeleteItem + PutItem (superset of `DDBQueryGetPutAPI`, adds Scan + DeleteItem)
- `SlackChannelChecker`: `IsChannelDead(ctx, channelID) (bool, error)`

`slackClientChannelChecker` wraps `*kmslack.Client.ChannelInfo` + `kmslack.IsChannelNotFound` for prod use.

### slack.go — four new registrations

All four repair commands registered in `newSlackCmdInternal` via `AddCommand` after `newSlackReplyCmd`, preserving 110-04's `reply` registration unchanged.

### Test suite (slack_repair_test.go)

10 tests, all green:
- `TestRunSlackForgetThread_ViaThreadChannel` — --thread+--channel DeleteItem on correct key
- `TestRunSlackForgetThread_ViaSession` — GSI Query → GetItem → DeleteItem on resolved key
- `TestRunSlackForgetThread_SessionNotFound` — error when GSI returns no rows
- `TestRunSlackForgetThread_RequiresFlags` — error when no flags provided
- `TestRunSlackForgetChannel_DeletesAliasRow` — alias DeleteItem with correct key attribute
- `TestRunSlackForgetChannel_RequiresAlias` — error on empty alias
- `TestRunSlackThreads_FiltersBySandboxID` — returns rows from Scan result
- `TestRunSlackPruneThreads_DryRun` — dead rows listed, zero DeleteItem calls (critical)
- `TestRunSlackPruneThreads_DeletesDeadRows` — dead row deleted without dry-run
- `TestRunSlackPruneThreads_TransientErrorDoesNotDelete` — transient Slack error never deletes

## Deviations from Plan

None - plan executed exactly as written.

## Self-Check: PASSED

- FOUND: internal/app/cmd/slack_repair.go
- FOUND: internal/app/cmd/slack_repair_test.go
- FOUND: internal/app/cmd/slack.go (has newSlackThreadsCmd/ForgetThreadCmd/PruneThreadsCmd/ForgetChannelCmd registrations)
- FOUND: commit 7e5e1fb7 (RED tests)
- FOUND: commit bf3daf13 (GREEN implementation + registrations)
- Full cmd suite: ok (492s, 0 failures)

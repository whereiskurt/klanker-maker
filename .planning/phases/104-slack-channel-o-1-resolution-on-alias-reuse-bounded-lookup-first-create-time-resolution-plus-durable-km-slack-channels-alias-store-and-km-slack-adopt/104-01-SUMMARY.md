---
phase: 104-slack-channel-o-1-resolution-on-alias-reuse
plan: "01"
subsystem: slack-channel-resolution
tags: [slack, p0-fix, p1-fix, tdd, bounded-scan, lookup-first]
dependency_graph:
  requires: []
  provides: [ErrScanCapExceeded, IsChannelNotFound, SlackResolveBudget, SlackMaxScanPages, SlackChannelStore, resolveSlackChannel-lookup-first]
  affects: [create.go, create_slack.go, pkg/slack/client.go]
tech_stack:
  added: []
  patterns: [TDD-red-green, bounded-retry-then-optimistic, lookup-first-state-machine]
key_files:
  created: []
  modified:
    - pkg/slack/client.go
    - pkg/slack/client_test.go
    - internal/app/cmd/create_slack.go
    - internal/app/cmd/create_slack_test.go
    - internal/app/cmd/create.go
    - internal/app/cmd/slack.go
    - internal/app/cmd/slack_invite.go
    - internal/app/cmd/slack_test.go
    - internal/app/cmd/create_slack_transcript_test.go
    - internal/app/cmd/create_slack_invite_test.go
    - internal/app/cmd/slack_invite_test.go
decisions:
  - "SlackMaxScanPages defaults to 0 (scan disabled) — new behavior; existing scan tests updated to opt-in with SlackMaxScanPages=100"
  - "SlackChannelStore interface is nil-tolerant; production create.go passes nil until plan 104-03 wires DDB"
  - "Transient conversations.info after bounded-retry treated as optimistic-use, not scan trigger"
  - "SSM-sourced hits back-fill DDB store on first touch (migrate-on-touch for pre-104 channels)"
metrics:
  duration: 1080s
  completed_date: "2026-06-10"
  tasks_completed: 2
  tasks_planned: 2
  files_modified: 11
---

# Phase 104 Plan 01: Bounded FindChannelByName + Lookup-First Resolver (P0+P1) Summary

Eliminates the unbounded Slack channel scan wedge at create time. Bounded `FindChannelByName` with page cap + ctx-per-page + typed `ErrScanCapExceeded`, `IsChannelNotFound` classifier, and a lookup-first budgeted `resolveSlackChannel` state machine that never falls through to `conversations.list` on a transient `conversations.info` error.

## Tasks Completed

| Task | Description | Commit |
|------|-------------|--------|
| 1 | Bound FindChannelByName (page cap + ctx-per-page) + IsChannelNotFound classifier | 826ec351 |
| 2 | Lookup-first budgeted resolver state machine + SlackChannelStore interface | d59a3920 |

## What Was Built

**Task 1 (pkg/slack):**
- `ErrScanCapExceeded` — distinct sentinel from `*SlackAPIError{Code:"ratelimited"}` so callers emit adopt/channelOverride guidance rather than a retry hint
- `FindChannelByName(ctx, name, maxPages int)` — 3-arg bounded form; `maxPages=0` returns immediately with no HTTP call; loop checks `ctx.Err()` per page; exhausting maxPages returns `ErrScanCapExceeded`
- `IsChannelNotFound(err error) bool` — true only for `*SlackAPIError{Code:"channel_not_found"}`; every other error (ratelimited, network) is treated as transient
- All `SlackAPI`/`SlackInitAPI` interfaces and their fakes updated to 3-arg form
- Existing `km slack init` and `km slack invite` callers pass `1000` (preserve current behavior)

**Task 2 (internal/app/cmd):**
- `SlackResolveBudget = 45*time.Second` — wall-clock budget wrapping Mode-2 (KM_SLACK_RESOLVE_BUDGET override)
- `SlackMaxScanPages = 0` — scan disabled by default; KM_SLACK_MAX_SCAN_PAGES to opt-in
- `slackInfoRetries = 2`, `slackInfoRetryDelay = 500ms` — bounded-retry for transient conversations.info
- `SlackChannelStore` interface (GetByAlias/UpsertByAlias) — nil-tolerant; DDB impl in plan 104-03
- `lookupStoredChannelID` — DDB-first then SSM by-name; returns (id, fromDDB)
- `validateStoredChannel` — bounded-retry info: ok / gone / transient-optimistic
- `storeChannelMapping` — write-through to DDB store + SSM by-name (best-effort)
- `slackResolveFailFast` — actionable guidance with `km slack adopt` and `channelOverride` mentions
- `ensureBotMemberAndInvite` — shared closure for bot-join + operator-invite + additional-invites
- `resolveSlackChannel` — new `store SlackChannelStore` parameter; Mode-2 fully restructured:
  1. `lookupStoredChannelID` → `validateStoredChannel` → `cache_hit` (O(1)) or `cache_optimistic` (transient)
  2. `CreateChannel` → `created` on success
  3. `name_taken` + `SlackMaxScanPages>0` → `scan_capped`; else → `failfast`
- SSM-sourced hits back-fill DDB store (migrate-on-touch for pre-104 / out-of-band channels)
- `slack_resolve` INFO log with path/ms/id for observability

## Verification

- `go test ./pkg/slack/ -run 'TestFindChannelByName|TestIsChannelNotFound'` — PASS (7 tests)
- `go test ./internal/app/cmd/ -run 'TestResolvePerSandbox'` — PASS (6 tests incl. `StoredID_SSMOnly_BackfillsDDB`)
- `go test ./internal/app/cmd/ -run 'TestResolveSlack'` — PASS (all existing tests)
- `TestResolvePerSandbox_StoredID_TransientInfo_NoScan` — PASS (the regression test for today's incident bug)
- `go build ./...` — clean

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated pre-existing tests that assumed scan-by-default**
- **Found during:** Task 2 GREEN phase
- **Issue:** 5 existing `TestResolveSlack_PerSandbox_NameTaken_*` tests assumed the old behavior: `name_taken` → always scan. New behavior: scan disabled by default; tests were checking the wrong invariant.
- **Fix:** Added `SlackMaxScanPages = 100` opt-in to each affected test that explicitly tests scan behavior. Tests that test O(1) cache path (`UsesByNameCache`) needed no change.
- **Tests modified:** `NameTaken_AutoRecoversViaLookup`, `NameTaken_ArchivedReservation`, `NameTaken_CacheMiss_FallsBackToScan`, `NameTaken_StaleCache_FallsBackToScan`, `NameTaken_RateLimited_ErrorMessage`
- **Commit:** d59a3920

**2. [Rule 3 - Blocking] Updated SlackInitAPI + SlackAPI interfaces in slack.go + slack_invite.go**
- **Found during:** Task 1 (interface drift — 3-arg form required across all consumers)
- **Fix:** Updated `SlackInitAPI` in `slack.go`, caller at `slack.go:294` (passes 1000), and `slack_invite.go:184` (passes 1000). Also updated all test fakes to 3-arg form.
- **Commit:** 826ec351

## Self-Check: PASSED

- pkg/slack/client.go: FOUND (ErrScanCapExceeded + IsChannelNotFound: 8 references)
- internal/app/cmd/create_slack.go: FOUND (SlackResolveBudget + SlackChannelStore: 9 references)
- Commit 826ec351: FOUND (feat(slack): bound FindChannelByName...)
- Commit d59a3920: FOUND (feat(slack): bounded lookup-first channel resolution...)

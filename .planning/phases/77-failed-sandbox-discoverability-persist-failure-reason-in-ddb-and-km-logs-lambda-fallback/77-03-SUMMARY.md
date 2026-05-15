---
phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback
plan: 3
subsystem: cli-status
tags: [cli, sandbox-status, failure-discoverability, go-testing, tdd, output-formatting]

# Dependency graph
requires:
  - 77-01: SandboxRecord.FailureReason (string) and SandboxRecord.FailedAt (*time.Time) populated by metadataToRecord
provides:
  - Failure:/Failed At: render block in printSandboxStatus (status.go) gated on failed/nocap status
  - Four behavior tests covering all four state combinations
affects: [77-00, operator-UX]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Status-gated conditional render block: if rec.Status == 'failed' || rec.Status == 'nocap' { render }"
    - "Column alignment math: label + colon + spaces = 13 chars before value (matching existing Status:/Created At: convention)"
    - "Verbatim timestamp format reuse: '2006-01-02 3:04:05 PM MST' copied from Created At: Fprintf"

key-files:
  created: []
  modified:
    - internal/app/cmd/status.go
    - internal/app/cmd/status_test.go

key-decisions:
  - "Failure:/Failed At: lines placed AFTER Status: and BEFORE Created At: — logically paired with failure status"
  - "When FailureReason == '': print <unknown> hint with actual sandbox ID via %s against rec.SandboxID"
  - "When FailureReason != '' and FailedAt == nil: print Failure: only, no Failed At: (avoids zero-value timestamp)"
  - "Gate on rec.Status field, not on field presence — defensive against stale values on recovered/restarted sandboxes"

requirements-completed: [STAT-77]

# Metrics
duration: 10min
completed: 2026-05-15
---

# Phase 77 Plan 03: km status Failure/Failed At Render Summary

**Failure: and Failed At: lines added to km status output for failed/nocap sandboxes, gated on status with <unknown> hint when DDB field is empty**

## Performance

- **Duration:** 10 min
- **Started:** 2026-05-15T00:11:24Z
- **Completed:** 2026-05-15T00:21:14Z
- **Tasks:** 1 (TDD — 2 commits: RED test + GREEN impl)
- **Files modified:** 2

## Accomplishments

- Inserted Phase 77 render block in `printSandboxStatus` between `Status:` and `Created At:` Fprintf calls
- Gate: `if rec.Status == "failed" || rec.Status == "nocap"` — does not fire for running/stopped/paused/etc.
- When `FailureReason != ""`: prints `Failure:     <reason>` and (if `FailedAt != nil`) `Failed At:   <ts>`
- When `FailureReason == ""`: prints `Failure:     <unknown — try km logs <sandbox-id>>` (no `Failed At:` line)
- Timestamp formatter is verbatim `"2006-01-02 3:04:05 PM MST"` matching existing `Created At:` Fprintf
- Column alignment preserved: `Failure:     ` = 7+1+5 = 13 chars, `Failed At:   ` = 9+1+3 = 13 chars
- Four behavior tests pass; all 11 existing status tests unaffected; `go build ./...` and `go vet` clean

## Task Commits

1. **Task 1 RED: Failing tests for Failure:/Failed At: render** — `a3d89e3` (test)
2. **Task 1 GREEN: Insert render block in printSandboxStatus** — `ef98164` (feat)

## Captured stdout fixture (TestStatusCmd_FailedWithReason)

```
km status — sb-fail01 [v0.0.0-dev] ...
──────────────────────────────────────────────
Sandbox ID:  sb-fail01
Profile:     open-dev
Substrate:   ec2
Region:      us-east-1
Status:      failed
Failure:     provision Slack channel: archived
Failed At:   2026-05-10 11:05:44 AM EDT
Created At:  2026-03-20 8:00:00 AM EDT
```

(Local timezone formatting of the UTC fixture `2026-05-10T15:05:44Z`.)

## Test Fixture Summary

| Test | Status | FailureReason | FailedAt | Asserts |
|---|---|---|---|---|
| TestStatusCmd_FailedWithReason | failed | "provision Slack channel: archived" | 2026-05-10T15:05:44Z | Failure: line + Failed At: line present; no `<unknown` |
| TestStatusCmd_FailedNoReason | failed | "" | nil | `<unknown — try km logs sb-fail02>` present; no `Failed At:` |
| TestStatusCmd_NocapWithReason | nocap | "no Spot capacity" | 2026-05-11T09:30:00Z | Failure: + Failed At: lines present |
| TestStatusCmd_Running_NoFailureLine | running | "stale value" | 2026-04-01T00:00:00Z | NO Failure: and NO Failed At: lines |

## Files Created/Modified

- `internal/app/cmd/status.go` — Phase 77 render block (17 lines) inserted at line 363, between Status: and Created At: Fprintf calls
- `internal/app/cmd/status_test.go` — Four new TDD tests (149 lines) covering the four state combinations

## Decisions Made

- Render block placed between `Status:` and `Created At:` — logically paired with the failure status, operator reads status then immediately sees why
- Gate uses `rec.Status` field (not field presence) — defensive: a sandbox that transitions from failed to running shouldn't show stale failure fields
- `<unknown>` hint includes actual sandbox ID via `%s` against `rec.SandboxID` — operator gets the exact `km logs <id>` command to run
- `Failed At:` line omitted when `FailedAt == nil` (even when reason is present) — avoids printing a zero-value timestamp for older records

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## Next Phase Readiness

- 77-03 is complete. The `km status` display surface for failed/nocap sandboxes is fully implemented.
- Wave 3 parallel: 77-02 (create-handler write path) and 77-04 (km logs CloudWatch fallback) are disjoint — no conflicts.
- After all wave 3 plans complete, end-to-end flow is: create-handler writes failure_reason/failed_at to DDB on failure → km status reads and renders both fields.

---
*Phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback*
*Completed: 2026-05-15*

## Self-Check: PASSED

Files exist:
- FOUND: internal/app/cmd/status.go
- FOUND: internal/app/cmd/status_test.go
- FOUND: .planning/phases/77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback/77-03-SUMMARY.md

Commits:
- FOUND: a3d89e3 (test RED)
- FOUND: ef98164 (feat GREEN)

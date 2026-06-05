---
phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
plan: 02
subsystem: infra
tags: [doctor, cloudwatchlogs, orphan-cleanup, log-retention, multi-install, tdd]

# Dependency graph
requires:
  - phase: 94-01
    provides: CWLogsCleanupAPI interface, DoctorDeps.CWLogsCleanupClient/DeleteLogs/SetLogRetention, cfg.GetDoctorLogRetentionDays/GetResourcePrefix, mockCWLogsCleanup fake
provides:
  - checkStaleLogGroups — four log-group families × (legacy km- + dynamic {prefix}) names, deduped orphan detection + delete + retention
  - applyLogRetention — PutRetentionPolicy pass over per-sandbox + management groups
  - 16 table-driven tests covering all DBG-LOGS/DBG-LOGS-PREFIX/DBG-LOGS-RET/DBG-PAGE/DBG-MULTI/DBG-SRCFIX requirements
  - buildChecks registration in doctor.go (DETECTION side of DBG-SRCFIX)
affects:
  - km doctor output: new "Stale Log Groups" check in parallel slice

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "logGroupFilterEntry struct pairs filter string with sandbox-ID extractor function"
    - "perSandboxFamilies returns all 8 (4 families × 2 name shapes) entries; caller deduplicates by filter string"
    - "describeAllLogGroupsForFilter paginated helper: single responsibility, accumulates into map[name]LogGroup for name-level dedup"
    - "applyLogRetention separately fetches management groups (exact prefix scan) and merges with already-fetched per-sandbox map"
    - "trackingCWLogsCleanup test wrapper records deleted/retentionSet slices under mutex for precise assertion"

key-files:
  created:
    - internal/app/cmd/doctor_log_groups.go
    - internal/app/cmd/doctor_log_groups_test.go
  modified:
    - internal/app/cmd/doctor.go

key-decisions:
  - "logGroupFilterEntry named exported type (not anonymous struct) to avoid Go return-type mismatch between perSandboxFamilies and callers"
  - "applyLogRetention re-fetches management groups via describeAllLogGroupsForFilter rather than expecting callers to pre-scan — avoids leaking management group names into the per-sandbox orphan logic"
  - "Test file placed at doctor_log_groups_test.go (same package cmd) to avoid import cycle; uses existing mockCWLogsCleanup + mockSandboxLister from doctor_test.go"
  - "ctx() helper in test file avoids shadowing context.Background() across 16 sub-tests without package-level variable"

# Metrics
duration: 10min
completed: 2026-06-04
---

# Phase 94 Plan 02: checkStaleLogGroups — Four Families, Legacy+Prefixed Names, Orphan Detection + Retention

**checkStaleLogGroups with four log-group families under both legacy km- and dynamic {prefix} names, deduped by full group name, WARN+hint on orphans, delete under --delete-logs, PutRetentionPolicy under --set-log-retention, registered in buildChecks**

## Performance

- **Duration:** 10 min
- **Started:** 2026-06-05T00:41:51Z
- **Completed:** 2026-06-05T00:52:12Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- `checkStaleLogGroups(ctx, client, lister, dryRun, deleteLogs, setLogRetention, retentionDays, prefix)` implemented covering all four per-sandbox log-group families (budget-enforcer, github-token-refresher, /…/sandboxes/, /…/sidecars/) under BOTH legacy `km-`/`/km/` AND dynamic `{prefix}-`/`/{prefix}/` filters
- Dedup by full log-group name: when `prefix=="km"` the 8 filter variants collapse to 4 distinct calls; `describeAllLogGroupsForFilter` accumulates into `map[string]cwlogstypes.LogGroup` keyed by name
- Paginated DescribeLogGroups via NextToken loop in `describeAllLogGroupsForFilter`
- `applyLogRetention` helper: sets `retentionInDays` on per-sandbox AND management groups whose `RetentionInDays==nil`; idempotent on already-set groups; management groups never passed to `DeleteLogGroup`
- `ResourceNotFoundException` during delete treated as already-deleted (mirrors SSM precedent)
- `buildChecks` registration in `doctor.go` after the SSM parameters check, using `deps.CWLogsCleanupClient/DeleteLogs/SetLogRetention` and `cfg.GetDoctorLogRetentionDays/GetResourcePrefix`
- 16 tests: OrphanDetected, AllActive, DryRun, DeleteFlag, Pagination, KmPrefix, BothNames, DefaultInstallCollapse, IgnorePrefix, NilClient, FourFamilies, RetentionAlreadySet, SetRetention, ManagementGroupsNeverDeleted, DeleteResourceNotFound, NoGroupsFound — all PASS

## Task Commits

1. **Task 1: checkStaleLogGroups + applyLogRetention + 16 tests** — `af50bb69` (feat)
2. **Task 2: register in buildChecks in doctor.go** — `09e59d71` (feat)

## Files Created/Modified

- `internal/app/cmd/doctor_log_groups.go` — `checkStaleLogGroups`, `applyLogRetention`, `logGroupFilterEntry`, `perSandboxFamilies`, `managementLogGroupPrefixes`, `describeAllLogGroupsForFilter`
- `internal/app/cmd/doctor_log_groups_test.go` — 16 table-driven tests via `trackingCWLogsCleanup` wrapper + `buildDescribeFn`/`buildDescribeMultiPageFn` helpers
- `internal/app/cmd/doctor.go` — `checkStaleLogGroups` wired into `buildChecks` after the SSM parameters check

## Decisions Made

- `logGroupFilterEntry` named as a package-level type (not anonymous struct) because Go doesn't allow returning `[]entry` (local type) as the return type declared as `[]struct{…}` — named type solves the mismatch cleanly
- `applyLogRetention` re-fetches management groups independently rather than embedding management group names in the orphan-detection scan; this avoids mixing management-only groups into the sandbox-ID extraction logic
- Test in same package `cmd` (not `cmd_test`) to access `mockCWLogsCleanup` and `mockSandboxLister` which are defined in `doctor_test.go`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Named type for logGroupFilterEntry required by Go type system**
- **Found during:** Task 1 GREEN phase (first compile attempt)
- **Issue:** `perSandboxFamilies` declared return type as anonymous `[]struct{…}` but returned `[]entry` (local named type); Go rejects this even when the struct shape is identical
- **Fix:** Promoted `logGroupFilterEntry` to a package-level named type; updated all callers
- **Files modified:** internal/app/cmd/doctor_log_groups.go
- **Verification:** `go build ./internal/app/cmd/` clean
- **Committed in:** af50bb69 (Task 1 commit)

**2. [Rule 1 - Bug] `extractFn` field name mismatch after type promotion**
- **Found during:** Task 1 GREEN phase (second compile attempt)
- **Issue:** After promoting the type, one call site still used `.extractFn` (the old local-type field name) instead of `.extractSandbox` (the logGroupFilterEntry field name)
- **Fix:** Renamed reference to `.extractSandbox`; also simplified `distinctFilters = append(distinctFilters, f)` (no re-wrap needed since types match now)
- **Files modified:** internal/app/cmd/doctor_log_groups.go
- **Verification:** `go test ./internal/app/cmd/ -run StaleLogGroups -count=1` all PASS
- **Committed in:** af50bb69 (Task 1 commit)

## Issues Encountered

Pre-existing: `TestRunAgentAuthClaude_TeesAndCleans` times out after 120s (requires real OAuth browser interaction). Confirmed pre-existing in 94-01-SUMMARY. All Phase 94 and doctor tests pass when run with focused `-run` patterns.

## Self-Check

Files:
- `internal/app/cmd/doctor_log_groups.go` — FOUND (created)
- `internal/app/cmd/doctor_log_groups_test.go` — FOUND (created)
- `internal/app/cmd/doctor.go` — FOUND (modified)

Commits:
- af50bb69 — FOUND
- 09e59d71 — FOUND

## Self-Check: PASSED

## Requirements Satisfied

- DBG-LOGS: four families enumerated (budget-enforcer, github-token-refresher, /…/sandboxes/, /…/sidecars/)
- DBG-LOGS-PREFIX: BOTH legacy km- AND dynamic {prefix} filters for each family
- DBG-LOGS-RET: --set-log-retention applies PutRetentionPolicy; idempotent on already-set
- DBG-PAGE: paginated DescribeLogGroups via NextToken
- DBG-MULTI: multi-install isolation via active-set diff (sibling sandbox IDs appear orphaned; --ignore-prefix operates at command level)
- DBG-SRCFIX: DETECTION side — check matches both legacy km- and new {prefix} groups; CREATE side owned by 94-05

---
*Phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle*
*Completed: 2026-06-05*

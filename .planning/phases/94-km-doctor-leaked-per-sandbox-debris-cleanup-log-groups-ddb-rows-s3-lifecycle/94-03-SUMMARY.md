---
phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
plan: 03
subsystem: infra
tags: [doctor, dynamodb, cleanup, orphan-detection, tdd]

# Dependency graph
requires:
  - phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
    plan: 01
    provides: DDBScanDeleteAPI interface, DoctorDeps.DDBScanDeleteClient + DeleteDDBRows fields, mockDDBScanDelete fake
provides:
  - checkOrphanedDDBRows — four-table DynamoDB orphan scan with AI-row preservation and status guard
  - doctor_ddb_rows.go — attrStr helper, four per-table scan helpers, ddbDeleteOp type
  - doctor_ddb_rows_test.go — 13 table-driven tests including AI-preservation and status-guard regressions
affects:
  - km doctor output — new "Orphaned DDB Rows" check visible in every km doctor run

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-table scan helpers (scanBudgetsTable, scanIdentitiesTable, scanSlackThreadsTable, scanSandboxesTable) with LastEvaluatedKey pagination"
    - "ddbDeleteOp struct — table+key pair accumulated before delete to allow dry-run reporting"
    - "sandboxDeletableStatuses map — explicit terminal-status allowlist for sandboxes purge guard"
    - "attrStr helper — nil-safe S-attribute reader for DynamoDB item maps"
    - "trackingDDBScanDelete — wraps mockDDBScanDelete, records DeleteItem key maps for assertion"
    - "ExpressionAttributeNames to escape DDB reserved word 'status' in ProjectionExpression"

key-files:
  created:
    - internal/app/cmd/doctor_ddb_rows.go
    - internal/app/cmd/doctor_ddb_rows_test.go
  modified:
    - internal/app/cmd/doctor.go

key-decisions:
  - "BUDGET#ai# rows preserved via strings.HasPrefix guard in scanBudgetsTable — unconditional, regardless of sandbox active state"
  - "sandboxes deletion restricted to sandboxDeletableStatuses{failed,nocap} — explicit map beats string equality chain for future extensibility"
  - "status is a DDB reserved word: ExpressionAttributeNames{#s: status} required in ProjectionExpression for sandboxes scan"
  - "ddbDeleteOp accumulation pattern (collect-then-delete) mirrors checkStaleLogGroups collect-then-delete, enabling dry-run/flag guard before any mutation"
  - "buildChecks insertion point: after checkStaleLogGroups block, before checkStaleLambdas — groups all Phase 94 cleanup checks together"

# Metrics
duration: 7min
completed: 2026-06-04
---

# Phase 94 Plan 03: Orphaned DDB Rows (checkOrphanedDDBRows) Summary

**Four-table DynamoDB orphan scan with BUDGET#ai# preservation, status=failed/nocap-only sandboxes purge, non-key sandbox_id handling for slack-threads, and full LastEvaluatedKey pagination — registered in buildChecks and covered by 13 table-driven TDD tests**

## Performance

- **Duration:** 7 min
- **Started:** 2026-06-04T20:54:00Z
- **Completed:** 2026-06-04T21:01:00Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- `checkOrphanedDDBRows` implemented across four tables with full pagination (LastEvaluatedKey loop per table)
- BUDGET#ai# rows unconditionally preserved (AI spend history — `strings.HasPrefix(sk, "BUDGET#ai#")` guard in scanBudgetsTable)
- sandboxes rows purged only for `status∈{failed,nocap}` via `sandboxDeletableStatuses` map; `starting` (in-flight create) and all other statuses skipped
- slack-threads orphans detected via non-key `sandbox_id` attribute; rows without the attribute skipped (legacy rows)
- `status` DDB reserved word handled via `ExpressionAttributeNames{"#s":"status"}` in ProjectionExpression
- Dry-run / !deleteDDBRows paths warn-only with appropriate hint (`--dry-run=false --delete-ddb-rows` / `--delete-ddb-rows`)
- `attrStr` helper added for nil-safe S-attribute extraction
- `trackingDDBScanDelete` wrapper records DeleteItem key maps for precise assertion in AI-preservation and status-guard tests
- Registered in `buildChecks` after the checkStaleLogGroups block with empty-fallback guards for all four table names

## Task Commits

Each task was committed atomically:

1. **Task 1 RED: add failing tests** - `9d9162cd` (test)
2. **Task 1 GREEN: implement checkOrphanedDDBRows** - `7dcf41ed` (feat)
3. **Task 2: register in buildChecks** - `af6d3171` (feat)

## Files Created/Modified

- `internal/app/cmd/doctor_ddb_rows.go` — `checkOrphanedDDBRows`, `attrStr`, `ddbDeleteOp`, `scanBudgetsTable`, `scanIdentitiesTable`, `scanSlackThreadsTable`, `scanSandboxesTable`, `sandboxDeletableStatuses`, `countByTable`, `buildOrphanSummary`
- `internal/app/cmd/doctor_ddb_rows_test.go` — `trackingDDBScanDelete`, `buildDDBScanFn`, `buildDDBScanMultiPageFn`, `strAttr`, `containsAny`, 13 `TestDoctor_OrphanedDDBRows_*` tests
- `internal/app/cmd/doctor.go` — `buildChecks`: orphaned DDB rows check block inserted with all four table names + deps wiring

## Decisions Made

- BUDGET#ai# rows preserved via `strings.HasPrefix` guard unconditionally — matches plan's "NEVER collected even for orphaned ids" requirement
- `sandboxDeletableStatuses` explicit map (not inline string comparison) — forward-compatible if new terminal statuses are added
- `status` projected via ExpressionAttributeNames to avoid DDB `ValidationException` for reserved word
- `ddbDeleteOp` accumulation before mutation enables unified dry-run gate without table-specific branching
- buildChecks insertion after checkStaleLogGroups groups all Phase 94 cleanup checks together for operator output readability

## Deviations from Plan

### Auto-fixed Issues

None — plan executed exactly as written.

## Issues Encountered

Pre-existing test `TestRunAgentAuthClaude_TeesAndCleans` (agent_auth_test.go) times out after 120s requiring real OAuth browser interaction — unrelated to Phase 94 changes. All Doctor and DDB tests pass.

## Self-Check

- `internal/app/cmd/doctor_ddb_rows.go` — FOUND (created)
- `internal/app/cmd/doctor_ddb_rows_test.go` — FOUND (created)
- `internal/app/cmd/doctor.go` — FOUND (modified)

Commits verified:
- 9d9162cd — FOUND (test: add failing tests)
- 7dcf41ed — FOUND (feat: implement checkOrphanedDDBRows)
- af6d3171 — FOUND (feat: register in buildChecks)

## Self-Check: PASSED

## Next Phase Readiness

Wave 4 (94-04): `checkS3LifecyclePolicy` can now proceed — consumes `S3LifecycleAPI`, `deps.SetS3Lifecycle`, `cfg.GetDoctorS3ExpireDays()`. Mock fake `mockS3Lifecycle` already in `doctor_test.go`.

---
*Phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle*
*Completed: 2026-06-04*

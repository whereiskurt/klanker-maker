---
phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
plan: 01
subsystem: infra
tags: [doctor, cloudwatchlogs, dynamodb, s3, config, interfaces, testing]

# Dependency graph
requires:
  - phase: 15-km-doctor-platform-health-check-and-bootstrap-verification
    provides: DoctorDeps struct, DoctorConfigProvider interface, runDoctor pattern, five-touchpoint config pattern
provides:
  - CWLogsCleanupAPI, DDBScanDeleteAPI, S3LifecycleAPI — narrow interfaces for Wave 2-4 check files
  - DoctorDeps.CWLogsCleanupClient, DDBScanDeleteClient, S3LifecycleClient — real client wiring
  - DoctorDeps.DeleteLogs, DeleteDDBRows, SetLogRetention, SetS3Lifecycle — bool flag fields
  - doctor_log_retention_days, doctor_s3_expire_days — five-touchpoint config knobs with merge-list
  - --delete-logs, --delete-ddb-rows, --set-log-retention, --set-s3-lifecycle flags in km doctor
  - --with-deletes fan-out extended to include --delete-logs + --delete-ddb-rows
  - mockCWLogsCleanup, mockDDBScanDelete, mockS3Lifecycle — configurable fakes for Wave 2-4 tests
affects:
  - 94-02-PLAN (checkStaleLogGroups — consumes CWLogsCleanupAPI + deps.DeleteLogs/SetLogRetention)
  - 94-03-PLAN (checkOrphanedDDBRows — consumes DDBScanDeleteAPI + deps.DeleteDDBRows)
  - 94-04-PLAN (checkS3LifecyclePolicy — consumes S3LifecycleAPI + deps.SetS3Lifecycle)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Five-touchpoint config pattern: struct field + SetDefault + merge-list + GetInt + clamp"
    - "Narrow mocked-API interface per check family (mirrors SSMDeleterAPI precedent)"
    - "runDoctor signature threading: new bools appended after deleteStateDigests, before ignorePrefixes"
    - "withDeletes fan-out extended for new delete flags only (guardrails excluded)"
    - "Configurable fake pattern: struct with fn fields per method, nil defaults to no-error"

key-files:
  created: []
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go

key-decisions:
  - "Guardrail flags (SetLogRetention, SetS3Lifecycle) excluded from --with-deletes fan-out — explicit opt-in required per design"
  - "Three real client assignments in initRealDepsWithExisting reuse the same awsCfg as existing cloudwatchlogs/dynamodb/s3 clients"
  - "DoctorLogRetentionDays and DoctorS3ExpireDays placed directly on Config struct (not behind getters on Config) matching DoctorStaleAMIDays precedent"

patterns-established:
  - "Phase 94 interfaces: CWLogsCleanupAPI, DDBScanDeleteAPI, S3LifecycleAPI follow narrow-interface naming convention"
  - "Mock fakes use func-field structs (not embed) so tests can override individual methods per table row"

requirements-completed: [DBG-INFRA, DBG-CFG, DBG-FLAGS]

# Metrics
duration: 13min
completed: 2026-06-04
---

# Phase 94 Plan 01: Shared Infrastructure (Interfaces, Flags, Config) Summary

**Shared contract layer for three new km doctor checks: three narrow AWS interfaces, four DoctorDeps bool flags, two five-touchpoint config knobs, and --delete-logs/--delete-ddb-rows/--set-log-retention/--set-s3-lifecycle flags wired through runDoctor with --with-deletes fan-out extended**

## Performance

- **Duration:** 13 min
- **Started:** 2026-06-04T20:25:43Z
- **Completed:** 2026-06-04T20:38:51Z
- **Tasks:** 3
- **Files modified:** 4

## Accomplishments

- Three narrow mocked-API interfaces (CWLogsCleanupAPI, DDBScanDeleteAPI, S3LifecycleAPI) added and real *cloudwatchlogs.Client / *dynamodb.Client / *s3.Client satisfy them (compile-proven in initRealDepsWithExisting)
- Two config knobs (doctor_log_retention_days, doctor_s3_expire_days) added via the full five-touchpoint pattern including the critical merge-list entry; TestDoctorRetentionAndExpireDays proves all three cases (default/yaml/clamp)
- Four flags (--delete-logs, --delete-ddb-rows, --set-log-retention, --set-s3-lifecycle) registered in NewDoctorCmdWithDeps and threaded through the runDoctor signature into DoctorDeps; --with-deletes fan-out extended for the two delete flags (guardrails excluded); TestDoctorFlags_WithDeletesImpliesNewFlags asserts the fan-out and guardrail exclusion

## Task Commits

Each task was committed atomically:

1. **Task 1: Config knobs via five-touchpoint pattern** - `bb8d2e66` (feat)
2. **Task 2: Three interfaces + DoctorDeps fields + initRealDeps wiring** - `dfb6fa1d` (feat)
3. **Task 3: Four flags, runDoctor threading, --with-deletes fan-out** - `d3e87314` (feat)

## Files Created/Modified

- `internal/app/config/config.go` - DoctorLogRetentionDays + DoctorS3ExpireDays: struct fields, SetDefault, merge-list entries, GetInt init, clamps
- `internal/app/config/config_test.go` - TestDoctorRetentionAndExpireDays (3 sub-tests: default/yaml/clamp)
- `internal/app/cmd/doctor.go` - CWLogsCleanupAPI + DDBScanDeleteAPI + S3LifecycleAPI interfaces; DoctorConfigProvider interface extended with GetDoctorLogRetentionDays/GetDoctorS3ExpireDays; appConfigAdapter methods added; DoctorDeps.CWLogsCleanupClient/DDBScanDeleteClient/S3LifecycleClient + DeleteLogs/DeleteDDBRows/SetLogRetention/SetS3Lifecycle fields; initRealDepsWithExisting Phase 94 wiring; NewDoctorCmdWithDeps flag block + fan-out extension; runDoctor signature + deps wiring
- `internal/app/cmd/doctor_test.go` - testConfig + testDoctorConfig stubs extended with GetDoctorLogRetentionDays/GetDoctorS3ExpireDays; mockCWLogsCleanup, mockDDBScanDelete, mockS3Lifecycle fakes; TestDoctorFlags_WithDeletesImpliesNewFlags

## Decisions Made

- Guardrail flags (SetLogRetention, SetS3Lifecycle) excluded from --with-deletes fan-out per plan spec (Pitfall 6) — explicit operator opt-in required for idempotent guardrail operations
- Real client assignments in initRealDepsWithExisting use the same awsCfg as existing clients in the function — no additional AWS config load needed
- Config struct accessors DoctorLogRetentionDays and DoctorS3ExpireDays follow DoctorStaleAMIDays precedent (direct field access in appConfigAdapter, not behind Config.Get*() methods)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Extended testDoctorConfig + doctorStaleAMIConfig stubs**
- **Found during:** Task 2 (go build ./... verification)
- **Issue:** testDoctorConfig and doctorStaleAMIConfig (which embeds testDoctorConfig) did not implement the two new DoctorConfigProvider methods, causing compilation failure with 10+ errors
- **Fix:** Added GetDoctorLogRetentionDays() and GetDoctorS3ExpireDays() to testDoctorConfig; doctorStaleAMIConfig inherits via embedding
- **Files modified:** internal/app/cmd/doctor_test.go
- **Verification:** go build ./... clean; go vet ./internal/app/cmd/ clean
- **Committed in:** dfb6fa1d (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 2 — missing stub methods on test config types)
**Impact on plan:** Essential for compilation. Interface expansion always requires updating all satisfying types.

## Issues Encountered

Pre-existing test failure `TestRunAgentAuthClaude_TeesAndCleans` (agent_auth_test.go:821) requiring real OAuth browser interaction — unrelated to Phase 94 changes. All Doctor and Config tests pass.

## Self-Check

- `internal/app/config/config.go` — FOUND (modified)
- `internal/app/config/config_test.go` — FOUND (modified)
- `internal/app/cmd/doctor.go` — FOUND (modified)
- `internal/app/cmd/doctor_test.go` — FOUND (modified)

Commits verified:
- bb8d2e66 — FOUND
- dfb6fa1d — FOUND
- d3e87314 — FOUND

## Self-Check: PASSED

## Next Phase Readiness

Wave 2-4 check plans (94-02, 94-03, 94-04) can now proceed in parallel:
- `doctor_log_groups.go` — consumes CWLogsCleanupAPI, deps.DeleteLogs, deps.SetLogRetention, cfg.GetDoctorLogRetentionDays()
- `doctor_ddb_rows.go` — consumes DDBScanDeleteAPI, deps.DeleteDDBRows
- `doctor_artifacts.go` extension — consumes S3LifecycleAPI, deps.SetS3Lifecycle, cfg.GetDoctorS3ExpireDays()
- Mock fakes (mockCWLogsCleanup, mockDDBScanDelete, mockS3Lifecycle) are ready in doctor_test.go

---
*Phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle*
*Completed: 2026-06-04*

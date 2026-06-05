---
phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle
plan: 04
subsystem: infra
tags: [doctor, s3, lifecycle, artifacts, guardrail]

# Dependency graph
requires:
  - phase: 94-01
    provides: S3LifecycleAPI interface, DoctorDeps.S3LifecycleClient + SetS3Lifecycle fields, mockS3Lifecycle fake, cfg.GetDoctorS3ExpireDays()
provides:
  - checkS3LifecyclePolicy — transient-prefix expiry guardrail with merge-preserving --set-s3-lifecycle
  - TestDoctor_S3LifecyclePolicy_* suite (7 tests)
  - checkS3LifecyclePolicy registered in buildChecks
affects:
  - km doctor output (new check visible in parallel check slice)
  - artifacts bucket lifecycle (when --set-s3-lifecycle applied)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "smithy.APIError error-code matching for NoSuchLifecycleConfiguration (same pattern as configure.go/unbootstrap.go)"
    - "Merge-and-put: Get existing rules → determine uncovered subset → append new rules de-duped by stable ID → Put merged set"
    - "Stable rule IDs (km-doctor-expire-{prefix-without-slash}) ensure idempotent re-runs"
    - "Filter.Prefix field (not deprecated top-level Prefix) for new rules; deprecated Prefix checked in coverage scan for compatibility"
    - "lifecycleRulePrefix() helper centralises prefix extraction for both Filter.Prefix and deprecated rule.Prefix"

key-files:
  created: []
  modified:
    - internal/app/cmd/doctor_artifacts.go
    - internal/app/cmd/doctor_artifacts_test.go
    - internal/app/cmd/doctor.go

key-decisions:
  - "smithy.APIError.ErrorCode() == 'NoSuchLifecycleConfiguration' pattern (not s3types type assert) — no typed error struct exists in sdk v1.97.1"
  - "LifecycleRuleFilter.Prefix (struct field, not union member) — the SDK uses a struct not an interface union at this version"
  - "Four new tests use mockS3Lifecycle from Wave 1 doctor_test.go directly (same package); noSuchLifecycleErr wraps smithy.GenericAPIError for correct errors.As matching"
  - "checkS3LifecyclePolicy placed in doctor_artifacts.go alongside checkOrphanedArtifacts per plan spec (same file, same bucket context)"
  - "Registration beside checkOrphanedArtifacts in buildChecks reuses same transcriptBucket/artifactsBucket local (same bucket)"

# Metrics
duration: 10min
completed: 2026-06-05
---

# Phase 94 Plan 04: S3 Lifecycle Expiry Guardrail Summary

**checkS3LifecyclePolicy with merge-and-put pattern: WARNs on missing transient-prefix expiry rules on the artifacts bucket, installs idempotent expiry rules under --set-s3-lifecycle without ever touching build-artifact prefixes or operator rules**

## Performance

- **Duration:** 10 min
- **Started:** 2026-06-05T01:22:55Z
- **Completed:** 2026-06-05T01:33:07Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- `checkS3LifecyclePolicy` added to `doctor_artifacts.go`: detects uncovered transient prefixes (logs/, remote-create/, agent-runs/, slack-inbound/) on the artifacts bucket via GetBucketLifecycleConfiguration, WARNs with the uncovered list + remediation hint, and installs merge-preserving expiry rules via PutBucketLifecycleConfiguration when --set-s3-lifecycle and --dry-run=false are both set
- Build-artifact prefixes (toolchain/, sidecars/, rsync/) are hardcoded absent from the transient set — they can never receive a doctor-installed expiry rule
- NoSuchLifecycleConfiguration (HTTP 404 from AWS) detected via smithy.APIError.ErrorCode() pattern and treated as "no existing rules" (not a hard failure)
- Stable deterministic rule IDs (km-doctor-expire-{prefix}) ensure Put is idempotent on partial or repeated runs
- `lifecycleRulePrefix()` helper extracts prefix from both the modern Filter.Prefix field and the deprecated top-level Prefix field for coverage-scan compatibility
- 7 table-driven tests covering: nil client / empty bucket (SKIPPED), NoSuchLifecycleConfiguration (WARN + no Put on dryRun), no setLifecycle flag (WARN + no Put), all covered (OK + zero Puts), merge-preserving set (exactly 1 Put; operator rule preserved; build-artifact prefixes absent), idempotent second run (OK + zero Puts)
- `checkS3LifecyclePolicy` registered in `buildChecks` beside `checkOrphanedArtifacts` using the Wave 1 deps fields and cfg getters

## Task Commits

Each task was committed atomically:

1. **Task 1: checkS3LifecyclePolicy implementation + tests (TDD)** - `5bdd4153` (feat)
2. **Task 2: Register checkS3LifecyclePolicy in buildChecks** - `c4dcd21e` (feat)

## Files Created/Modified

- `internal/app/cmd/doctor_artifacts.go` — checkS3LifecyclePolicy + lifecycleRulePrefix helper added; transientPrefixes package var; smithy + errors imports added
- `internal/app/cmd/doctor_artifacts_test.go` — 7 TestDoctor_S3LifecyclePolicy_* tests added; smithy import added; makeTransientRule + noSuchLifecycleConfigErr helpers; package comment updated
- `internal/app/cmd/doctor.go` — checkS3LifecyclePolicy closure appended in buildChecks beside checkOrphanedArtifacts (Phase 94 block comment)

## Decisions Made

- smithy.APIError.ErrorCode() == "NoSuchLifecycleConfiguration" is the correct detection pattern — s3types has no typed error struct for this error in sdk v1.97.1
- LifecycleRuleFilter is a struct with a Prefix *string field (not a union interface member as the RESEARCH.md sketched) — corrected during implementation
- noSuchLifecycleConfigErr embeds smithy.GenericAPIError directly so errors.As(err, &apiErr) resolves correctly in tests without importing the real AWS SDK response parsing
- Placed in doctor_artifacts.go (not a new file) per plan spec — same logical grouping as checkOrphanedArtifacts

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] s3types.NoSuchLifecycleConfiguration typed error does not exist in sdk v1.97.1**
- **Found during:** Task 1 RED phase (go test -c)
- **Issue:** RESEARCH.md canonical shape used `errors.As(err, &nslc)` with `*s3types.NoSuchLifecycleConfiguration` but this type does not exist in the SDK version used by the project (v1.97.1 documents it only as an error code in a doc comment, not as a Go type). Tests would not compile.
- **Fix:** Used `smithy.APIError` interface with `apiErr.ErrorCode() == "NoSuchLifecycleConfiguration"` (matches the project's existing pattern in configure.go and unbootstrap.go). Test fake uses `smithy.GenericAPIError{Code: "NoSuchLifecycleConfiguration"}` via `newNoSuchLifecycleErr()` constructor.
- **Files modified:** `internal/app/cmd/doctor_artifacts.go`, `internal/app/cmd/doctor_artifacts_test.go`

**2. [Rule 1 - Bug] LifecycleRuleFilterMemberPrefix union type does not exist — filter is a struct**
- **Found during:** Task 1 RED phase (go test -c)
- **Issue:** RESEARCH.md and initial tests used `*s3types.LifecycleRuleFilterMemberPrefix{Value: "prefix/"}` but the SDK uses `LifecycleRuleFilter` as a plain struct with a `Prefix *string` field (no union interface at this SDK version).
- **Fix:** Updated both production code and tests to use `&s3types.LifecycleRuleFilter{Prefix: awssdk.String(p)}`. Added `lifecycleRulePrefix()` helper that also checks the deprecated `rule.Prefix` field for coverage scan compatibility.
- **Files modified:** `internal/app/cmd/doctor_artifacts.go`, `internal/app/cmd/doctor_artifacts_test.go`

---

**Total deviations:** 2 auto-fixed (Rule 1 — SDK type mismatch vs RESEARCH.md canonical shape; corrected at RED-phase compile time with no behavior change to the logic)
**Impact on plan:** None — logic and outcomes identical to plan spec; only the Go types used differ from RESEARCH.md's SDK-version-agnostic sketch.

## Issues Encountered

Pre-existing test timeout: `TestRunAgentAuthClaude_TeesAndCleans` (agent_auth_test.go) times out at 120s requiring real OAuth browser interaction — unrelated to Phase 94 changes. All doctor and config tests pass when run targeted.

## Self-Check

Files:
- `internal/app/cmd/doctor_artifacts.go` — FOUND (modified)
- `internal/app/cmd/doctor_artifacts_test.go` — FOUND (modified)
- `internal/app/cmd/doctor.go` — FOUND (modified)

Commits verified:
- 5bdd4153 — FOUND (Task 1)
- c4dcd21e — FOUND (Task 2)

`checkS3LifecyclePolicy` present in doctor_artifacts.go: FOUND
`checkS3LifecyclePolicy` registered in buildChecks (doctor.go): FOUND
`go test -run S3Lifecycle`: 7/7 PASS
`make build`: km v0.4.822 SUCCEEDED

## Self-Check: PASSED

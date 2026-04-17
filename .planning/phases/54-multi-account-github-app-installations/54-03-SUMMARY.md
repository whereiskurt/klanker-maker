---
phase: 54-multi-account-github-app-installations
plan: 03
subsystem: infra
tags: [github-app, ssm, doctor, multi-account, health-check]

requires:
  - phase: 54-multi-account-github-app-installations
    provides: per-account SSM storage at /km/config/github/installations/{account}
provides:
  - multi-installation-aware GitHub config health check in km doctor
  - GetParametersByPath support on SSMReadAPI interface
affects: [doctor-checks, github-integration, platform-health]

tech-stack:
  added: []
  patterns: [SSM GetParametersByPath for prefix-based parameter discovery]

key-files:
  created: []
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
    - internal/app/cmd/create.go
    - internal/app/cmd/create_github_test.go

key-decisions:
  - "Extended SSMReadAPI interface with GetParametersByPath rather than multiple GetParameter calls"
  - "Per-account installations take priority over legacy key in doctor check reporting"
  - "Account names extracted from SSM parameter path suffix for human-readable output"

patterns-established:
  - "SSM path prefix discovery via GetParametersByPath for multi-key enumeration"

requirements-completed: [GHMI-05]

duration: 11min
completed: 2026-04-17
---

# Phase 54 Plan 03: Multi-Installation Doctor Health Check Summary

**km doctor GitHub check recognizes per-account installation keys via GetParametersByPath, reports count with account names, and falls back to legacy key**

## Performance

- **Duration:** 11 min
- **Started:** 2026-04-17T22:28:59Z
- **Completed:** 2026-04-17T22:40:11Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- checkGitHubConfig now checks per-account installation keys at /km/config/github/installations/ before legacy key
- Reports OK with installation count and account names when per-account keys found (e.g. "2 installation(s) found (userA, userB)")
- Falls back to legacy installation-id with "(legacy)" label for backward compatibility
- WARN only when neither per-account nor legacy installation keys exist
- 6 test scenarios covering all combinations (per-account only, legacy only, both, neither, missing app-client-id, original OK)

## Task Commits

Each task was committed atomically:

1. **Task 1 RED: Add failing tests for multi-installation doctor check** - `2fe7f69` (test)
2. **Task 1 GREEN: Implement multi-installation-aware checkGitHubConfig** - `6be3e78` (feat)
3. **Task 2: Full integration test and build verification** - `e4eaf36` (fix)

## Files Created/Modified
- `internal/app/cmd/doctor.go` - Extended SSMReadAPI with GetParametersByPath; rewrote checkGitHubConfig for per-account + legacy fallback
- `internal/app/cmd/doctor_test.go` - Added GetParametersByPath to mock; added 5 new test scenarios for multi-installation
- `internal/app/cmd/create.go` - Fixed generateAndStoreGitHubToken return signature to (string, error); inject installation ID into HCL
- `internal/app/cmd/create_github_test.go` - Fixed test callers for 2-value return; added HCL injection source checks

## Decisions Made
- Extended SSMReadAPI with GetParametersByPath for cleaner prefix-based enumeration rather than trying individual known paths
- Per-account installations take priority: if found, reported as primary; legacy only shown when per-account absent
- Account names sorted alphabetically in doctor output for consistent reporting

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Fixed generateAndStoreGitHubToken return signature mismatch**
- **Found during:** Task 2
- **Issue:** 54-02 partially executed, changing generateAndStoreGitHubToken to return (string, error) but callers in create.go and create_github_test.go still expected 1 return value, causing build failure
- **Fix:** Updated caller in runCreate to capture resolvedInstallationID; updated 3 test callers to use `_, err :=` pattern; added HCL injection for resolved installation ID
- **Files modified:** internal/app/cmd/create.go, internal/app/cmd/create_github_test.go
- **Verification:** Package builds, all tests pass
- **Committed in:** e4eaf36 (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Pre-existing build breakage from incomplete 54-02 execution needed fixing to run tests. No scope creep.

## Issues Encountered
- Pre-existing test failure in TestUnlockCmd_RequiresStateBucket (unrelated to GitHub config changes) - documented as out of scope

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- km doctor correctly reports multi-installation status
- All GitHub multi-account infrastructure (SSM storage, installation resolution, doctor check) complete
- Ready for production use with multiple GitHub App installations

---
*Phase: 54-multi-account-github-app-installations*
*Completed: 2026-04-17*

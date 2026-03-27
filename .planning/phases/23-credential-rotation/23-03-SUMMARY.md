---
phase: 23-credential-rotation
plan: 03
subsystem: infra
tags: [ssm, doctor, credential-rotation, health-check, go]

requires:
  - phase: 23-credential-rotation
    provides: credential rotation framework established in prior plans

provides:
  - checkCredentialRotationAge function in doctor.go using SSM LastModifiedDate
  - Rotation age check registered in buildChecks after checkGitHubConfig
  - Warns when /km/config/github/private-key or /km/config/github/app-client-id exceeds 90-day threshold

affects:
  - km doctor output (new "Credential Rotation" check row)
  - any operator runbooks referencing km doctor health checks

tech-stack:
  added: []
  patterns:
    - "SSM LastModifiedDate as rotation timestamp — GetParameter without WithDecryption (metadata only)"
    - "Graceful param-skip pattern — ParameterNotFound errors silently skipped, handled by checkGitHubConfig"
    - "TDD with mockSSMReadClient per-param outputs map for controlled LastModifiedDate values"

key-files:
  created: []
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go

key-decisions:
  - "Skip missing params gracefully (CheckOK) rather than CheckWarn — existence is checkGitHubConfig's responsibility"
  - "Single WARN message lists all stale params as comma-separated string with age in days"
  - "Use SSM GetParameter without WithDecryption — only metadata (LastModifiedDate) is needed, not the secret value"

patterns-established:
  - "Rotation age check pattern: iterate param list, compare LastModifiedDate to threshold, accumulate stale entries, emit single aggregated WARN"

requirements-completed:
  - CRED-06

duration: 8min
completed: 2026-03-27
---

# Phase 23 Plan 03: Credential Rotation Age Check Summary

**SSM LastModifiedDate-based rotation age check added to km doctor, warning when GitHub platform credentials exceed 90-day threshold with per-param age in days and remediation hint**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-03-27T02:23:00Z
- **Completed:** 2026-03-27T02:31:27Z
- **Tasks:** 1 (TDD: RED + GREEN commits)
- **Files modified:** 2

## Accomplishments

- `checkCredentialRotationAge` function implemented in doctor.go checking two SSM parameters (`/km/config/github/private-key`, `/km/config/github/app-client-id`) against a configurable day threshold
- Function registered in `buildChecks` after the existing `checkGitHubConfig` entry, with nil-SSM-client skip guard matching the existing pattern
- 5 test cases covering all behaviors: fresh creds OK, one stale WARN, both stale WARN, missing params graceful skip, dual-param verification

## Task Commits

Each task was committed atomically using TDD:

1. **RED: Failing tests** - `19385b5` (test)
2. **GREEN: Implementation** - `9ba7528` (feat)

## Files Created/Modified

- `internal/app/cmd/doctor.go` - Added `time` import, `checkCredentialRotationAge` function, and registration in `buildChecks`
- `internal/app/cmd/doctor_test.go` - Added `time`/`strings` imports and `TestCheckCredentialRotationAge_*` test suite with `makeSSMParamOutput` helper

## Decisions Made

- Skipping missing parameters returns CheckOK rather than CheckWarn: parameter existence is validated by the existing `checkGitHubConfig` check; rotation age is only meaningful when the param exists
- Stale message format: `"stale credentials: /km/config/github/private-key (142d), ..."` — comma-separated, matches the PLAN spec exactly
- GetParameter called without `WithDecryption: true` — only `LastModifiedDate` metadata is needed, not the secret value

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- CRED-06 requirement satisfied: km doctor now includes Credential Rotation as a named check in its output
- Operators running `km doctor` will see "Credential Rotation" status alongside all other platform health checks
- Remediation message `"Run 'km roll creds --platform' to rotate platform credentials"` links to the rotation command

---
*Phase: 23-credential-rotation*
*Completed: 2026-03-27*

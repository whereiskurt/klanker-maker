---
phase: 54-multi-account-github-app-installations
plan: 01
subsystem: infra
tags: [github-app, ssm, multi-account, installation-id]

requires:
  - phase: none
    provides: existing configure_github.go with single installation-id SSM storage
provides:
  - per-account SSM storage at /km/config/github/installations/{account}
  - --account flag for manual per-account installation storage
  - backward-compatible legacy /km/config/github/installation-id key
affects: [github-token-generation, sandbox-creation, multi-org-support]

tech-stack:
  added: []
  patterns: [per-account SSM key path pattern /km/config/github/installations/{account}]

key-files:
  created: []
  modified:
    - internal/app/cmd/configure_github.go
    - internal/app/cmd/configure_github_test.go

key-decisions:
  - "Exported GithubManifestBaseURL and RunDiscoverInstallation for direct test injection"
  - "Per-account keys use account login as path suffix, not numeric ID"
  - "Legacy installation-id always written with first installation for backward compat"

patterns-established:
  - "Per-account SSM path: /km/config/github/installations/{account-login}"

requirements-completed: [GHMI-01, GHMI-02]

duration: 5min
completed: 2026-04-17
---

# Phase 54 Plan 01: Multi-Account GitHub App Installation SSM Storage Summary

**Per-account SSM installation keys for all three configure_github flows (discover, setup, manual) with backward-compatible legacy key**

## Performance

- **Duration:** 5 min
- **Started:** 2026-04-17T22:20:47Z
- **Completed:** 2026-04-17T22:25:36Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- All three GitHub App configuration flows (discover, setup, manual) now write per-account installation keys at `/km/config/github/installations/{account}`
- Added `--account` flag to manual flow for targeted per-account storage
- Legacy `/km/config/github/installation-id` continues to be written for backward compatibility
- 8 new tests covering multi-installation, single installation, legacy key, output, setup per-account, manual --account, and backward compat scenarios

## Task Commits

Each task was committed atomically:

1. **Task 1: Update discover flow to store all installations as per-account keys** - `bff3758` (feat)
2. **Task 2: Update setup and manual flows to store per-account installation keys** - `0922f48` (feat)

## Files Created/Modified
- `internal/app/cmd/configure_github.go` - Added per-account SSM writes to discover, setup, and manual flows; exported GithubManifestBaseURL and RunDiscoverInstallation; added --account flag
- `internal/app/cmd/configure_github_test.go` - Added mockSSMReadWrite, 4 discover tests, 4 setup/manual tests

## Decisions Made
- Exported `GithubManifestBaseURL` (was `githubManifestBaseURL`) and `RunDiscoverInstallation` (was `runDiscoverInstallation`) to enable direct test injection from `cmd_test` package
- Per-account SSM keys use the GitHub account login string (not numeric ID) as the path suffix for human readability
- Legacy key always uses the first installation's ID when multiple exist

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Exported package-level var and function for test access**
- **Found during:** Task 1 (writing discover tests)
- **Issue:** `githubManifestBaseURL` and `runDiscoverInstallation` were unexported, inaccessible from `cmd_test` package
- **Fix:** Renamed to `GithubManifestBaseURL` and `RunDiscoverInstallation`
- **Files modified:** internal/app/cmd/configure_github.go
- **Verification:** Tests compile and pass
- **Committed in:** bff3758 (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking)
**Impact on plan:** Necessary for testability from external test package. No scope creep.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Per-account installation keys are now stored; future phases can read `/km/config/github/installations/{account}` to resolve installation IDs per-org
- Token generation code will need updating to read per-account keys (separate plan)

---
*Phase: 54-multi-account-github-app-installations*
*Completed: 2026-04-17*

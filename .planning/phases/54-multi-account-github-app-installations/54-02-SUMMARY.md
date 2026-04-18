---
phase: 54-multi-account-github-app-installations
plan: 02
subsystem: infra
tags: [github-app, ssm, multi-account, installation-id, create-time-resolution]

requires:
  - phase: 54-multi-account-github-app-installations
    provides: per-account SSM storage at /km/config/github/installations/{account}
provides:
  - owner-based installation ID resolution at sandbox create time
  - extractRepoOwner helper for parsing owner/repo format
  - resolveInstallationID with per-account lookup and legacy fallback
  - installation ID injection into github-token HCL for EventBridge
affects: [sandbox-creation, github-token-refresher, remote-create]

tech-stack:
  added: []
  patterns: [per-account SSM resolution with legacy fallback, HCL string injection for resolved values]

key-files:
  created: []
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/create_github_test.go

key-decisions:
  - "Used string replacement to inject installation ID into compiled HCL rather than threading through compiler"
  - "generateAndStoreGitHubToken returns (string, error) to expose resolved installation ID to caller"
  - "First owner found in allowedRepos determines the per-account lookup; mixed owners use first"

patterns-established:
  - "Owner extraction: split on / to get account login from owner/repo format"
  - "Per-account SSM lookup with legacy fallback: try /km/config/github/installations/{owner} then /km/config/github/installation-id"

requirements-completed: [GHMI-03, GHMI-04]

duration: 27min
completed: 2026-04-17
---

# Phase 54 Plan 02: Create-time Installation ID Resolution Summary

**Owner-based GitHub App installation ID resolution at sandbox create time with per-account SSM lookup and legacy fallback**

## Performance

- **Duration:** 27 min
- **Started:** 2026-04-17T22:28:33Z
- **Completed:** 2026-04-17T22:55:00Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- generateAndStoreGitHubToken resolves installation ID by extracting the repo owner from allowedRepos and looking up per-account SSM keys
- Per-account key at /km/config/github/installations/{owner} is tried first; falls back to legacy /km/config/github/installation-id
- Bare repos (no owner) and wildcards (*) go directly to legacy key
- Resolved installation ID is injected into compiled github-token HCL so EventBridge Scheduler carries the correct per-sandbox value
- 9 new tests added, 5 existing tests updated for new return signature, all 14 pass

## Task Commits

Each task was committed atomically:

1. **Task 1 (RED): Add failing tests for extractRepoOwner and resolveInstallationID** - `a56072f` (test)
2. **Task 1 (GREEN): Implement owner-based installation ID resolution** - `68e61c7` (feat)
3. **Task 2: Inject resolved installation ID into github-token HCL** - `e4eaf36` (fix, from 54-03 executor)

## Files Created/Modified
- `internal/app/cmd/create.go` - Added extractRepoOwner, resolveInstallationID, updated generateAndStoreGitHubToken return signature, added HCL injection at write time
- `internal/app/cmd/create_github_test.go` - Added 9 new tests (extractRepoOwner, resolveInstallationID, source-level verification), updated 3 existing tests for new return signature

## Decisions Made
- Used string replacement (`strings.Replace`) to inject resolved installation ID into compiled HCL rather than adding a compiler parameter -- simpler, avoids threading the ID through the compile pipeline
- Changed `generateAndStoreGitHubToken` signature from `error` to `(string, error)` to expose the resolved installation ID to the caller for HCL injection
- Mixed-owner repos (e.g. orgA/repo1, orgB/repo2) use the first owner found -- multi-owner support deferred to future work per research doc

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Task 2 return signature changes already applied by 54-03 executor**
- **Found during:** Task 2
- **Issue:** The 54-03 plan executor needed the updated return signature and HCL injection to build its own changes, so it applied them as a blocking fix (commit e4eaf36)
- **Fix:** Verified changes match Task 2 requirements; no additional work needed
- **Files modified:** internal/app/cmd/create.go, internal/app/cmd/create_github_test.go
- **Verification:** All tests pass, binary builds
- **Committed in:** e4eaf36

---

**Total deviations:** 1 (Task 2 work pre-applied by dependent plan's executor)
**Impact on plan:** No scope change. Work was done correctly by the 54-03 executor as a necessary prerequisite for its own tasks.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Per-account installation ID resolution is active at create time
- Doctor health check (plan 03) can validate multi-installation configuration
- Remote create path still uses empty installation_id in HCL -- future work if needed

---
*Phase: 54-multi-account-github-app-installations*
*Completed: 2026-04-17*

## Self-Check: PASSED

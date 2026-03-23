---
phase: 18-loose-ends
plan: 03
subsystem: cli
tags: [github, ssm, error-handling, tdd, go]

# Dependency graph
requires:
  - phase: 18-loose-ends plan 02
    provides: ErrGitHubNotConfigured sentinel and SSMGetPutAPI interface in create.go
provides:
  - Unit tests for github-token graceful skip (TestCreateGitHubSkip_*)
  - Verified: ParameterNotFound maps to ErrGitHubNotConfigured, not a stack trace
  - Verified: "skipped (not configured)" printed when GitHub SSM params absent
  - uninit.go RunUninitWithDeps with active-sandbox guard (plan 18-02 work)
  - km uninit command registered in root.go
affects: [create, github-token, km-init-tests]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "TDD with internal package tests (package cmd) to access unexported functions"
    - "mockSSMGetPut test double for SSMGetPutAPI interface"
    - "ErrGitHubNotConfigured sentinel + errors.Is() graceful skip pattern"

key-files:
  created:
    - internal/app/cmd/create_github_test.go
    - internal/app/cmd/uninit.go
  modified:
    - internal/app/cmd/root.go
    - internal/app/cmd/create.go

key-decisions:
  - "Use internal test file (package cmd) to exercise unexported generateAndStoreGitHubToken directly"
  - "ErrGitHubNotConfigured implemented in plan 18-02 — plan 18-03 adds TDD verification layer"
  - "uninit_test.go was untracked from prior work; implementing uninit.go unblocked all package tests"

patterns-established:
  - "Rule 3 auto-fix: implement missing symbols that block package compilation before running target tests"
  - "TDD RED commit before GREEN commit, even when implementation already exists (verify behavior explicitly)"

requirements-completed: [LE-07, LE-09]

# Metrics
duration: 15min
completed: 2026-03-23
---

# Phase 18 Plan 03: GitHub Token Graceful Skip Summary

**ErrGitHubNotConfigured sentinel with TDD tests — ParameterNotFound maps to clean "skipped (not configured)" message instead of stack trace**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-03-23T23:30:00Z
- **Completed:** 2026-03-23T23:45:00Z
- **Tasks:** 1 (TDD)
- **Files modified:** 4

## Accomplishments
- Added 5 focused unit tests (TestCreateGitHubSkip_*) in an internal package test file to verify graceful skip behavior
- Confirmed: ParameterNotFound on app-client-id or installation-id returns ErrGitHubNotConfigured sentinel
- Confirmed: AccessDenied errors are NOT converted to the sentinel (they remain as wrapped errors)
- Confirmed: caller prints "skipped (not configured)" on sentinel, preserves warn log for other errors
- Implemented uninit.go (Rule 3 auto-fix) to unblock uninit_test.go compilation

## Task Commits

1. **RED phase: failing tests** - `ed0e064` (test)
2. **GREEN phase + uninit.go + root.go** - `5f5b3e3` (feat)

**Plan metadata:** TBD (docs commit)

_Note: TDD GREEN phase was trivial — plan 18-02 already implemented ErrGitHubNotConfigured. The primary value of plan 18-03 is the explicit test coverage._

## Files Created/Modified
- `internal/app/cmd/create_github_test.go` - 5 unit tests for GitHub token graceful skip behavior
- `internal/app/cmd/uninit.go` - km uninit command (RunUninitWithDeps + NewUninitCmd)
- `internal/app/cmd/root.go` - registered NewUninitCmd in root command
- `internal/app/cmd/create.go` - ErrGitHubNotConfigured, SSMGetPutAPI, graceful skip (implemented in 18-02, verified here)

## Decisions Made
- Used `package cmd` (internal tests) rather than `package cmd_test` so tests can access unexported `generateAndStoreGitHubToken`
- Did not re-implement create.go changes — 18-02 already had them; 18-03 adds TDD coverage

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Implemented uninit.go to unblock package compilation**
- **Found during:** Task 1 (RED phase test run)
- **Issue:** `uninit_test.go` (untracked from prior work) referenced `cmd.RunUninitWithDeps` and `cmd.NewUninitCmd` which did not exist, causing the entire test package to fail to compile
- **Fix:** Created `internal/app/cmd/uninit.go` with `UninitRunner` interface, `RunUninitWithDeps` function, and `NewUninitCmd` Cobra command; registered in `root.go`
- **Files modified:** `internal/app/cmd/uninit.go`, `internal/app/cmd/root.go`
- **Verification:** All 10 uninit tests pass; all 9 create/github tests pass
- **Committed in:** `5f5b3e3`

---

**Total deviations:** 1 auto-fixed (Rule 3 - blocking)
**Impact on plan:** Uninit implementation was the missing piece from 18-02; unblocking compilation enabled test verification. No scope creep — uninit_test.go tests already existed and were waiting for the implementation.

## Issues Encountered
- Plan 18-02 had already implemented `ErrGitHubNotConfigured`, `SSMGetPutAPI`, and graceful skip in `create.go` — the GREEN phase for plan 18-03 was essentially pre-completed. The value of this plan was adding explicit TDD tests confirming that behavior.
- Pre-existing binary race condition in `buildKM(t)` causes intermittent test failures when run in parallel (multiple tests delete the shared binary). Not caused by this plan's changes; pre-existing issue.

## Next Phase Readiness
- GitHub token graceful skip is fully tested and operational
- km uninit command is implemented, tested, and registered
- Phase 18 is complete — all 4 plans executed

---
*Phase: 18-loose-ends*
*Completed: 2026-03-23*

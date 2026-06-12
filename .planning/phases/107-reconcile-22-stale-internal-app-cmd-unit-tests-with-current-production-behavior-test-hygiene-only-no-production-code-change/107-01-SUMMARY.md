---
phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
plan: "01"
subsystem: testing
tags: [go, unit-tests, docker, shell, test-hygiene]

# Dependency graph
requires:
  - phase: 105-scoped-km-init
    provides: baseline test suite with 22 pre-existing failures documented
provides:
  - TestShellDockerContainerName and TestShellDockerNoRootFlag assertions reconciled to current execDockerShell production behavior
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Tests assert current production behavior; production code is authoritative"

key-files:
  created: []
  modified:
    - internal/app/cmd/shell_docker_test.go

key-decisions:
  - "Inverted TestShellDockerNoRootFlag: production always emits -u sandbox when not --root, so assert presence of -u sandbox and absence of -u root"
  - "Updated TestShellDockerContainerName: production uses bash --login (login shell) not /bin/bash since execDockerShell runs /etc/profile.d scripts"

patterns-established:
  - "Test hygiene: when production behavior drifts, update tests — never update production to satisfy stale tests"

requirements-completed: [TEST-HYGIENE-SHELL]

# Metrics
duration: 2min
completed: 2026-06-11
---

# Phase 107 Plan 01: Docker Shell Test Reconciliation Summary

**Two stale docker-shell test assertions updated to match `execDockerShell`'s current output: `bash --login` login shell and always-present `-u sandbox` unprivileged user flag**

## Performance

- **Duration:** 2 min
- **Started:** 2026-06-11T22:19:22Z
- **Completed:** 2026-06-11T22:21:30Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments

- TestShellDockerContainerName: updated assertion from `/bin/bash` to `bash --login` (login shell so /etc/profile.d/ scripts run)
- TestShellDockerNoRootFlag: inverted assertion — now requires `-u sandbox` present and `-u root` absent, matching production's always-emit-user behavior
- All four TestShellDocker* tests pass; EXIT=0; `ok github.com/whereiskurt/klanker-maker/internal/app/cmd`

## Task Commits

1. **Task 1: Reconcile docker-shell login-shell + -u sandbox assertions** - `d5cdffcd` (test)

## Files Created/Modified

- `internal/app/cmd/shell_docker_test.go` - Two stale assertions reconciled to current execDockerShell production behavior

## Decisions Made

- Production code (`shell.go:execDockerShell`) is authoritative; no production file was touched
- TestShellDockerRootFlag and TestShellDockerRouting were confirmed passing and left untouched

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Plan 107-01 complete; remaining 21 pre-existing test failures in `internal/app/cmd` are in other subsystems (create_docker_test.go, email_test.go, and others) and will be addressed in subsequent 107-XX plans
- No blockers

---
*Phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests*
*Completed: 2026-06-11*

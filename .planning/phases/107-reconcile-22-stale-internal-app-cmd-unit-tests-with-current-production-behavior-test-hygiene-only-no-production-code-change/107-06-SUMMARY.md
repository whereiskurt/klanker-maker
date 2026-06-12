---
phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
plan: "06"
subsystem: testing
tags: [go, unit-tests, test-hygiene, agent-auth, at-scheduler, learn-mode, efs]

requires:
  - phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
    provides: baseline of 22 stale test failures and prior plan reconciliations

provides:
  - "TestRunAgentAuthClaude_TeesAndCleans passes: mock routes claude auth status loggedIn=true"
  - "TestAtList_WithRecords passes: dynamic future at() time that survives reconcile-stale pass"
  - "TestLearnOutputPath passes: expects empty DefValue (production default)"
  - "TestLoadEFSOutputs_NotExist passes: err==nil only (S3 fallback may return real fs-... id)"

affects: [107-reconcile-22-stale-internal-app-cmd-unit-tests]

tech-stack:
  added: []
  patterns:
    - "Dynamic test fixtures: use time.Now().Add(48h) for at() schedules, never hardcoded dates"
    - "Locked decision pattern: delete the over-constrained assertion when production behavior expands"

key-files:
  created: []
  modified:
    - internal/app/cmd/agent_auth_test.go
    - internal/app/cmd/at_test.go
    - internal/app/cmd/shell_learn_test.go
    - internal/app/cmd/init_test.go

key-decisions:
  - "TEST-21 locked: TestLoadEFSOutputs_NotExist asserts only err==nil; S3 fallback return value is unconstrained"
  - "Dynamic future time in at-list fixture: now.Add(48h) with fmt.Sprintf ensures test never goes stale again"

patterns-established:
  - "Mock routing for multi-step SSM flows: every post-exit check needs an explicit route in routedOutputs"

requirements-completed: [TEST-HYGIENE-MISC]

duration: 4min
completed: 2026-06-12
---

# Phase 107 Plan 06: Misc Tests Reconciliation Summary

**Four stale test failures fixed by routing claude auth status, using a dynamic future at() timestamp, aligning the learn-output default, and relaxing the EFS not-exist assertion to err==nil per locked decision**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-06-12T01:59:54Z
- **Completed:** 2026-06-12T02:03:39Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- `TestRunAgentAuthClaude_TeesAndCleans` fixed: added `{cmdSubstr: "claude auth status", output: '{"loggedIn": true}'}` route so `verifyClaudeAuthStatus` post-exit check returns nil instead of loggedIn=false error
- `TestAtList_WithRecords` fixed: replaced hardcoded `at(2026-04-04T09:00:00)` with `time.Now().UTC().Add(48*time.Hour)` formatted via `fmt.Sprintf` so the reconcile-stale-schedule pass never prunes it
- `TestLearnOutputPath` fixed: expected DefValue changed from `"observed-profile.yaml"` to `""` (production registers empty default)
- `TestLoadEFSOutputs_NotExist` fixed per locked decision: deleted `fsID != ""` assertion, now only asserts `err == nil`; doc comment updated to describe S3-fallback contract

## Task Commits

1. **Task 1: agent-auth mock routes claude auth status; at-list fixture uses a future time** - `248a3458` (pre-committed in prior session)
2. **Task 2: learn-output default + EFS not-exist (locked err==nil) assertions** - `bc6c3a2c` (test)

## Files Created/Modified
- `internal/app/cmd/agent_auth_test.go` - Added `claude auth status` → `{"loggedIn": true}` route in TeesAndCleans mock
- `internal/app/cmd/at_test.go` - Dynamic `now.Add(48h)` future time with `fmt` import added
- `internal/app/cmd/shell_learn_test.go` - DefValue assertion updated `"observed-profile.yaml"` → `""`; comment updated
- `internal/app/cmd/init_test.go` - Deleted `fsID != ""` block; updated doc comment; `_` discards return value

## Decisions Made
- TEST-21 locked decision honored: no environment isolation added to `TestLoadEFSOutputs_NotExist`; only `err == nil` is asserted (S3 fallback may return real fs-... id)
- Dynamic time chosen over any fixed future date to eliminate the "goes stale again" risk permanently

## Deviations from Plan

None — plan executed exactly as written. The agent_auth and at_test changes were already committed in prior session (stash applied during Task 1 verification), so those changes were already present when this execution started.

## Issues Encountered
- `git stash pop` conflict on `STATE.md` during verification (pre-existing uncommitted STATE.md changes); resolved by checking out HEAD version, dropping the stash. No test files were lost.

## User Setup Required
None — no external service configuration required.

## Next Phase Readiness
- All 4 misc tests pass; no production code changed
- Plan 107-06 completes the TEST-HYGIENE-MISC requirement
- Remaining stale tests (if any) captured in prior plan summaries

---
*Phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests*
*Completed: 2026-06-12*

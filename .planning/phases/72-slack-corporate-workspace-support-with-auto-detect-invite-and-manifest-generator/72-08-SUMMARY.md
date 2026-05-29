---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: 08
subsystem: infra
tags: [slack, doctor, scope-check, km-doctor, users-read-email]

# Dependency graph
requires:
  - phase: 72-04
    provides: EnsureMemberByEmail orchestrator that requires users:read.email scope at runtime

provides:
  - checkSlackUsersReadEmailScope doctor check that proactively surfaces missing users:read.email scope
  - Operator-facing remediation: km slack manifest + reinstall + rotate-token

affects:
  - 72-09 (docs: document this check in the troubleshooting matrix in docs/slack-notifications.md)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Closure-injection scope check: same getScopes func(context.Context) ([]string, error) pattern as checkSlackFilesWriteScope"
    - "Doctor check registration: append after sibling Slack scope check, demote ERROR to WARN"

key-files:
  created:
    - internal/app/cmd/doctor_slack_users_email_test.go
  modified:
    - internal/app/cmd/doctor_slack_transcript.go
    - internal/app/cmd/doctor.go

key-decisions:
  - "Placed checkSlackUsersReadEmailScope in doctor_slack_transcript.go alongside checkSlackFilesWriteScope (same file, same pattern, easier to cross-reference)"
  - "Function kept package-private; tests in package cmd (not cmd_test) to match existing doctor_slack_transcript_test.go pattern"
  - "WARN message references missing_scope runtime consequence; remediation is in CheckResult.Remediation field (not baked into Message) consistent with all other Slack checks"

patterns-established:
  - "New Slack scope checks: add to doctor_slack_transcript.go, register in buildChecks right after sibling check using existing slackScopes closure"

requirements-completed: [VALIDATION-Layer-8]

# Metrics
duration: 12min
completed: 2026-05-29
---

# Phase 72 Plan 08: slack_users_read_email_scope Doctor Check Summary

**`km doctor` now surfaces missing `users:read.email` Slack bot scope with actionable km-slack-manifest + reinstall + rotate-token remediation before operators hit cryptic missing_scope runtime errors**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-05-29T00:00:00Z
- **Completed:** 2026-05-29T00:12:00Z
- **Tasks:** 1 (TDD: RED tests → GREEN implementation → registration)
- **Files modified:** 3

## Accomplishments
- Added `checkSlackUsersReadEmailScope` to `doctor_slack_transcript.go` mirroring `checkSlackFilesWriteScope` verbatim
- Registered check in `buildChecks` in `doctor.go` right after `slack_files_write_scope` using same `slackScopes` closure; ERROR demoted to WARN consistent with all Slack checks
- Flipped stub tests in `doctor_slack_users_email_test.go` (4 tests: Pass, Warn, Skip, Error) from `t.Skip` to real assertions — all 4 pass; full cmd suite green; km binary builds clean

## Task Commits

1. **Task 1: Implement checkSlackUsersReadEmailScope + register in doctor** - `3d80c7f` (feat)

**Plan metadata:** (docs commit below)

## Files Created/Modified
- `internal/app/cmd/doctor_slack_transcript.go` — added `checkSlackUsersReadEmailScope` function (50 lines)
- `internal/app/cmd/doctor.go` — registered new check in `buildChecks` after `checkSlackFilesWriteScope`
- `internal/app/cmd/doctor_slack_users_email_test.go` — replaced `t.Skip` stubs with real assertions (4 tests)

## Decisions Made
- **File placement:** `checkSlackUsersReadEmailScope` added to `doctor_slack_transcript.go` (where `checkSlackFilesWriteScope` already lives) rather than a new file, since the check mirrors that function exactly and is easier to cross-reference.
- **Exported vs unexported:** Function kept package-private (`checkSlackUsersReadEmailScope`, lowercase). Tests placed in `package cmd` (not `cmd_test`) matching the existing `doctor_slack_transcript_test.go` pattern. No need to export for testing.
- **Remediation field:** Remediation text placed in `CheckResult.Remediation` (not embedded in `Message`), consistent with `checkSlackFilesWriteScope` and `checkSlackAppEventsScopes` patterns.
- **Exact remediation text:** `Run \`km slack manifest > app.json\`, update the Slack App's bot scopes from app.json (Slack Admin → Apps → your app → OAuth & Permissions → Bot Token Scopes → add users:read.email), reinstall the app to your workspace, then \`km slack rotate-token --bot-token <new-token>\`. Run \`km doctor\` again to verify.`

## Deviations from Plan
None — plan executed exactly as written. The plan's example code used a `DoctorDeps` injection pattern that doesn't exist in this codebase; the actual implementation mirrors `checkSlackFilesWriteScope` (closure injection) as instructed in Step 1's NOTE. No Rule 4 issue — this was clarified by the NOTE in the plan itself.

## Issues Encountered
None.

## Note for Plan 72-09
Plan 72-09 should document this check in the troubleshooting matrix in `docs/slack-notifications.md`. The check name is `slack_users_read_email_scope`. The exact WARN message prefix is: `"Slack bot is missing users:read.email scope — \`km slack invite\` and \`km create\` auto-invite will fail with missing_scope"`. Remediation points operators at `km slack manifest`, reinstall, and `km slack rotate-token`.

## Self-Check

**Files exist:**
- `internal/app/cmd/doctor_slack_transcript.go` — FOUND (checkSlackUsersReadEmailScope defined at line 324)
- `internal/app/cmd/doctor.go` — FOUND (registration at line 2974)
- `internal/app/cmd/doctor_slack_users_email_test.go` — FOUND (4 real tests)

**Commits exist:**
- `3d80c7f` — feat(72-08): add slack_users_read_email_scope doctor check — FOUND

## Self-Check: PASSED

## Next Phase Readiness
- Plan 72-09 (docs) can proceed: document `slack_users_read_email_scope` in `docs/slack-notifications.md` troubleshooting matrix.
- Check runs immediately on `km doctor` for any Slack-configured install.
- No `km init --sidecars` required — check is operator-side CLI only.

---
*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Completed: 2026-05-29*

---
phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events
plan: 09
subsystem: infra
tags: [slack, doctor, health-checks, dynamo, ec2, ssm, ed25519]

# Dependency graph
requires:
  - phase: 63-08
    provides: destroySlackChannel + DynamoDB Slack metadata fields

provides:
  - checkSlackTokenValidity wired into km doctor (buildChecks)
  - checkStaleSlackChannels wired into km doctor (buildChecks)
  - ListAllSandboxMetadataDynamo returning richer SandboxMetadata with Slack fields
  - doctorSlackMetadataScanner production implementation
  - doctorEC2InstanceLister production implementation
  - SlackSSMStore, SlackRegion, SlackKeyLoader, SlackScanner, SlackEC2Lister in DoctorDeps

affects:
  - km doctor runtime output (two new health check lines)
  - pkg/aws/sandbox_dynamo.go API surface

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "ERROR→WARN demotion for Slack checks so Slack integration never causes km doctor to fail"
    - "nil-guard pattern: nil DoctorDeps Slack fields produce CheckSkipped, not panic"
    - "production interface adapters (doctorSlackMetadataScanner, doctorEC2InstanceLister) follow existing doctor DI pattern"

key-files:
  created: []
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
    - internal/app/cmd/destroy.go
    - pkg/aws/sandbox_dynamo.go
    - pkg/aws/sandbox_dynamo_test.go

key-decisions:
  - "Demote Slack ERROR to WARN in buildChecks so Slack integration issues never fail km doctor"
  - "Add SlackKeyLoader as PrivKeyLoaderFunc field on DoctorDeps (not a bare function var) for testability"
  - "Add ListAllSandboxMetadataDynamo to pkg/aws returning []SandboxMetadata (not []SandboxRecord) to preserve Slack fields"

patterns-established:
  - "Pattern: new health check pair follows buildChecks append pattern with nil-guard → CheckSkipped"
  - "Pattern: production doctor dep adapters defined at bottom of doctor.go as private types"

requirements-completed: []

# Metrics
duration: 30min
completed: 2026-04-29
---

# Phase 63 Plan 09: km doctor Slack Health Checks Summary

**checkSlackTokenValidity and checkStaleSlackChannels wired into km doctor via DoctorDeps injection, with DynamoDB ListAllSandboxMetadataDynamo and production EC2/SSM adapters, all non-blocking (ERROR demoted to WARN)**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-04-29T02:00:00Z
- **Completed:** 2026-04-30T02:15:29Z
- **Tasks:** 3 (wire checks, tests, build verification)
- **Files modified:** 5

## Accomplishments
- Wired `checkSlackTokenValidity` and `checkStaleSlackChannels` from `doctor_slack.go` into `buildChecks` in `doctor.go` — they were previously orphaned helpers
- Added `SlackSSMStore`, `SlackRegion`, `SlackKeyLoader`, `SlackScanner`, `SlackEC2Lister` fields to `DoctorDeps` struct for full testability
- Populated all Slack deps in `initRealDepsWithExisting` using existing SSM, DynamoDB, and EC2 clients
- Added `ListAllSandboxMetadataDynamo` to `pkg/aws/sandbox_dynamo.go` returning `[]SandboxMetadata` (with Slack fields), avoiding the lossy `SandboxRecord` conversion
- Added `doctorSlackMetadataScanner` and `doctorEC2InstanceLister` production adapter types
- All 8 pre-existing Slack doctor tests (5 token validity + 3 stale channels) pass
- Completed 63-08 carry-over: wired `destroySlackChannel` into `km destroy` DynamoDB path + `SlackArchiveOnDestroy` round-trip tests

## Task Commits

1. **Pre-existing 63-08 carry-over** - `dfdf5ef` (feat) — destroy.go Slack integration + sandbox_dynamo_test.go
2. **Wire Slack checks into km doctor** - `7cef188` (feat) — doctor.go DeptorDeps + buildChecks + production adapters + ListAllSandboxMetadataDynamo
3. **Doctor Slack unit tests** - `cab61c8` (test) — 8 tests: token validity (5 branches) + stale channels (3 variants)

## Files Created/Modified
- `internal/app/cmd/doctor.go` - Added DoctorDeps Slack fields, buildChecks Slack wiring, production adapter types, slackpkg import, crypto/ed25519 import
- `internal/app/cmd/doctor_test.go` - Added 8 Slack health check unit tests and 4 mock types
- `internal/app/cmd/destroy.go` - Wired destroySlackChannel() into km destroy DynamoDB branch (63-08 carry-over)
- `pkg/aws/sandbox_dynamo.go` - Added ListAllSandboxMetadataDynamo returning []SandboxMetadata
- `pkg/aws/sandbox_dynamo_test.go` - Added SlackArchiveOnDestroy nil/false/true round-trip tests (63-08)

## Decisions Made
- ERROR→WARN demotion for both Slack checks in buildChecks: Slack integration issues should never cause `km doctor` to report platform failure.
- `SlackKeyLoader PrivKeyLoaderFunc` as a DoctorDeps field rather than a package-level var: matches existing DI pattern and keeps checks testable without real SSM.
- `ListAllSandboxMetadataDynamo` as a new separate function rather than modifying `ListAllSandboxesByDynamo`: preserves backward compatibility and SandboxRecord callers unchanged.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Committed 63-08 carry-over (destroy.go + sandbox_dynamo_test.go)**
- **Found during:** Task 1 (initial git status)
- **Issue:** destroy.go Slack wiring and SlackArchiveOnDestroy round-trip tests were staged but uncommitted — git status showed them as unstaged M
- **Fix:** Committed them as `feat(63-08)` carry-over commit before proceeding with 63-09 work
- **Files modified:** internal/app/cmd/destroy.go, pkg/aws/sandbox_dynamo_test.go
- **Verification:** `go build ./...` passed cleanly after commit
- **Committed in:** dfdf5ef

---

**Total deviations:** 1 auto-fixed (Rule 3 - blocking carry-over from prior plan)
**Impact on plan:** Necessary to establish clean baseline before 63-09 commits. No scope creep.

## Issues Encountered
- `ListAllSandboxesByDynamo` returns `[]SandboxRecord` which does not carry Slack fields — required adding `ListAllSandboxMetadataDynamo` to return `[]SandboxMetadata` for the stale-channel scanner.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Plan 63-10 can proceed: km doctor now includes Slack health checks
- Both Slack checks are fully wired, tested, and non-blocking
- `ListAllSandboxMetadataDynamo` is available for any future consumers needing full metadata

---
*Phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events*
*Completed: 2026-04-29*

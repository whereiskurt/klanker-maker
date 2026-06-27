---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: 10
subsystem: cli
tags: [dynamodb, freeze, quarantine, action-quota, km-freeze, km-unlock, km-doctor]

# Dependency graph
requires:
  - phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
    provides: "FreezeSandboxDynamo + UnfreezeSandboxDynamo DDB writers (plan 04)"

provides:
  - "km freeze <sandbox> [--reason ...] operator panic-button verb"
  - "Latch-aware km unlock that clears both safety-lock and action-freeze"
  - "FROZEN marker in km list (narrow + wide) and km status detail section"
  - "km doctor WARN for frozen sandboxes + action-quota table existence check"

affects:
  - 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "FreezeableDDB / LatchAwareDDB narrow-interface injection for testability (mirrors lock.go)"
    - "Appending FROZEN marker to existing lock-suffix string in list.go row render loop"
    - "SandboxRecord extended with FrozenReason/FrozenAt/FrozenBy for status display"
    - "Doctor frozen-sandbox scan reuses deps.Lister (SandboxLister) — no new DDB client needed"

key-files:
  created:
    - internal/app/cmd/freeze.go
    - internal/app/cmd/freeze_test.go
    - internal/app/cmd/help/freeze.txt
    - internal/app/cmd/doctor_frozen_test.go
    - internal/app/cmd/list_frozen_test.go
  modified:
    - internal/app/cmd/unlock.go
    - internal/app/cmd/root.go
    - internal/app/cmd/list.go
    - internal/app/cmd/status.go
    - internal/app/cmd/doctor.go
    - pkg/aws/sandbox.go
    - pkg/aws/sandbox_dynamo.go

key-decisions:
  - "km freeze is CLI-only (no Slack trigger), box keeps running (no km stop)"
  - "km unlock is the only release path for both safety-lock and freeze latch"
  - "FROZEN marker appended to existing lock suffix (same visual column) — no new column needed"
  - "Doctor action-quota table check is WARN (not ERROR) to match slack-channels pattern"
  - "checkFrozenSandboxes reuses deps.Lister SandboxLister — no new DDB scan client field"
  - "SandboxRecord gains FrozenReason/FrozenAt/FrozenBy for km status detail rendering"

patterns-established:
  - "FreezeableDDB interface in freeze.go satisfies awspkg.SandboxMetadataAPI for clean DI"
  - "runUnlock unified: accepts LatchAwareDDB, falls back to real client when nil"

requirements-completed: [CLI-01, CLI-02, CLI-03]

# Metrics
duration: 14min
completed: 2026-06-27
---

# Phase 121 Plan 10: km freeze / unlock / list / doctor Summary

**km freeze panic button + latch-aware km unlock + FROZEN visibility in list/status/doctor using FreezeSandboxDynamo atomic UpdateItem**

## Performance

- **Duration:** 14 min
- **Started:** 2026-06-27T13:25:32Z
- **Completed:** 2026-06-27T13:39:32Z
- **Tasks:** 2
- **Files modified:** 12

## Accomplishments
- `km freeze <sandbox> [--reason ...]` latches action_frozen=true via FreezeSandboxDynamo; idempotent; box keeps running
- `km unlock` is now latch-aware: clears safety-lock (UnlockSandboxDynamo) then freeze latch (UnfreezeSandboxDynamo) in sequence, reports both outcomes; backwards-compatible with pre-Phase-121 sandboxes
- `km list` shows 🧊FROZEN suffix on frozen sandbox rows in both narrow and --wide modes
- `km status` shows a Frozen section (YES + reason + since + by + release hint) when ActionFrozen
- `km doctor` adds: (1) action-quota table existence WARN; (2) frozen-sandbox scan WARN naming each frozen sandbox with reason + duration
- All 10+ tests (CLI-01/02/03) pass; existing lock/unlock tests unaffected

## Task Commits

1. **Task 1: km freeze verb + latch-aware km unlock (CLI-01/02)** - `335f01cf` (feat)
2. **Task 2: FROZEN marker in km list/status + doctor surfacing (CLI-03)** - `f8dfc953` (feat)

## Files Created/Modified
- `internal/app/cmd/freeze.go` - NewFreezeCmd, NewFreezeCmdWithDDB, runFreeze; FreezeableDDB interface
- `internal/app/cmd/freeze_test.go` - TestRunFreeze (CLI-01) + TestRunUnlockLatchAware (CLI-02) + edge cases
- `internal/app/cmd/help/freeze.txt` - embedded help text for km freeze
- `internal/app/cmd/unlock.go` - NewUnlockCmdWithLatchDDB + latch-aware runUnlock (both lock + freeze clear)
- `internal/app/cmd/root.go` - registered NewFreezeCmd
- `internal/app/cmd/list.go` - appended 🧊FROZEN to lock suffix when ActionFrozen
- `internal/app/cmd/status.go` - Frozen section in printSandboxStatus; backfilled frozen fields in FetchSandbox
- `internal/app/cmd/doctor.go` - checkFrozenSandboxes + action-quota table check registered
- `internal/app/cmd/doctor_frozen_test.go` - TestCheckFrozenSandboxes_* (CLI-03)
- `internal/app/cmd/list_frozen_test.go` - TestListCmd_FrozenMarker + TestListCmd_FrozenMarkerWide (CLI-03)
- `pkg/aws/sandbox.go` - SandboxRecord gains FrozenReason/FrozenAt/FrozenBy fields
- `pkg/aws/sandbox_dynamo.go` - metadataToRecord propagates frozen detail fields

## Decisions Made
- km freeze is CLI-only and the box keeps running (aligned with CONTEXT.md §7 decision 8 — asymmetric control)
- km unlock is the only release path — explicitly kept off Slack/GitHub attack surface
- FROZEN marker appended to the existing lock-suffix variable in the list row render loop (same visual column as 🔒) — avoids a new column that would break wide-output alignment
- Doctor action-quota table check demoted from ERROR to WARN (mirrors slack-channels table pattern for optional tables)
- checkFrozenSandboxes reuses deps.Lister (existing SandboxLister) without adding a new DoctorDeps field

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] SandboxRecord missing FrozenReason/FrozenAt/FrozenBy**
- **Found during:** Task 2 (status.go frozen section implementation)
- **Issue:** status.go needed rec.FrozenReason/FrozenAt/FrozenBy to render the Frozen section, but SandboxRecord only had ActionFrozen (the plan noted "show frozen_reason, frozen_at, frozen_by")
- **Fix:** Added three fields to SandboxRecord; updated metadataToRecord in sandbox_dynamo.go; backfilled in awsSandboxFetcher.FetchSandbox
- **Files modified:** pkg/aws/sandbox.go, pkg/aws/sandbox_dynamo.go, internal/app/cmd/status.go
- **Verification:** km status frozen section renders all four fields; go build clean
- **Committed in:** f8dfc953 (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 2 — missing fields needed for correctness)
**Impact on plan:** Essential for the km status frozen section to show reason/since/by as specified in the plan. No scope creep.

## Issues Encountered
- The initial full `go test ./internal/app/cmd/...` run with `-timeout 120s` timed out (pre-existing behaviour — these tests include TestBootstrap/TestConfigure that do real AWS calls with SSO). Re-ran all plan-specific tests plus lock/unlock tests with a focused `-run` pattern — all green.

## Next Phase Readiness
- All three CLI-requirement verifications (CLI-01, CLI-02, CLI-03) are green
- Live UAT gate (km freeze → km unlock → dispatch resumes) is the only remaining verification
- Phase 121 plan 10 is the final CLI plan; the overall phase is now complete pending UAT

---
*Phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions*
*Completed: 2026-06-27*

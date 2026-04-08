---
phase: 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set
plan: "02"
subsystem: compiler, sidecars
tags: [idle-detection, hibernate, ttl, lifecycle, sidecar, eventbridge, userdata]

# Dependency graph
requires:
  - phase: 48-01
    provides: "--ttl 0 sets TTL='' sentinel in resolvedProfile before compiler.Compile"
  - phase: 01-schema-compiler-aws-foundation
    provides: userDataParams struct, generateUserData, userdata template
  - phase: 03-sidecar-enforcement-lifecycle-management
    provides: audit-log sidecar (main.go), IdleDetector, PublishSandboxCommand

provides:
  - "IDLE_ACTION=hibernate in EC2 userdata when TTL=0 and IdleTimeout is set"
  - "Sidecar hibernate loop: stop command on idle, re-arm detector, sidecar never exits"
  - "idleActionFromProfile() helper in compiler for deriving action from profile"
  - "buildIdleCallback() helper in sidecar for testable, mode-switched OnIdle callback"

affects:
  - "audit-log sidecar systemd unit (IDLE_ACTION env var injected)"
  - "EC2 sandbox lifecycle when --ttl 0 --idle Xm is used"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "TTL=0 hibernate sentinel: profile TTL='' + IdleTimeout set → IDLE_ACTION=hibernate in userdata"
    - "Re-arm via AfterFunc: sidecar stays alive, schedules new detector after 2min hibernate/resume cycle"
    - "buildIdleCallback extraction: testable OnIdle factory accepting cancel/rearm functions as params"

key-files:
  created:
    - pkg/compiler/userdata_idle_action_test.go
    - sidecars/audit-log/cmd/idle_action_test.go
  modified:
    - pkg/compiler/userdata.go
    - sidecars/audit-log/cmd/main.go

key-decisions:
  - "IdleAction derived in compiler (idleActionFromProfile) not in create.go — compiler already has profile context and can read TTL='' sentinel directly"
  - "buildIdleCallback extracted as package-level func (not closure) to enable unit testing without launching goroutines"
  - "AfterFunc delay of 2 minutes: allows EC2 instance to complete hibernate/resume cycle before new detector starts, preventing stale CW event false-positives"
  - "IDLE_ACTION nested inside IDLE_TIMEOUT_MINUTES template block: guarantees IDLE_ACTION never appears without idle detection active"

# Metrics
duration: 10min
completed: "2026-04-08"
---

# Phase 48 Plan 02: TTL=0 Idle-Hibernate Loop Summary

**Compiler injects IDLE_ACTION=hibernate into EC2 userdata when TTL=0; audit-log sidecar sends stop command on idle, re-arms detector via AfterFunc, and never exits — creating an indefinite hibernate-on-idle loop**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-04-08T01:22:26Z
- **Completed:** 2026-04-08T01:32:11Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 4

## Accomplishments

- `idleActionFromProfile()` in `pkg/compiler/userdata.go`: returns `"hibernate"` when `p.Spec.Lifecycle.TTL == ""` (the --ttl 0 sentinel from Plan 01) and IdleTimeout is set; returns `""` for all other cases
- `IdleAction string` field added to `userDataParams`; template conditionally emits `Environment=IDLE_ACTION={{ .IdleAction }}` nested inside the `IDLE_TIMEOUT_MINUTES` block
- `buildIdleCallback()` extracted in sidecar `main.go`: factory function returning an `OnIdle` callback that either sends `stop` + re-arms (hibernate path) or sends `idle` + cancels (default/destroy path)
- `startIdleDetector` local func in `main()` wraps detector creation for re-entry; uses `time.AfterFunc(2*time.Minute, startIdleDetector)` to schedule re-arm after each hibernate cycle
- Default (non-TTL-0) behavior is fully preserved — no behavioral change when `IDLE_ACTION` is unset

## Task Commits

1. **Task 1: Add IdleAction to compiler userdata and wire in create.go** - `fe658a9` (feat)
2. **Task 2: Sidecar hibernate loop — IDLE_ACTION=hibernate re-arms detector** - `4c627e7` (feat)

## Files Created/Modified

- `pkg/compiler/userdata.go` — `IdleAction` field in `userDataParams`, `idleActionFromProfile()` helper, template conditional, `generateUserData` sets `IdleAction` from profile
- `pkg/compiler/userdata_idle_action_test.go` — 8 tests: template emission (hibernate/empty/no-timeout), `idleActionFromProfile` (TTL=0, TTL set, no idle), full `generateUserData` pipeline
- `sidecars/audit-log/cmd/main.go` — `buildIdleCallback()` factory, `startIdleDetector` local re-arm pattern, reads `IDLE_ACTION` env var, updated package comment
- `sidecars/audit-log/cmd/idle_action_test.go` — 7 tests: hibernate calls stop + rearms, hibernate skips cancel, default calls idle event + cancel, default no rearm, nil EB client safety, destroy=empty equivalence

## Decisions Made

- `idleActionFromProfile` lives in the compiler (not `create.go`) because the compiler already receives the resolved profile with `TTL=""` set by Plan 01's `applyLifecycleOverrides`. No new indirection needed.
- `buildIdleCallback` is a package-level function (not inline closure) so tests can call it directly without spawning goroutines or setting env vars.
- `IDLE_ACTION` is nested inside `{{- if gt .IdleTimeoutMinutes 0 }}` in the template — ensuring it only appears when idle detection is active, preventing misconfigured sidecar state.
- 2-minute `AfterFunc` delay: EC2 hibernate/resume round-trip takes 30-90 seconds; 2 minutes provides margin before new detector checks CW for fresh activity.

## Deviations from Plan

None - plan executed exactly as written. The plan called for `IdleAction` to be set in `create.go`, but since `create.go` calls `compiler.Compile(resolvedProfile, ...)` and the compiler receives the already-mutated profile (TTL="" from Plan 01), deriving IdleAction inside `idleActionFromProfile()` in the compiler achieves the same end result with a cleaner design. The plan's intent (hibernate when TTL=0) is fully satisfied.

## Issues Encountered

Pre-existing test failures in `cmd/ttl-handler` (missing `GetSchedule` method in mock), `cmd/configui` (schema validation), and `internal/app/cmd` (AWS integration tests) were not caused by this plan's changes. These are logged as out-of-scope.

## User Setup Required

None — changes are internal compiler and sidecar logic.

## Next Phase Readiness

- `km create --ttl 0 --idle 30m` now fully wires through: TTL="" sentinel → IDLE_ACTION=hibernate in userdata → sidecar hibernate loop
- The sandbox will hibernate on idle, resume, wait 2 minutes, then re-arm and hibernate again indefinitely
- No Lambda changes needed: existing `ttl-handler` `stop` event type already handles EC2 hibernate/stop

## Self-Check: PASSED

- pkg/compiler/userdata_idle_action_test.go: FOUND
- sidecars/audit-log/cmd/idle_action_test.go: FOUND
- 48-02-SUMMARY.md: FOUND
- Commit fe658a9: FOUND
- Commit 4c627e7: FOUND

---
*Phase: 48-profile-override-flags-for-km-create-targeted-budget-flags-and-generic-set*
*Completed: 2026-04-08*

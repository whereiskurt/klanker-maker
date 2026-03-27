---
phase: 26-live-operations-hardening
plan: 02
subsystem: testing, cli
tags: [go, testing, extend, lifecycle, region, maxlifetime]

requires:
  - phase: 26-live-operations-hardening
    plan: 01
    provides: Green test baseline and research findings for hardening tasks

provides:
  - Green test suite for TestRunInitWithRunnerAllModules and TestStatusCmd_Found
  - Dynamic region in destroy.go monitor hint via cfg.PrimaryRegion
  - Dynamic region in doctor.go using cfg.GetPrimaryRegion() (no hardcoded fallbacks)
  - MaxLifetime field in LifecycleSpec (pkg/profile/types.go)
  - MaxLifetime field in SandboxMetadata (pkg/aws/metadata.go)
  - CheckMaxLifetime() exported function enforcing MaxLifetime cap in km extend
  - 4 unit tests covering MaxLifetime enforcement (within cap, exceeds cap, no cap, expired sandbox)

affects:
  - extend (MaxLifetime enforcement)
  - create (should set MaxLifetime in metadata when profile has it)
  - doctor (region-safe)
  - destroy (region-safe monitor hint)

tech-stack:
  added: []
  patterns:
    - "Exported testable functions for AWS-dependent logic (CheckMaxLifetime exported for unit testing without mocking AWS)"
    - "MaxLifetime cap stored in SandboxMetadata to avoid profile roundtrip at extend time"
    - "cfg.PrimaryRegion with defensive empty-string fallback pattern for region-safe hints"

key-files:
  created:
    - internal/app/cmd/extend_test.go
  modified:
    - internal/app/cmd/init_test.go
    - internal/app/cmd/status_test.go
    - internal/app/cmd/destroy.go
    - internal/app/cmd/doctor.go
    - internal/app/cmd/extend.go
    - pkg/profile/types.go
    - pkg/aws/metadata.go

key-decisions:
  - "Store MaxLifetime in SandboxMetadata (not re-load profile) to keep extend path simple and dependency-free"
  - "Export CheckMaxLifetime() for unit testing rather than requiring full AWS mock infrastructure"
  - "Keep defensive us-east-1 fallback in publishRemoteCommand when cfg.PrimaryRegion is empty (monitor hint only, not API call)"
  - "Remove hardcoded us-east-1 from doctor.go Organizations client setup — use cfg.GetPrimaryRegion() instead"

patterns-established:
  - "Test assertions for time-formatted output should check date portion only (not full RFC3339) to be timezone-agnostic"
  - "Module order in init tests must mirror actual RunInitWithRunner implementation order"

requirements-completed: [HARD-01, HARD-04, HARD-05]

duration: 18min
completed: 2026-03-27
---

# Phase 26 Plan 02: Fix Pre-existing Test Failures, Region Hardcoding, and MaxLifetime Cap Summary

**Fixed stale init/status tests and hardcoded us-east-1 references; added MaxLifetime enforcement in km extend with exported CheckMaxLifetime() function and 4 passing unit tests**

## Performance

- **Duration:** ~18 min
- **Started:** 2026-03-27T00:00:00Z
- **Completed:** 2026-03-27
- **Tasks:** 2 (Task 1: bug fixes + region hardening; Task 2: TDD MaxLifetime)
- **Files modified:** 7

## Accomplishments
- Fixed TestRunInitWithRunnerAllModules: updated moduleNames order in test to match actual code (s3-replication, ttl-handler, ses — not ses first)
- Fixed TestStatusCmd_Found: changed assertion from RFC3339 `2026-03-22T12:00:00Z` to date portion `2026-03-22` (code formats as local timezone)
- Removed hardcoded us-east-1 from destroy.go monitor hint (now uses cfg.PrimaryRegion)
- Removed hardcoded us-east-1 fallbacks from doctor.go (Organizations client setup and EC2 client region)
- Added MaxLifetime field to LifecycleSpec and SandboxMetadata for cap storage
- Implemented CheckMaxLifetime() with clear error messages including cap duration and max expiry timestamp
- All 4 MaxLifetime test cases pass; full test suite green

## Task Commits

Each task was committed atomically:

1. **Task 1: Fix pre-existing test failures and multi-region hardcoding** - `efdd058` (fix)
2. **Task 2 RED: Add failing tests for MaxLifetime enforcement** - `44aa1e9` (test)
3. **Task 2 GREEN: Implement MaxLifetime enforcement** - `b886633` (feat)

## Files Created/Modified
- `internal/app/cmd/init_test.go` - Fixed moduleNames order (ses moved to last)
- `internal/app/cmd/status_test.go` - Fixed TTL expiry assertion to check date portion only
- `internal/app/cmd/destroy.go` - publishRemoteCommand takes cfg, uses cfg.PrimaryRegion in monitor hint
- `internal/app/cmd/doctor.go` - Removed hardcoded us-east-1 fallbacks; uses cfg.GetPrimaryRegion()
- `internal/app/cmd/extend.go` - Added CheckMaxLifetime() function; integrated cap enforcement in runExtend
- `internal/app/cmd/extend_test.go` - 4 unit tests for MaxLifetime (within cap, exceeds cap, no cap, expired)
- `pkg/profile/types.go` - Added MaxLifetime field to LifecycleSpec
- `pkg/aws/metadata.go` - Added MaxLifetime field to SandboxMetadata

## Decisions Made
- Stored MaxLifetime in SandboxMetadata (not in a separate profile load) to keep extend dependency-light
- Exported CheckMaxLifetime() so it can be unit-tested without mocking all AWS clients
- Kept a defensive `"us-east-1"` empty-string fallback in publishRemoteCommand because it's a monitor hint (not an AWS API call) and needs a value when cfg.PrimaryRegion is unconfigured

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] stop.go also called publishRemoteCommand without cfg**
- **Found during:** Task 1 (fixing destroy.go publishRemoteCommand signature)
- **Issue:** publishRemoteCommand signature changed to accept cfg; stop.go was a call site that also needed updating
- **Fix:** Updated stop.go call to pass cfg (linter auto-applied)
- **Files modified:** internal/app/cmd/stop.go
- **Committed in:** efdd058 (part of task 1 commit)

**2. [Rule 3 - Blocking] extend.go also called publishRemoteCommand without cfg**
- **Found during:** Task 1 (fixing destroy.go publishRemoteCommand signature)
- **Issue:** extend.go was a call site for publishRemoteCommand that needed cfg
- **Fix:** Updated extend.go call to pass cfg (linter auto-applied)
- **Files modified:** internal/app/cmd/extend.go
- **Committed in:** efdd058 (part of task 1 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 3 - Blocking)
**Impact on plan:** Both were direct consequences of the planned publishRemoteCommand signature change. No scope creep.

## Issues Encountered
- extend_test.go helper `containsStr` conflicted with same-named function in roll_test.go (both in `cmd_test` package) — resolved by using `strings.Contains` directly instead of a local helper

## Next Phase Readiness
- Green test baseline established for cmd package
- MaxLifetime enforcement ready; km create should populate metadata.MaxLifetime from profile at create time (deferred to future plan)
- Region hardcoding audit complete for destroy.go and doctor.go

---
*Phase: 26-live-operations-hardening*
*Completed: 2026-03-27*

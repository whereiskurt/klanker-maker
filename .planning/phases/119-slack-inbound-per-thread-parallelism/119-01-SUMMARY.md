---
phase: 119-slack-inbound-per-thread-parallelism
plan: 01
subsystem: testing
tags: [slack, tdd, sqs, profile-validation, userdata-compiler]

# Dependency graph
requires:
  - phase: 118-slack-inbound-per-thread-parallelism
    provides: Allow/Private fields on NotificationSlackInboundSpec, events_handler_test.go fakeSQS infrastructure
provides:
  - MaxConcurrentThreads *int field on NotificationSlackInboundSpec
  - maxConcurrentThreads schema property (integer, minimum:1) in JSON schema
  - RED bridge tests asserting MessageGroupId == threadTS (P119-A coverage)
  - RED validate WARN test for cap>1 without perSandbox+inbound.enabled (P119-G)
  - RED userdata env var emission test for KM_SLACK_MAX_CONCURRENCY (P119-C)
  - RED SQS VisibilityTimeout=1800 test in pkg/aws/sqs_test.go (P119-E)
affects: [119-02, 119-03, 119-04, 119-05]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Wave 0 RED stub first: struct field + schema property added solely for compile floor; behavior in Waves 1-2"
    - "containsInboundWarn helper mirrors containsInboundError for IsWarning=true assertions"
    - "intPtrInbound local helper avoids redeclaring intPtr from validate_test.go in same test package"

key-files:
  created:
    - pkg/aws/sqs_test.go
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/slack/bridge/events_handler_test.go
    - pkg/profile/validate_slack_inbound_test.go
    - pkg/compiler/userdata_slack_inbound_test.go

key-decisions:
  - "maxConcurrentThreads schema minimum:1 already rejects cap=0 via Task 1 — schema-reject test passes immediately (by design)"
  - "dormancy tests for cap=1/nil pass immediately — compiler currently emits nothing, which IS the dormancy behavior; only the cap>1 emission test is RED"
  - "intPtr redeclared: renamed to intPtrInbound in validate_slack_inbound_test.go to avoid collision with validate_test.go in package profile_test"

patterns-established:
  - "Phase 119 RED scaffold follows Phase 118 Wave-0 pattern: compile floor (struct+schema) first, then behavior tests"

requirements-completed: [P119-A, P119-C, P119-E, P119-G]

# Metrics
duration: 4min
completed: 2026-06-25
---

# Phase 119 Plan 01: Wave 0 RED Test Scaffold Summary

**MaxConcurrentThreads *int field + schema property added as compile floor; 4 RED test groups cover all Phase 119 validation gaps (bridge group ID, validate WARN, userdata env, queue timeout)**

## Performance

- **Duration:** 4 min
- **Started:** 2026-06-25T12:49:14Z
- **Completed:** 2026-06-25T12:53:24Z
- **Tasks:** 3
- **Files modified:** 5 (+ 1 created)

## Accomplishments

- Added `MaxConcurrentThreads *int` to `NotificationSlackInboundSpec` and `maxConcurrentThreads` (integer, minimum:1) to the JSON schema — tree compiles with no behavior change
- RED bridge tests: `TestEventsHandler_GroupID_IsThreadTS_NoFiles`, `_IsMsgTS_TopLevel`, `_IsThreadTS_Files` — all fail with `group="sb-abc123"` (want threadTS); Wave 1 fixes the `Send()` calls
- RED validate WARN test: `TestValidate_SlackInbound_MaxConcurrency_WarnsWithoutPerSandbox` — fails because WARN rule not yet added to `ValidateSemantic`; positive and schema-reject cases pass immediately
- RED userdata emission test: `TestUserdata_SlackInbound_MaxConcurrency_EmittedWhenCap3` — fails because compiler doesn't yet emit `KM_SLACK_MAX_CONCURRENCY`; dormancy (cap=1/nil) tests pass immediately
- RED queue attr test: `TestInboundQueueAttrs_VisibilityTimeout` — fails with `"30"` vs `"1800"`; Wave 1 bumps the constant

## Task Commits

1. **Task 1: MaxConcurrentThreads struct field + schema property** - `bb908a60` (feat)
2. **Task 2: RED bridge tests — group==threadTS** - `8cc5bb49` (test)
3. **Task 3: RED validate + schema-reject + userdata-env + queue-attr tests** - `17c7d592` (test)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/profile/types.go` - Added MaxConcurrentThreads *int field to NotificationSlackInboundSpec with Phase 119 doc comment
- `/Users/khundeck/working/klankrmkr/pkg/profile/schemas/sandbox_profile.schema.json` - Added maxConcurrentThreads integer property (minimum:1) to inbound block
- `/Users/khundeck/working/klankrmkr/pkg/slack/bridge/events_handler_test.go` - 3 RED group-ID tests covering no-files in-thread, top-level, and files paths
- `/Users/khundeck/working/klankrmkr/pkg/profile/validate_slack_inbound_test.go` - MaxConcurrency WARN test + positive case + schema-reject for cap=0
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_slack_inbound_test.go` - KM_SLACK_MAX_CONCURRENCY emission + dormancy tests
- `/Users/khundeck/working/klankrmkr/pkg/aws/sqs_test.go` (new) - inboundQueueAttrs VisibilityTimeout assertion

## Decisions Made

- Schema minimum:1 makes the `RejectsZero` test pass immediately after Task 1 — this is expected behavior, not a problem; the test stays as a regression guard
- Dormancy tests (cap=1 and nil both absent) pass immediately because the compiler doesn't emit `KM_SLACK_MAX_CONCURRENCY` at all yet — the dormancy behavior is already correct by default
- `intPtr` collision in `package profile_test`: renamed to `intPtrInbound` (matches existing `boolPtrInbound` naming convention in the same file)

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

- `intPtr` collision: `validate_test.go` and `validate_slack_inbound_test.go` share `package profile_test`, so `intPtr` was already declared. Renamed to `intPtrInbound` following the existing `boolPtrInbound` convention in the same file.

## Next Phase Readiness

- Wave 0 RED scaffold complete; all 4 P119-* gaps covered
- Wave 1 (Plan 02) implements: bridge Send group=threadTS, ValidateSemantic WARN rule, compiler KM_SLACK_MAX_CONCURRENCY emission, inboundQueueAttrs VisibilityTimeout="1800"
- `go build ./...` is clean; pre-existing tests are all green; only Phase 119 RED tests fail

---
*Phase: 119-slack-inbound-per-thread-parallelism*
*Completed: 2026-06-25*

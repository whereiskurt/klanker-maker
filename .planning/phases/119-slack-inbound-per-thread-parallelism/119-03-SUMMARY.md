---
phase: 119-slack-inbound-per-thread-parallelism
plan: 03
subsystem: slack-inbound
tags: [slack, validate, userdata, notify-env, max-concurrency]

# Dependency graph
requires:
  - phase: 119-01
    provides: MaxConcurrentThreads field in NotificationSlackInboundSpec + Wave-0 RED tests
  - phase: 118-slack-trigger-allowlist-private-channels
    provides: S-allow WARN rule pattern in validate.go; notifySlackInbound accessor in userdata.go

provides:
  - Rule S-maxconcurrency WARN in ValidateSemantic (cap>1 without perSandbox+inbound.enabled)
  - KM_SLACK_MAX_CONCURRENCY=N env emission in notify.env/profile.d (emit-only-when>1 dormancy)

affects: [119-04, 119-05]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Emit-only-when>1 dormancy: scalar env var emitted only for non-default values to preserve byte-identity with prior phase"
    - "S-warn rule mirrors Phase-118 S-allow shape: IsWarning=true, same perSandbox scope local"

key-files:
  created: []
  modified:
    - pkg/profile/validate.go
    - pkg/compiler/userdata.go

key-decisions:
  - "KM_SLACK_MAX_CONCURRENCY emitted only when cap>1 (not cap=1 and not nil) to preserve byte-identical userdata for every pre-Phase-119 profile"
  - "No DDB attr, no SandboxMetadata round-trip — cap is poller-only (Research Pitfall 5)"
  - "S-maxconcurrency placed immediately after S-allow in ValidateSemantic; same perSandbox local bool used"

patterns-established:
  - "emit-only-when-non-default: add env var to notifyEnv map inside the relevant enabled block only when the value deviates from the poller default"

requirements-completed: [P119-C, P119-G]

# Metrics
duration: 1min
completed: 2026-06-25
---

# Phase 119 Plan 03: Slack Inbound Per-Thread Parallelism — Env Plumbing + Validate WARN Summary

**km validate WARN when cap>1 without perSandbox/inbound.enabled (S-maxconcurrency); KM_SLACK_MAX_CONCURRENCY=N emitted only when cap>1 so every pre-Phase-119 profile is byte-identical**

## Performance

- **Duration:** 1 min
- **Started:** 2026-06-25T12:56:27Z
- **Completed:** 2026-06-25T12:57:29Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Added Rule S-maxconcurrency to ValidateSemantic immediately after the Phase-118 S-allow rule — same `perSandbox` local bool, same `IsWarning: true` shape
- Added `KM_SLACK_MAX_CONCURRENCY=N` emission inside `slackInboundEnabled` block in userdata.go — guarded by `sl.MaxConcurrentThreads != nil && *sl.MaxConcurrentThreads > 1`; cap=1 or nil emits nothing
- Turned all three Wave-0 RED compiler tests GREEN: EmittedWhenCap3, AbsentWhenCap1, AbsentWhenNil

## Task Commits

1. **Task 1: validate WARN — cap>1 without perSandbox/inbound.enabled** - `90c3b3c0` (feat)
2. **Task 2: Emit KM_SLACK_MAX_CONCURRENCY into notify.env when cap>1** - `4558f925` (feat)

## Files Created/Modified

- `pkg/profile/validate.go` - Added Rule S-maxconcurrency WARN block after S-allow (~line 362)
- `pkg/compiler/userdata.go` - Added KM_SLACK_MAX_CONCURRENCY=N emission inside slackInboundEnabled block (~line 5509)

## Decisions Made

- Emit-only-when>1 is the critical dormancy invariant: the poller defaults `${KM_SLACK_MAX_CONCURRENCY:-1}` so absent env line is equivalent to cap=1; no env line for default-1 profiles keeps the frozen golden byte-identical
- No DDB attr, no SandboxMetadata field — the cap is entirely poller-side (the bridge never reads it); contrast with Phase 91.5/118 which needed per-sandbox DDB round-trips

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- Plan 04 (poller script patch) can now consume `KM_SLACK_MAX_CONCURRENCY` — the env var is guaranteed present when cap>1 and absent otherwise
- Plan 05 (golden + integration tests) will verify byte-identity of pre-Phase-119 profiles against frozen goldens
- Phase-117 deepMerge carries the `*int` scalar with zero new merger code (confirmed by existing inherit tests staying green in full suite)

---
*Phase: 119-slack-inbound-per-thread-parallelism*
*Completed: 2026-06-25*

---
phase: 116-km-check-serverless-check-runner
plan: 02
subsystem: dispatch
tags: [dispatch, alias-resolution, eventbridge, dynamodb, tdd, go]

# Dependency graph
requires:
  - phase: pkg/github/bridge
    provides: "DynamoAliasResolver.ResolveByAliasWithStatus, EC2Resumer, DynamoGitHubNonceStore patterns (library, not import)"
provides:
  - "pkg/dispatch.ResumeOrCreate: alias-resolution + resume/cold-create decision function with cooldown"
  - "pkg/dispatch interfaces: AliasResolver, AgentRunSink, ColdCreateSink, NonceStore"
  - "5 unit tests covering all 3-way decision branches + cooldown suppression"
affects:
  - "116-06 (ttl-handler wiring): wires pkg/dispatch.ResumeOrCreate into handleCheckDispatch case"
  - "pkg/github/bridge, pkg/h1/bridge: NOT modified (parity preserved, bridges stay independent)"

# Tech tracking
tech-stack:
  added: ["pkg/dispatch package (net-new standalone library)"]
  patterns:
    - "Interface-injected decision core: all AWS SDK boundaries delegated to sink interfaces"
    - "Fail-open cooldown: nonce store errors proceed rather than suppressing real fires"
    - "Warm path = AgentRunSink (SSM dispatch), NOT SQS enqueue — no per-sandbox queue required"

key-files:
  created:
    - pkg/dispatch/interfaces.go
    - pkg/dispatch/dispatch.go
    - pkg/dispatch/dispatch_test.go
  modified: []

key-decisions:
  - "Warm path terminal action is SSM agent-run dispatch sink (AgentRunSink), NOT SQS FIFO enqueue — existing sandboxes receive check dispatches without recreate"
  - "pkg/dispatch imports NO bridge packages; bridges import pkg/dispatch when wired in 116-06"
  - "Cooldown fail-open: nonce store errors proceed to dispatch (never strand a real fire on transient read error)"
  - "status='' (DDB attribute absent) treated as absent/cold-create rather than running — prevents blindly dispatching to uninitialized rows"
  - "onAbsent='cold-create' is the default (empty string also maps to cold-create) — mirrors bridge Phase 109 pattern"

patterns-established:
  - "ResumeOrCreate signature: (ctx, check, alias, prompt, profile, onAbsent, cooldownSeconds, resolver, agentRun, cold, nonces, log)"
  - "Cooldown key format: check-trigger:{checkName}"
  - "TDD: interfaces + tests first (RED commit), implementation second (GREEN commit)"

requirements-completed: []

# Metrics
duration: 2min
completed: 2026-06-17
---

# Phase 116 Plan 02: pkg/dispatch — ResumeOrCreate Decision Core Summary

**Standalone `pkg/dispatch` package with `ResumeOrCreate` decision function: alias-resolution + SSM agent-run (warm) / cold-create / skip (absent) dispatch, cooldown via nonce table, 5 unit tests, bridges unmodified**

## Performance

- **Duration:** 2 min
- **Started:** 2026-06-17T04:48:21Z
- **Completed:** 2026-06-17T04:50:33Z
- **Tasks:** 2 (TDD: RED then GREEN)
- **Files modified:** 3

## Accomplishments
- Created `pkg/dispatch/interfaces.go` with 4 clean interfaces: `AliasResolver`, `AgentRunSink`, `ColdCreateSink`, `NonceStore`
- Implemented `pkg/dispatch.ResumeOrCreate` with cooldown-first + 3-way dispatch decision (warm agent-run, cold-create, skip)
- All 5 unit tests pass with mock sinks: RunningAgentRun, StoppedAgentRun, AbsentColdCreate, AbsentSkip, Cooldown
- `go build ./...` clean; `pkg/github/bridge` and `pkg/h1/bridge` untouched

## Task Commits

1. **Task 1: interfaces + failing tests (RED)** - `082f898c` (test)
2. **Task 2: implement ResumeOrCreate (GREEN)** - `35abe332` (feat)

## Files Created/Modified
- `pkg/dispatch/interfaces.go` — 4 interfaces: AliasResolver, AgentRunSink, ColdCreateSink, NonceStore
- `pkg/dispatch/dispatch.go` — ResumeOrCreate decision function (129 lines)
- `pkg/dispatch/dispatch_test.go` — 5 unit tests with mock sinks (311 lines)

## Decisions Made
- Warm path is `AgentRunSink.DispatchAgentRun` (SSM), NOT SQS enqueue — CONTEXT.md Stage B locked decision; consequences: no per-sandbox queue needed, bridges unmodified
- Cooldown fail-open: nonce store errors proceed to dispatch rather than suppressing (mirror bridge semantics)
- `status=""` treated as absent rather than running — prevents dispatching to uninitialized DDB rows
- `onAbsent` default (empty string) maps to `cold-create` for safe behavior

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None. RED phase failed correctly (`dispatch.ResumeOrCreate undefined`); GREEN phase passes all 5 tests.

## User Setup Required

None — this is a standalone Go library package with no external service configuration.

## Next Phase Readiness

- `pkg/dispatch.ResumeOrCreate` is ready for mechanical wiring in Plan 116-06 (ttl-handler `handleCheckDispatch`)
- Concrete adapters from `pkg/github/bridge/aws_adapters.go` (`DynamoAliasResolver`, `DynamoGitHubNonceStore`) satisfy the interfaces; ttl-handler can wire them directly
- The `AgentRunSink` interface is the one net-new concrete type 116-06 must implement (wrapping `handleAgentRun` AutoStart=true mechanics)

## Self-Check

- [x] `pkg/dispatch/interfaces.go` — FOUND
- [x] `pkg/dispatch/dispatch.go` — FOUND
- [x] `pkg/dispatch/dispatch_test.go` — FOUND
- [x] Commit `082f898c` (RED) — FOUND
- [x] Commit `35abe332` (GREEN) — FOUND
- [x] `go test ./pkg/dispatch/ -count=1` — EXIT 0, 5 tests PASS
- [x] `go build ./...` — EXIT 0, clean
- [x] No pkg/github/bridge or pkg/h1/bridge files modified

## Self-Check: PASSED

---
*Phase: 116-km-check-serverless-check-runner*
*Completed: 2026-06-17*

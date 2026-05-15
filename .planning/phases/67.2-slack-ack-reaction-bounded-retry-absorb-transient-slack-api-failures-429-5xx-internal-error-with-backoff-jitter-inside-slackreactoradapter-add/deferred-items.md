# Phase 67.2 — Deferred Items

Items discovered during plan 67.2-02 execution that are out-of-scope
for this phase.

## Pre-existing pkg/compiler test failures

**Discovered during:** Plan 67.2-02 execution (Task 3 verification — final
project-wide `go test ./...`)

**Status:** Pre-existing — verified by `git stash`-ing all 67.2-02
changes and re-running `go test ./pkg/compiler/`. Same six failures
reproduce on a clean tree.

**Failing tests:**
- `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`
- `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`
- `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`
- `TestUserDataKMTracingServicectlStart`
- `TestAuditHookNonBlocking`
- `TestGitHubUserDataGITASKPASS`

**Reason for deferral:** Plan 67.2-02 only modified files in
`pkg/slack/bridge/` (aws_adapters.go, aws_adapters_test.go,
events_handler.go). The `pkg/compiler` failures are entirely unrelated
to ACK reaction retry, and per executor scope-boundary policy
("Only auto-fix issues DIRECTLY caused by the current task's
changes"), they are out-of-scope for this plan.

**Fix path:** A separate phase or hot-fix plan should investigate the
six `pkg/compiler` test failures. They appear to be userdata-template
assertion drift, possibly from recent Phase 79 / 80 / 81 changes.
Recommended starting point: `git log --oneline pkg/compiler/userdata*`
to find the most recent userdata-template edits and bisect from there.

**Impact on Phase 67.2:** None. The
`pkg/slack/bridge/` test suite — which is the only surface plan 67.2
touches — is fully green:
- 10 new `TestReactor_*` tests (added in plan 02 task 2)
- 4 existing `TestSlackReactorAdapter_*` tests (1 updated for new retry
  loop)
- 4 existing `TestEventsHandler_Reactor_*` tests (unchanged, verified
  pass after handler-timeout bump)
- 1 `TestClassifyReactionError` table-driven test (added in plan 01)

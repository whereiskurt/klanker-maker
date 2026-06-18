---
phase: 116-km-check-serverless-check-runner
plan: "06"
subsystem: infra
tags: [eventbridge, ttl-handler, dispatch, lambda, dynamodb, check-runner, iam]

requires:
  - phase: 116-km-check-serverless-check-runner
    plan: "02"
    provides: "pkg/dispatch.ResumeOrCreate + 4 interfaces (AliasResolver, AgentRunSink, ColdCreateSink, NonceStore)"

provides:
  - "ttl-handler check-dispatch case: alias-targeted resume-or-cold-create via pkg/dispatch (Stage B)"
  - "ttl-handler check-run case: synchronous lambda:Invoke of {prefix}-check-{name}"
  - "CheckDispatch EventBridge rule + target + lambda permission (km.sandbox source, CheckDispatch detail-type)"
  - "ttl-handler IAM widened: events:PutEvents + lambda:InvokeFunction on check-* + dynamodb:PutItem on nonces"
  - "km at '...' check run <name> schedulable command"
  - "4 dispatch adapters: ttlAliasResolver, ttlAgentRunSink, ttlColdCreateSink, ttlNonceStore"

affects:
  - "116-km-check-serverless-check-runner"
  - "ttl-handler deployment (make build-lambdas + km init --dry-run=false)"

tech-stack:
  added: []
  patterns:
    - "CheckDispatch EventBridge rule with input_path=$.detail (full detail passthrough to TTLEvent)"
    - "Test-seam interface injection: handleCheckDispatchWithAdapters / handleCheckRunWithInvoker"
    - "Two-word command merge pattern for 'check run' → 'check-run' in at.go"
    - "zerologSlogHandler: bridges zerolog to slog.Handler for pkg/dispatch structured logging"

key-files:
  created:
    - cmd/ttl-handler/check_dispatch.go
  modified:
    - cmd/ttl-handler/main.go
    - cmd/ttl-handler/main_test.go
    - infra/modules/ttl-handler/v1.0.0/main.tf
    - internal/app/cmd/at.go
    - cmd/budget-enforcer/main_test.go

key-decisions:
  - "CheckDispatch + CheckRun cases routed BEFORE sandbox_id guard in HandleTTLEvent (these events carry alias, not sandbox_id)"
  - "AgentRunSink delegates to handleAgentRun (CANONICAL path) with AutoStart=true — do NOT fork buildAgentRunScript (project_ttl_agent_run_stale_fork)"
  - "ColdCreateSink uses compilerpkg.GenerateSandboxID('chk') prefix + 'check-profiles/{slug}' artifact_prefix convention"
  - "ttlAliasResolver defined locally (not importing bridge package) to avoid coupling cmd/ttl-handler to pkg/github/bridge"
  - "NonceStore uses existing {prefix}-slack-bridge-nonces table (KM_NONCE_TABLE env, no new table)"
  - "CheckDispatch TTLEvent detail contract: check_name, alias, prompt, profile_name, on_absent, cooldown_seconds, reason (snake_case, matches 116-04 bootstrap output)"
  - "check-run sandbox ID resolution skipped in at.go (extraArgs[0] is a check name, not a sandbox reference)"

requirements-completed: []

duration: 20min
completed: "2026-06-18"
---

# Phase 116 Plan 06: Stage B — ttl-handler CheckDispatch consumer + CheckDispatch EventBridge rule Summary

**Stage B wired: ttl-handler handles CheckDispatch via pkg/dispatch.ResumeOrCreate (alias-targeted resume-or-cold-create using CANONICAL handleAgentRun SSM path) + new CheckDispatch EventBridge rule, widened IAM, and km at check run schedulable command**

## Performance

- **Duration:** 20 min
- **Started:** 2026-06-18T00:38:22Z
- **Completed:** 2026-06-18T00:58:39Z
- **Tasks:** 3 (+ 1 auto-fix deviation)
- **Files modified:** 6

## Accomplishments

- Stage B fully wired: `check-dispatch` + `check-run` cases in `HandleTTLEvent` delegate to `handleCheckDispatch` / `handleCheckRun` in the new `check_dispatch.go`
- `handleCheckDispatch` builds 4 adapters and calls `pkg/dispatch.ResumeOrCreate` — the warm path calls `h.handleAgentRun(AutoStart=true)` (CANONICAL builder, no stale fork) and the cold path calls `PutSandboxCreateEvent` with `created_by="check"`
- `CheckDispatch` EventBridge rule added to `infra/modules/ttl-handler/v1.0.0/main.tf` with `input_path="$.detail"` passthrough; IAM widened with `events:PutEvents`, `lambda:InvokeFunction` on `{prefix}-check-*`, `dynamodb:PutItem` on nonces
- `km at '...' check run <name>` now schedulable (two-word merge, no sandbox ID resolution, `check_name` field in TTLEvent detail)
- 3 new tests green covering: running alias→agent-run, absent→cold-create, check-run→invoke

## Task Commits

1. **Task 1: handleCheckDispatch + handleCheckRun + dispatch adapters** - `1eadbf72` (feat)
2. **Task 2: CheckDispatch EventBridge rule + widened IAM** - `c26a0a9e` (feat)
3. **Task 3: km at check run schedulable command** - `4db8ce43` (feat)

**Auto-fix:** `4c00d41f` (fix: budget-enforcer mock CreateScheduleGroup)

## Files Created/Modified

- `cmd/ttl-handler/check_dispatch.go` — New file: handleCheckDispatch, handleCheckRun, all adapters (ttlAliasResolver, ttlAgentRunSink, ttlColdCreateSink, ttlNonceStore, ttlLambdaInvoker), zerologSlogHandler, dispatch bridge adapters (508 lines)
- `cmd/ttl-handler/main.go` — Added CheckName/OnAbsent/Reason/CooldownSeconds to TTLEvent; routed check-dispatch/check-run before sandbox_id guard
- `cmd/ttl-handler/main_test.go` — 3 new check-dispatch/check-run tests; fixed pre-existing CreateScheduleGroup missing from mockSchedulerAPI
- `infra/modules/ttl-handler/v1.0.0/main.tf` — CheckDispatch rule + target + permission; new check_dispatch IAM policy
- `internal/app/cmd/at.go` — check-run entry in schedulableCommands; "check run" two-word merge; check_name injection in buildTargetInput; sandbox ID resolution skip for check-run
- `cmd/budget-enforcer/main_test.go` — Auto-fixed same CreateScheduleGroup gap

## Decisions Made

### Agent-run path: CANONICAL builder (no fork)

The warm dispatch delegates to `h.handleAgentRun` (the existing handler) via `ttlAgentRunSink`. This routes through `buildAgentRunScript` which already has the 3 bug fixes documented in `project_ttl_agent_run_stale_fork`:
1. Sources `/etc/profile.d/*.sh` (not a subset) for `KM_SLACK_*` env
2. Omits `--bare` (preserves plugin/hook loading)
3. Uses `CLAUDE_CODE_OAUTH_TOKEN` for no-bedrock OAuth (not `ANTHROPIC_API_KEY`)

A new `AutoStart=true` TTLEvent is constructed with `SandboxID` from the alias resolution result, delegating all EC2 resume + SSM dispatch to the existing handler.

### CheckDispatch detail contract consumed as-is

The `check-dispatch` case reads `event.CheckName`, `event.Alias`, `event.Prompt`, `event.ProfileName`, `event.OnAbsent`, `event.CooldownSeconds`, `event.Reason` — exactly matching the 116-04 bootstrap's `CheckDispatch` emit format (snake_case JSON, `check_name` field). No transform needed.

### Local ttlAliasResolver (not importing bridge)

Rather than importing `pkg/github/bridge.DynamoAliasResolver`, a local `ttlAliasResolver` struct was implemented in `check_dispatch.go`. This avoids coupling `cmd/ttl-handler` to the bridge package and keeps the bridge unmodified. The implementation is identical (alias-index GSI, `status` attribute, backward-compat `status=""` = running).

### Nonces table reuse (no new table)

The cooldown nonce store reuses `{prefix}-slack-bridge-nonces` (read from `KM_NONCE_TABLE` env var) with key prefix `check-trigger:{name}`. No new DynamoDB table is needed; the existing nonces table already has TTL-aware PutItem + ConditionExpression idempotency.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Pre-existing mockSchedulerAPI missing CreateScheduleGroup**
- **Found during:** Task 1 verification (running full ttl-handler test suite)
- **Issue:** `SchedulerAPI` interface added `CreateScheduleGroup` method; both `cmd/ttl-handler/main_test.go` and `cmd/budget-enforcer/main_test.go` had `mockSchedulerAPI` structs missing this method, causing build failures in both packages
- **Fix:** Added `CreateScheduleGroup` no-op method to both mocks
- **Files modified:** `cmd/ttl-handler/main_test.go`, `cmd/budget-enforcer/main_test.go`
- **Verification:** Both packages build and test suites green
- **Committed in:** Part of Task 1 commit (`1eadbf72`) for ttl-handler; separate fix commit (`4c00d41f`) for budget-enforcer

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug)
**Impact on plan:** Essential for test compilation. Both mocks were missing the same method; fix is mechanical and zero-risk.

## Issues Encountered

- The `check-dispatch` and `check-run` events do not carry `sandbox_id` (they operate on alias / check name respectively). The existing `HandleTTLEvent` gate checked `sandbox_id != ""` before routing. Fixed by routing the two new cases BEFORE the guard, in a separate pre-switch block.
- `internal/app/cmd` tests (`TestConfigureMaxSandboxesFlag`, `TestUninitDestroyOrder`, etc.) fail due to a pre-existing missing `ecr` module in `pkg/check/ecr.go` (from an earlier plan on this branch, not introduced by this plan). These are out-of-scope pre-existing failures.

## Next Phase Readiness

- Stage B is complete: ttl-handler will consume `CheckDispatch` events and perform alias-targeted resume-or-cold-create
- Bridges (`pkg/github/bridge`, `pkg/h1/bridge`, `pkg/slack/bridge`) are UNMODIFIED
- No new per-sandbox SQS queue introduced; existing sandboxes need no recreate
- Deploy requires: `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars` — IAM + env block change needs full apply)
- The `KM_NONCE_TABLE` env var must be set in the ttl-handler Lambda environment (same value as the slack/github bridges use: `{prefix}-slack-bridge-nonces`) — this is wired in `infra/live/use1/ttl-handler/terragrunt.hcl` update (deferred to 116-08 deploy plan)

---
*Phase: 116-km-check-serverless-check-runner*
*Completed: 2026-06-18*

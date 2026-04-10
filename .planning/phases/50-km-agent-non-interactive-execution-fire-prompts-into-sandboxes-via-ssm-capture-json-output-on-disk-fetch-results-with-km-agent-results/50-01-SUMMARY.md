---
phase: 50-km-agent-non-interactive-execution
plan: 01
subsystem: cli
tags: [ssm, sendcommand, base64, eventbridge, agent, claude]

requires:
  - phase: shell
    provides: SandboxFetcher, ShellExecFunc, extractResourceID
provides:
  - NewAgentCmd parent command with run subcommand
  - SSMSendAPI interface for non-interactive SSM dispatch
  - BuildAgentShellCommand for testable command construction
  - AgentIdleResetInterval/AgentPollInterval exported for test override
affects: [50-02-agent-results, agent-list]

tech-stack:
  added: []
  patterns: [base64 prompt encoding for shell injection prevention, configurable poll/heartbeat intervals for testing]

key-files:
  created:
    - internal/app/cmd/agent.go
    - internal/app/cmd/agent_test.go
  modified:
    - internal/app/cmd/shell.go

key-decisions:
  - "Moved NewAgentCmd and runAgent from shell.go to agent.go for clear module separation"
  - "Used base64 encoding for prompt injection prevention rather than shell escaping"
  - "Exported AgentPollInterval and AgentIdleResetInterval so tests run in <5s instead of 30s+"

patterns-established:
  - "SSMSendAPI: narrow interface for SSM SendCommand + GetCommandInvocation dependency injection"
  - "Base64 prompt encoding: all user-supplied text base64-encoded before embedding in shell commands"

requirements-completed: [AGENT-01, AGENT-02, AGENT-03, AGENT-06, AGENT-07, AGENT-08]

duration: 5min
completed: 2026-04-10
---

# Phase 50 Plan 01: Agent Non-Interactive Execution Summary

**Fire-and-forget Claude prompts into sandboxes via SSM SendCommand with base64 injection prevention and idle-reset heartbeat**

## Performance

- **Duration:** 5 min
- **Started:** 2026-04-10T17:29:21Z
- **Completed:** 2026-04-10T17:34:38Z
- **Tasks:** 1
- **Files modified:** 3

## Accomplishments
- Restructured km agent as parent command with backward-compatible interactive mode and new run subcommand
- Non-interactive execution fires Claude prompts via SSM SendCommand with base64 encoding to prevent shell injection
- Idle-reset heartbeat publishes EventBridge extend events every 5 minutes during --wait polling
- All 6 unit tests pass covering SendCommand dispatch, command construction, prompt escaping, idle reset, stopped sandbox, and backward compat

## Task Commits

Each task was committed atomically:

1. **Task 1: Create agent.go with parent command, run subcommand, and non-interactive execution** - `c8c1abe` (feat)

**Plan metadata:** (pending)

## Files Created/Modified
- `internal/app/cmd/agent.go` - Parent agent command with run subcommand, SSMSendAPI interface, non-interactive execution, idle-reset heartbeat, BuildAgentShellCommand helper
- `internal/app/cmd/agent_test.go` - 6 unit tests covering all agent requirements
- `internal/app/cmd/shell.go` - Removed NewAgentCmd and runAgent (moved to agent.go)

## Decisions Made
- Moved NewAgentCmd and runAgent from shell.go to agent.go for clearer module separation; shell.go retains shell/learn/port-forward logic
- Used base64 encoding for prompt text rather than shell escaping -- simpler, more robust against all special characters
- Made poll and heartbeat intervals exported package variables so tests can override them (reduces test time from 31s to 4.5s)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed SSM TimeoutSeconds type**
- **Found during:** Task 1 (compilation)
- **Issue:** TimeoutSeconds field is *int32, not int -- compilation error
- **Fix:** Used awssdk.Int32(28800) wrapper
- **Files modified:** internal/app/cmd/agent.go
- **Verification:** make build succeeds
- **Committed in:** c8c1abe

**2. [Rule 1 - Bug] Removed duplicate min() function**
- **Found during:** Task 1 (test compilation)
- **Issue:** min() already declared in email_test.go in same package
- **Fix:** Removed duplicate declaration from agent_test.go
- **Files modified:** internal/app/cmd/agent_test.go
- **Verification:** Tests compile and pass
- **Committed in:** c8c1abe

---

**Total deviations:** 2 auto-fixed (2 bugs)
**Impact on plan:** Both minor compilation fixes. No scope creep.

## Issues Encountered
- Initial test run for IdleReset took 31s due to hardcoded 10s poll interval; resolved by exporting AgentPollInterval for test override

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- agent.go parent command structure ready for results and list subcommands (Plan 02)
- SSMSendAPI interface established for consistent SSM testing

---
*Phase: 50-km-agent-non-interactive-execution*
*Completed: 2026-04-10*

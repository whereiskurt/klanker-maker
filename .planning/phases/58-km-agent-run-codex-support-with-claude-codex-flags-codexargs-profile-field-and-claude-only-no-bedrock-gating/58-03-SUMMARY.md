---
phase: 58-km-agent-run-codex-support
plan: 03
subsystem: agent
tags: [go, agent, codex, claude, cli, flags, tdd]

requires:
  - phase: 58-02
    provides: AgentRunOptions struct, BuildAgentShellCommands branching on AgentType, runAgentNonInteractive with agentType string parameter
  - phase: 58-01
    provides: CLISpec.CodexArgs []string field in profile schema

provides:
  - --claude and --codex bool flags on km agent run subcommand
  - Mutex early-exit before ResolveSandboxID (--claude && --codex errors)
  - no-bedrock gate before ResolveSandboxID (--codex && --no-bedrock errors)
  - loadProfileCLICodexArgs helper (mirrors loadProfileCLIClaudeArgs, fail-open)
  - runAgentNonInteractive conditionally loads profile args by agentType
  - NewAgentRunCmd exported test seam
  - Five new tests proving flag wiring, mutex, no-bedrock gate, backward compat, helper

affects:
  - Manual smoke test step (Task 3 checkpoint:human-verify awaiting operator)

tech-stack:
  added: []
  patterns:
    - "Early-exit validation pattern: mutex + no-bedrock checks fire in RunE BEFORE ctx/ResolveSandboxID (no AWS calls)"
    - "Exported test seam: NewAgentRunCmd exported wrapper around newAgentRunCmd for isolated run-subcommand testing"
    - "Conditional profile args loading: switch on agentType selects loadProfileCLICodexArgs or loadProfileCLIClaudeArgs (no cross-contamination)"

key-files:
  created: []
  modified:
    - internal/app/cmd/agent.go
    - internal/app/cmd/agent_test.go

key-decisions:
  - "Default agent type is claude (empty or no flag) — backward compat preserved"
  - "Mutex and no-bedrock checks use early return in RunE before ctx or ResolveSandboxID — guarantees zero AWS calls on validation failure"
  - "loadProfileCLICodexArgs is a separate helper (not unified with ClaudeArgs) — parallel field pattern per CONTEXT.md design lock-in"
  - "km init --sidecars NOT required after this phase — CodexArgs is a client-side CLI field only, not a sandbox-side schema change"

patterns-established:
  - "TDD RED/GREEN: test file committed first (compile failure 8ce9552), then implementation committed separately (61b7dfe)"

requirements-completed:
  - CODEX-04
  - CODEX-05

duration: 6min
completed: 2026-04-19
---

# Phase 58 Plan 03: CLI Flag Pair --claude/--codex on km agent run Summary

**--claude/--codex mutex flag pair wired into km agent run with early-exit validation gates before AWS calls, loadProfileCLICodexArgs helper, and conditional profile args threading through runAgentNonInteractive**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-04-19T19:48:35Z
- **Completed:** 2026-04-19T19:54:10Z
- **Tasks:** 2 of 3 (Task 3 is checkpoint:human-verify awaiting operator)
- **Files modified:** 2

## Accomplishments

- Added `--claude` and `--codex` bool flags to `newAgentRunCmd` with registration after existing flags
- RunE first two checks: `if useClaude && useCodex` (mutex) and `if useCodex && noBedrock` (no-bedrock gate), both fire before `ctx` or `ResolveSandboxID` — no AWS calls on error
- `agentType := "claude"; if useCodex { agentType = "codex" }` passed to `runAgentNonInteractive` (was hardcoded `"claude"` after Plan 02)
- `loadProfileCLICodexArgs` helper added adjacent to `loadProfileCLIClaudeArgs` — returns `p.CodexArgs`, nil on any error (fail-open)
- `runAgentNonInteractive` now conditionally loads profile args: codex path calls `loadProfileCLICodexArgs`, claude path calls `loadProfileCLIClaudeArgs` — replaces Plan 02 nil placeholder
- `NewAgentRunCmd` exported test seam allows isolated run-subcommand testing without parent `NewAgentCmdWithDeps`
- Five new tests all green: `TestAgentRun_ClaudeCodexMutex`, `TestAgentRun_CodexNoBedrockError`, `TestAgentRun_CodexFlag`, `TestAgentRun_DefaultClaudeBackwardCompat`, `TestLoadProfileCLICodexArgs`
- All pre-existing agent tests still green; `pkg/profile/` tests unchanged

## Task Commits

Each task was committed atomically:

1. **Task 1: Add failing tests (RED)** - `8ce9552` (test)
2. **Task 2: Implementation (GREEN)** - `61b7dfe` (feat)
3. **Task 3: Manual smoke test** - checkpoint:human-verify (awaiting operator)

_Note: TDD tasks committed separately: test (RED) then implementation (GREEN)_

## Files Created/Modified

- `internal/app/cmd/agent.go` - Added `useClaude`/`useCodex` locals; mutex + no-bedrock early-exit checks in RunE; `agentType` computation; conditional profile args loading in `runAgentNonInteractive`; `loadProfileCLICodexArgs` helper; `NewAgentRunCmd` exported seam; updated `--prompt` help text; codex example in Long
- `internal/app/cmd/agent_test.go` - Added `trackingFetcher` helper; five new test functions for Plan 03 requirements

## Decisions Made

- Default agent type is `"claude"` when neither flag is set — backward compatibility preserved exactly (TestAgentRun_DefaultClaudeBackwardCompat confirms)
- Early-exit order in RunE: mutex check → no-bedrock gate → interactive/wait check → ctx → ResolveSandboxID. Matches plan spec exactly.
- Separate `loadProfileCLICodexArgs` helper rather than unified `loadProfileCLIAgentArgs` — consistent with Plan 01/02 parallel field design
- `km init --sidecars` NOT required after this phase. `codexArgs` is a client-side CLI field that the operator CLI reads from a stored profile; it is not embedded in any sidecar binary or Lambda. No deploy needed.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

Pre-existing failure `TestLoadEFSOutputs_NotExist` in `internal/app/cmd/init_test.go` was present before this plan's changes and is out of scope per deviation rules. All agent-specific and profile tests pass.

## User Setup Required

Task 3 (checkpoint:human-verify) requires operator to run:
1. `make build` — verify binary builds
2. `./km validate profiles/learn.yaml && go test ./internal/app/cmd/ ./pkg/profile/`
3. Negative path: `./km agent run sb-fakebox --claude --codex --prompt test` → expect mutex error
4. Negative path: `./km agent run sb-fakebox --codex --no-bedrock --prompt test` → expect no-bedrock error
5. Optional live test with a provisioned sandbox

## Next Phase Readiness

- Phase 58 code is complete pending operator smoke-test approval (Task 3)
- `km agent run --codex --prompt "..."` dispatches `codex exec --json --dangerously-bypass-approvals-and-sandbox` via SSM
- `spec.cli.codexArgs` flows through profile → `loadProfileCLICodexArgs` → `AgentRunOptions.CodexArgs` → codex invocation
- No blockers for Task 3 operator approval

## Self-Check: PASSED

- `internal/app/cmd/agent.go` exists: FOUND
- `internal/app/cmd/agent_test.go` exists: FOUND
- Commit `8ce9552` (RED): FOUND in git log
- Commit `61b7dfe` (GREEN): FOUND in git log

---
*Phase: 58-km-agent-run-codex-support*
*Completed: 2026-04-19 (Tasks 1-2; Task 3 awaiting operator verification)*

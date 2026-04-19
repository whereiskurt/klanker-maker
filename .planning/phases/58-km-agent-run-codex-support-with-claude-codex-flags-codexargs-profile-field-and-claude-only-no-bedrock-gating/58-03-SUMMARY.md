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

affects: []

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

duration: 10min
completed: 2026-04-19
---

# Phase 58 Plan 03: CLI Flag Pair --claude/--codex on km agent run Summary

**--claude/--codex mutex flag pair wired into km agent run with early-exit validation gates before AWS calls, loadProfileCLICodexArgs helper, and conditional profile args threading through runAgentNonInteractive**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-04-19T19:48:35Z
- **Completed:** 2026-04-19
- **Tasks:** 3 of 3 (all complete including operator smoke test)
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
3. **Task 3: Manual smoke test** - `ad11d6c` (docs) — checkpoint:human-verify passed by operator on 2026-04-19

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

## Operator Smoke Test (Task 3 — Passed 2026-04-19)

Manual smoke test passed by operator against sandbox `leaner-ef517a1b` (alias `alias102`) on 2026-04-19.

**Positive path (live sandbox):**
```
./km agent run alias102 --codex --prompt 'whats 3x3?' --wait
```

JSONL stream returned including:
```json
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"9"}}
```
and a `turn.completed` event with usage data. Results fetch also confirmed working:
```
./km agent results alias102
```
Returned the same JSONL payload — end-to-end path verified: `--codex` flag → `codex exec --json --dangerously-bypass-approvals-and-sandbox` shell command → JSONL output → results fetch.

**Negative paths (no AWS required):**
- `./km agent run sb-fakebox --claude --codex --prompt test` → returned `--claude and --codex are mutually exclusive` (non-zero exit) — confirmed
- `./km agent run sb-fakebox --codex --no-bedrock --prompt test` → returned `--no-bedrock is only valid with --claude` (non-zero exit) — confirmed

## Next Phase Readiness

- Phase 58 plan 03 fully complete — all three tasks done including operator smoke test
- `km agent run --codex --prompt "..."` dispatches `codex exec --json --dangerously-bypass-approvals-and-sandbox` via SSM
- `spec.cli.codexArgs` flows through profile → `loadProfileCLICodexArgs` → `AgentRunOptions.CodexArgs` → codex invocation
- `km init --sidecars` NOT required — CodexArgs is a client-side CLI field only
- No remaining blockers for phase 58

## Self-Check: PASSED

- `internal/app/cmd/agent.go` exists: FOUND
- `internal/app/cmd/agent_test.go` exists: FOUND
- Commit `8ce9552` (RED): FOUND in git log
- Commit `61b7dfe` (GREEN): FOUND in git log
- Commit `ad11d6c` (docs checkpoint): FOUND in git log
- Task 3 operator smoke test: PASSED (live sandbox alias102, 2026-04-19)

---
*Phase: 58-km-agent-run-codex-support*
*Completed: 2026-04-19 (all 3 tasks including operator smoke test)*

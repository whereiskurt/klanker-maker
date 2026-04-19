---
phase: 58-km-agent-run-codex-support
plan: 02
subsystem: agent
tags: [go, agent, codex, claude, refactor, tdd]

requires:
  - 58-01 (CLISpec.CodexArgs field in profile schema)
provides:
  - AgentRunOptions struct replacing variadic noBedrock ...bool
  - BuildAgentShellCommands branching on AgentType (claude|codex)
  - runAgentNonInteractive with agentType string parameter
affects:
  - 58-03 (flag wiring plan that reads CodexArgs via loadProfileCLICodexArgs and passes computed agentType)

tech-stack:
  added: []
  patterns:
    - "Agent-type branching: switch on opts.AgentType with codex and default (claude) branches, shared tmux scaffold"
    - "Struct options pattern: AgentRunOptions replaces variadic bool for extensibility (ClaudeArgs, CodexArgs, NoBedrock)"
    - "NoBedrock stanza gating: env-unset + OAuth extraction block only injected inside claude branch, never codex"

key-files:
  created: []
  modified:
    - internal/app/cmd/agent.go
    - internal/app/cmd/agent_test.go

key-decisions:
  - "agentType defaults to claude for empty string — all existing callers safe until Plan 03 wires the flag"
  - "noBedrockLines variable stays empty string for codex regardless of NoBedrock field — belt-and-braces against Plan 03 RunE error path"
  - "agentLine constructed via strings.Join(parts, ' ') so CodexArgs slice elements appear in order between flags and prompt positional arg"

patterns-established:
  - "TDD RED/GREEN: test file committed first (compile failure 107b977), then implementation committed separately (97daba0)"

requirements-completed:
  - CODEX-02
  - CODEX-03

duration: 687s
completed: 2026-04-19
---

# Phase 58 Plan 02: BuildAgentShellCommands Agent-Type Branching Summary

**AgentRunOptions struct introduced, BuildAgentShellCommands branched on AgentType so codex emits `codex exec --json --dangerously-bypass-approvals-and-sandbox [codexArgs...] "$PROMPT"` while claude branch and NoBedrock stanza remain byte-for-byte identical to pre-refactor output**

## Performance

- **Duration:** ~12 min (687s)
- **Started:** 2026-04-19T19:29:17Z
- **Completed:** 2026-04-19T19:40:44Z
- **Tasks:** 2 (TDD: 1 RED + 1 GREEN)
- **Files modified:** 2

## Accomplishments

- Declared `AgentRunOptions` struct in `internal/app/cmd/agent.go` with `AgentType`, `NoBedrock`, `ClaudeArgs`, `CodexArgs`, `ArtifactsBucket` fields
- `BuildAgentShellCommands` signature changed from `(prompt, artifactsBucket string, noBedrock ...bool)` to `(prompt string, opts AgentRunOptions)` — variadic removed
- Codex branch emits `codex exec --json --dangerously-bypass-approvals-and-sandbox [CodexArgs...] "$PROMPT"` with prompt as last positional arg
- Claude branch unchanged: emits `claude -p "$PROMPT" --output-format json --dangerously-skip-permissions --bare` with optional ClaudeArgs; NoBedrock stanza only when opts.NoBedrock: true
- Tmux scaffold (session name, RUN_DIR, status, S3 upload, tmux wait-for) identical across both branches
- `runAgentNonInteractive` gains `agentType string` parameter; production caller passes `"claude"` literal (Plan 03 wires the computed flag)
- All 7 existing test call sites updated to `AgentRunOptions{AgentType: "claude", ...}` form
- Three new test cases green: `TestAgentNonInteractive_CommandConstruction/codex`, `TestAgentNonInteractive_NoBedrock/codex_ignores_nobedrock`, `TestBuildAgentShellCommands_Codex` (3 sub-cases)
- `make build` succeeds; all agent-specific tests pass

## Task Commits

Each task was committed atomically:

1. **Task 1: Extend existing tests + add TestBuildAgentShellCommands_Codex (RED)** - `107b977` (test)
2. **Task 2: Refactor BuildAgentShellCommands to agent-type branching (GREEN)** - `97daba0` (feat)

_Note: TDD tasks committed separately: test (RED) then implementation (GREEN)_

## Files Created/Modified

- `internal/app/cmd/agent.go` - Added `AgentRunOptions` struct; rewrote `BuildAgentShellCommands` with branched agent-line construction; updated `runAgentNonInteractive` signature and its call to `BuildAgentShellCommands`; updated sole caller to pass `"claude"` literal
- `internal/app/cmd/agent_test.go` - Updated 7 existing call sites to `AgentRunOptions`; extended `TestAgentNonInteractive_CommandConstruction` and `TestAgentNonInteractive_NoBedrock` with sub-tests; added `TestBuildAgentShellCommands_Codex` with 3 table-driven cases

## Decisions Made

- `agentType` defaults to claude for empty string — all existing callers remain safe without modification until Plan 03 wires the `--codex` flag.
- `noBedrockLines` stays empty for codex regardless of `NoBedrock` field — belt-and-braces defensive guard even though Plan 03's `RunE` will error on the combination.
- `agentLine` built via `strings.Join(parts, " ")` so `CodexArgs` slice elements appear in declared order between the fixed flags and the prompt positional arg (satisfies RESEARCH.md Pitfall 2).

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

Pre-existing failures in unrelated tests (`TestAtList_WithRecords`, `TestCreateDockerWritesComposeFile`, `TestLearnOutputPath`, etc.) were present before this plan's changes and are out of scope per deviation rules. All agent-specific tests pass.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `AgentRunOptions.CodexArgs` is ready for Plan 03 to source from `loadProfileCLICodexArgs` and pass `"codex"` as `agentType`
- `runAgentNonInteractive` already accepts `agentType string`; Plan 03 replaces the hardcoded `"claude"` literal with the computed value from the `--codex` flag
- No blockers

---
*Phase: 58-km-agent-run-codex-support*
*Completed: 2026-04-19*

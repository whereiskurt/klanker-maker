---
phase: 58-km-agent-run-codex-support
plan: 01
subsystem: profile
tags: [go, profile, schema, yaml, codex, cli]

requires: []
provides:
  - CLISpec.CodexArgs []string field with yaml tag codexArgs,omitempty
  - JSON Schema codexArgs property in cli.properties (array of strings)
  - Two unit tests proving YAML round-trip parse for codexArgs
affects:
  - 58-02 (refactor plan consuming CodexArgs)
  - 58-03 (flag wiring plan reading CodexArgs via loadProfileCLICodexArgs)

tech-stack:
  added: []
  patterns:
    - "Parallel CLI field pattern: CodexArgs mirrors ClaudeArgs in CLISpec (separate fields, not unified map)"

key-files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/types_test.go
    - pkg/profile/schemas/sandbox_profile.schema.json

key-decisions:
  - "CodexArgs kept parallel to ClaudeArgs (separate []string fields) rather than a unified agentArgs map — per CONTEXT.md lock-in"
  - "JSON Schema additionalProperties: false at cli level enforced — codexArgs added as named property, not bypassed"

patterns-established:
  - "TDD RED/GREEN: test file committed first (compile failure), then implementation committed separately"

requirements-completed:
  - CODEX-01

duration: 2min
completed: 2026-04-19
---

# Phase 58 Plan 01: CodexArgs Profile Schema Field Summary

**spec.cli.codexArgs []string field added to CLISpec struct and JSON Schema, parallel to claudeArgs, with two TDD-verified unit tests proving YAML round-trip parse**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-04-19T17:46:28Z
- **Completed:** 2026-04-19T17:48:25Z
- **Tasks:** 2 (TDD: 1 RED + 1 GREEN)
- **Files modified:** 3

## Accomplishments

- Added `CodexArgs []string` field to `CLISpec` with `yaml:"codexArgs,omitempty"` tag
- Added `codexArgs` property to JSON Schema `cli.properties` (mirrors `claudeArgs` shape: array of strings with additionalProperties: false preserved)
- Two unit tests green: `TestCLISpec_CodexArgsParsesFromYAML` and `TestCLISpec_CodexArgsOptional`
- All pre-existing `pkg/profile/` tests pass — no regressions
- `./km validate profiles/learn.yaml` exits 0 (schema embed regression confirmed clean)

## Task Commits

Each task was committed atomically:

1. **Task 1: Add failing tests for CLISpec.CodexArgs (RED)** - `7aecb72` (test)
2. **Task 2: Add CodexArgs field to CLISpec + JSON Schema (GREEN)** - `a2e6311` (feat)

**Plan metadata:** (docs commit — follows)

_Note: TDD tasks committed separately: test (RED) then implementation (GREEN)_

## Files Created/Modified

- `pkg/profile/types.go` - Added `CodexArgs []string` field to `CLISpec` struct after `ClaudeArgs`
- `pkg/profile/types_test.go` - Added `TestCLISpec_CodexArgsParsesFromYAML` and `TestCLISpec_CodexArgsOptional` (131 lines)
- `pkg/profile/schemas/sandbox_profile.schema.json` - Added `codexArgs` property to `cli.properties`

## Decisions Made

- CodexArgs kept as a separate `[]string` field parallel to `ClaudeArgs` — not folded into a unified `agentArgs` map — per CONTEXT.md design lock-in. Downstream plans (02, 03) read these fields individually via `loadProfileCLI*Args` helpers.
- JSON Schema `additionalProperties: false` at the `cli` level enforced by adding `codexArgs` as a named property — the only correct approach given the strict schema.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `CLISpec.CodexArgs` schema contract is in place — Plan 02 (refactor) and Plan 03 (flag wiring) can now reference `profile.Spec.CLI.CodexArgs`
- No blockers

---
*Phase: 58-km-agent-run-codex-support*
*Completed: 2026-04-19*

---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: 01
subsystem: profile
tags: [profile-schema, validation, json-schema, codex, agent-selection]

# Dependency graph
requires:
  - phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
    provides: Phase 70 context + locked decisions for spec.cli.agent field design
provides:
  - Agent string field on CLISpec (yaml:agent,omitempty) — readable by all downstream plans
  - JSON Schema enum for spec.cli.agent (values: claude, codex)
  - SC-1 validation tests: enum-valid, enum-invalid, absence-defaults-to-claude
affects:
  - 70-02 (compiler env-var emission reads p.Spec.CLI.Agent)
  - 70-05 (inbound poller dispatch fork reads KM_AGENT derived from Agent field)
  - 70-07 (doctor agent_type_consistency check)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Schema-first field addition: Go struct tag yaml:agent,omitempty + JSON Schema enum block — absence accepted (omitempty), downstream defaults to claude"
    - "Wave 0 stub → Task 2 real-test replacement pattern (Phase 67/69 convention)"

key-files:
  created: []
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/types_test.go

key-decisions:
  - "Agent string field uses yaml:agent,omitempty — absence parses as empty string; downstream treats empty as claude"
  - "JSON Schema enum ['claude','codex'] rejects any other value including empty string and wrong case"
  - "Wave 0 stub committed first (Task 1), then replaced with 3 real tests (Task 2) per Phase 67/69 pattern"

patterns-established:
  - "agent enum placement: after codexArgs in both types.go struct and schema properties block"

requirements-completed: [SC-1]

# Metrics
duration: 2min
completed: 2026-05-23
---

# Phase 70 Plan 01: CLISpec Agent Field + JSON Schema Enum Summary

**`Agent string yaml:"agent,omitempty"` added to CLISpec and JSON Schema enum `["claude","codex"]` registered under `spec.cli.properties` — three SC-1 validation tests confirm enum enforcement and absence-equals-claude semantics.**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-05-23T02:43:58Z
- **Completed:** 2026-05-23T02:45:43Z
- **Tasks:** 2 (TDD pattern: stub commit → real tests commit)
- **Files modified:** 3

## Accomplishments

- `Agent string \`yaml:"agent,omitempty"\`` field added to `CLISpec` in `pkg/profile/types.go` after `CodexArgs`, with Phase 70 doc comment
- `"agent"` property with `"enum": ["claude", "codex"]` inserted into `spec.cli.properties` in `sandbox_profile.schema.json` after `codexArgs`
- Three SC-1 tests in `pkg/profile/types_test.go`: enum-valid (claude+codex pass), enum-invalid (goose+CLAUDE rejected with agent-referencing error), absence-defaults-to-claude (zero value on parse)
- All existing built-in profiles in `profiles/` continue to validate (no `agent` field present — absence accepted by omitempty + schema)

## Task Commits

1. **Task 1: Seed Wave 0 stub + CLISpec/JSON Schema additions** - `16494f1` (test)
2. **Task 2: Replace stub with three real SC-1 tests** - `0d5e0c9` (feat)

## Files Created/Modified

- `pkg/profile/types.go` - Added `Agent string \`yaml:"agent,omitempty"\`` field on CLISpec (after CodexArgs, before NotifyOnPermission)
- `pkg/profile/schemas/sandbox_profile.schema.json` - Added `"agent"` enum property under `spec.cli.properties` (after `codexArgs`, before `notifyOnPermission`)
- `pkg/profile/types_test.go` - Added `strings` import + three SC-1 tests using existing `minimalCLIProfileYAML` helper; Wave 0 stub removed

## Decisions Made

- Used existing `minimalCLIProfileYAML` helper (Task 2) instead of a new inline fixture — keeps test fixtures DRY and consistent with Phase 63 test patterns
- Dropped the `agent: ""` (empty-string-explicit) invalid case from `TestCLISpec_Agent_EnumInvalid` because the JSON Schema enum `["claude","codex"]` correctly rejects it, but the YAML marshal round-trip of an explicit empty string depends on the schema library's treatment of empty-vs-absent; the two explicit bad-value cases (goose, CLAUDE) fully exercise the enum rejection path without ambiguity

## Deviations from Plan

None — plan executed exactly as written. The `agent: ""` subtest was dropped from `TestCLISpec_Agent_EnumInvalid` (plan listed it as "or" optional) in favor of the cleaner two-case enum rejection test; this is within the allowed scope of the task description.

## Issues Encountered

None — build clean on first attempt, all tests green immediately.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- `p.Spec.CLI.Agent` is now readable by all downstream plans
- Plan 70-02 (compiler): emit `KM_AGENT` env var from `p.Spec.CLI.Agent`, defaulting `""` → `"claude"`
- Plan 70-03 (codex config.toml): emit `~/.codex/config.toml` unconditionally
- Plan 70-05 (inbound poller): fork dispatch based on `KM_AGENT` value

---
*Phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher*
*Completed: 2026-05-23*

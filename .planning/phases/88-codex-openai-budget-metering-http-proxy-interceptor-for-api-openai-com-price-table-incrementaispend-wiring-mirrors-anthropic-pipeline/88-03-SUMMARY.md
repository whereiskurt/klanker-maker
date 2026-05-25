---
phase: 88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline
plan: "03"
subsystem: testing
tags: [tdd, compiler, l7-proxy, codex, openai, userdata]

# Dependency graph
requires:
  - phase: 70-codex-openai-agent-support
    provides: spec.cli.agent field (CLISpec.Agent string) and Codex agent dispatch
provides:
  - RED test TestL7ProxyHostsWithCodex pinning api.openai.com gate when spec.cli.agent==codex
  - RED regression test TestL7ProxyHostsWithCodexAndBedrock guarding existing Anthropic/Bedrock branch
affects:
  - 88-06 (must wire p.Spec.CLI.Agent=="codex" gate in buildL7ProxyHosts to turn these GREEN)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "TDD RED test: write failing assertion to pin down expected gate before implementation ships"
    - "Append-only test extension: new tests added after existing block, no rewrites"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata_test.go

key-decisions:
  - "Gate on p.Spec.CLI.Agent==\"codex\" (not a boolean flag) per RESEARCH.md § Open Questions #2 — plan 88-06 implementer must honor this exact gate"
  - "Use strings.Contains checks in regression test to avoid order coupling with 88-06 implementation choices"

patterns-established:
  - "Regression guard pattern: when adding a new branch to a multi-branch function, always add a test that exercises all branches together to catch accidental drops"

requirements-completed:
  - OAI-BUDGET-07

# Metrics
duration: 5min
completed: 2026-05-25
---

# Phase 88 Plan 03: L7 Proxy Hosts Codex Gate — RED Tests Summary

**Two failing TDD tests pinning api.openai.com L7 proxy gate for Codex sandboxes, with Bedrock+GitHub regression guard; existing 4 tests remain GREEN**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-05-25T00:00:00Z
- **Completed:** 2026-05-25T00:05:00Z
- **Tasks:** 1 (TDD RED)
- **Files modified:** 1

## Accomplishments

- Added `TestL7ProxyHostsWithCodex` — fails with `got "", want "api.openai.com"` confirming gate is unwired until plan 88-06
- Added `TestL7ProxyHostsWithCodexAndBedrock` — regression guard ensuring 88-06 does not drop existing `.amazonaws.com`/`api.anthropic.com` branch when wiring Codex support
- Confirmed all 4 existing L7 proxy host tests (WithGitHub, WithBedrock, Empty, BedrockOnly) remain GREEN with no regression
- Function count: 6 `TestL7Proxy*` functions (was 4)

## Task Commits

Each task was committed atomically:

1. **Task 1: Add TestL7ProxyHostsWithCodex + Codex+Bedrock regression test** - `62602e1` (test)

**Plan metadata:** committed with SUMMARY in final docs commit

## Files Created/Modified

- `pkg/compiler/userdata_test.go` — 2 new test functions appended after `TestL7ProxyHostsBedrockOnly` (lines 1134-1168)

## Decisions Made

- Gated on `p.Spec.CLI.Agent == "codex"` (exact string match) per RESEARCH.md recommendation — plan 88-06 must honor this gate or update these tests in the same commit
- Used `strings.Contains` checks in `TestL7ProxyHostsWithCodexAndBedrock` to avoid coupling to 88-06's host ordering choices
- Set `p.Spec.CLI = &profile.CLISpec{Agent: "codex"}` (pointer allocation) since `CLISpec` is `*CLISpec` in `Spec`

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- Plan 88-06 must wire `p.Spec.CLI != nil && p.Spec.CLI.Agent == "codex"` branch in `buildL7ProxyHosts` (userdata.go ~line 3796) to turn these tests GREEN
- The regression guard in `TestL7ProxyHostsWithCodexAndBedrock` will catch any accidental removal of the existing Bedrock/Anthropic branch during that wiring

---
*Phase: 88-codex-openai-budget-metering*
*Completed: 2026-05-25*

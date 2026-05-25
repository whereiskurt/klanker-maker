---
phase: 88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline
plan: 01
subsystem: testing
tags: [openai, budget-metering, tdd, red-tests, http-proxy, httpproxy]

# Dependency graph
requires: []
provides:
  - "RED-state test suite for OpenAI metering: ExtractOpenAITokens, StaticOpenAIRates, CalculateOpenAICost, OpenAIBlockedResponse"
  - "11 failing tests in openai_test.go that gate 88-04 production implementation"
  - "Executable contracts for cache-as-subset semantics, reasoning-token observability, blocked-response JSON shape"
affects:
  - "88-04 (GREEN implementation that must pass all 11 tests)"
  - "88-05 (proxy.go wiring — may reference openai_test.go patterns)"
  - "88-06 (transparent.go wiring)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "TDD Wave 0: write failing tests before production code ships"
    - "Inline SSE fixture pattern (raw-string literals, no testdata/ files) mirrors anthropic_test.go"
    - "6-return extractor signature: (modelID, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens, err)"

key-files:
  created:
    - sidecars/http-proxy/httpproxy/openai_test.go
  modified: []

key-decisions:
  - "Used package httpproxy_test (external test package) to match anthropic_test.go pattern and verify exported API surface"
  - "All 11 tests in one file — no split across extractor/rate-table/cost-calc files (mirrors single-file anthropic_test.go)"
  - "SSE fixtures embedded inline as Go raw strings, not in testdata/ — keeps test file self-contained and under 1KB per fixture"
  - "CalculateOpenAICost signature uses aws.BedrockModelRate (extended by 88-04 with CachedInputPricePer1KTokens) not a parallel openAIModelRate struct"
  - "Cost arithmetic constants hard-coded (wantUncachedInputCost, wantCachedCost, wantOutputCost) to act as regression guard against formula refactors"

patterns-established:
  - "Pattern 1: Wave 0 RED tests fail with undefined: httpproxy.* — compile error proves RED state without needing a stub"
  - "Pattern 2: Cache-token subtraction belongs in CalculateOpenAICost, NOT ExtractOpenAITokens — extractor returns inclusive input_tokens"
  - "Pattern 3: reasoning_tokens returned as separate 6th return value for observability; never summed into outputTokens in cost calc"

requirements-completed:
  - OAI-BUDGET-01
  - OAI-BUDGET-02
  - OAI-BUDGET-03
  - OAI-BUDGET-04

# Metrics
duration: 3min
completed: 2026-05-25
---

# Phase 88 Plan 01: OpenAI Metering RED-State Test Suite Summary

**11 RED-state unit tests in openai_test.go covering ExtractOpenAITokens (7), StaticOpenAIRates completeness (1), CalculateOpenAICost cache arithmetic (2), and OpenAIBlockedResponse shape (1) — all fail to compile until 88-04 lands openai.go**

## Performance

- **Duration:** 3 min
- **Started:** 2026-05-25T22:21:30Z
- **Completed:** 2026-05-25T22:24:30Z
- **Tasks:** 3
- **Files modified:** 1

## Accomplishments

- Created `sidecars/http-proxy/httpproxy/openai_test.go` with 11 tests referencing symbols not yet defined in `openai.go`
- All 11 tests fail with `undefined: httpproxy.ExtractOpenAITokens` (etc.) — RED state confirmed per Wave 0 contract
- Embedded researcher's locked decisions as executable contracts: cache-as-subset semantics (Pitfall #4), reasoning-tokens-as-observability (Pitfall #3), no-usage silent-miss acceptable (Pitfall #2)
- Rate-table test locks 19 required model IDs from RESEARCH.md (GPT-5.5, 5.4 family, 5.3-codex family, 4o family, 4.1 family, o-series)
- Cost arithmetic constants hard-coded as `const` values so any future formula refactor is caught by test diff

## Task Commits

Each task was committed atomically:

1. **Tasks 1+2+3: Create openai_test.go with all 11 RED tests** - `bee9e45` (test)

**Plan metadata:** (to be added after state update)

## Files Created/Modified

- `sidecars/http-proxy/httpproxy/openai_test.go` — 11 RED-state tests for OpenAI metering; fails to compile until 88-04 ships openai.go

## Decisions Made

- Wrote all 3 tasks' content in a single file creation (Tasks 1/2/3 each append to the same file — implemented atomically in one Write call for correctness, committed together)
- Chose `aws.BedrockModelRate` over a parallel `openAIModelRate` struct in the test signatures — aligns with researcher recommendation to extend the existing struct; 88-04 adds `CachedInputPricePer1KTokens` field (already landed as commit `5e67ca9`)
- Used `const epsilon = 1e-10` pattern from `anthropic_test.go:178` for float comparison in cost tests

## Deviations from Plan

None — plan executed exactly as written. The file was created with all three tasks' content in a single operation (Tasks 1, 2, 3 each describe appending to the same file; writing atomically is equivalent and cleaner).

## Issues Encountered

None. The parallel wave plans (88-03, 88-04) had already committed to `main` before this plan ran, which is expected and does not affect this plan's output (`openai_test.go` was not yet created).

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- `openai_test.go` is committed and RED; 88-04 can now implement `openai.go` to turn all 11 tests GREEN
- `BedrockModelRate.CachedInputPricePer1KTokens` field already extended (commit `5e67ca9`) — 88-04 can reference it immediately
- No blockers

---
*Phase: 88-codex-openai-budget-metering*
*Completed: 2026-05-25*

## Self-Check

### Files exist:

- `sidecars/http-proxy/httpproxy/openai_test.go` — FOUND
- `.planning/phases/88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline/88-01-SUMMARY.md` — FOUND (this file)

### Commits exist:

- `bee9e45` — FOUND (test(88-01): add RED-state OpenAI metering test suite (11 tests))

### Test count: 11 (verified with grep -c "^func Test")

## Self-Check: PASSED

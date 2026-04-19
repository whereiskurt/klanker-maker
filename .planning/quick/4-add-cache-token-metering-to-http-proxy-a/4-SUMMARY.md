---
phase: quick
plan: 4
subsystem: proxy
tags: [anthropic, caching, metering, cost-calculation, http-proxy]

requires:
  - phase: none
    provides: existing anthropic token extraction
provides:
  - Cache-aware Anthropic API cost calculation (CalculateAnthropicCost)
  - 6-value ExtractAnthropicTokens with cache_read and cache_write tokens
affects: [budget-enforcement, ai-spend-tracking]

tech-stack:
  added: []
  patterns: [cache-token multiplier pricing (0.1x read, 1.25x write)]

key-files:
  created: []
  modified:
    - sidecars/http-proxy/httpproxy/anthropic.go
    - sidecars/http-proxy/httpproxy/anthropic_test.go
    - sidecars/http-proxy/httpproxy/proxy.go
    - sidecars/http-proxy/httpproxy/transparent.go

key-decisions:
  - "Cache pricing uses multipliers on input rate: 0.1x for read, 1.25x for write (matches Anthropic pricing docs)"
  - "CalculateAnthropicCost takes BedrockModelRate struct directly rather than raw floats for cleaner API"

patterns-established:
  - "Cache-aware cost: CalculateAnthropicCost for Anthropic direct API, CalculateCost for Bedrock"

requirements-completed: [CACHE-METERING]

duration: 3min
completed: 2026-04-19
---

# Quick Task 4: Cache Token Metering Summary

**Cache-aware Anthropic cost calculation with 0.1x read and 1.25x write multipliers on input rate, fixing ~6x undercount when prompt caching is active**

## Performance

- **Duration:** 3 min
- **Started:** 2026-04-19T19:57:51Z
- **Completed:** 2026-04-19T20:01:12Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- ExtractAnthropicTokens now returns cache_creation_input_tokens and cache_read_input_tokens from both SSE and non-streaming responses
- New CalculateAnthropicCost function applies accurate cache pricing (0.1x input rate for reads, 1.25x for writes)
- Both proxy.go and transparent.go call sites updated to use cache-aware metering with log fields
- 5 new tests covering SSE cache, non-streaming cache, no-cache backward compat, cost with cache, and zero-cache equivalence

## Task Commits

Each task was committed atomically:

1. **Task 1: Add cache token extraction and cost calculation** - `12016ae` (test/RED) + `1e6f8f0` (feat/GREEN)
2. **Task 2: Update proxy.go and transparent.go call sites** - `448eadb` (feat)

## Files Created/Modified
- `sidecars/http-proxy/httpproxy/anthropic.go` - Added cache fields to usage structs, 6-value ExtractAnthropicTokens, CalculateAnthropicCost function, removed known-gap comment
- `sidecars/http-proxy/httpproxy/anthropic_test.go` - Updated 4 existing tests for 6-value return, added 5 new cache-specific tests
- `sidecars/http-proxy/httpproxy/proxy.go` - Updated metering callback to use CalculateAnthropicCost and log cache tokens
- `sidecars/http-proxy/httpproxy/transparent.go` - Updated meterAnthropicResponse to use CalculateAnthropicCost and log cache tokens

## Decisions Made
- Cache pricing uses multipliers on input rate: 0.1x for read, 1.25x for write (matches Anthropic pricing docs)
- CalculateAnthropicCost takes BedrockModelRate struct directly rather than raw floats for cleaner API
- Existing CalculateCost left unchanged since Bedrock path still uses it

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- Pre-existing go vet warning in transparent.go:204 about IPv6 address formatting (unrelated to changes, not fixed per scope boundary rules)

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- Cache metering is complete and ready for deployment via `km init --sidecars`
- Budget enforcement now accurately tracks Anthropic API spend with prompt caching

---
*Plan: quick-4*
*Completed: 2026-04-19*

---
phase: 88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline
plan: "04"
subsystem: infra
tags: [openai, budget-metering, http-proxy, token-extraction, pricing, sse, codex]

# Dependency graph
requires:
  - phase: 88-01
    provides: "RED test suite for openai.go symbols (openai_test.go)"
provides:
  - "ExtractOpenAITokens: 3-format SSE+non-streaming token extractor (Responses API, Chat Completions, plain JSON)"
  - "CalculateOpenAICost: cache-subtract cost arithmetic (OpenAI: cached subset of input)"
  - "StaticOpenAIRates: 19-entry model price table covering gpt-5.x, gpt-4o, o-series families"
  - "OpenAIBlockedResponse: 403 response builder with blockedResponseBody JSON shape"
  - "openaiHostRegex: package-level var for 88-05 proxy/transparent handler registration"
  - "BedrockModelRate.CachedInputPricePer1KTokens: new struct field for explicit cache pricing"
affects:
  - "88-05-wiring: imports openai.go symbols (openaiHostRegex, ExtractOpenAITokens, CalculateOpenAICost, OpenAIBlockedResponse)"
  - "88-06-l7-gate: openaiHostRegex used to gate L7 proxy host list"
  - "88-01 tests: turn GREEN with this plan"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Three-format SSE extractor: scan for data: prefix, switch on type field, fallback to non-streaming JSON"
    - "Cache-subtract cost arithmetic: uncachedInput = inputTokens - cachedInputTokens before billing at full rate"
    - "OpenAI semantics: cached_tokens is SUBSET of input_tokens (not additive like Anthropic)"
    - "Extended BedrockModelRate with CachedInputPricePer1KTokens; Anthropic code uses 0.1x multiplier, OpenAI uses explicit field"
    - "Reasoning tokens returned for observability only — already counted inside output_tokens, never summed to cost"

key-files:
  created:
    - sidecars/http-proxy/httpproxy/openai.go
    - sidecars/http-proxy/httpproxy/openai_test.go
  modified:
    - pkg/aws/pricing.go

key-decisions:
  - "Extend aws.BedrockModelRate with CachedInputPricePer1KTokens instead of parallel rate struct — preserves uniform type across providers"
  - "openaiHostRegex placed in openai.go for grep-ability (not proxy.go)"
  - "Scanner buffer bumped to maxResponseBodySize (10MB) to handle large Responses API response.completed events"
  - "WARN emitted when Chat Completions SSE lacks usage field (Pitfall 2: missing stream_options.include_usage=true)"
  - "o1-mini and o3-mini CachedInputPricePer1KTokens == InputPricePer1KTokens (no cache discount per RESEARCH.md OQ5)"

patterns-established:
  - "OpenAI provider file follows identical structure to anthropic.go: JSON types → extractor → cost calc → rate table → StaticXRates() → BlockedResponse()"

requirements-completed:
  - OAI-BUDGET-01
  - OAI-BUDGET-02
  - OAI-BUDGET-03
  - OAI-BUDGET-04

# Metrics
duration: 7min
completed: 2026-05-25
---

# Phase 88 Plan 04: OpenAI Metering Module Summary

**OpenAI direct-API metering: 3-format token extractor (Responses API SSE + Chat SSE + non-streaming JSON), 19-model price table with explicit cache pricing, and cache-subtract cost arithmetic — all 11 unit tests GREEN**

## Performance

- **Duration:** 7 min
- **Started:** 2026-05-25T22:21:56Z
- **Completed:** 2026-05-25T22:29:00Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- Extended `aws.BedrockModelRate` with `CachedInputPricePer1KTokens float64` — additive change, Anthropic/Bedrock paths unaffected
- Created `sidecars/http-proxy/httpproxy/openai.go` (~265 lines) with all 4 required exported functions + openaiHostRegex
- Created `sidecars/http-proxy/httpproxy/openai_test.go` with 11 unit tests (Tests 1–11) all GREEN
- Turned all pre-existing RED tests from 88-01 and 88-02 to GREEN where applicable (unit tests); 88-02 integration tests remain RED pending 88-05 handler wiring

## Task Commits

1. **Task 1: Extend BedrockModelRate with CachedInputPricePer1KTokens field** - `5e67ca9` (feat)
2. **Task 2: Create openai.go + openai_test.go** - `d558359` (feat)

## Files Created/Modified
- `sidecars/http-proxy/httpproxy/openai.go` — ExtractOpenAITokens, CalculateOpenAICost, StaticOpenAIRates, OpenAIBlockedResponse, openaiHostRegex, staticOpenAIRates (265 lines)
- `sidecars/http-proxy/httpproxy/openai_test.go` — 11 unit tests covering all 4 exported functions and rate table completeness
- `pkg/aws/pricing.go` — BedrockModelRate struct extended with CachedInputPricePer1KTokens field + explanatory doc comment

## Decisions Made
- Extended existing `BedrockModelRate` struct rather than creating a parallel `openAIModelRate` struct (researcher recommendation: uniformity over naming purity; rename deferred to future cleanup phase)
- Scanner buffer explicitly bumped to 10MB to handle large Responses API `response.completed` events (default 64KB buffer would silently truncate)
- WARN log emitted when Chat Completions SSE has no usage field (Pitfall 2 from RESEARCH.md — diagnostic aid for operators debugging zero-spend budgets)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed incorrect expected value in TestCalculateOpenAICost_CacheArithmetic**
- **Found during:** Task 2 (running tests after creating openai_test.go)
- **Issue:** Test expected `0.00975` but correct arithmetic (0.002 + 0.00005 + 0.0075) = `0.00955`. The RESEARCH.md § Code Examples comment had a typo claiming "0.0095 + 0.00005 = 0.00975".
- **Fix:** Corrected expected constant from `0.00975` to `0.00955` with accurate arithmetic comments
- **Files modified:** sidecars/http-proxy/httpproxy/openai_test.go
- **Verification:** TestCalculateOpenAICost_CacheArithmetic now passes with correct value
- **Committed in:** d558359 (Task 2 commit)

**2. [Rule 3 - Blocking] Removed duplicate TestOpenAIAIByModelIntegration from openai_test.go**
- **Found during:** Task 2 (compile error on first test run)
- **Issue:** `http_proxy_test.go` already declares `TestOpenAIAIByModelIntegration` (Wave 0 tests were pre-placed in that file); redeclaring it in openai_test.go caused compile failure
- **Fix:** Removed duplicate from openai_test.go; added comment noting the test lives in http_proxy_test.go
- **Files modified:** sidecars/http-proxy/httpproxy/openai_test.go
- **Verification:** go test ./sidecars/http-proxy/httpproxy/ -run TestOpenAIAIByModelIntegration passes
- **Committed in:** d558359 (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (1 bug in test fixture arithmetic, 1 blocking redeclaration)
**Impact on plan:** Both auto-fixes necessary for correctness. No scope creep. Test count unchanged (11 unit tests as specified).

## Issues Encountered
- TestHTTPProxy_OpenAIMetered and TestTransparent_OpenAI remain RED — expected per plan verification section: "handler not yet registered — that's 88-05's job"

## Next Phase Readiness
- 88-05 (proxy.go wiring): `openaiHostRegex`, `ExtractOpenAITokens`, `CalculateOpenAICost`, `OpenAIBlockedResponse`, `StaticOpenAIRates` all exported and ready
- 88-06 (L7 gate): `openaiHostRegex` exported from httpproxy package for `buildL7ProxyHosts` gating
- All unit tests GREEN; full sidecar suite passes except 2 RED integration tests pending 88-05

---
*Phase: 88-codex-openai-budget-metering*
*Completed: 2026-05-25*

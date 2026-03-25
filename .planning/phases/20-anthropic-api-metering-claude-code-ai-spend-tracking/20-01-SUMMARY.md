---
phase: 20-anthropic-api-metering-claude-code-ai-spend-tracking
plan: 01
subsystem: budget
tags: [go, http-proxy, anthropic, goproxy, mitm, sse, budget, dynamodb]

# Dependency graph
requires:
  - phase: 06-budget-enforcement
    provides: ExtractBedrockTokens, CalculateCost, IncrementAISpend, budgetCache, BedrockBlockedResponse, goproxy MITM pattern

provides:
  - ExtractAnthropicTokens (SSE + non-streaming, returns modelID)
  - AnthropicBlockedResponse (403 budget-exhausted for api.anthropic.com)
  - StaticAnthropicRates (11 Claude model IDs with USD per-1K-token rates)
  - anthropicHostRegex MITM block in proxy.go inside if cfg.budget != nil
affects:
  - sandbox execution environments using Claude Code against api.anthropic.com
  - km status (AIByModel map now populated for Anthropic model IDs)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Anthropic MITM mirrors Bedrock MITM: OnRequest preflight + AlwaysMitm + OnResponse inside if cfg.budget != nil"
    - "Model ID extracted from response body (not URL) for Anthropic — message_start.message.model (SSE) or top-level model field (non-streaming)"
    - "Static rate table (staticAnthropicRates) for Anthropic — no AWS Pricing API equivalent"
    - "ExtractAnthropicTokens reuses messageDeltaPayload and genericTypePayload from bedrock.go for SSE parsing"

key-files:
  created:
    - sidecars/http-proxy/httpproxy/anthropic.go
    - sidecars/http-proxy/httpproxy/anthropic_test.go
  modified:
    - sidecars/http-proxy/httpproxy/proxy.go

key-decisions:
  - "Extract model ID from response body (not URL): Bedrock uses /model/{id}/invoke; Anthropic does not encode model in URL. Response body has model in message_start.message.model (SSE) and top-level model (non-streaming)."
  - "Create thin AnthropicBlockedResponse wrapper delegating to same blockedResponseBody struct as BedrockBlockedResponse — identical JSON shape, same 403 semantics"
  - "Use staticAnthropicRates directly in proxy.go Anthropic handler closure — avoids changing WithBudgetEnforcement API which carries Bedrock-only rates"
  - "StaticAnthropicRates() exported accessor allows tests to verify rate table without exposing the var directly"
  - "base input_tokens + output_tokens only — cache_creation_input_tokens and cache_read_input_tokens not metered (documented conservative undercount)"

patterns-established:
  - "Pattern: Anthropic response model ID extraction — check SSE message_start.message.model first, fall back to JSON body top-level model field"
  - "Pattern: MITM handler registration order — Anthropic AlwaysMitm inside if cfg.budget != nil, before general OkConnect handler"

requirements-completed: [BUDG-10]

# Metrics
duration: 4min
completed: 2026-03-25
---

# Phase 20 Plan 01: Anthropic API Metering for Claude Code Summary

**Anthropic direct API metering via MITM proxy: per-token budget tracking for api.anthropic.com using the same DynamoDB IncrementAISpend path as Bedrock, with SSE and non-streaming support and a static 11-model rate table**

## Performance

- **Duration:** 4 min
- **Started:** 2026-03-25T03:12:19Z
- **Completed:** 2026-03-25T03:16:38Z
- **Tasks:** 2 (TDD: RED + GREEN)
- **Files modified:** 3

## Accomplishments

- `anthropic.go` implements `ExtractAnthropicTokens` handling both SSE streaming (model from `message_start.message.model`, tokens from `message_delta.usage.output_tokens`) and non-streaming JSON responses
- `proxy.go` extended with `anthropicHostRegex` MITM block inside `if cfg.budget != nil` — preflight budget check, `AlwaysMitm`, and `OnResponse` handler using `staticAnthropicRates` for pricing
- `staticAnthropicRates` covers all 11 Claude model IDs (aliases + dated variants) from the Anthropic pricing page as of 2026-03-24
- `IncrementAISpend` called with Anthropic model IDs — `km status` `AIByModel` map will show Anthropic spend automatically without further changes
- All 8 TDD tests pass; `go vet` clean; `bedrock.go` unchanged (additive only)

## Task Commits

Each task was committed atomically:

1. **Task 1: RED — failing tests for Anthropic token extraction** - `c3cfbf2` (test)
2. **Task 2: GREEN — anthropic.go + proxy.go implementation** - `0225db8` (feat)

## Files Created/Modified

- `sidecars/http-proxy/httpproxy/anthropic.go` — `ExtractAnthropicTokens`, `AnthropicBlockedResponse`, `staticAnthropicRates`, `StaticAnthropicRates()`
- `sidecars/http-proxy/httpproxy/anthropic_test.go` — 8 unit tests covering all BUDG-10 behaviors
- `sidecars/http-proxy/httpproxy/proxy.go` — `anthropicHostRegex` var + Anthropic MITM block

## Decisions Made

- **Model ID from response body:** Anthropic does not encode model in URL (unlike Bedrock's `/model/{id}/invoke`). Extracted from `message_start.message.model` for SSE or top-level `"model"` for non-streaming JSON — no request body buffering needed.
- **Separate `AnthropicBlockedResponse`:** Thin wrapper delegating to the same `blockedResponseBody` struct. Keeps Bedrock and Anthropic 403 shapes identical while enabling Anthropic-specific naming in code.
- **Static rate table used directly in handler closure:** `WithBudgetEnforcement` carries Bedrock-only rates; Anthropic rates live in `staticAnthropicRates` which the closure captures directly. No API change required.
- **Prompt cache tokens not metered:** `cache_creation_input_tokens` and `cache_read_input_tokens` are excluded from metering. Conservative undercount; documented in `anthropic.go` comment. Future improvement.

## Deviations from Plan

None - plan executed exactly as written.

One structural issue was encountered during proxy.go editing (closing brace for `if cfg.budget != nil` placement) and corrected inline before committing — not a plan deviation, just an edit mechanics issue resolved immediately.

## Issues Encountered

During proxy.go modification, the Anthropic MITM block was accidentally placed outside the `if cfg.budget != nil` guard. Detected via `go build` and corrected before the task commit. Final code has the correct structure: Anthropic block inside the guard, closing `}` after both Bedrock and Anthropic sections.

## User Setup Required

None - no external service configuration required. The proxy CA trust for `api.anthropic.com` is already covered by the sandbox-wide proxy CA injection used for Bedrock MITM.

## Next Phase Readiness

- BUDG-10 complete: Claude Code sessions routed through the http-proxy sidecar will have Anthropic API spend tracked, budget-enforced, and displayed in `km status`
- Prompt cache token metering (`cache_creation_input_tokens`, `cache_read_input_tokens`) is a known gap — tracked for future improvement

---
*Phase: 20-anthropic-api-metering-claude-code-ai-spend-tracking*
*Completed: 2026-03-25*

## Self-Check: PASSED

- anthropic.go: FOUND
- anthropic_test.go: FOUND
- 20-01-SUMMARY.md: FOUND
- Commit c3cfbf2 (RED): FOUND
- Commit 0225db8 (GREEN): FOUND

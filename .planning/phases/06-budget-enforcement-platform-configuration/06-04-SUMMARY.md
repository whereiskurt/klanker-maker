---
phase: 06-budget-enforcement-platform-configuration
plan: "04"
subsystem: sidecars/http-proxy
tags: [bedrock, mitm, budget-enforcement, tokens, dynamodb, proxy]
dependency_graph:
  requires: ["06-02"]
  provides: ["BUDG-04"]
  affects: ["sidecars/http-proxy/httpproxy"]
tech_stack:
  added: []
  patterns:
    - "AlwaysMitm for bedrock-runtime hosts via goproxy first-match CONNECT handlers"
    - "Functional options (ProxyOption / WithBudgetEnforcement) for backward-compatible proxy extension"
    - "Fire-and-forget goroutine for DynamoDB IncrementAISpend (non-blocking response path)"
    - "Optimistic local spend cache (budgetCache) — 10s TTL between DynamoDB reads"
    - "TDD: failing tests committed first, then implementation to pass"
key_files:
  created:
    - sidecars/http-proxy/httpproxy/bedrock.go
    - sidecars/http-proxy/httpproxy/bedrock_test.go
    - sidecars/http-proxy/httpproxy/budget_cache.go
  modified:
    - sidecars/http-proxy/httpproxy/proxy.go
    - sidecars/http-proxy/main.go
decisions:
  - "BedrockBlockedResponse uses goproxy.NewResponse with application/json content type — client receives parseable JSON even for MITM-intercepted HTTPS responses"
  - "AlwaysMitm handler registered before general OnRequest HandleConnect so goproxy first-match routes Bedrock CONNECT through MITM, all other CONNECT through OkConnect"
  - "IncrementAISpend called in fire-and-forget goroutine — response is never held pending DynamoDB; DynamoDB errors are logged but do not fail the request"
  - "budgetCache.UpdateLocalSpend called synchronously before goroutine launch so immediate follow-on requests see optimistic spend increment"
  - "Custom CA (KM_PROXY_CA_CERT) deferred as TODO — compiler user-data handles CA injection; goproxy built-in CA is sufficient for proof-of-concept"
metrics:
  duration: "218s"
  completed_date: "2026-03-22"
  tasks_completed: 2
  files_changed: 5
---

# Phase 06 Plan 04: Bedrock MITM AI Token Budget Enforcement Summary

**One-liner:** HTTP proxy MITM for bedrock-runtime SSE responses — extracts input/output tokens, prices via model rate table, atomically increments DynamoDB spend, and returns 403 JSON when AI budget is exhausted.

## Tasks Completed

| # | Task | Commit | Files |
|---|------|--------|-------|
| 1 (RED) | Add failing Bedrock tests | 6e14d40 | bedrock_test.go |
| 1 (GREEN) | Bedrock SSE parser + budget cache + 403 response | d291f15 | bedrock.go, budget_cache.go |
| 2 | Wire MITM interception into proxy.go + main.go | a839e54 | proxy.go, main.go |

## What Was Built

### bedrock.go
- `ExtractBedrockTokens(io.Reader) (int, int, error)` — reads SSE events (`message_start` for input_tokens, `message_delta` for final output_tokens) and non-streaming JSON bodies; 10MB cap with best-effort partial return
- `CalculateCost(inputTokens, outputTokens int, inputRate, outputRate float64) float64` — `(in * rate/1K) + (out * rate/1K)`
- `ExtractModelID(urlPath string) string` — regex on `/model/{id}/invoke` and `/model/{id}/invoke-with-response-stream`
- `BedrockBlockedResponse(req, sandboxID, modelID, spent, limit) *http.Response` — goproxy 403 with `{"error":"ai_budget_exhausted","spent":...,"limit":...,"model":...,"topUp":"km budget add ..."}`
- `AIBudgetExhaustedError` — structured error type

### budget_cache.go
- `BudgetEntry` struct: `ComputeLimit`, `AILimit`, `AISpent`, `fetchedAt`
- `budgetCache` with `sync.Mutex`, 10s default TTL
- `NewBudgetCache()` / `NewBudgetCacheWithTTL(ttl)` for test isolation
- `Get(sandboxID)` — returns copy within TTL, nil on miss/expiry
- `Set(sandboxID, entry)` — resets TTL clock
- `UpdateLocalSpend(sandboxID, cost)` — optimistic increment before DynamoDB round-trip

### proxy.go (extended)
- `ProxyOption` / `proxyConfig` / `WithBudgetEnforcement(client, tableName, modelRates, onBudgetUpdate)` — functional options
- `BudgetUpdater` callback type for writing `/run/km/budget_remaining`
- Pre-flight `OnRequest` handler: rejects Bedrock requests when cache shows budget exhausted (no DynamoDB read)
- `AlwaysMitm` CONNECT handler registered before general `OkConnect` (goproxy first-match ordering)
- `OnResponse` handler: reads body, extracts tokens, calculates cost, updates cache, fires DynamoDB goroutine, rebuilds `resp.Body` so client gets full response
- Non-Bedrock HTTPS traffic: unaffected OkConnect passthrough

### main.go (extended)
- Reads `KM_BUDGET_ENABLED`, `KM_BUDGET_TABLE`, `SANDBOX_ID`
- Loads AWS config + DynamoDB client when `KM_BUDGET_ENABLED=true`
- Falls back to static model rates via `GetBedrockModelRates(ctx, nil)`
- `onBudgetUpdate` writes to `/run/km/budget_remaining`
- TODO comment for `KM_PROXY_CA_CERT` custom CA support

## Deviations from Plan

None — plan executed exactly as written.

## Test Results

```
--- PASS: TestExtractBedrockTokens_SSEStream
--- PASS: TestExtractBedrockTokens_NonStreaming
--- PASS: TestExtractBedrockTokens_EmptyBody
--- PASS: TestBudgetCache_HitWithinTTL
--- PASS: TestBudgetCache_MissAfterTTL
--- PASS: TestBudgetCache_UpdateLocalSpend
--- PASS: TestBlockedResponse
--- PASS: TestCalculateCost/claude_sonnet
--- PASS: TestCalculateCost/zero_tokens
--- PASS: TestCalculateCost/haiku_small_request
--- PASS: TestExtractModelID (5 subtests)
--- PASS: TestHTTPProxy_AllowedHost
--- PASS: TestHTTPProxy_BlockedHost
--- PASS: TestHTTPProxy_AllowedWithPort
--- PASS: TestHTTPProxy_TraceparentInjected
--- PASS: TestIsHostAllowed (7 subtests)
PASS
ok  github.com/whereiskurt/klankrmkr/sidecars/http-proxy/httpproxy  0.373s
```

## Self-Check: PASSED

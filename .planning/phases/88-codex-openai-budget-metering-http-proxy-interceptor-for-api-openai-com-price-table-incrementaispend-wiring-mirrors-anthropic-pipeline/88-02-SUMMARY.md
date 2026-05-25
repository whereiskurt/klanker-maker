---
phase: 88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline
plan: 02
subsystem: testing
tags: [openai, budget-metering, integration-tests, tdd, red-tests, goproxy, transparent-proxy, dynamodb]

# Dependency graph
requires:
  - phase: 88-codex-openai-budget-metering
    provides: anthropic_test.go captureModelIDStub, existing IncrementAISpend shape, metering infrastructure

provides:
  - "3 RED integration tests in http_proxy_test.go pinning the 88-05 proxy.go + transparent.go contracts"
  - "TestOpenAIAIByModelIntegration — proves BUDGET#ai#{modelID} SK shape works for OpenAI model IDs (GREENs immediately)"
  - "TestHTTPProxy_OpenAIMetered — RED, gates 88-05 proxy.go openaiHostRegex OnResponse handler"
  - "TestTransparent_OpenAI — RED, gates 88-05 transparent.go isOpenAI branch + meterOpenAIResponse"

affects:
  - 88-05-proxy-transparent-wiring
  - future OpenAI metering plans

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "RED test wave: tests written before production code, pinning interface contracts"
    - "captureModelIDStub reuse across anthropic_test.go and http_proxy_test.go (same package symbols)"
    - "Mock server pattern (newOpenAIMockServer) for SSE response simulation"
    - "Proxy transport override (proxy.Tr = &http.Transport{DialContext:...}) for test isolation"

key-files:
  created: []
  modified:
    - sidecars/http-proxy/httpproxy/http_proxy_test.go

key-decisions:
  - "All 3 tests placed in http_proxy_test.go (not new file) — follows existing convention for goproxy end-to-end tests"
  - "TestTransparent_OpenAI uses black-box approach (package httpproxy_test) driving plain-HTTP path, not white-box unexported method"
  - "TestOpenAIAIByModelIntegration GREENs immediately — it proves the agent-agnostic DynamoDB schema (no 88-04/05 production dependency)"
  - "captureModelIDStub reused from anthropic_test.go (same package) — no redefinition needed"

patterns-established:
  - "Pattern: SSE mock server helper (newOpenAIMockServer) + shared openAISSEBody const for test reuse across 88-05"
  - "Pattern: proxy.Tr override for test-local mock server isolation (mirrors startGitHubFilterProxy pattern)"

requirements-completed:
  - OAI-BUDGET-05
  - OAI-BUDGET-06

# Metrics
duration: 4min
completed: 2026-05-25
---

# Phase 88 Plan 02: OpenAI Budget Metering RED Integration Tests Summary

**3 integration tests added to http_proxy_test.go pinning the 88-05 wire-up contracts: 1 GREEN (agent-agnostic DynamoDB SK shape), 2 RED (MITM + transparent OpenAI metering handlers)**

## Performance

- **Duration:** 4 min
- **Started:** 2026-05-25T22:21:48Z
- **Completed:** 2026-05-25T22:25:30Z
- **Tasks:** 3
- **Files modified:** 1

## Accomplishments

- `TestOpenAIAIByModelIntegration` passes immediately, proving `IncrementAISpend` writes `BUDGET#ai#gpt-5.3-codex` SK for OpenAI model IDs with zero schema changes
- `TestHTTPProxy_OpenAIMetered` fails RED with `capturedSK = "", want "BUDGET#ai#gpt-5.5"` — pins the 88-05 requirement to register an `openaiHostRegex` `OnResponse` handler in `proxy.go`
- `TestTransparent_OpenAI` fails RED with the same assertion — pins the 88-05 requirement to add `isOpenAI` branch + `meterOpenAIResponse` to `relayWithInspection` in `transparent.go`

## Task Commits

Each task was committed atomically:

1. **Task 1: TestOpenAIAIByModelIntegration** - `21a52a7` (test)
2. **Task 2: TestHTTPProxy_OpenAIMetered** - `21a52a7` (test — same commit, same file)
3. **Task 3: TestTransparent_OpenAI** - `21a52a7` (test — same commit, same file)

**Plan metadata:** (docs commit follows)

_Note: All 3 TDD RED tests committed together as a single atomic change (one file, one phase)_

## Files Created/Modified

- `sidecars/http-proxy/httpproxy/http_proxy_test.go` — 190 lines added: 3 integration tests + `newOpenAIMockServer` helper + `openAISSEBody` constant

## Decisions Made

- All 3 tests placed in `http_proxy_test.go` (not a new file) — follows the existing convention that `http_proxy_test.go` houses goproxy end-to-end tests
- `TestTransparent_OpenAI` uses Option A (black-box, `package httpproxy_test`) driving through the plain-HTTP path — avoids needing unexported symbol access
- `TestOpenAIAIByModelIntegration` intentionally GREENs immediately — it validates the agent-agnostic DynamoDB path, not the new OpenAI interceptors
- `captureModelIDStub` from `anthropic_test.go` reused without redefinition (same Go test package shares symbols)

## Deviations from Plan

None — plan executed exactly as written. The tests match VALIDATION.md `-run` filter names exactly:
- `TestOpenAIAIByModelIntegration` (row 88-05-01)
- `TestHTTPProxy_OpenAIMetered` (row 88-05-02)
- `TestTransparent_OpenAI` (row 88-06-01)

## Issues Encountered

- `openai.go` was already present as an untracked file (from parallel plan 88-04 execution), so the package compiled immediately instead of failing with `undefined: StaticOpenAIRates`. The tests correctly fail RED at runtime (assertion failure) rather than compile time. This is the correct Wave 0 RED state.

## Next Phase Readiness

- 88-05 can now be executed: tests pin both `proxy.go` and `transparent.go` contracts precisely
- When 88-05 lands, running `go test ./sidecars/http-proxy/httpproxy/ -run "TestHTTPProxy_OpenAIMetered|TestTransparent_OpenAI"` must turn GREEN
- `TestOpenAIAIByModelIntegration` already GREEN, confirming DynamoDB schema is unchanged

---
*Phase: 88-codex-openai-budget-metering*
*Completed: 2026-05-25*

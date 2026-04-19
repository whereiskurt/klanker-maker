---
phase: quick
plan: 4
type: execute
wave: 1
depends_on: []
files_modified:
  - sidecars/http-proxy/httpproxy/anthropic.go
  - sidecars/http-proxy/httpproxy/anthropic_test.go
  - sidecars/http-proxy/httpproxy/proxy.go
  - sidecars/http-proxy/httpproxy/transparent.go
autonomous: true
requirements: [CACHE-METERING]
must_haves:
  truths:
    - "Cache creation and cache read tokens are extracted from Anthropic SSE and non-streaming responses"
    - "Cost calculation includes cache token costs using 1.25x (write) and 0.1x (read) of input rate"
    - "Proxy and transparent listener log cache token counts and use accurate cost"
    - "Zero cache tokens produces identical cost to existing CalculateCost"
  artifacts:
    - path: "sidecars/http-proxy/httpproxy/anthropic.go"
      provides: "ExtractAnthropicTokens with 6-value return, CalculateAnthropicCost function"
      contains: "CalculateAnthropicCost"
    - path: "sidecars/http-proxy/httpproxy/anthropic_test.go"
      provides: "Tests for cache token extraction and cost calculation"
      contains: "TestCalculateAnthropicCost"
  key_links:
    - from: "sidecars/http-proxy/httpproxy/proxy.go"
      to: "ExtractAnthropicTokens"
      via: "6-value return destructuring"
      pattern: "cacheReadTokens, cacheWriteTokens"
    - from: "sidecars/http-proxy/httpproxy/transparent.go"
      to: "ExtractAnthropicTokens"
      via: "6-value return destructuring"
      pattern: "cacheReadTokens, cacheWriteTokens"
---

<objective>
Add cache token metering to the HTTP proxy's Anthropic API cost calculation so that
`cache_creation_input_tokens` and `cache_read_input_tokens` are included in budget spend.
Currently the proxy undercounts by ~6x when Claude Code uses prompt caching.

Purpose: Accurate AI budget enforcement and spend tracking for sandboxes using Anthropic direct API.
Output: Updated anthropic.go with cache-aware extraction and cost calculation, updated call sites in proxy.go and transparent.go, comprehensive tests.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@sidecars/http-proxy/httpproxy/anthropic.go
@sidecars/http-proxy/httpproxy/anthropic_test.go
@sidecars/http-proxy/httpproxy/proxy.go (lines 350-400)
@sidecars/http-proxy/httpproxy/transparent.go (lines 403-430)
@sidecars/http-proxy/httpproxy/bedrock.go (lines 270-274 for CalculateCost reference)
@docs/superpowers/specs/2026-04-19-anthropic-cache-token-metering-design.md

<interfaces>
<!-- Existing function that must NOT change (Bedrock path still uses it): -->
From sidecars/http-proxy/httpproxy/bedrock.go:
```go
func CalculateCost(inputTokens, outputTokens int, inputPricePer1K, outputPricePer1K float64) float64
```

From pkg/aws/pricing.go (used but NOT modified):
```go
type BedrockModelRate struct {
    InputPricePer1KTokens  float64
    OutputPricePer1KTokens float64
}
```

From pkg/aws/budget.go (called but NOT modified):
```go
func IncrementAISpend(ctx context.Context, client BudgetAPI, tableName, sandboxID, modelID string, inputTokens, outputTokens int, costUSD float64) (float64, error)
```
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Add cache token extraction and cost calculation to anthropic.go</name>
  <files>sidecars/http-proxy/httpproxy/anthropic.go, sidecars/http-proxy/httpproxy/anthropic_test.go</files>
  <behavior>
    - TestExtractAnthropicTokens_SSEStreamWithCache: SSE body with cache_creation_input_tokens=500 and cache_read_input_tokens=2000 in message_start.message.usage returns those values as cacheWriteTokens and cacheReadTokens respectively
    - TestExtractAnthropicTokens_NonStreamingWithCache: Non-streaming JSON with cache fields returns them correctly
    - TestExtractAnthropicTokens_NoCacheTokens: Existing SSE/non-streaming without cache fields returns 0 for both cache values (backward compat)
    - TestCalculateAnthropicCost_WithCache: 1000 input + 500 output + 2000 cacheRead + 500 cacheWrite on claude-sonnet-4-6 ($0.003/$0.015) = input(0.003) + output(0.0075) + cacheRead(2000*0.0003/1000=0.0006) + cacheWrite(500*0.00375/1000=0.001875) = 0.012975
    - TestCalculateAnthropicCost_ZeroCache: zero cache tokens produces same result as CalculateCost(input, output, inputRate, outputRate)
    - All existing tests updated for 6-value return from ExtractAnthropicTokens (add two _ placeholders for tests that don't check cache values)
  </behavior>
  <action>
1. In anthropic.go, add `CacheCreationInputTokens int json:"cache_creation_input_tokens"` and `CacheReadInputTokens int json:"cache_read_input_tokens"` to both the `anthropicMessageStartPayload.Message.Usage` struct and the `anthropicNonStreamingPayload.Usage` struct.

2. Change `ExtractAnthropicTokens` signature from `(modelID string, inputTokens, outputTokens int, err error)` to `(modelID string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int, err error)`. Update all return statements:
   - Empty body: `return "", 0, 0, 0, 0, nil`
   - SSE message_start: also capture `payload.Message.Usage.CacheCreationInputTokens` and `payload.Message.Usage.CacheReadInputTokens` into local vars
   - SSE return: `return modelID, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, nil`
   - Non-streaming return: include cache fields from payload
   - Unparseable: `return "", 0, 0, 0, 0, nil`

3. Add new function:
```go
// CalculateAnthropicCost returns USD cost including cache token pricing.
// Cache read = 0.1x input rate, cache write = 1.25x input rate.
func CalculateAnthropicCost(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int, rate aws.BedrockModelRate) float64 {
    inputCost := float64(inputTokens) * rate.InputPricePer1KTokens / 1000.0
    outputCost := float64(outputTokens) * rate.OutputPricePer1KTokens / 1000.0
    cacheReadCost := float64(cacheReadTokens) * (rate.InputPricePer1KTokens * 0.1) / 1000.0
    cacheWriteCost := float64(cacheWriteTokens) * (rate.InputPricePer1KTokens * 1.25) / 1000.0
    return inputCost + outputCost + cacheReadCost + cacheWriteCost
}
```

4. Remove the "known gap" comment block at lines 9-12 (the "Note: cache_creation_input_tokens..." paragraph).

5. In anthropic_test.go, update ALL existing ExtractAnthropicTokens call sites to destructure 6 values (use `_` for the two new cache values where not being tested). Specifically:
   - TestExtractAnthropicTokens_NonStreaming (line 19): `modelID, inputTokens, outputTokens, _, _, err`
   - TestExtractAnthropicTokens_SSEStream (line 51): `modelID, inputTokens, outputTokens, _, _, err`
   - TestExtractAnthropicTokens_EmptyBody (line 69): `modelID, inputTokens, outputTokens, _, _, err`
   - TestExtractAnthropicTokens_ModelIDFromSSE (line 95): `modelID, _, _, _, _, err`

6. Add the new test functions described in the behavior block.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./sidecars/http-proxy/httpproxy/ -run "TestExtractAnthropicTokens|TestCalculateAnthropicCost" -v</automated>
  </verify>
  <done>ExtractAnthropicTokens returns 6 values including cache tokens. CalculateAnthropicCost correctly prices cache tokens. All existing tests pass with updated signatures. New cache-specific tests pass.</done>
</task>

<task type="auto">
  <name>Task 2: Update proxy.go and transparent.go call sites to use cache-aware metering</name>
  <files>sidecars/http-proxy/httpproxy/proxy.go, sidecars/http-proxy/httpproxy/transparent.go</files>
  <action>
1. In proxy.go around line 366, update the metering callback:
   - Change destructuring from `modelID, inputTokens, outputTokens, parseErr` to `modelID, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, parseErr`
   - Replace cost calculation block (lines 371-374):
     ```go
     var costUSD float64
     if rate, ok := staticAnthropicRates[modelID]; ok {
         costUSD = CalculateAnthropicCost(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, rate)
     }
     ```
   - Add two new fields to the log line (after `output_tokens`):
     ```go
     Int("cache_read_tokens", cacheReadTokens).
     Int("cache_write_tokens", cacheWriteTokens).
     ```

2. In transparent.go meterAnthropicResponse (around line 408), make the same three changes:
   - Update destructuring to 6 values
   - Replace CalculateCost with CalculateAnthropicCost(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, rate)
   - Add cache_read_tokens and cache_write_tokens to the log line
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go build ./sidecars/http-proxy/... && go vet ./sidecars/http-proxy/...</automated>
  </verify>
  <done>Both proxy.go and transparent.go use CalculateAnthropicCost with cache tokens. Log lines include cache_read_tokens and cache_write_tokens. Project compiles cleanly with no vet warnings.</done>
</task>

</tasks>

<verification>
```bash
cd /Users/khundeck/working/klankrmkr && go test ./sidecars/http-proxy/httpproxy/ -v -count=1
```
All tests pass including new cache token tests. No compilation errors across the sidecar package.
</verification>

<success_criteria>
- ExtractAnthropicTokens returns 6 values (model, input, output, cacheRead, cacheWrite, err)
- CalculateAnthropicCost exists and applies 0.1x/1.25x multipliers to input rate
- proxy.go and transparent.go call CalculateAnthropicCost instead of CalculateCost for Anthropic
- Log lines include cache_read_tokens and cache_write_tokens
- All existing tests updated and passing
- New tests cover: SSE with cache, non-streaming with cache, cost calc with cache, zero-cache equivalence
- "Known gap" comment removed from anthropic.go header
</success_criteria>

<output>
After completion, create `.planning/quick/4-add-cache-token-metering-to-http-proxy-a/4-SUMMARY.md`
</output>

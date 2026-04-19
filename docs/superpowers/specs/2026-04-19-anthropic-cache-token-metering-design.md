# Anthropic Cache Token Metering

**Date:** 2026-04-19
**Status:** Approved

## Problem

The HTTP proxy's Anthropic API metering only counts `input_tokens` and `output_tokens` from responses. It ignores `cache_creation_input_tokens` and `cache_read_input_tokens`, which are significant when Claude Code uses prompt caching (which it does automatically).

This causes `km status` budget to show ~6x less AI spend than actual. Example: $1.77 budget vs $11.10 OTEL for the same session with 160K+ cache read tokens per turn.

The gap was documented as a known conservative undercount in Phase 20 research and in `anthropic.go` header comments.

## Solution: Option B — Accurate Cost, Minimal Surface Area

Fold cache token costs into the existing `costUSD` passed to `IncrementAISpend`. No changes to DynamoDB schema, `budget.go`, or `BedrockModelRate`.

Cache pricing uses standard Anthropic multipliers of the input price (consistent across all models):
- Cache write: 1.25x input price (5-min TTL)
- Cache read: 0.1x input price

Source: [Anthropic Pricing](https://docs.anthropic.com/en/docs/about-claude/pricing)

## Changes

### `sidecars/http-proxy/httpproxy/anthropic.go`

1. **Payload structs** — Add `CacheCreationInputTokens` and `CacheReadInputTokens` fields to both `anthropicMessageStartPayload.Message.Usage` and `anthropicNonStreamingPayload.Usage`.

2. **`ExtractAnthropicTokens` return signature** — Change from:
   ```go
   func ExtractAnthropicTokens(body io.Reader) (modelID string, inputTokens, outputTokens int, err error)
   ```
   To:
   ```go
   func ExtractAnthropicTokens(body io.Reader) (modelID string, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int, err error)
   ```
   Parse cache fields from `message_start` (SSE) and non-streaming payloads.

3. **New `CalculateAnthropicCost` function**:
   ```go
   func CalculateAnthropicCost(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int, rate BedrockModelRate) float64
   ```
   Computes:
   ```
   cost = input * inputRate/1K
        + output * outputRate/1K
        + cacheRead * (inputRate * 0.1)/1K
        + cacheWrite * (inputRate * 1.25)/1K
   ```
   Uses `aws.BedrockModelRate` (existing type, no changes needed).

4. **Remove known-gap comment** from file header (lines 9-12).

### `sidecars/http-proxy/httpproxy/proxy.go` (~line 366)

Update the Anthropic metering callback:
- Receive `cacheReadTokens, cacheWriteTokens` from `ExtractAnthropicTokens`
- Call `CalculateAnthropicCost` instead of `CalculateCost`
- Add `cache_read_tokens` and `cache_write_tokens` to the log line

### `sidecars/http-proxy/httpproxy/transparent.go` (~line 408)

Same changes as `proxy.go` for the transparent listener path.

### `sidecars/http-proxy/httpproxy/anthropic_test.go`

- Update all existing `ExtractAnthropicTokens` call sites for new 6-value return
- Add test: SSE stream with cache tokens in `message_start.message.usage`
- Add test: non-streaming response with cache tokens
- Add `TestCalculateAnthropicCost` with known values
- Add test: zero cache tokens produces same result as `CalculateCost`

## Not Changed

- `pkg/aws/budget.go` — `IncrementAISpend` signature unchanged; receives accurate `costUSD`
- `pkg/aws/pricing.go` — `BedrockModelRate` struct unchanged; cache rates derived from input price
- `sidecars/http-proxy/httpproxy/bedrock.go` — `CalculateCost` and Bedrock path unchanged (separate concern)
- DynamoDB schema — no new columns; `spentUSD` is now accurate
- `km status` display — shows accurate total; no per-cache-token breakdown (future work)

## Pricing Reference

| Model | Input/1K | Output/1K | Cache Write/1K | Cache Read/1K |
|-------|----------|-----------|----------------|---------------|
| claude-opus-4-6 | $0.005 | $0.025 | $0.00625 | $0.0005 |
| claude-sonnet-4-6 | $0.003 | $0.015 | $0.00375 | $0.0003 |
| claude-haiku-4-5 | $0.001 | $0.005 | $0.00125 | $0.0001 |

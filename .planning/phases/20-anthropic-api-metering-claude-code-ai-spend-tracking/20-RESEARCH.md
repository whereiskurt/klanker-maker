# Phase 20: Anthropic API Metering — Research

**Researched:** 2026-03-24
**Domain:** Go http-proxy MITM extension — Anthropic API response parsing and budget enforcement
**Confidence:** HIGH

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| BUDG-10 | AI/token spend tracked for Anthropic API (Claude Code) calls via `api.anthropic.com`; http-proxy sidecar intercepts `POST /v1/messages` responses (both non-streaming and SSE streaming), extracts `usage.input_tokens`/`usage.output_tokens`, prices against Anthropic's published model rates, and increments DynamoDB budget record using the same `IncrementAISpend` path as Bedrock metering | New `anthropic.go` file mirrors `bedrock.go`. New hostname regex + MITM handler in `proxy.go` mirrors existing Bedrock pattern. Static rate table for all Claude 4.x API model IDs. Re-use `ExtractBedrockTokens`, `CalculateCost`, `IncrementAISpend`, `BudgetCache`, `BedrockBlockedResponse` (or a renamed Anthropic-specific variant). |
</phase_requirements>

---

## Summary

Phase 20 extends the http-proxy sidecar to meter Anthropic direct API calls (`api.anthropic.com/v1/messages`) — the transport Claude Code uses when running against the Anthropic 1P endpoint rather than Bedrock. The Bedrock metering path (Phase 6 / BUDG-04) is a complete, working template. Phase 20 is primarily a parallel instantiation of that template for a different hostname and URL pattern, with a new static model rate table.

The SSE streaming format is effectively identical between Bedrock (Anthropic model) and the Anthropic 1P API. The `message_start` event carries `message.usage.input_tokens`; the `message_delta` event carries `usage.output_tokens` (cumulative). Non-streaming responses carry `usage.input_tokens` and `usage.output_tokens` at top level. The existing `ExtractBedrockTokens` function in `bedrock.go` already handles exactly this format. The token extraction logic can be shared directly (the function is not Bedrock-specific in implementation, only in naming).

Model ID extraction differs: Bedrock uses `/model/{model-id}/invoke`; Anthropic API uses a `model` field in the JSON request body. However, since the proxy operates on the response, not the request, the model is also present in the non-streaming response body (`"model": "claude-opus-4-6"`) and in the `message_start` SSE event (`"message": {"model": "..."}`). This avoids needing to buffer/parse the request body.

**Primary recommendation:** Add `sidecars/http-proxy/httpproxy/anthropic.go` that mirrors `bedrock.go` structure. Add an `anthropicHostRegex` and a second MITM block in `proxy.go` inside the existing `if cfg.budget != nil` guard. Wire a static Anthropic model rate table (6 entries for current Claude 4 API IDs). Share `CalculateCost`, `IncrementAISpend`, and `budgetCache` unchanged.

---

## Standard Stack

### Core (no new dependencies — all already in go.mod)

| Component | Location | Purpose |
|-----------|----------|---------|
| `github.com/elazarl/goproxy` v1.8.2 | `go.mod` | MITM proxy; `AlwaysMitm` + `OnResponse` already proven for Bedrock |
| `encoding/json` stdlib | — | Parse response body for model ID and token counts |
| `bufio` / `strings` stdlib | — | SSE line scanner (identical to Bedrock path) |
| `pkg/aws.IncrementAISpend` | `pkg/aws/budget.go` | Atomic DynamoDB spend increment — reused unchanged |
| `pkg/aws.BudgetAPI` | `pkg/aws/budget.go` | DynamoDB interface — reused unchanged |
| `sidecars/http-proxy/httpproxy.budgetCache` | `budget_cache.go` | In-process 10s TTL cache — reused unchanged |

No new Go modules required. No new AWS services required.

---

## Architecture Patterns

### Recommended File Structure

```
sidecars/http-proxy/httpproxy/
├── proxy.go          # add anthropicHostRegex + second MITM block
├── bedrock.go        # unchanged
├── bedrock_test.go   # unchanged
├── anthropic.go      # NEW: ExtractAnthropicTokens, ExtractAnthropicModelID,
│                     #      AnthropicBlockedResponse, staticAnthropicRates
├── anthropic_test.go # NEW: unit tests for streaming + non-streaming parsing
└── budget_cache.go   # unchanged
```

### Pattern 1: Anthropic Hostname Regex + MITM Block in proxy.go

**What:** Mirror the Bedrock block. Add a new regex for `api.anthropic.com`, register `AlwaysMitm`, add `OnRequest` preflight, add `OnResponse` handler.

**When to use:** Any outbound HTTPS to `api.anthropic.com`.

**Confirmed SSE wire format (from official docs):**

```
event: message_start
data: {"type":"message_start","message":{"id":"msg_...","model":"claude-opus-4-6","usage":{"input_tokens":25,"output_tokens":1},...}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}
```

Key observation: `message_delta.usage.output_tokens` is **cumulative** (confirmed by official docs warning). The existing `ExtractBedrockTokens` function already reads `message_start.message.usage.input_tokens` and `message_delta.usage.output_tokens` — this is identical to the Anthropic 1P format.

**Confirmed non-streaming format:**
```json
{
  "id": "msg_...",
  "model": "claude-opus-4-6",
  "usage": {
    "input_tokens": 100,
    "output_tokens": 50
  }
}
```

The existing `nonStreamingPayload` struct in `bedrock.go` maps exactly to this — same field names.

### Pattern 2: Model ID Extraction from Response Body

**Problem:** Bedrock encodes model ID in the URL path (`/model/{id}/invoke`). Anthropic API does not — it is in the request body. However, the proxy intercepts the **response**, not the request.

**Solution:** Extract model from response body rather than URL.

For non-streaming responses, `"model"` is a top-level field in the JSON body.

For SSE streaming, `"model"` appears in the `message_start` event's message object.

**Add to `anthropic.go`:**

```go
// anthropicMessageStartPayload peeks at model + usage from message_start.
type anthropicMessageStartPayload struct {
    Type    string `json:"type"`
    Message struct {
        Model string `json:"model"`
        Usage struct {
            InputTokens  int `json:"input_tokens"`
            OutputTokens int `json:"output_tokens"`
        } `json:"usage"`
    } `json:"message"`
}

// anthropicNonStreamingPayload reads model + usage from plain JSON responses.
type anthropicNonStreamingPayload struct {
    Model string `json:"model"`
    Usage struct {
        InputTokens  int `json:"input_tokens"`
        OutputTokens int `json:"output_tokens"`
    } `json:"usage"`
}

// ExtractAnthropicTokens returns (modelID, inputTokens, outputTokens, err).
// Handles both SSE streaming and non-streaming Anthropic API responses.
func ExtractAnthropicTokens(body io.Reader) (modelID string, inputTokens, outputTokens int, err error)
```

**Design note:** This is the only structural difference from the Bedrock path. The return signature adds `modelID` since there is no URL to parse.

### Pattern 3: Anthropic Model Rate Table

**No AWS Pricing API for Anthropic 1P.** Rates are static, sourced from official Anthropic pricing page. Add `staticAnthropicRates` in `anthropic.go`.

**Verified rates as of 2026-03-24 (source: platform.claude.com/docs/en/about-claude/pricing):**

| API Model ID | Input $/MTok | Output $/MTok | Per-1K input | Per-1K output |
|---|---|---|---|---|
| `claude-opus-4-6` | $5 | $25 | $0.005 | $0.025 |
| `claude-opus-4-5-20251101` | $5 | $25 | $0.005 | $0.025 |
| `claude-sonnet-4-6` | $3 | $15 | $0.003 | $0.015 |
| `claude-sonnet-4-20250514` | $3 | $15 | $0.003 | $0.015 |
| `claude-haiku-4-5-20251001` | $1 | $5 | $0.001 | $0.005 |
| `claude-haiku-4-5` | $1 | $5 | $0.001 | $0.005 |

Note: `claude-opus-4-1-20250805` ($15/$75) and earlier Opus 4 variants should also be included for completeness. Claude Code currently defaults to `claude-opus-4-6` (Max/Team Premium) or `claude-sonnet-4-6` (Pro/Team Standard). Haiku is used for background tasks.

**Legacy models to include (still accessible via API):**
- `claude-opus-4-1-20250805`: $0.015 / $0.075 per 1K
- `claude-opus-4-20250514`: $0.015 / $0.075 per 1K
- `claude-sonnet-4-5-20250929`: $0.003 / $0.015 per 1K
- `claude-haiku-3-5-20241022` (Haiku 3.5): $0.0008 / $0.004 per 1K
- `claude-3-haiku-20240307` (deprecated, retires 2026-04-19): $0.00025 / $0.00125 per 1K

### Anti-Patterns to Avoid

- **Buffering/parsing the request body to extract model ID:** The proxy would need to buffer, parse, re-emit. Complex, fragile. Extract model from response body instead.
- **Sharing `BedrockBlockedResponse` function name:** The function is generic enough to reuse. If renamed, use `AIBudgetBlockedResponse` to avoid confusion. However, the simplest approach is a thin `AnthropicBlockedResponse` wrapper that delegates to the same logic (same JSON shape, same 403).
- **Separate `budgetCache` instance for Anthropic:** The sandbox has one AI budget that covers both Bedrock and Anthropic spend. Use the same `budgetCache` instance shared across both handler closures.
- **Registering the Anthropic MITM handler after the general CONNECT handler:** goproxy uses first-match for CONNECT. The Anthropic `AlwaysMitm` MUST be registered before `OkConnect` (exactly like Bedrock). This is already the case in `proxy.go` — the new handler must go inside the `if cfg.budget != nil` block alongside the Bedrock handlers.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| SSE line parsing | Custom tokenizer | `bufio.Scanner` + string prefix check — already in `bedrock.go` |
| Atomic spend increment | CAS loop | `aws.IncrementAISpend` with DynamoDB `ADD` expression |
| Budget caching | Manual TTL map | `budgetCache` with `NewBudgetCacheWithTTL` |
| 403 response construction | Custom HTTP response | `goproxy.NewResponse` (already used by `BedrockBlockedResponse`) |
| Model rate lookup | HTTP call to Anthropic | Static `staticAnthropicRates` map — Anthropic has no pricing API |

---

## Common Pitfalls

### Pitfall 1: goproxy Handler Registration Order

**What goes wrong:** If `anthropicHostRegex` MITM handler is registered after the general `OkConnect` handler, goproxy first-match semantics mean Anthropic CONNECT requests get `OkConnect` (passthrough), not MITM. Token extraction never fires.

**Why it happens:** The general CONNECT handler is a catch-all registered at the bottom of `NewProxy`. Bedrock MITM works because it's registered first. Anthropic must follow the same placement.

**How to avoid:** Register both `OnRequest(ReqHostMatches(anthropicHostRegex)).HandleConnect(AlwaysMitm)` calls inside the `if cfg.budget != nil` block, before the general `OnRequest().HandleConnectFunc(...)`.

**Warning signs:** Tokens are never logged in proxy output; `anthropic_tokens_metered` event never appears.

### Pitfall 2: `message_delta.usage` vs. `message_start.message.usage`

**What goes wrong:** Developer reads `message_start.message.usage.output_tokens` (which is 1, the initial allocation) instead of `message_delta.usage.output_tokens` (the final cumulative count).

**Why it happens:** Both carry `output_tokens`. The `message_start` value is a placeholder (always 1). The `message_delta` value is cumulative final.

**How to avoid:** Parse `message_delta` for output tokens, `message_start` for input tokens. This is exactly how `ExtractBedrockTokens` already works — same logic applies.

**Warning signs:** Output token counts are always 1 regardless of response length.

### Pitfall 3: Response Body Already Consumed

**What goes wrong:** The Anthropic `OnResponse` handler reads `resp.Body` but forgets to replace it with a new `io.NopCloser(bytes.NewReader(bodyBytes))`. The client receives an empty body.

**Why it happens:** `resp.Body` is an `io.ReadCloser` — reading it exhausts the stream.

**How to avoid:** Follow the exact pattern in `proxy.go`'s Bedrock handler:
```go
bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize))
_ = resp.Body.Close()
resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
```
Restore body immediately before any early return.

### Pitfall 4: Anthropic API Returns `model` Field with Alias, Not Pinned ID

**What goes wrong:** Claude Code sends `"model": "claude-sonnet-4-6"` (alias). Anthropic's response `"model"` field echoes back the resolved model name. The static rate table must include all alias forms.

**How to avoid:** Include both alias and dated IDs in `staticAnthropicRates`. Since aliases like `claude-sonnet-4-6` map to current versions without a date suffix in the API response, include both the dated variant and the alias as separate map keys pointing to the same rate.

### Pitfall 5: MITM TLS Certificate Trust

**What goes wrong:** Claude Code uses its own HTTPS transport to reach `api.anthropic.com`. The proxy must MITM this connection. If Claude Code (or the underlying Anthropic SDK) pins the certificate or uses cert pinning, MITM will fail with TLS errors.

**Why it happens:** The Anthropic Go/Python SDKs do not pin certificates (they use standard TLS). Claude Code routes through the proxy via `HTTPS_PROXY` env var or system proxy settings. The proxy's `AlwaysMitm` generates a per-hostname cert signed by the proxy's CA (goproxy default self-signed CA). Claude Code must trust this CA.

**How to avoid:** The sandbox already injects the proxy CA cert for Bedrock MITM. The same CA trust applies to `api.anthropic.com`. No additional CA configuration needed — the CA trust is proxy-wide, not host-specific. Confirm in the sidecar entrypoint that `HTTPS_PROXY` is set for Claude Code's process and that the proxy CA cert is in the container's system trust store.

**Warning signs:** TLS handshake errors in proxy logs; `anthropic_tokens_metered` never fires; client sees `x509: certificate signed by unknown authority`.

### Pitfall 6: Prompt Cache Tokens

**What goes wrong:** Anthropic API responses include `cache_creation_input_tokens` and `cache_read_input_tokens` in `usage` when prompt caching is active. These are not in `input_tokens`. If not accounted for, metered cost is lower than actual.

**Why it happens:** Claude Code uses prompt caching automatically. Cache write tokens are billed at 1.25x–2x the input rate; cache read tokens at 0.1x.

**How to avoid for v1:** Document that Phase 20 meters `input_tokens` + `output_tokens` only. Cache token metering is a known gap. The error is conservative (undercount), not an overcount. Track as a future improvement. Add a comment in `anthropic.go` near the parsing logic.

---

## Code Examples

### ExtractAnthropicTokens — Verified Pattern

```go
// Source: Anthropic API docs (platform.claude.com/docs/en/api/messages-streaming)
// SSE format confirmed. Non-streaming format confirmed via pricing page usage examples.

// ExtractAnthropicTokens reads an Anthropic API response body and returns the
// model ID, input token count, and output token count.
// Handles both SSE streaming (stream: true) and non-streaming JSON responses.
// Returns ("", 0, 0, nil) on empty or unrecognized body.
func ExtractAnthropicTokens(body io.Reader) (modelID string, inputTokens, outputTokens int, err error) {
    data, _ := io.ReadAll(io.LimitReader(body, maxResponseBodySize))
    if len(data) == 0 {
        return "", 0, 0, nil
    }

    // Try SSE first.
    hasSSE := false
    scanner := bufio.NewScanner(bytes.NewReader(data))
    for scanner.Scan() {
        line := scanner.Text()
        if !strings.HasPrefix(line, "data: ") {
            continue
        }
        jsonData := strings.TrimPrefix(line, "data: ")

        var typed struct{ Type string `json:"type"` }
        if json.Unmarshal([]byte(jsonData), &typed) != nil {
            continue
        }

        switch typed.Type {
        case "message_start":
            hasSSE = true
            var p anthropicMessageStartPayload
            if json.Unmarshal([]byte(jsonData), &p) == nil {
                modelID = p.Message.Model
                inputTokens = p.Message.Usage.InputTokens
            }
        case "message_delta":
            hasSSE = true
            var p messageDeltaPayload // reuse from bedrock.go
            if json.Unmarshal([]byte(jsonData), &p) == nil {
                outputTokens = p.Usage.OutputTokens // cumulative
            }
        }
    }
    if hasSSE {
        return modelID, inputTokens, outputTokens, nil
    }

    // Non-streaming JSON.
    var p anthropicNonStreamingPayload
    if json.Unmarshal(data, &p) == nil && (p.Usage.InputTokens > 0 || p.Usage.OutputTokens > 0) {
        return p.Model, p.Usage.InputTokens, p.Usage.OutputTokens, nil
    }
    return "", 0, 0, nil
}
```

### Anthropic MITM Block in proxy.go — Verified Pattern

```go
// Source: mirrors existing Bedrock block in proxy.go (lines 122-256)
// Must be inside if cfg.budget != nil block, BEFORE the general CONNECT handler.

var anthropicHostRegex = regexp.MustCompile(`^api\.anthropic\.com`)

// Pre-flight: reject if budget exhausted.
proxy.OnRequest(goproxy.ReqHostMatches(anthropicHostRegex)).DoFunc(
    func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
        entry := be.cache.Get(sandboxID)
        if entry != nil && entry.AILimit > 0 && entry.AISpent >= entry.AILimit {
            return req, AnthropicBlockedResponse(req, sandboxID, "", entry.AISpent, entry.AILimit)
        }
        return req, nil
    },
)

// MITM: intercept HTTPS CONNECT to api.anthropic.com.
proxy.OnRequest(goproxy.ReqHostMatches(anthropicHostRegex)).HandleConnect(goproxy.AlwaysMitm)

// OnResponse: extract tokens, price, increment DynamoDB.
proxy.OnResponse(goproxy.ReqHostMatches(anthropicHostRegex)).DoFunc(
    func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
        // ... same structure as Bedrock OnResponse handler ...
        // Uses ExtractAnthropicTokens instead of ExtractBedrockTokens.
        // Model ID comes from response body, not URL.
    },
)
```

### Static Rate Table — Verified Rates

```go
// Source: platform.claude.com/docs/en/about-claude/pricing (2026-03-24)
// Rates in USD per 1,000 tokens.
var staticAnthropicRates = map[string]BedrockModelRate{
    // Current models (Claude 4.6)
    "claude-opus-4-6":            {InputPricePer1KTokens: 0.005,   OutputPricePer1KTokens: 0.025},
    "claude-sonnet-4-6":          {InputPricePer1KTokens: 0.003,   OutputPricePer1KTokens: 0.015},
    "claude-haiku-4-5-20251001":  {InputPricePer1KTokens: 0.001,   OutputPricePer1KTokens: 0.005},
    "claude-haiku-4-5":           {InputPricePer1KTokens: 0.001,   OutputPricePer1KTokens: 0.005},
    // Legacy Claude 4.5
    "claude-opus-4-5-20251101":   {InputPricePer1KTokens: 0.005,   OutputPricePer1KTokens: 0.025},
    "claude-sonnet-4-5-20250929": {InputPricePer1KTokens: 0.003,   OutputPricePer1KTokens: 0.015},
    // Legacy Claude 4.1 / 4.0
    "claude-opus-4-1-20250805":   {InputPricePer1KTokens: 0.015,   OutputPricePer1KTokens: 0.075},
    "claude-opus-4-20250514":     {InputPricePer1KTokens: 0.015,   OutputPricePer1KTokens: 0.075},
    "claude-sonnet-4-20250514":   {InputPricePer1KTokens: 0.003,   OutputPricePer1KTokens: 0.015},
    // Older Haiku (deprecated April 2026)
    "claude-3-haiku-20240307":    {InputPricePer1KTokens: 0.00025, OutputPricePer1KTokens: 0.00125},
}
```

Note: `BedrockModelRate` is already defined in `pkg/aws/pricing.go` and serves as the shared rate struct. It can be reused for Anthropic rates without modification.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| Bedrock-only MITM metering | Bedrock + Anthropic 1P MITM metering | Phase 20 | Sandboxes using Claude Code directly via `api.anthropic.com` (not Bedrock) now have spend tracked |
| `ExtractBedrockTokens` returns `(input, output, err)` | `ExtractAnthropicTokens` returns `(modelID, input, output, err)` | Phase 20 | Model ID sourced from response body instead of URL path |

**Note on Anthropic API model IDs in 2026:** The models table (source: platform.claude.com, 2026-03-24) shows Claude Opus 4.6 and Sonnet 4.6 as the latest. Claude Haiku 3 (`claude-3-haiku-20240307`) is deprecated and retires 2026-04-19. The requirement text mentions `claude-haiku-4-5-20251001` specifically — this is present in the rate table above.

---

## Open Questions

1. **Should `AnthropicBlockedResponse` be a separate function or reuse `BedrockBlockedResponse`?**
   - What we know: The JSON body shape is identical; both are 403 responses with `{error, spent, limit, model, topUp}`.
   - What's unclear: Whether operators would benefit from distinguishing "Bedrock blocked" vs "Anthropic API blocked" in logs/responses.
   - Recommendation: Create a thin `AnthropicBlockedResponse` that delegates to the same internal logic, or simply add an `event_type` field to the log and reuse `BedrockBlockedResponse`. Either is fine; lean toward reuse for simplicity.

2. **Prompt caching token metering (scope clarification)**
   - What we know: Anthropic API usage response includes `cache_creation_input_tokens` and `cache_read_input_tokens` alongside standard `input_tokens`. Cache writes cost 1.25x–2x; cache reads cost 0.1x.
   - What's unclear: Whether BUDG-10 requires full cache-aware costing or just base tokens.
   - Recommendation: BUDG-10 requirement text says "extracts `usage.input_tokens`/`usage.output_tokens`" — this is unambiguous. Implement base tokens only. Document cache tokens as a known undercount in Phase 20 comments.

3. **`km status` display — no code change needed for BUDG-10**
   - What we know: `status.go`'s `printSandboxStatus` already iterates `budget.AIByModel` and prints any model ID present. DynamoDB keys use `BUDGET#ai#{modelID}`. Anthropic API model IDs (e.g., `claude-opus-4-6`) will appear in that map automatically when `IncrementAISpend` is called.
   - What's unclear: Nothing. The display just works once spend records exist.
   - Recommendation: No change to `status.go` required. Verify in test that Anthropic model IDs appear in the `AIByModel` map correctly.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` stdlib + table-driven tests |
| Config file | none (Go native) |
| Quick run command | `go test ./sidecars/http-proxy/httpproxy/... -run TestAnthropicAPI -v` |
| Full suite command | `go test ./sidecars/http-proxy/...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| BUDG-10 | Non-streaming: extract model + tokens from JSON body | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestExtractAnthropicTokens_NonStreaming` | ❌ Wave 0 |
| BUDG-10 | Streaming: extract model + tokens from SSE events | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestExtractAnthropicTokens_SSEStream` | ❌ Wave 0 |
| BUDG-10 | Empty body returns ("", 0, 0, nil) | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestExtractAnthropicTokens_EmptyBody` | ❌ Wave 0 |
| BUDG-10 | Model ID extracted from SSE message_start | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestExtractAnthropicTokens_ModelID` | ❌ Wave 0 |
| BUDG-10 | Budget exhausted: proxy returns 403 | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAnthropicBlockedResponse` | ❌ Wave 0 |
| BUDG-10 | Rate table covers all current Claude 4 model IDs | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAnthropicRates` | ❌ Wave 0 |
| BUDG-10 | proxy.go handler registered before general CONNECT | integration | `go test ./sidecars/http-proxy/httpproxy/... -run TestHTTPProxy_AnthropicMITM` | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./sidecars/http-proxy/httpproxy/... -count=1`
- **Per wave merge:** `go test ./sidecars/http-proxy/... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `sidecars/http-proxy/httpproxy/anthropic.go` — implementation file
- [ ] `sidecars/http-proxy/httpproxy/anthropic_test.go` — test file covering all BUDG-10 behaviors

*(No missing framework or fixtures — Go stdlib test infrastructure fully present)*

---

## Sources

### Primary (HIGH confidence)

- `sidecars/http-proxy/httpproxy/bedrock.go` — Existing implementation; Phase 20 mirrors this exactly
- `sidecars/http-proxy/httpproxy/proxy.go` — Existing MITM registration pattern
- `pkg/aws/budget.go` — `IncrementAISpend`, `BudgetAPI`, `GetBudget` — reused unchanged
- `pkg/aws/pricing.go` — `BedrockModelRate` struct reused for Anthropic rate table
- [platform.claude.com/docs/en/api/messages-streaming](https://platform.claude.com/docs/en/api/messages-streaming) — SSE wire format verified (full event sequence with exact JSON)
- [platform.claude.com/docs/en/about-claude/pricing](https://platform.claude.com/docs/en/about-claude/pricing) — Model pricing table verified (all Claude 4.x models, rates per MTok)
- [platform.claude.com/docs/en/about-claude/models/overview](https://platform.claude.com/docs/en/about-claude/models/overview) — Exact API model ID strings verified (claude-opus-4-6, claude-sonnet-4-6, etc.)

### Secondary (MEDIUM confidence)

- [code.claude.com/docs/en/model-config](https://code.claude.com/docs/en/model-config) — Claude Code model aliases confirmed (default → Opus 4.6 for Max/Team Premium, Sonnet 4.6 for Pro/Team Standard, Haiku for background)

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in repo; no new dependencies
- Architecture: HIGH — SSE format verified from official docs; Bedrock handler is proven template
- Model rates: HIGH — verified from official Anthropic pricing page (2026-03-24)
- Pitfalls: HIGH — handler registration order confirmed by existing code pattern; TLS MITM behavior confirmed from goproxy docs and existing Bedrock usage

**Research date:** 2026-03-24
**Valid until:** 2026-07-01 (model rates update when new models launch; SSE format is stable API)

**Rate table currency note:** Claude Haiku 3 (`claude-3-haiku-20240307`) is deprecated and retires 2026-04-19. It is included in the rate table for completeness but may be removed in a later phase. The three current production models for Claude Code are `claude-opus-4-6`, `claude-sonnet-4-6`, and `claude-haiku-4-5-20251001`.

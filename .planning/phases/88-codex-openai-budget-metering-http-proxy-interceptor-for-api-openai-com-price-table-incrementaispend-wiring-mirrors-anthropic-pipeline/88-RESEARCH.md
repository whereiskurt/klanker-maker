# Phase 88: Codex/OpenAI budget metering — Research

**Researched:** 2026-05-24
**Domain:** HTTP MITM proxy + OpenAI API stream parsing + budget accounting
**Confidence:** HIGH (codebase patterns), HIGH (OpenAI Responses API schema), MEDIUM (pricing — current as of fetch date, table will drift)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Scope:** OpenAI direct API (`api.openai.com`) only. Google/Mistral/etc. deferred.
- **Pattern:** Mirror `anthropic.go` line-for-line — response-stream parse → token-count → `IncrementAISpend(modelID, in, out, cost)`. No new abstraction.
- **Price table:** Hard-coded in the new Go file, same shape as `staticAnthropicRates`. Must cover gpt-5 family, gpt-4o family, o1 reasoning, codex-specific model IDs. Both alias forms AND dated variants.
- **DynamoDB row shape:** `BUDGET#ai#{modelID}` — no schema change. OpenAI modelIDs slot in as new SK values.
- **Enforcement parity:** Existing exceeds-budget paths (IAM revoke + proxy 403) read per-model rows + aggregate; fire identically once OpenAI rows accrue. No new enforcement code.
- **Cache-token handling:** If OpenAI exposes cache token counts, account at cache-read price per Phase 6 quick-task #4 precedent. If absent, no-op for OpenAI path.

### Claude's Discretion

- File layout: `sidecars/http-proxy/httpproxy/openai.go` sibling. Split into `openai.go` + `openai_pricing.go` if price table long.
- Stream-parse format: research must surface exact delta shape for Responses API vs Chat Completions.
- TLS allowlist: locate analogous wiring for `api.anthropic.com` and add `api.openai.com`.
- Failure mode for unknown model: mirror what `anthropic.go` does.
- Test fixtures: capture real streaming response OR hand-construct matching documented schema.

### Deferred Ideas (OUT OF SCOPE)

- Non-OpenAI providers (Google Gemini, Mistral, Cohere) — separate phases.
- Registry-driven price table loaded from S3.
- OpenAI batch API (`/v1/batches`) — different pricing.
- Assistants API / Threads API — not used by Codex CLI.
- Budget UI changes, OTEL/MLflow spend changes, `pkg/aws/budget.go` schema, `cmd/budget-enforcer/`.
</user_constraints>

<phase_requirements>
## Phase Requirements

No upstream REQUIREMENTS.md IDs assigned. Synthetic phase-local IDs proposed below; planner should adopt or modify.

| ID | Description | Research Support |
|----|-------------|------------------|
| OAI-BUDGET-01 | New file `sidecars/http-proxy/httpproxy/openai.go` implements `ExtractOpenAITokens(body) (modelID, in, out, cachedIn, reasoningOut, err)` parsing both Responses API SSE and Chat Completions SSE + non-streaming bodies | `anthropic.go` lines 52-124 is the line-for-line pattern; Responses API schema confirmed from openai docs; Chat Completions usage shape confirmed |
| OAI-BUDGET-02 | New file declares `staticOpenAIRates map[string]aws.BedrockModelRate` with current pricing for at least gpt-5.5, gpt-5.4, gpt-5.4-mini, gpt-5.4-nano, gpt-5.3-codex, gpt-4o, gpt-4o-mini, gpt-4.1, gpt-4.1-mini, o1, o1-mini | Pricing table fetched 2026-05-24 from developers.openai.com/api/docs/pricing + pricepertoken.com (cross-verified) |
| OAI-BUDGET-03 | `CalculateOpenAICost(in, out, cachedIn int, rate)` mirrors `CalculateAnthropicCost` shape; cached input priced at separately-tiered cache rate (not derived multiplier) | Anthropic uses 0.1×/1.25× multipliers; OpenAI publishes explicit cache prices per model — need separate `CachedInputPricePer1KTokens` field on rate struct OR a parallel rates map |
| OAI-BUDGET-04 | `OpenAIBlockedResponse(req, sandboxID, modelID, spent, limit)` returns 403 with same `blockedResponseBody` JSON shape as Anthropic | `anthropic.go:180-190` is the exact template; reuse `blockedResponseBody` struct from `bedrock.go:296` |
| OAI-BUDGET-05 | `proxy.go` gains a third intercept block (after the Anthropic one at lines 316-431) for `openaiHostRegex = ^api\.openai\.com`: preflight check + AlwaysMitm + OnResponse tee-reader with `ExtractOpenAITokens` → `IncrementAISpend` | Pattern at `proxy.go:316-431` for Anthropic; copy-paste-modify |
| OAI-BUDGET-06 | `transparent.go` gains `isOpenAI := openaiHostRegex.MatchString(host)` branch + `meterOpenAIResponse` mirror of `meterAnthropicResponse` (lines 403-432); preflight uses `OpenAIBlockedResponse` | `transparent.go:264-431` is the exact pattern |
| OAI-BUDGET-07 | `pkg/compiler/userdata.go::buildL7ProxyHosts` adds `"api.openai.com"` (likely gated on `p.Spec.Cli.Agent == "codex"` OR unconditionally since profiles already allowlist `.openai.com`) | `userdata.go:3783-3795` is the function; current Anthropic add is gated on `UseBedrock` (interesting inversion — useBedrock true means use bedrock so api.anthropic.com is NOT used; this is a known bug pattern to research). Tests at `userdata_test.go:1094-1132` show assertion style |
| OAI-BUDGET-08 | New `openai_test.go` mirrors `anthropic_test.go` 13-test suite — non-streaming, SSE streaming, empty body, model-ID extraction, blocked-response shape, rate-table completeness, cost calc reuse, AIByModel integration, cache-token variants | `anthropic_test.go` lines 1-348 is the template, test names + assertions directly copyable |
| OAI-BUDGET-09 | Validation: an end-to-end test through `proxy.go::NewProxy` with the budget option enabled and a synthetic OpenAI SSE response asserts the metering callback fires with the right modelID/tokens/cost | `http_proxy_test.go` is the integration-test harness; budget-enabled flow uses `httpproxy.WithBudgetEnforcement(...)` |
| OAI-BUDGET-10 | `km doctor` adds an `openai_metering_healthy` check that warns when a sandbox with `spec.cli.agent: codex` has zero `BUDGET#ai#gpt-*` rows after 24h of agent activity (parallels existing claude check pattern) | Optional/stretch — discretion item; could ship as Phase 88.1 follow-up |
</phase_requirements>

## Summary

The implementation is mechanically obvious — copy `anthropic.go` to `openai.go`, replace the streaming parse, swap the price table, register a third handler block in `proxy.go` and `transparent.go`, and add `api.openai.com` to the L7 proxy host list. The DynamoDB write path (`IncrementAISpend`) is already agent-agnostic.

The only **non-trivial** decisions are:

1. **Which API surface to parse first.** Codex CLI as of v0.131+ (May 2026) uses `/v1/responses` exclusively — chat/completions was hard-removed in Feb 2026. But other OpenAI clients on the sandbox (raw `openai` Python SDK, LangChain, etc.) still emit `/v1/chat/completions`. Best practice: parse **both** in one `ExtractOpenAITokens` (mirroring how `ExtractBedrockTokens` handles three formats: SSE text, AWS event-stream binary, non-streaming JSON).
2. **Cache pricing model.** Anthropic uses derived multipliers (0.1× input, 1.25× input) baked into `CalculateAnthropicCost`. OpenAI publishes **explicit** cache-input rates per model (e.g. gpt-5.4 input $2.50, cached input $0.25 — 10× cheaper, but the ratio differs per model). The Anthropic-style derived multiplier approach is **wrong** for OpenAI — extend `aws.BedrockModelRate` with `CachedInputPricePer1KTokens` OR use a parallel `staticOpenAIRates map[string]openAIModelRate` with its own struct.
3. **L7 proxy host gating.** Current `buildL7ProxyHosts` only adds Anthropic when `useBedrock=true` (which is contradictory and was likely a Phase 20 bug). For OpenAI, the cleanest trigger is `p.Spec.Cli.Agent == "codex"` OR unconditionally when the profile has `.openai.com` in its DNS allowlist (which all codex profiles already do).

**Primary recommendation:** Single new file `openai.go` (no split — table is ~12 entries, not enough to warrant separation). Parse Responses API SSE as the primary path with Chat Completions SSE + non-streaming JSON as fallbacks. Add separate `CachedInputPricePer1KTokens` field on `aws.BedrockModelRate` (the struct name is misleading but the type is generic — extending it avoids a parallel rate map; Anthropic ignores the field).

## Standard Stack

### Core (already in tree — no new deps)

| Component | Source | Purpose |
|-----------|--------|---------|
| `goproxy` | `github.com/elazarl/goproxy` | MITM proxy framework, already used by `proxy.go` |
| `meteringReader` | `sidecars/http-proxy/httpproxy/metering_reader.go` | Non-blocking tee reader that captures full body for token extraction on EOF — reuse verbatim |
| `aws.IncrementAISpend` | `pkg/aws/budget.go:65` | Atomic DynamoDB `ADD spentUSD,inputTokens,outputTokens SET last_updated` against `BUDGET#ai#{modelID}` row — agent-agnostic, takes modelID string |
| `aws.BedrockModelRate` | `pkg/aws/pricing.go` | `{InputPricePer1KTokens, OutputPricePer1KTokens}` struct — name is legacy, type is generic. Recommend extending with `CachedInputPricePer1KTokens float64` |
| `extractBalancedJSON` | `bedrock.go:231` | Binary frame scanner — needed if any OpenAI client uses binary framing (probably not, but free fallback) |

### Supporting (reused)

| Library | Source | When |
|---------|--------|------|
| `bufio.Scanner` | stdlib | SSE line-by-line parse (Anthropic pattern) |
| `encoding/json` | stdlib | Per-line JSON unmarshal of `data: {...}` payloads |
| `regexp` | stdlib | `openaiHostRegex = ^api\.openai\.com` matching |

### No new dependencies

No SDK is needed — we parse raw SSE bytes. Using the OpenAI Go SDK would couple us to its types and require us to import 100KB+ of generated code for two integer fields.

## Architecture Patterns

### Recommended File Layout

```
sidecars/http-proxy/httpproxy/
├── openai.go              # NEW — ExtractOpenAITokens, CalculateOpenAICost,
│                          #       staticOpenAIRates, OpenAIBlockedResponse
├── openai_test.go         # NEW — mirror anthropic_test.go (13 tests)
├── proxy.go               # EDIT — add openaiHostRegex + 3 handlers (mirror lines 316-431)
├── transparent.go         # EDIT — add isOpenAI branch + meterOpenAIResponse
├── anthropic.go           # UNCHANGED
├── bedrock.go             # UNCHANGED
└── metering_reader.go     # UNCHANGED (reused)
```

Cumulative LoC: ~250 new lines in `openai.go`, ~120 edited lines split across `proxy.go` and `transparent.go`, ~300 new test lines in `openai_test.go`. One-line edits to `userdata.go::buildL7ProxyHosts` + `userdata_test.go`.

### Pattern 1: Three-format extractor

**What:** `ExtractOpenAITokens` should accept any of: Responses API SSE, Chat Completions SSE, or non-streaming JSON body. Returns `(modelID, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens, err)`.

**Example:**

```go
// sidecars/http-proxy/httpproxy/openai.go

// Responses API SSE event payloads (only the events we care about).
// Source: https://developers.openai.com/api/reference/resources/responses/streaming-events
type openaiResponsesCreatedPayload struct {
    Type     string `json:"type"`     // "response.created"
    Response struct {
        Model string `json:"model"`
    } `json:"response"`
}

type openaiResponsesCompletedPayload struct {
    Type     string `json:"type"`     // "response.completed"
    Response struct {
        Model string `json:"model"`
        Usage struct {
            InputTokens         int `json:"input_tokens"`
            OutputTokens        int `json:"output_tokens"`
            TotalTokens         int `json:"total_tokens"`
            InputTokensDetails  struct {
                CachedTokens int `json:"cached_tokens"`
            } `json:"input_tokens_details"`
            OutputTokensDetails struct {
                ReasoningTokens int `json:"reasoning_tokens"`
            } `json:"output_tokens_details"`
        } `json:"usage"`
    } `json:"response"`
}

// Chat Completions SSE final-chunk payload (when stream_options.include_usage=true).
// All non-final chunks have usage == nil and choices != [].
// The final chunk has choices == [] and usage populated.
type openaiChatCompletionsChunkPayload struct {
    Model   string                 `json:"model"`
    Choices []json.RawMessage      `json:"choices"`
    Usage   *openaiChatUsagePayload `json:"usage"`  // nil except on final chunk
}

type openaiChatUsagePayload struct {
    PromptTokens         int `json:"prompt_tokens"`
    CompletionTokens     int `json:"completion_tokens"`
    TotalTokens          int `json:"total_tokens"`
    PromptTokensDetails  *struct {
        CachedTokens int `json:"cached_tokens"`
    } `json:"prompt_tokens_details"`
    CompletionTokensDetails *struct {
        ReasoningTokens int `json:"reasoning_tokens"`
    } `json:"completion_tokens_details"`
}

// Non-streaming Responses API body — same shape as the nested "response" object
// inside response.completed event.
type openaiResponsesNonStreamingPayload struct {
    Model string `json:"model"`
    Usage struct {
        InputTokens         int `json:"input_tokens"`
        OutputTokens        int `json:"output_tokens"`
        InputTokensDetails  struct {
            CachedTokens int `json:"cached_tokens"`
        } `json:"input_tokens_details"`
        OutputTokensDetails struct {
            ReasoningTokens int `json:"reasoning_tokens"`
        } `json:"output_tokens_details"`
    } `json:"usage"`
}

// Non-streaming Chat Completions body.
type openaiChatNonStreamingPayload struct {
    Model string `json:"model"`
    Usage openaiChatUsagePayload `json:"usage"`
}
```

### Pattern 2: Extractor function structure

Mirror `ExtractAnthropicTokens` (`anthropic.go:66-124`) — read up to `maxResponseBodySize`, try SSE parsing first (scan for `data: ` prefix), if no SSE events were seen try non-streaming JSON, return zero counts on unparseable.

The OpenAI SSE has a `event:` line **before** the `data:` line (unlike Anthropic, where `event:` is optional/cosmetic):

```
event: response.completed
data: {"type":"response.completed","sequence_number":42,"response":{...}}
```

But the Anthropic code only reads `data: ` lines and ignores `event: ` lines (`anthropic.go:78`), and that's correct for OpenAI too because the `type` field is duplicated inside the JSON payload. **Copy that pattern.**

### Pattern 3: Handler registration in proxy.go

Add a third intercept block immediately after the Anthropic one (`proxy.go:316-431`):

```go
// -----------------------------------------------------------------
// OpenAI direct API (api.openai.com) MITM handlers.
// -----------------------------------------------------------------

proxy.OnRequest(goproxy.ReqHostMatches(openaiHostRegex)).DoFunc(
    func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
        entry := be.cache.Get(sandboxID)
        if entry != nil && entry.AILimit > 0 && entry.AISpent >= entry.AILimit {
            // ... log + return OpenAIBlockedResponse
        }
        return req, nil
    },
)

proxy.OnRequest(goproxy.ReqHostMatches(openaiHostRegex)).HandleConnect(goproxy.AlwaysMitm)

proxy.OnResponse(goproxy.ReqHostMatches(openaiHostRegex)).DoFunc(
    func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
        // ... mirror anthropic block: tee-reader wraps body, on EOF parses tokens, IncrementAISpend
    },
)
```

Add `openaiHostRegex` next to existing regexes (`proxy.go:28-33`):
```go
var openaiHostRegex = regexp.MustCompile(`^api\.openai\.com`)
```

### Anti-Patterns to Avoid

- **Don't import openai-go SDK** — adds 100KB+ of types for 2 integer fields.
- **Don't build a streaming JSON parser** — full body fits in 10 MB cap; load and `bufio.Scanner` line-by-line.
- **Don't merge openai + chat/completions logic into anthropic.go** — keep providers in separate files for grep-ability.
- **Don't gate L7 proxy host on `spec.cli.agent == "codex"` alone** — operator may run raw OpenAI SDK in a Claude sandbox. Better gate: profile DNS allowlist contains `.openai.com` OR `spec.cli.agent == "codex"`.
- **Don't reuse `bedrockHostRegex` matcher for openai** — they're disjoint hosts; separate matchers keeps the handler logs cleanly differentiated for ops debugging.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| SSE parsing | Custom event-stream parser | `bufio.Scanner` + `strings.HasPrefix("data: ")` (Anthropic pattern) | Anthropic already does it correctly; pattern is 8 lines |
| DynamoDB atomic increment | Read-modify-write loop | `aws.IncrementAISpend` | Uses `ADD` expression — no races, no locks |
| Per-model rate lookup | Map fallback chains | Single `staticOpenAIRates` map, `map[modelID]rate` lookup with `_, ok :=` guard | Match `anthropic.go:147-165` |
| Tee-reader for streaming | Custom buffer + callback | `newMeteringReader(resp.Body, func(captured) {...})` | Already exists, exactly the right primitive |
| Budget cache + preflight | New cache layer | Shared `budgetCache` already wired through `proxyConfig.budget.cache` | Reused by Bedrock + Anthropic; just add OpenAI handlers to the same struct |

**Key insight:** All the hard plumbing (DynamoDB writes, cache invalidation, streaming body capture, 403 response shape, budget-enforcer Lambda integration) is **already done** and agent-agnostic. Phase 88 is a leaf addition — purely a new file + handler block + price table + L7 host wire-up.

## Common Pitfalls

### Pitfall 1: Responses API vs Chat Completions confusion

**What goes wrong:** Implementer parses only chat/completions and Codex traffic never gets metered (Codex uses `/v1/responses` exclusively as of Feb 2026 hard-removal).
**Why it happens:** OpenAI docs default to showing chat/completions examples; the Responses API is newer and less surfaced in tutorials.
**How to avoid:** Parse **both**. Responses API SSE events have `"type": "response.completed"` carrying usage; Chat Completions SSE final chunk has `choices: []` + `usage: {...}` when `stream_options.include_usage=true`.
**Warning signs:** Codex sandbox runs, no `BUDGET#ai#gpt-5.3-codex` rows appear in DynamoDB. Test fixture for Responses API streaming must be included in the test suite.

### Pitfall 2: Chat Completions without `include_usage`

**What goes wrong:** Older OpenAI clients call `/v1/chat/completions` with `stream: true` but **without** `stream_options.include_usage: true`. The final chunk has `choices: []` but `usage: null`. Token counts come back as zero, no metering happens.
**Why it happens:** `stream_options` was added later (~2024); older client code doesn't set it.
**How to avoid:** Two options:
1. **Do nothing** — silent metering miss; agent operates fine but budget never accrues. Acceptable for v1 if Codex always sets it (Codex's Responses API mode is unaffected).
2. **Log a WARN** — when SSE was detected but no usage found, emit `WARN: openai chat completions response missing usage field — set stream_options.include_usage=true to enable metering`. Helps operator debug "why is my Codex budget always $0?"
**Warning signs:** Non-Codex OpenAI SDK calls (LangChain, raw Python) with no spend rows.
**Recommendation:** Ship #2 (the log) — diagnostic cost is one log line, debuggability win is large.

### Pitfall 3: Reasoning tokens counted twice

**What goes wrong:** Responses API `usage.output_tokens` **includes** reasoning tokens (`output_tokens_details.reasoning_tokens`). If the implementer adds reasoning tokens to output tokens for cost calc, they're billed twice.
**Why it happens:** The nested `output_tokens_details.reasoning_tokens` field looks like a separate counter; implementers may sum them.
**How to avoid:** `output_tokens` is the **inclusive total**. Reasoning tokens are billed at the **same price** as output tokens (no separate tier in OpenAI pricing for o-series / gpt-5 reasoning). Just use `output_tokens` directly; surface `reasoning_tokens` to logs for observability only, never sum into cost.
**Warning signs:** Test for o1 / o3 / gpt-5.5 (reasoning models) shows costs ~2× expected.

### Pitfall 4: Cached tokens billed at full input rate

**What goes wrong:** Implementer treats `prompt_tokens` / `input_tokens` as uncached and bills the full input rate. But OpenAI's `input_tokens` field **includes** cached tokens. Cached tokens should be billed at the (cheaper) cached rate, and the remainder at the full rate.
**Why it happens:** Anthropic separates `input_tokens` (uncached) from `cache_read_input_tokens` (cached) — they're disjoint. OpenAI does **not** — `input_tokens` is the inclusive total.
**How to avoid:** `effective_uncached_input = input_tokens - cached_tokens`; cost = `effective_uncached_input * input_rate + cached_tokens * cached_rate`.
**Reference:** OpenAI cache pricing docs: "cached_tokens is a subset of prompt_tokens / input_tokens, not additive".
**Warning signs:** Sandbox runs the same long prompt repeatedly and budget burns at full rate.

### Pitfall 5: L7 proxy host gating accidentally drops Anthropic

**What goes wrong:** Implementer edits `buildL7ProxyHosts` and changes the gating logic for the Anthropic line, breaking existing Claude metering.
**Why it happens:** The current Anthropic gate (`if p.Spec.Execution.UseBedrock`) is **counterintuitive** — `useBedrock: true` actually triggers the Anthropic line. (Likely a Phase 20 bug — when `useBedrock` is true, Anthropic SDK calls are sent to Bedrock, not api.anthropic.com, so adding api.anthropic.com to the L7 list is redundant. The line probably should be `if !p.Spec.Execution.UseBedrock` or unconditional.)
**How to avoid:** Add OpenAI as a **new** conditional, don't refactor the existing Anthropic logic. Out of scope for Phase 88 to fix the Anthropic gate.
**Suggested gate:** `if p.Spec.Cli.Agent == "codex" || profileAllowlistContainsOpenAI(p)` → append `"api.openai.com"`.

### Pitfall 6: Interrupted streams never fire onComplete

**What goes wrong:** If a sandbox kills its connection mid-stream (Ctrl-C), the EOF never arrives, `meteringReader.fireOnce` never fires, no metering happens.
**Why it happens:** Tee-reader fires on EOF or Close; cancelled connections close the reader from the proxy side.
**How to avoid:** `meteringReader.Close()` also fires the callback (`metering_reader.go:49-52`) — this is the existing behavior for both Bedrock and Anthropic. As long as goproxy closes the response body (which it does), partial usage is captured. The Responses API only emits `usage` on the final `response.completed` event, so partial streams will have zero tokens — **this is fine** (no charge for a request that never completed).
**Mention in docs:** Document that interrupted Codex/OpenAI requests are not billed (already-stated Anthropic behavior).

## Code Examples

### Streaming format reference — Responses API SSE

```
event: response.created
data: {"type":"response.created","response":{"id":"resp_abc","model":"gpt-5.5","status":"in_progress","output":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":1,"delta":"Hello"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":2,"delta":" world"}

event: response.completed
data: {"type":"response.completed","sequence_number":42,"response":{"id":"resp_abc","model":"gpt-5.5","status":"completed","output":[...],"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":80},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":12},"total_tokens":165}}}
```

Source: https://developers.openai.com/api/reference/resources/responses/streaming-events (53 event types total; we only care about `response.created` for the model ID and `response.completed` for the final usage). The model ID also appears inside `response.completed.response.model`, so `response.created` parsing is **optional** — `response.completed` alone is sufficient.

### Streaming format reference — Chat Completions SSE

Non-final chunks (n of them):
```
data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}
```

Final chunk (one, only when `stream_options.include_usage=true`):
```
data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":45,"total_tokens":165,"prompt_tokens_details":{"cached_tokens":80}}}
```

Terminator:
```
data: [DONE]
```

Source: https://community.openai.com/t/usage-stats-now-available-when-using-streaming-with-the-chat-completions-api-or-completions-api/738156

### Non-streaming Responses API body

```json
{
  "id": "resp_abc",
  "object": "response",
  "model": "gpt-5.5",
  "status": "completed",
  "output": [...],
  "usage": {
    "input_tokens": 120,
    "input_tokens_details": { "cached_tokens": 80 },
    "output_tokens": 45,
    "output_tokens_details": { "reasoning_tokens": 12 },
    "total_tokens": 165
  }
}
```

### Cost calculation (handles cache correctly)

```go
// openai.go
func CalculateOpenAICost(inputTokens, outputTokens, cachedInputTokens int, rate openAIModelRate) float64 {
    // OpenAI: input_tokens INCLUDES cached_tokens. Bill cached at cache rate, remainder at full rate.
    uncachedInput := inputTokens - cachedInputTokens
    if uncachedInput < 0 {
        uncachedInput = 0  // defensive
    }
    inputCost := float64(uncachedInput) * rate.InputPricePer1KTokens / 1000.0
    cachedCost := float64(cachedInputTokens) * rate.CachedInputPricePer1KTokens / 1000.0
    outputCost := float64(outputTokens) * rate.OutputPricePer1KTokens / 1000.0
    return inputCost + cachedCost + outputCost
}
```

### Price table

```go
// Verified 2026-05-24 against:
//   - https://developers.openai.com/api/docs/pricing (primary)
//   - https://pricepertoken.com/pricing-page/provider/openai (cross-check)
// Prices in USD per 1,000 tokens (convert from /1M by dividing by 1000).
// Update cadence: OpenAI pricing has historically changed quarterly; recommend
// re-verifying every 90 days and on each new model launch.
//
// Note: response.model echoes back the model ID exactly as sent in the request.
// Alias forms (e.g. "gpt-4o") and dated variants (e.g. "gpt-4o-2024-08-06") both
// appear in real traffic — include both. When OpenAI rolls a new dated variant
// for an alias, add the dated entry but keep the alias entry.

var staticOpenAIRates = map[string]openAIModelRate{
    // GPT-5.5 family (current flagship as of 2026-05-24)
    "gpt-5.5":      {InputPricePer1KTokens: 0.005,  CachedInputPricePer1KTokens: 0.0005,  OutputPricePer1KTokens: 0.030},
    "gpt-5.5-pro":  {InputPricePer1KTokens: 0.030,  CachedInputPricePer1KTokens: 0.030,   OutputPricePer1KTokens: 0.180},

    // GPT-5.4 family
    "gpt-5.4":      {InputPricePer1KTokens: 0.0025, CachedInputPricePer1KTokens: 0.00025, OutputPricePer1KTokens: 0.015},
    "gpt-5.4-mini": {InputPricePer1KTokens: 0.00075,CachedInputPricePer1KTokens: 0.000075,OutputPricePer1KTokens: 0.0045},
    "gpt-5.4-nano": {InputPricePer1KTokens: 0.0002, CachedInputPricePer1KTokens: 0.00002, OutputPricePer1KTokens: 0.00125},
    "gpt-5.4-pro":  {InputPricePer1KTokens: 0.030,  CachedInputPricePer1KTokens: 0.030,   OutputPricePer1KTokens: 0.180},

    // GPT-5.3 Codex family (most important for Phase 88 — Codex CLI default)
    "gpt-5.3-codex":       {InputPricePer1KTokens: 0.00175, CachedInputPricePer1KTokens: 0.000175, OutputPricePer1KTokens: 0.014},
    "gpt-5.3-codex-spark": {InputPricePer1KTokens: 0.00175, CachedInputPricePer1KTokens: 0.000175, OutputPricePer1KTokens: 0.014}, // placeholder — verify

    // GPT-4o family (legacy but still in use)
    "gpt-4o":             {InputPricePer1KTokens: 0.0025, CachedInputPricePer1KTokens: 0.00125, OutputPricePer1KTokens: 0.010},
    "gpt-4o-2024-08-06":  {InputPricePer1KTokens: 0.0025, CachedInputPricePer1KTokens: 0.00125, OutputPricePer1KTokens: 0.010},
    "gpt-4o-2024-11-20":  {InputPricePer1KTokens: 0.0025, CachedInputPricePer1KTokens: 0.00125, OutputPricePer1KTokens: 0.010},
    "gpt-4o-mini":            {InputPricePer1KTokens: 0.00015, CachedInputPricePer1KTokens: 0.000075, OutputPricePer1KTokens: 0.0006},
    "gpt-4o-mini-2024-07-18": {InputPricePer1KTokens: 0.00015, CachedInputPricePer1KTokens: 0.000075, OutputPricePer1KTokens: 0.0006},

    // GPT-4.1 family
    "gpt-4.1":      {InputPricePer1KTokens: 0.002,  CachedInputPricePer1KTokens: 0.0005,  OutputPricePer1KTokens: 0.008},
    "gpt-4.1-mini": {InputPricePer1KTokens: 0.0004, CachedInputPricePer1KTokens: 0.0001,  OutputPricePer1KTokens: 0.0016},

    // O-series reasoning (legacy — still occasionally invoked)
    "o1":      {InputPricePer1KTokens: 0.015,  CachedInputPricePer1KTokens: 0.0075,  OutputPricePer1KTokens: 0.060},
    "o1-mini": {InputPricePer1KTokens: 0.00055,CachedInputPricePer1KTokens: 0.00055, OutputPricePer1KTokens: 0.0022}, // o1-mini has no separate cache tier
    "o3-mini": {InputPricePer1KTokens: 0.00055,CachedInputPricePer1KTokens: 0.00055, OutputPricePer1KTokens: 0.0022},
}

type openAIModelRate struct {
    InputPricePer1KTokens       float64
    CachedInputPricePer1KTokens float64
    OutputPricePer1KTokens      float64
}
```

**ALTERNATIVE:** Extend `aws.BedrockModelRate` with `CachedInputPricePer1KTokens float64` field instead of a parallel struct. Anthropic ignores the new field (calculates cache via 0.1× multiplier on input). Single struct is more uniform but the field-name `BedrockModelRate` is increasingly misleading. **Researcher recommendation: extend the struct.** Rename it later in a v2 if it bothers anyone.

### Unknown-model fallback (mirror Anthropic exactly)

Anthropic does **log-and-skip** for unknown models (`proxy.go:377` — `if rate, ok := staticAnthropicRates[modelID]; ok { ... }`, else `costUSD` stays 0 but tokens still get written to DynamoDB). Mirror that — write the row with cost=0 (so the model_id appears in `km status` so operator knows the table needs an entry) and log `WARN openai_unknown_model model=<id>`.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact for Phase 88 |
|--------------|------------------|--------------|---------------------|
| Codex CLI v0.117 used `/v1/chat/completions` by default | Codex CLI v0.131+ uses `/v1/responses` exclusively | Feb 2026 hard-removal | Phase 88 **must** parse Responses API SSE — chat/completions is fallback only |
| `stream_options.include_usage` was optional add-on | Codex CLI always sets it; raw SDK still optional | Continuous | Defensive: log a WARN when usage missing |
| OpenAI cache was an opt-in beta with separate tracking | Cache is automatic; `cached_tokens` always emitted (zero when <1024 prompt or no hit) | Late 2024 | Always parse `input_tokens_details.cached_tokens`; subtract from input before billing |
| Reasoning models (o1, o3) had separate `reasoning_tokens` billing | Reasoning tokens billed as regular output tokens (counted inside `output_tokens`) | Always | Don't double-count |

**Deprecated/outdated:**
- `gpt-4` (non-o) — superseded by gpt-4.1; OpenAI may sunset 2026
- `gpt-3.5-turbo` — superseded by gpt-4.1-mini / gpt-4o-mini; rarely used by Codex
- Chat/completions in Codex CLI — removed Feb 2026

## Open Questions

1. **Should the rate struct be extended or parallel?**
   - What we know: Anthropic uses derived multipliers (0.1× cache read, 1.25× cache write); OpenAI publishes explicit per-model cache prices.
   - What's unclear: refactor cost vs uniformity benefit.
   - Recommendation: Extend `aws.BedrockModelRate` with `CachedInputPricePer1KTokens` and `CachedWritePricePer1KTokens float64`. Anthropic code stays as-is (uses multiplier). OpenAI code uses the new field. Document the asymmetry in a doc comment on the struct.

2. **Should `buildL7ProxyHosts` add openai unconditionally or gate?**
   - What we know: All codex-using profiles already have `.openai.com` in their DNS allowlist; only `eBPF` + `both` enforcement modes need the L7 list for transparent MITM.
   - What's unclear: do non-codex profiles ever hit api.openai.com?
   - Recommendation: Gate on `p.Spec.Cli.Agent == "codex"`. If a non-codex profile uses OpenAI SDK directly, operator adds an explicit flag (out of scope for Phase 88). Document in plan that future phases may broaden.

3. **Should we support OpenAI Assistants API / Threads API?**
   - What we know: CONTEXT.md explicitly defers these.
   - Recommendation: Defer (locked decision). `ExtractOpenAITokens` should silently return zeros on bodies that don't match Responses or Chat shapes — no error.

4. **What model ID does the `gpt-5.3-codex-spark` variant report in `response.model`?**
   - What we know: It's listed in Codex docs as a model; pricing is presumably same as `gpt-5.3-codex`.
   - What's unclear: Whether real traffic uses the alias or dated variants like `gpt-5.3-codex-2026-04-01`.
   - Recommendation: Ship with alias-only entry; add dated variants reactively when real traffic surfaces unknown-model warnings in `km status` / proxy logs.

5. **Cache-pricing fidelity for older models** — some sources list cached price = input price for o1-mini / o3-mini (no cache discount). Verify against OpenAI's authoritative pricing page during plan-check. If verified, set `CachedInputPricePer1KTokens` = `InputPricePer1KTokens` for those entries.

## Validation Architecture

(`workflow.nyquist_validation: true` in `.planning/config.json`.)

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (no third-party — match existing sidecar tests) |
| Config file | None — `go test` discovers `*_test.go` by convention |
| Quick run command | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAI -count=1` |
| Full suite command | `go test ./sidecars/http-proxy/... -count=1` |
| Integration command | `go test ./sidecars/http-proxy/httpproxy/ -run TestHTTPProxy_OpenAI -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| OAI-BUDGET-01 | `ExtractOpenAITokens` parses Responses API SSE final event | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_ResponsesAPI_SSE -count=1` | ❌ Wave 0 |
| OAI-BUDGET-01 | `ExtractOpenAITokens` parses Chat Completions SSE final chunk with usage | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_ChatCompletions_SSE -count=1` | ❌ Wave 0 |
| OAI-BUDGET-01 | `ExtractOpenAITokens` parses non-streaming Responses API JSON body | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_NonStreaming -count=1` | ❌ Wave 0 |
| OAI-BUDGET-01 | Empty body returns zero counts, no error | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_EmptyBody -count=1` | ❌ Wave 0 |
| OAI-BUDGET-01 | Cache tokens parsed from `input_tokens_details.cached_tokens` | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_CacheTokens -count=1` | ❌ Wave 0 |
| OAI-BUDGET-01 | Reasoning tokens parsed from `output_tokens_details.reasoning_tokens` (observability only, not summed) | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_ReasoningTokens -count=1` | ❌ Wave 0 |
| OAI-BUDGET-01 | Chat Completions stream WITHOUT `include_usage` returns zero counts (no panic) | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_ChatNoUsage -count=1` | ❌ Wave 0 |
| OAI-BUDGET-02 | `staticOpenAIRates` contains all required model IDs | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestOpenAIRateTableCompleteness -count=1` | ❌ Wave 0 |
| OAI-BUDGET-03 | `CalculateOpenAICost` correctly subtracts cached from input before billing | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestCalculateOpenAICost_CacheArithmetic -count=1` | ❌ Wave 0 |
| OAI-BUDGET-03 | Cost with zero cache equals base input + output calc | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestCalculateOpenAICost_ZeroCache -count=1` | ❌ Wave 0 |
| OAI-BUDGET-04 | `OpenAIBlockedResponse` returns 403 with correct JSON shape | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestOpenAIBlockedResponse -count=1` | ❌ Wave 0 |
| OAI-BUDGET-05 | Proxy with budget enabled meters synthetic OpenAI SSE response → DynamoDB stub captures correct SK | integration | `go test ./sidecars/http-proxy/httpproxy/ -run TestOpenAIAIByModelIntegration -count=1` | ❌ Wave 0 |
| OAI-BUDGET-05 | End-to-end: goproxy CONNECT to api.openai.com is MITM'd, response is metered | integration | `go test ./sidecars/http-proxy/httpproxy/ -run TestHTTPProxy_OpenAIMetered -count=1` | ❌ Wave 0 |
| OAI-BUDGET-06 | TransparentListener routes openai responses through meter | integration | `go test ./sidecars/http-proxy/httpproxy/ -run TestTransparent_OpenAI -count=1` | ❌ Wave 0 (transparent mode currently has no Anthropic test either — add at least one as defense) |
| OAI-BUDGET-07 | `buildL7ProxyHosts` appends `api.openai.com` when `spec.cli.agent == "codex"` | unit | `go test ./pkg/compiler/ -run TestL7ProxyHostsWithCodex -count=1` | ❌ Wave 0 |
| OAI-BUDGET-09 | Manual UAT — Live Codex sandbox makes calls, `km status` shows per-model rows | manual | UAT runbook documented in 88-XX-UAT.md | manual-only — requires live Codex sandbox + OpenAI API key |

### Sampling Rate
- **Per task commit:** `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAI -count=1` (~5 sec)
- **Per wave merge:** `go test ./sidecars/http-proxy/... -count=1 && go test ./pkg/compiler/ -run TestL7Proxy -count=1` (~20 sec)
- **Phase gate:** `go test ./... -count=1` (full repo build + test — ~3-5 min) before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `sidecars/http-proxy/httpproxy/openai_test.go` — new file, mirrors `anthropic_test.go` (covers OAI-BUDGET-01..04)
- [ ] Test fixtures embedded as Go raw strings inline (match `anthropic_test.go:35-48` SSE pattern — no separate testdata files needed; OpenAI SSE is similarly small)
- [ ] Extension to `sidecars/http-proxy/httpproxy/http_proxy_test.go` for end-to-end OpenAI integration test (mirror existing Anthropic budget integration test if one exists; otherwise net-new)
- [ ] Extension to `pkg/compiler/userdata_test.go` — new `TestL7ProxyHostsWithCodex` test case
- [ ] UAT runbook `88-XX-UAT.md` for live-sandbox verification (single-sandbox manual test; takes ~10 min)
- [ ] Framework install: none needed — Go stdlib testing already used everywhere

## Sources

### Primary (HIGH confidence)

- **Codebase patterns** — `sidecars/http-proxy/httpproxy/anthropic.go` (line-for-line template), `bedrock.go` (host regex + 403 pattern), `proxy.go:316-431` (handler block pattern), `transparent.go:264-432` (transparent meter), `pkg/aws/budget.go:65` (IncrementAISpend), `metering_reader.go` (tee reader)
- **OpenAI Responses API streaming events** — [community.openai.com/t/responses-api-streaming-the-simple-guide-to-events/1363122](https://community.openai.com/t/responses-api-streaming-the-simple-guide-to-events/1363122) — exact JSON shape of `response.completed`, 53 event types enumerated, usage object schema with `input_tokens_details.cached_tokens` and `output_tokens_details.reasoning_tokens`
- **OpenAI Codex CLI Responses API requirement** — [github.com/openai/codex/discussions/7782](https://github.com/openai/codex/discussions/7782) — Chat Completions hard removed Feb 2026, `wire_api = "responses"` is only supported value
- **OpenAI API pricing (current)** — [developers.openai.com/api/docs/pricing](https://developers.openai.com/api/docs/pricing) — gpt-5.5 ($5/$0.50/$30), gpt-5.4 family, gpt-5.3-codex ($1.75/$0.175/$14)
- **Codex models docs** — [developers.openai.com/codex/models](https://developers.openai.com/codex/models) — model IDs: gpt-5.5, gpt-5.4, gpt-5.4-mini, gpt-5.3-codex, gpt-5.3-codex-spark, gpt-5.2
- **Codex config reference** — [developers.openai.com/codex/config-reference](https://developers.openai.com/codex/config-reference) — `"responses" is the only supported value and is the default`
- **OpenAI prompt caching** — [developers.openai.com/api/docs/guides/prompt-caching](https://developers.openai.com/api/docs/guides/prompt-caching) — `cached_tokens` is subset of input/prompt tokens (not additive)

### Secondary (MEDIUM confidence)

- **Pricing cross-verification** — [pricepertoken.com/pricing-page/provider/openai](https://pricepertoken.com/pricing-page/provider/openai) — corroborates developers.openai.com prices; also lists older models (gpt-4o, gpt-4.1, o1, o1-mini, o3-mini) not on the current OpenAI page
- **Chat Completions usage SSE shape** — [community.openai.com/t/usage-stats-now-available-when-using-streaming-with-the-chat-completions-api-or-completions-api/738156](https://community.openai.com/t/usage-stats-now-available-when-using-streaming-with-the-chat-completions-api-or-completions-api/738156) — final chunk has `choices: []` + `usage: {prompt_tokens, completion_tokens, total_tokens}`; `prompt_tokens_details.cached_tokens` available
- **GPT-5.5 pricing breakdown** — [apidog.com/blog/gpt-5-5-pricing/](https://apidog.com/blog/gpt-5-5-pricing/) — confirms $5/$30 standard, $2.50/$15 batch, $12.50/$75 priority
- **OpenAI Streaming Responses guide** — [developers.openai.com/api/docs/guides/streaming-responses](https://developers.openai.com/api/docs/guides/streaming-responses) — overview of event types and lifecycle

### Tertiary (LOW confidence — flag for plan-time re-verify)

- **gpt-5.3-codex-spark exact pricing** — listed in codex models docs but not in main pricing table; assumed same as gpt-5.3-codex. Verify during plan-check.
- **o1-mini / o3-mini cache pricing** — pricepertoken shows `cached = input` (no discount); confirm during plan-check from authoritative OpenAI source.
- **Whether `response.model` field always echoes back the requested ID verbatim, or substitutes dated variants** — OpenAI behavior has historically been "echoes exactly what you sent" but dated-variant substitution does occur for some snapshots. Defensive: include both aliases and known dated variants in price table.

## Metadata

**Confidence breakdown:**
- Standard stack (Go stdlib, goproxy, existing helpers): HIGH — all already used by anthropic.go/bedrock.go
- Architecture (file layout, handler registration order, tee-reader pattern): HIGH — copy-paste-modify from anthropic.go
- Responses API streaming format: HIGH — confirmed against official streaming-events reference
- Chat Completions streaming format: HIGH — long-established
- Pricing: MEDIUM — current as of 2026-05-24; quarterly drift expected. Include `// Verified 2026-05-24` comments in source.
- L7 proxy host gating decision: MEDIUM — current `useBedrock` gate is suspicious; recommend new `codex` gate without touching existing logic.
- Cache pricing model: MEDIUM — OpenAI's "cached_tokens is subset of input" semantics differs from Anthropic; cost calc must subtract before applying input rate.
- Pitfalls: HIGH — patterns from cross-referencing Anthropic test suite (anthropic_test.go covers 13 cases; mirror gives ~13 OpenAI cases).

**Research date:** 2026-05-24
**Valid until:** 2026-08-24 for stack/architecture; **2026-06-24 for pricing** (OpenAI pricing changes ~quarterly).

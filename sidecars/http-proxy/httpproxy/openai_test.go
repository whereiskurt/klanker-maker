// openai_test.go — Wave 0 RED tests for OpenAI metering (Phase 88).
// These tests reference symbols defined in openai.go which lands in 88-04.
// Until then they fail with `undefined: httpproxy.Extract*` — that is intentional.
package httpproxy_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

// ---------------------------------------------------------------------------
// Task 1: Extractor tests (7) + blocked-response test (1)
// ---------------------------------------------------------------------------

// Test 1 — Responses API SSE extraction (primary path for Codex CLI v0.131+).
// Codex CLI uses /v1/responses exclusively (chat/completions hard-removed Feb 2026).
// The final event is "response.completed" and carries the full usage object.
func TestExtractOpenAITokens_ResponsesAPI_SSE(t *testing.T) {
	sseBody := strings.NewReader(
		"event: response.created\n" +
			`data: {"type":"response.created","response":{"id":"resp_abc","model":"gpt-5.5","status":"in_progress","output":[]}}` + "\n" +
			"\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","sequence_number":1,"delta":"Hello"}` + "\n" +
			"\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","sequence_number":2,"delta":" world"}` + "\n" +
			"\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","sequence_number":42,"response":{"id":"resp_abc","model":"gpt-5.5","status":"completed","output":[],"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":0},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":165}}}` + "\n" +
			"\n",
	)

	modelID, inputTokens, outputTokens, _, _, err := httpproxy.ExtractOpenAITokens(sseBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modelID != "gpt-5.5" {
		t.Errorf("modelID = %q, want %q", modelID, "gpt-5.5")
	}
	if inputTokens != 120 {
		t.Errorf("inputTokens = %d, want 120", inputTokens)
	}
	if outputTokens != 45 {
		t.Errorf("outputTokens = %d, want 45", outputTokens)
	}
}

// Test 2 — Chat Completions SSE extraction with stream_options.include_usage=true.
// The final chunk has choices:[] and usage populated (non-final chunks have usage:null).
// Source: https://community.openai.com/t/usage-stats-now-available.../738156
func TestExtractOpenAITokens_ChatCompletions_SSE(t *testing.T) {
	sseBody := strings.NewReader(
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}],"usage":null}` + "\n" +
			"\n" +
			`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}],"usage":null}` + "\n" +
			"\n" +
			`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":45,"total_tokens":165,"prompt_tokens_details":{"cached_tokens":0}}}` + "\n" +
			"\n" +
			"data: [DONE]\n" +
			"\n",
	)

	modelID, inputTokens, outputTokens, _, _, err := httpproxy.ExtractOpenAITokens(sseBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modelID != "gpt-4o" {
		t.Errorf("modelID = %q, want %q", modelID, "gpt-4o")
	}
	if inputTokens != 120 {
		t.Errorf("inputTokens = %d, want 120", inputTokens)
	}
	if outputTokens != 45 {
		t.Errorf("outputTokens = %d, want 45", outputTokens)
	}
}

// Test 3 — Non-streaming Responses API JSON body.
// OpenAI /v1/responses returns a top-level JSON object with model + usage fields.
func TestExtractOpenAITokens_NonStreaming(t *testing.T) {
	body := strings.NewReader(`{"id":"resp_abc","object":"response","model":"gpt-5.5","status":"completed","output":[],"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":0},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":165}}`)

	modelID, inputTokens, outputTokens, _, _, err := httpproxy.ExtractOpenAITokens(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modelID != "gpt-5.5" {
		t.Errorf("modelID = %q, want %q", modelID, "gpt-5.5")
	}
	if inputTokens != 120 {
		t.Errorf("inputTokens = %d, want 120", inputTokens)
	}
	if outputTokens != 45 {
		t.Errorf("outputTokens = %d, want 45", outputTokens)
	}
}

// Test 4 — Empty body returns zero counts, no error (mirrors anthropic_test.go:67).
func TestExtractOpenAITokens_EmptyBody(t *testing.T) {
	body := strings.NewReader("")
	modelID, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens, err := httpproxy.ExtractOpenAITokens(body)
	if err != nil {
		t.Fatalf("unexpected error on empty body: %v", err)
	}
	if modelID != "" {
		t.Errorf("modelID = %q, want empty string", modelID)
	}
	if inputTokens != 0 {
		t.Errorf("inputTokens = %d, want 0", inputTokens)
	}
	if outputTokens != 0 {
		t.Errorf("outputTokens = %d, want 0", outputTokens)
	}
	if cachedInputTokens != 0 {
		t.Errorf("cachedInputTokens = %d, want 0", cachedInputTokens)
	}
	if reasoningOutputTokens != 0 {
		t.Errorf("reasoningOutputTokens = %d, want 0", reasoningOutputTokens)
	}
}

// Test 5 — Cache tokens from input_tokens_details.cached_tokens.
//
// RESEARCHER PITFALL #4: OpenAI's input_tokens INCLUDES cached_tokens (they are a subset,
// not additive). The extractor must return input_tokens as-is and cachedInputTokens as
// the cache subset. The CalculateOpenAICost function (not the extractor) is responsible
// for subtracting cached from input before billing. Do NOT subtract in the extractor.
func TestExtractOpenAITokens_CacheTokens(t *testing.T) {
	body := strings.NewReader(`{"id":"resp_abc","object":"response","model":"gpt-5.5","status":"completed","output":[],"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":80},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":165}}`)

	_, inputTokens, _, cachedInputTokens, _, err := httpproxy.ExtractOpenAITokens(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// input_tokens is the INCLUSIVE total (includes cached). Must return 120, not 120-80=40.
	if inputTokens != 120 {
		t.Errorf("inputTokens = %d, want 120 (inclusive of cached; do NOT subtract in extractor)", inputTokens)
	}
	if cachedInputTokens != 80 {
		t.Errorf("cachedInputTokens = %d, want 80", cachedInputTokens)
	}
}

// Test 6 — Reasoning tokens from output_tokens_details.reasoning_tokens.
//
// RESEARCHER PITFALL #3: output_tokens is the INCLUSIVE total (includes reasoning tokens).
// Reasoning tokens are billed at the same rate as output tokens (no separate tier).
// Return reasoningOutputTokens for observability only — do NOT sum into outputTokens.
func TestExtractOpenAITokens_ReasoningTokens(t *testing.T) {
	body := strings.NewReader(`{"id":"resp_abc","object":"response","model":"gpt-5.5","status":"completed","output":[],"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":0},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":12},"total_tokens":165}}`)

	_, _, outputTokens, _, reasoningOutputTokens, err := httpproxy.ExtractOpenAITokens(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// output_tokens is the INCLUSIVE total (includes reasoning). Must return 45, not 45+12=57.
	if outputTokens != 45 {
		t.Errorf("outputTokens = %d, want 45 (inclusive of reasoning; do NOT double-count)", outputTokens)
	}
	if reasoningOutputTokens != 12 {
		t.Errorf("reasoningOutputTokens = %d, want 12 (observability only, not summed)", reasoningOutputTokens)
	}
}

// Test 7 — Chat Completions SSE WITHOUT include_usage: zero counts, no panic, no error.
//
// RESEARCHER PITFALL #2: Older SDK callers set stream:true but not stream_options.include_usage.
// The final chunk has choices:[] but usage:null. Silent metering miss is acceptable for v1
// (Codex CLI always sets include_usage; only raw SDK callers are affected).
// Implementer should log WARN "openai chat completions response missing usage field".
func TestExtractOpenAITokens_ChatNoUsage(t *testing.T) {
	sseBody := strings.NewReader(
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n" +
			"\n" +
			`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}` + "\n" +
			"\n" +
			"data: [DONE]\n" +
			"\n",
	)

	_, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens, err := httpproxy.ExtractOpenAITokens(sseBody)
	if err != nil {
		t.Fatalf("unexpected error on no-usage body: %v", err)
	}
	// All counts must be zero — silent metering miss is acceptable.
	if inputTokens != 0 {
		t.Errorf("inputTokens = %d, want 0 (no usage in stream)", inputTokens)
	}
	if outputTokens != 0 {
		t.Errorf("outputTokens = %d, want 0 (no usage in stream)", outputTokens)
	}
	if cachedInputTokens != 0 {
		t.Errorf("cachedInputTokens = %d, want 0", cachedInputTokens)
	}
	if reasoningOutputTokens != 0 {
		t.Errorf("reasoningOutputTokens = %d, want 0", reasoningOutputTokens)
	}
}

// Test 8 — Budget blocked response returns 403 with expected JSON fields.
// Mirrors TestAnthropicBlockedResponse (anthropic_test.go:104-133) exactly.
func TestOpenAIBlockedResponse(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/responses", nil)
	resp := httpproxy.OpenAIBlockedResponse(req, "sb-test", "gpt-5.5", 5.00, 5.00)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode 403 body: %v", err)
	}

	if body["error"] != "ai_budget_exhausted" {
		t.Errorf("error field = %v, want ai_budget_exhausted", body["error"])
	}
	if _, ok := body["spent"]; !ok {
		t.Error("missing 'spent' field in 403 body")
	}
	if _, ok := body["limit"]; !ok {
		t.Error("missing 'limit' field in 403 body")
	}
	if _, ok := body["model"]; !ok {
		t.Error("missing 'model' field in 403 body")
	}
	if _, ok := body["topUp"]; !ok {
		t.Error("missing 'topUp' field in 403 body")
	}
}

// ---------------------------------------------------------------------------
// Task 2: Rate-table completeness test (OAI-BUDGET-02)
// ---------------------------------------------------------------------------

// Test 9 — Rate table completeness: StaticOpenAIRates covers all required model IDs.
// Mirrors TestAnthropicRateTableCompleteness (anthropic_test.go:135-165).
//
// NOTE for 88-04 implementer: RESEARCH.md § Open Questions #5 flagged that o1-mini and
// o3-mini may have CachedInputPricePer1KTokens == InputPricePer1KTokens (no cache discount).
// Verify against the OpenAI authoritative pricing page before shipping 88-04.
// This test asserts key presence only — it does NOT assert the cache rate value.
func TestOpenAIRateTableCompleteness(t *testing.T) {
	required := []string{
		// GPT-5.5 (current flagship)
		"gpt-5.5", "gpt-5.5-pro",
		// GPT-5.4 family
		"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.4-pro",
		// GPT-5.3 Codex family (most important for Phase 88)
		"gpt-5.3-codex", "gpt-5.3-codex-spark",
		// GPT-4o family (legacy)
		"gpt-4o", "gpt-4o-2024-08-06", "gpt-4o-2024-11-20",
		"gpt-4o-mini", "gpt-4o-mini-2024-07-18",
		// GPT-4.1 family
		"gpt-4.1", "gpt-4.1-mini",
		// O-series reasoning
		"o1", "o1-mini", "o3-mini",
	}

	rates := httpproxy.StaticOpenAIRates()
	for _, modelID := range required {
		if _, ok := rates[modelID]; !ok {
			t.Errorf("StaticOpenAIRates missing entry for model %q", modelID)
		}
	}
	if t.Failed() {
		keys := make([]string, 0, len(rates))
		for k := range rates {
			keys = append(keys, k)
		}
		t.Logf("present model IDs: %v", keys)
	}
}

// ---------------------------------------------------------------------------
// Task 3: Cost calculation tests (OAI-BUDGET-03)
// ---------------------------------------------------------------------------

// TestCalculateOpenAICost_CacheArithmetic verifies that cached input tokens are billed
// at the cache rate (not the full input rate) and that the subtraction is correct.
//
// Arithmetic (gpt-5.4 rates: input=$0.0025/1K, cached=$0.00025/1K, output=$0.015/1K):
//   - input_tokens = 1000 (INCLUSIVE of cached_tokens)
//   - cached_tokens = 200 (subset of input_tokens)
//   - uncached_input = 1000 - 200 = 800
//   - input_cost    = 800  * 0.0025  / 1000 = 0.002
//   - cached_cost   = 200  * 0.00025 / 1000 = 0.00005
//   - output_cost   = 500  * 0.015   / 1000 = 0.0075
//   - total                                 = 0.00975
//
// RESEARCHER PITFALL #4: Do NOT use inputTokens directly without subtracting cached.
// The buggy formula (inputTokens * fullRate) yields 0.0025+0.00005+0.0075 = 0.01255,
// not the correct 0.00975. This test is sensitive to that exact bug.
func TestCalculateOpenAICost_CacheArithmetic(t *testing.T) {
	rates := httpproxy.StaticOpenAIRates()
	rate, ok := rates["gpt-5.4"]
	if !ok {
		t.Fatal("gpt-5.4 not in StaticOpenAIRates")
	}

	// Hard-coded expected value acts as a regression guard against cost formula refactors.
	const (
		wantUncachedInputCost = 0.002   // 800 * 0.0025 / 1000
		wantCachedCost        = 0.00005 // 200 * 0.00025 / 1000
		wantOutputCost        = 0.0075  // 500 * 0.015 / 1000
		want                  = wantUncachedInputCost + wantCachedCost + wantOutputCost // 0.00975
		epsilon               = 1e-10
	)

	got := httpproxy.CalculateOpenAICost(1000, 500, 200, rate)
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		t.Errorf("CalculateOpenAICost(1000, 500, 200, gpt-5.4) = %f, want %f (check: cached must be subtracted from input before billing)", got, want)
	}
}

// TestCalculateOpenAICost_ZeroCache verifies that when cachedInputTokens=0, the result
// equals a straightforward input+output cost (no cache discount applied).
//
// Arithmetic (gpt-5.4 rates: input=$0.0025/1K, output=$0.015/1K):
//   - input_cost  = 1000 * 0.0025 / 1000 = 0.0025
//   - output_cost = 500  * 0.015  / 1000 = 0.0075
//   - total                               = 0.010
func TestCalculateOpenAICost_ZeroCache(t *testing.T) {
	rates := httpproxy.StaticOpenAIRates()
	rate, ok := rates["gpt-5.4"]
	if !ok {
		t.Fatal("gpt-5.4 not in StaticOpenAIRates")
	}

	// Hard-coded expected value acts as a regression guard.
	const (
		want    = 0.0025 + 0.0075 // = 0.010
		epsilon = 1e-10
	)

	got := httpproxy.CalculateOpenAICost(1000, 500, 0, rate)
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		t.Errorf("CalculateOpenAICost(1000, 500, 0, gpt-5.4) = %f, want %f (zero-cache case should equal plain input+output cost)", got, want)
	}
}

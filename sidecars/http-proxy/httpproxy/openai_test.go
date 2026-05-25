// openai_test.go — RED/GREEN tests for OpenAI metering (Phase 88).
// Wave 0 tests (88-01) that turn GREEN when openai.go lands (88-04).
// Mirrors anthropic_test.go line-for-line with OpenAI-specific SSE fixtures,
// model IDs, and cache-subtract cost arithmetic.
package httpproxy_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

// ---------------------------------------------------------------------------
// Test 1 — Responses API SSE streaming extraction (primary Codex path).
// ---------------------------------------------------------------------------

func TestExtractOpenAITokens_ResponsesAPI_SSE(t *testing.T) {
	sseBody := strings.NewReader(
		"event: response.created\n" +
			`data: {"type":"response.created","response":{"id":"resp_abc","model":"gpt-5.5","status":"in_progress","output":[]}}` + "\n" +
			"\n" +
			"event: response.output_text.delta\n" +
			`data: {"type":"response.output_text.delta","sequence_number":1,"delta":"Hello world"}` + "\n" +
			"\n" +
			"event: response.completed\n" +
			`data: {"type":"response.completed","sequence_number":42,"response":{"id":"resp_abc","model":"gpt-5.5","status":"completed","usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":0},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":165}}}` + "\n" +
			"\n",
	)

	modelID, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens, err := httpproxy.ExtractOpenAITokens(sseBody)
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
	if cachedInputTokens != 0 {
		t.Errorf("cachedInputTokens = %d, want 0", cachedInputTokens)
	}
	if reasoningOutputTokens != 0 {
		t.Errorf("reasoningOutputTokens = %d, want 0", reasoningOutputTokens)
	}
}

// ---------------------------------------------------------------------------
// Test 2 — Chat Completions SSE (with stream_options.include_usage=true).
// ---------------------------------------------------------------------------

func TestExtractOpenAITokens_ChatCompletions_SSE(t *testing.T) {
	sseBody := strings.NewReader(
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n" +
			"\n" +
			`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}` + "\n" +
			"\n" +
			`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","created":1693600060,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":120,"completion_tokens":45,"total_tokens":165}}` + "\n" +
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

// ---------------------------------------------------------------------------
// Test 3 — Non-streaming Responses API JSON body.
// ---------------------------------------------------------------------------

func TestExtractOpenAITokens_NonStreaming(t *testing.T) {
	body := strings.NewReader(
		`{"id":"resp_abc","object":"response","model":"gpt-5.5","status":"completed","usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":0},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":165}}`,
	)

	modelID, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens, err := httpproxy.ExtractOpenAITokens(body)
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
	if cachedInputTokens != 0 {
		t.Errorf("cachedInputTokens = %d, want 0", cachedInputTokens)
	}
	if reasoningOutputTokens != 0 {
		t.Errorf("reasoningOutputTokens = %d, want 0", reasoningOutputTokens)
	}
}

// ---------------------------------------------------------------------------
// Test 4 — Empty body returns zero counts, no error.
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Test 5 — Cache tokens parsed as SUBSET of input (not subtracted in extractor).
// RESEARCH.md Pitfall 4: CalculateOpenAICost does the subtraction, not ExtractOpenAITokens.
// ---------------------------------------------------------------------------

func TestExtractOpenAITokens_CacheTokens(t *testing.T) {
	body := strings.NewReader(
		`{"id":"resp_abc","object":"response","model":"gpt-5.5","status":"completed","usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":80},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":165}}`,
	)

	_, inputTokens, _, cachedInputTokens, _, err := httpproxy.ExtractOpenAITokens(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// inputTokens must be the INCLUSIVE total (120), not input minus cached (40).
	if inputTokens != 120 {
		t.Errorf("inputTokens = %d, want 120 (inclusive — cached is subset, not additive)", inputTokens)
	}
	if cachedInputTokens != 80 {
		t.Errorf("cachedInputTokens = %d, want 80", cachedInputTokens)
	}
}

// ---------------------------------------------------------------------------
// Test 6 — Reasoning tokens parsed as SUBSET of output (observability only).
// RESEARCH.md Pitfall 3: reasoning_tokens already counted in output_tokens.
// ---------------------------------------------------------------------------

func TestExtractOpenAITokens_ReasoningTokens(t *testing.T) {
	body := strings.NewReader(
		`{"id":"resp_abc","object":"response","model":"gpt-5.5","status":"completed","usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":0},"output_tokens":45,"output_tokens_details":{"reasoning_tokens":12},"total_tokens":165}}`,
	)

	_, _, outputTokens, _, reasoningOutputTokens, err := httpproxy.ExtractOpenAITokens(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// outputTokens must be INCLUSIVE total (45), not output minus reasoning (33).
	if outputTokens != 45 {
		t.Errorf("outputTokens = %d, want 45 (inclusive — reasoning is subset, not additive)", outputTokens)
	}
	if reasoningOutputTokens != 12 {
		t.Errorf("reasoningOutputTokens = %d, want 12", reasoningOutputTokens)
	}
}

// ---------------------------------------------------------------------------
// Test 7 — Chat Completions SSE WITHOUT include_usage — zero counts, no panic.
// RESEARCH.md Pitfall 2: silent metering miss is acceptable; no crash required.
// ---------------------------------------------------------------------------

func TestExtractOpenAITokens_ChatNoUsage(t *testing.T) {
	sseBody := strings.NewReader(
		`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n" +
			"\n" +
			`data: {"id":"chatcmpl-abc","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n" +
			"\n" +
			"data: [DONE]\n" +
			"\n",
	)

	_, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens, err := httpproxy.ExtractOpenAITokens(sseBody)
	if err != nil {
		t.Fatalf("unexpected error on SSE without usage: %v", err)
	}
	if inputTokens != 0 {
		t.Errorf("inputTokens = %d, want 0 (no usage field in SSE)", inputTokens)
	}
	if outputTokens != 0 {
		t.Errorf("outputTokens = %d, want 0 (no usage field in SSE)", outputTokens)
	}
	if cachedInputTokens != 0 {
		t.Errorf("cachedInputTokens = %d, want 0", cachedInputTokens)
	}
	if reasoningOutputTokens != 0 {
		t.Errorf("reasoningOutputTokens = %d, want 0", reasoningOutputTokens)
	}
}

// ---------------------------------------------------------------------------
// Test 8 — OpenAIBlockedResponse returns 403 with correct JSON shape.
// Mirrors TestAnthropicBlockedResponse (anthropic_test.go:104-133).
// ---------------------------------------------------------------------------

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
// Test 9 — Rate table completeness: staticOpenAIRates contains all required model IDs.
// Mirrors TestAnthropicRateTableCompleteness (anthropic_test.go:135-165).
//
// NOTE: o1-mini and o3-mini may have CachedInputPricePer1KTokens == InputPricePer1KTokens
// (no cache discount) per RESEARCH.md Open Questions 5. This test asserts key presence
// only — not cache rate values.
// ---------------------------------------------------------------------------

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
			t.Errorf("staticOpenAIRates missing entry for model %q", modelID)
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
// Test 10 — CalculateOpenAICost: cache-subtract arithmetic.
// RESEARCH.md Pitfall 4: cached_tokens is subset of input — subtract before billing.
//
// input=1000, output=500, cached=200 on gpt-5.4 (input=$0.0025/1K, cached=$0.00025/1K, output=$0.015/1K):
//   uncached_input = 1000 - 200 = 800
//   input_cost     = 800 * 0.0025 / 1000   = 0.002
//   cached_cost    = 200 * 0.00025 / 1000  = 0.00005
//   output_cost    = 500 * 0.015 / 1000    = 0.0075
//   total          = 0.002 + 0.00005 + 0.0075 = 0.00955
//
// Buggy non-subtracting version: input_cost = 1000 * 0.0025 / 1000 = 0.0025 -> total 0.01255 (WRONG).
// ---------------------------------------------------------------------------

func TestCalculateOpenAICost_CacheArithmetic(t *testing.T) {
	rates := httpproxy.StaticOpenAIRates()
	rate, ok := rates["gpt-5.4"]
	if !ok {
		t.Fatal("gpt-5.4 not in StaticOpenAIRates")
	}

	const (
		wantCost = 0.00955 // = 0.002 + 0.00005 + 0.0075
		epsilon  = 1e-10
	)

	got := httpproxy.CalculateOpenAICost(1000, 500, 200, rate)
	diff := got - wantCost
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		t.Errorf("CalculateOpenAICost(1000, 500, 200, gpt-5.4) = %.10f, want %.10f (cache-subtract mismatch)", got, wantCost)
	}
}

// ---------------------------------------------------------------------------
// Test 11 — CalculateOpenAICost: zero cached equals base cost.
//
// input=1000, output=500, cached=0 on gpt-5.4:
//   total = 1000*0.0025/1000 + 500*0.015/1000 = 0.0025 + 0.0075 = 0.010
// ---------------------------------------------------------------------------

func TestCalculateOpenAICost_ZeroCache(t *testing.T) {
	rates := httpproxy.StaticOpenAIRates()
	rate, ok := rates["gpt-5.4"]
	if !ok {
		t.Fatal("gpt-5.4 not in StaticOpenAIRates")
	}

	const (
		wantCost = 0.010 // = 0.0025 + 0.0075 (no cache component)
		epsilon  = 1e-10
	)

	got := httpproxy.CalculateOpenAICost(1000, 500, 0, rate)
	diff := got - wantCost
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		t.Errorf("CalculateOpenAICost(1000, 500, 0, gpt-5.4) = %.10f, want %.10f", got, wantCost)
	}
}

// Note: TestOpenAIAIByModelIntegration is defined in http_proxy_test.go (Phase 88 Wave 0
// integration tests were placed there alongside TestHTTPProxy_OpenAIMetered and
// TestTransparent_OpenAI). No duplication needed here.

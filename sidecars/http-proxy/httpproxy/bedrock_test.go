package httpproxy_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/sidecars/http-proxy/httpproxy"
)

// TestExtractBedrockTokens_SSEStream verifies SSE stream parsing for streaming responses.
func TestExtractBedrockTokens_SSEStream(t *testing.T) {
	// Simulated SSE stream with message_start (input_tokens=25) and message_delta (output_tokens=15).
	sseBody := strings.NewReader(
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1}}}` + "\n" +
			"\n" +
			"event: content_block_start\n" +
			`data: {"type":"content_block_start","index":0}` + "\n" +
			"\n" +
			"event: message_delta\n" +
			`data: {"type":"message_delta","usage":{"output_tokens":15}}` + "\n" +
			"\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n" +
			"\n",
	)

	inputTokens, outputTokens, err := httpproxy.ExtractBedrockTokens(sseBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inputTokens != 25 {
		t.Errorf("inputTokens = %d, want 25", inputTokens)
	}
	if outputTokens != 15 {
		t.Errorf("outputTokens = %d, want 15", outputTokens)
	}
}

// TestExtractBedrockTokens_NonStreaming verifies non-streaming JSON response parsing.
func TestExtractBedrockTokens_NonStreaming(t *testing.T) {
	body := strings.NewReader(`{"usage":{"input_tokens":10,"output_tokens":5},"content":[{"text":"hello"}]}`)

	inputTokens, outputTokens, err := httpproxy.ExtractBedrockTokens(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inputTokens != 10 {
		t.Errorf("inputTokens = %d, want 10", inputTokens)
	}
	if outputTokens != 5 {
		t.Errorf("outputTokens = %d, want 5", outputTokens)
	}
}

// TestExtractBedrockTokens_EmptyBody verifies empty body returns (0, 0, nil).
func TestExtractBedrockTokens_EmptyBody(t *testing.T) {
	body := strings.NewReader("")

	inputTokens, outputTokens, err := httpproxy.ExtractBedrockTokens(body)
	if err != nil {
		t.Fatalf("unexpected error on empty body: %v", err)
	}
	if inputTokens != 0 {
		t.Errorf("inputTokens = %d, want 0", inputTokens)
	}
	if outputTokens != 0 {
		t.Errorf("outputTokens = %d, want 0", outputTokens)
	}
}

// TestBudgetCache_HitWithinTTL verifies cache returns entry within 10s TTL.
func TestBudgetCache_HitWithinTTL(t *testing.T) {
	cache := httpproxy.NewBudgetCache()

	entry := &httpproxy.BudgetEntry{
		ComputeLimit: 10.0,
		AILimit:      5.0,
		AISpent:      1.0,
	}
	cache.Set("sb-test", entry)

	got := cache.Get("sb-test")
	if got == nil {
		t.Fatal("expected cache hit within TTL, got nil")
	}
	if got.AILimit != 5.0 {
		t.Errorf("AILimit = %f, want 5.0", got.AILimit)
	}
}

// TestBudgetCache_MissAfterTTL verifies cache returns nil after TTL expires.
func TestBudgetCache_MissAfterTTL(t *testing.T) {
	cache := httpproxy.NewBudgetCacheWithTTL(50 * time.Millisecond)

	entry := &httpproxy.BudgetEntry{
		AILimit: 5.0,
		AISpent: 1.0,
	}
	cache.Set("sb-expire", entry)

	// Within TTL.
	if cache.Get("sb-expire") == nil {
		t.Fatal("expected cache hit before TTL expires")
	}

	time.Sleep(100 * time.Millisecond)

	// After TTL.
	if cache.Get("sb-expire") != nil {
		t.Fatal("expected cache miss after TTL, got hit")
	}
}

// TestBudgetCache_UpdateLocalSpend verifies optimistic local spend tracking.
func TestBudgetCache_UpdateLocalSpend(t *testing.T) {
	cache := httpproxy.NewBudgetCache()

	entry := &httpproxy.BudgetEntry{
		AILimit: 5.0,
		AISpent: 1.0,
	}
	cache.Set("sb-spend", entry)

	cache.UpdateLocalSpend("sb-spend", 0.50)

	got := cache.Get("sb-spend")
	if got == nil {
		t.Fatal("expected cache hit after UpdateLocalSpend")
	}
	if got.AISpent != 1.50 {
		t.Errorf("AISpent = %f, want 1.50", got.AISpent)
	}
}

// TestBlockedResponse verifies 403 response has parseable JSON body.
func TestBlockedResponse(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-sonnet-4-5/invoke", nil)
	resp := httpproxy.BedrockBlockedResponse(req, "sb-test", "anthropic.claude-sonnet-4-5", 5.00, 5.00)

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

// TestCalculateCost verifies correct USD calculation for known model rates.
func TestCalculateCost(t *testing.T) {
	tests := []struct {
		name         string
		inputTokens  int
		outputTokens int
		inputRate    float64
		outputRate   float64
		wantCost     float64
	}{
		{
			name:         "claude sonnet",
			inputTokens:  1000,
			outputTokens: 500,
			inputRate:    0.003,
			outputRate:   0.015,
			wantCost:     0.003*1 + 0.015*0.5, // 0.003 + 0.0075 = 0.0105
		},
		{
			name:         "zero tokens",
			inputTokens:  0,
			outputTokens: 0,
			inputRate:    0.003,
			outputRate:   0.015,
			wantCost:     0.0,
		},
		{
			name:         "haiku small request",
			inputTokens:  100,
			outputTokens: 50,
			inputRate:    0.00025,
			outputRate:   0.00125,
			wantCost:     0.00025*0.1 + 0.00125*0.05, // 0.000025 + 0.0000625 = 0.0000875
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := httpproxy.CalculateCost(tc.inputTokens, tc.outputTokens, tc.inputRate, tc.outputRate)
			const epsilon = 1e-10
			diff := got - tc.wantCost
			if diff < 0 {
				diff = -diff
			}
			if diff > epsilon {
				t.Errorf("CalculateCost(%d, %d, %.5f, %.5f) = %f, want %f",
					tc.inputTokens, tc.outputTokens, tc.inputRate, tc.outputRate, got, tc.wantCost)
			}
		})
	}
}

// TestExtractModelID verifies model ID extraction from Bedrock URL paths.
func TestExtractModelID(t *testing.T) {
	tests := []struct {
		path    string
		want    string
	}{
		{"/model/anthropic.claude-sonnet-4-5/invoke", "anthropic.claude-sonnet-4-5"},
		{"/model/anthropic.claude-3-haiku-20240307-v1:0/invoke-with-response-stream", "anthropic.claude-3-haiku-20240307-v1:0"},
		{"/model/anthropic.claude-opus-4-5/invoke", "anthropic.claude-opus-4-5"},
		{"/v1/model/meta.llama3/invoke", "meta.llama3"},
		{"/unknown/path", ""},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := httpproxy.ExtractModelID(tc.path)
			if got != tc.want {
				t.Errorf("ExtractModelID(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

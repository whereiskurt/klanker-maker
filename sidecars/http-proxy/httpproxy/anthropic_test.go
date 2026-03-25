package httpproxy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/sidecars/http-proxy/httpproxy"
)

// Test 1 — Non-streaming extraction.
func TestExtractAnthropicTokens_NonStreaming(t *testing.T) {
	body := strings.NewReader(`{"model":"claude-sonnet-4-6","usage":{"input_tokens":100,"output_tokens":50}}`)
	modelID, inputTokens, outputTokens, err := httpproxy.ExtractAnthropicTokens(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modelID != "claude-sonnet-4-6" {
		t.Errorf("modelID = %q, want %q", modelID, "claude-sonnet-4-6")
	}
	if inputTokens != 100 {
		t.Errorf("inputTokens = %d, want 100", inputTokens)
	}
	if outputTokens != 50 {
		t.Errorf("outputTokens = %d, want 50", outputTokens)
	}
}

// Test 2 — SSE streaming extraction.
func TestExtractAnthropicTokens_SSEStream(t *testing.T) {
	sseBody := strings.NewReader(
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"model":"claude-opus-4-6","usage":{"input_tokens":25,"output_tokens":1}}}` + "\n" +
			"\n" +
			"event: content_block_delta\n" +
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}` + "\n" +
			"\n" +
			"event: message_delta\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}` + "\n" +
			"\n" +
			"event: message_stop\n" +
			`data: {"type":"message_stop"}` + "\n" +
			"\n",
	)

	modelID, inputTokens, outputTokens, err := httpproxy.ExtractAnthropicTokens(sseBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modelID != "claude-opus-4-6" {
		t.Errorf("modelID = %q, want %q", modelID, "claude-opus-4-6")
	}
	if inputTokens != 25 {
		t.Errorf("inputTokens = %d, want 25", inputTokens)
	}
	if outputTokens != 15 {
		t.Errorf("outputTokens = %d, want 15", outputTokens)
	}
}

// Test 3 — Empty body.
func TestExtractAnthropicTokens_EmptyBody(t *testing.T) {
	body := strings.NewReader("")
	modelID, inputTokens, outputTokens, err := httpproxy.ExtractAnthropicTokens(body)
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
}

// Test 4 — Model ID from SSE message_start nested message.model field.
func TestExtractAnthropicTokens_ModelIDFromSSE(t *testing.T) {
	sseBody := strings.NewReader(
		"event: message_start\n" +
			`data: {"type":"message_start","message":{"id":"msg_01","model":"claude-haiku-4-5","usage":{"input_tokens":10,"output_tokens":1}}}` + "\n" +
			"\n" +
			"event: message_delta\n" +
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}` + "\n" +
			"\n",
	)

	modelID, _, _, err := httpproxy.ExtractAnthropicTokens(sseBody)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if modelID != "claude-haiku-4-5" {
		t.Errorf("modelID = %q, want %q", modelID, "claude-haiku-4-5")
	}
}

// Test 5 — Budget blocked response returns 403 with expected JSON fields.
func TestAnthropicBlockedResponse(t *testing.T) {
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	resp := httpproxy.AnthropicBlockedResponse(req, "sb-test", "claude-sonnet-4-6", 5.00, 5.00)

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

// Test 6 — Rate table completeness: staticAnthropicRates covers all 11 model IDs.
func TestAnthropicRateTableCompleteness(t *testing.T) {
	required := []string{
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
		"claude-haiku-4-5-20251001",
		"claude-opus-4-5-20251101",
		"claude-sonnet-4-5-20250929",
		"claude-opus-4-1-20250805",
		"claude-opus-4-20250514",
		"claude-sonnet-4-20250514",
		"claude-haiku-3-5-20241022",
		"claude-3-haiku-20240307",
	}

	rates := httpproxy.StaticAnthropicRates()
	for _, modelID := range required {
		if _, ok := rates[modelID]; !ok {
			t.Errorf("staticAnthropicRates missing entry for model %q", modelID)
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

// Test 7 — CalculateCost reuse with Anthropic rates.
// 1000 input + 500 output on claude-sonnet-4-6 @ $0.003/$0.015 = $0.003 + $0.0075 = $0.0105
func TestAnthropicCalculateCostReuse(t *testing.T) {
	rates := httpproxy.StaticAnthropicRates()
	rate, ok := rates["claude-sonnet-4-6"]
	if !ok {
		t.Fatal("claude-sonnet-4-6 not in staticAnthropicRates")
	}

	got := httpproxy.CalculateCost(1000, 500, rate.InputPricePer1KTokens, rate.OutputPricePer1KTokens)
	const want = 0.003 + 0.0075 // 0.0105
	const epsilon = 1e-10
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	if diff > epsilon {
		t.Errorf("CalculateCost(1000, 500, %.4f, %.4f) = %f, want %f",
			rate.InputPricePer1KTokens, rate.OutputPricePer1KTokens, got, want)
	}
}

// Test 8 — AIByModel integration: IncrementAISpend with an Anthropic model ID
// passes the modelID through the DynamoDB SK correctly. This confirms that
// km status will show Anthropic model spend in the per-model breakdown.
func TestAnthropicAIByModelIntegration(t *testing.T) {
	stub := &captureModelIDStub{}

	_, err := aws.IncrementAISpend(
		context.Background(),
		stub,
		"km-budgets",
		"sb-test",
		"claude-sonnet-4-6",
		1000,
		500,
		0.0105,
	)
	if err != nil {
		t.Fatalf("IncrementAISpend returned error: %v", err)
	}

	// IncrementAISpend writes SK = "BUDGET#ai#{modelID}".
	// Verify the captured SK encodes the Anthropic model ID.
	const expectedSK = "BUDGET#ai#claude-sonnet-4-6"
	if stub.capturedSK != expectedSK {
		t.Errorf("DynamoDB SK = %q, want %q", stub.capturedSK, expectedSK)
	}
}

// captureModelIDStub implements aws.BudgetAPI and captures the DynamoDB SK
// from UpdateItem to verify the model ID encoding.
type captureModelIDStub struct {
	capturedSK string
}

func (s *captureModelIDStub) UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if skAV, ok := input.Key["SK"]; ok {
		if sv, ok := skAV.(*dynamodbtypes.AttributeValueMemberS); ok {
			s.capturedSK = sv.Value
		}
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (s *captureModelIDStub) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}

func (s *captureModelIDStub) Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

// Package httpproxy — anthropic.go
// Anthropic direct API (api.anthropic.com) MITM interception: token extractor,
// static rate table, and 403 response builder for AI budget enforcement.
//
// ExtractAnthropicTokens handles both SSE streaming and non-streaming responses.
// The model ID is extracted from the response body (not the URL path — unlike Bedrock,
// Anthropic does not encode the model in the URL).
//
// Note: cache_creation_input_tokens and cache_read_input_tokens (prompt caching)
// are NOT metered in this implementation. Only base input_tokens and output_tokens
// are counted. This is a known conservative undercount when prompt caching is active.
// Prompt cache metering is tracked as a future improvement.
package httpproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/whereiskurt/klankrmkr/pkg/aws"
)

// anthropicMessageStartPayload peeks at model + usage from a message_start SSE event.
// Unlike the Bedrock messageStartPayload, this includes the Model field since
// Anthropic 1P responses carry the model in the response body, not the URL.
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

// anthropicNonStreamingPayload reads model + usage from a non-streaming JSON response.
// Unlike the Bedrock nonStreamingPayload, this includes the Model field.
type anthropicNonStreamingPayload struct {
	Model string `json:"model"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ExtractAnthropicTokens reads an Anthropic API response body (SSE streaming or
// plain JSON) and returns the model ID, input token count, and output token count.
// Returns ("", 0, 0, nil) on empty or unrecognized body.
// Caps the read at maxResponseBodySize (10 MB) and returns best-effort counts on overflow.
//
// For SSE responses:
//   - model ID comes from message_start.message.model
//   - input tokens come from message_start.message.usage.input_tokens
//   - output tokens come from message_delta.usage.output_tokens (cumulative final value)
//
// For non-streaming responses:
//   - model ID comes from the top-level "model" field
//   - input/output tokens come from the top-level "usage" object
func ExtractAnthropicTokens(body io.Reader) (modelID string, inputTokens, outputTokens int, err error) {
	lr := io.LimitReader(body, int64(maxResponseBodySize))
	data, _ := io.ReadAll(lr)
	if len(data) == 0 {
		return "", 0, 0, nil
	}

	// Try SSE parsing first (same structure as ExtractBedrockTokens).
	hasSSEEvents := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, sseDataPrefix) {
			continue
		}
		jsonData := strings.TrimPrefix(line, sseDataPrefix)

		// Peek at the type field.
		var typed genericTypePayload
		if jsonErr := json.Unmarshal([]byte(jsonData), &typed); jsonErr != nil {
			continue
		}

		switch typed.Type {
		case "message_start":
			hasSSEEvents = true
			var payload anthropicMessageStartPayload
			if jsonErr := json.Unmarshal([]byte(jsonData), &payload); jsonErr == nil {
				modelID = payload.Message.Model
				inputTokens = payload.Message.Usage.InputTokens
				// Note: payload.Message.Usage.OutputTokens is a placeholder (always 1).
				// Use message_delta for the actual cumulative output token count.
			}
		case "message_delta":
			hasSSEEvents = true
			// Reuse messageDeltaPayload from bedrock.go — same JSON shape.
			var payload messageDeltaPayload
			if jsonErr := json.Unmarshal([]byte(jsonData), &payload); jsonErr == nil {
				// message_delta carries the final cumulative output_tokens — use it directly.
				outputTokens = payload.Usage.OutputTokens
			}
		}
	}

	if hasSSEEvents {
		return modelID, inputTokens, outputTokens, nil
	}

	// No SSE events — try non-streaming JSON body.
	var payload anthropicNonStreamingPayload
	if jsonErr := json.Unmarshal(data, &payload); jsonErr == nil && (payload.Usage.InputTokens > 0 || payload.Usage.OutputTokens > 0) {
		return payload.Model, payload.Usage.InputTokens, payload.Usage.OutputTokens, nil
	}

	// Unparseable but non-empty — return zero counts, no error (best-effort).
	return "", 0, 0, nil
}

// staticAnthropicRates maps Anthropic API model IDs to their per-1K-token USD rates.
// Source: platform.claude.com/docs/en/about-claude/pricing (verified 2026-03-24).
// Rates in USD per 1,000 tokens.
//
// Both alias forms (e.g. "claude-sonnet-4-6") and dated variants
// (e.g. "claude-sonnet-4-5-20250929") are included because Anthropic API responses
// echo back the model name as sent in the request — aliases and dated IDs may both appear.
//
// Valid until: 2026-07-01 (update when new models launch).
// Note: claude-3-haiku-20240307 is deprecated and retires 2026-04-19.
var staticAnthropicRates = map[string]aws.BedrockModelRate{
	// Current models (Claude 4.6)
	"claude-opus-4-6":   {InputPricePer1KTokens: 0.005, OutputPricePer1KTokens: 0.025},
	"claude-sonnet-4-6": {InputPricePer1KTokens: 0.003, OutputPricePer1KTokens: 0.015},
	"claude-haiku-4-5":  {InputPricePer1KTokens: 0.001, OutputPricePer1KTokens: 0.005},
	// Legacy Claude 4.5 (dated variants)
	"claude-haiku-4-5-20251001":  {InputPricePer1KTokens: 0.001, OutputPricePer1KTokens: 0.005},
	"claude-opus-4-5-20251101":   {InputPricePer1KTokens: 0.005, OutputPricePer1KTokens: 0.025},
	"claude-sonnet-4-5-20250929": {InputPricePer1KTokens: 0.003, OutputPricePer1KTokens: 0.015},
	// Legacy Claude 4.1 / 4.0
	"claude-opus-4-1-20250805": {InputPricePer1KTokens: 0.015, OutputPricePer1KTokens: 0.075},
	"claude-opus-4-20250514":   {InputPricePer1KTokens: 0.015, OutputPricePer1KTokens: 0.075},
	"claude-sonnet-4-20250514": {InputPricePer1KTokens: 0.003, OutputPricePer1KTokens: 0.015},
	// Older Haiku (Haiku 3.5 and deprecated Claude 3 Haiku)
	"claude-haiku-3-5-20241022": {InputPricePer1KTokens: 0.0008, OutputPricePer1KTokens: 0.004},
	"claude-3-haiku-20240307":   {InputPricePer1KTokens: 0.00025, OutputPricePer1KTokens: 0.00125},
}

// StaticAnthropicRates returns a copy of the static Anthropic model rate table.
// Exported for use in tests and logging.
func StaticAnthropicRates() map[string]aws.BedrockModelRate {
	out := make(map[string]aws.BedrockModelRate, len(staticAnthropicRates))
	for k, v := range staticAnthropicRates {
		out[k] = v
	}
	return out
}

// AnthropicBlockedResponse returns a goproxy-compatible 403 http.Response for the
// given request. The body contains a parseable JSON object with error, spent, limit,
// model, and topUp fields — identical shape to BedrockBlockedResponse.
func AnthropicBlockedResponse(req *http.Request, sandboxID, modelID string, spent, limit float64) *http.Response {
	body := blockedResponseBody{
		Error: "ai_budget_exhausted",
		Spent: spent,
		Limit: limit,
		Model: modelID,
		TopUp: fmt.Sprintf("km budget add %s --ai 5", sandboxID),
	}
	encoded, _ := json.Marshal(body)
	return goproxy.NewResponse(req, "application/json", http.StatusForbidden, string(encoded))
}

// Package httpproxy — openai.go
// OpenAI direct API (api.openai.com) MITM interception: token extractor,
// static rate table, cost calculator, and 403 response builder for AI budget
// enforcement. Mirrors anthropic.go line-for-line with three deltas:
//  1. Three-format extractor: Responses API SSE + Chat Completions SSE + non-streaming JSON.
//  2. Cost calc subtracts cached from input before billing (OpenAI semantics: cached_tokens
//     is a subset of input_tokens, not additive as in Anthropic).
//  3. Rate table uses the explicit CachedInputPricePer1KTokens field on aws.BedrockModelRate.
//
// Note: the unknown-model fallback is NOT handled here — ExtractOpenAITokens returns the
// modelID as-parsed. The handler in proxy.go checks staticOpenAIRates[modelID] and logs
// WARN on miss while still writing the DynamoDB row with cost=0 so the model ID surfaces
// in km status. This mirrors the Anthropic handler behavior at proxy.go.
package httpproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/elazarl/goproxy"
	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klanker-maker/pkg/aws"
)

// openaiHostRegex matches the OpenAI direct API endpoint.
// Placed in this file for grep-ability (all OpenAI symbols in one place).
// proxy.go and transparent.go reference this package-level var.
var openaiHostRegex = regexp.MustCompile(`^api\.openai\.com`)

// ---------------------------------------------------------------------------
// JSON payload types for response parsing
// ---------------------------------------------------------------------------

// openaiResponsesCreatedPayload peeks at the model from a response.created SSE event.
// The model ID also appears in response.completed — parsing this is a defensive fallback
// in case response.completed is truncated or slow to arrive.
type openaiResponsesCreatedPayload struct {
	Type     string `json:"type"` // "response.created"
	Response struct {
		Model string `json:"model"`
	} `json:"response"`
}

// openaiResponsesCompletedPayload reads model + full usage from a response.completed SSE event.
// This is the primary event for Responses API metering: Codex CLI uses /v1/responses exclusively
// (Chat Completions was hard-removed from Codex in Feb 2026).
type openaiResponsesCompletedPayload struct {
	Type     string `json:"type"` // "response.completed"
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

// openaiChatUsagePayload is the usage object in Chat Completions SSE final chunk
// and non-streaming Chat Completions body.
type openaiChatUsagePayload struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// openaiChatCompletionsChunkPayload is a Chat Completions SSE chunk.
// All non-final chunks have Usage == nil and Choices != [].
// The final chunk (with stream_options.include_usage=true) has Choices == [] and Usage populated.
type openaiChatCompletionsChunkPayload struct {
	Model   string                  `json:"model"`
	Choices []json.RawMessage       `json:"choices"`
	Usage   *openaiChatUsagePayload `json:"usage"` // nil except on final chunk
}

// openaiResponsesNonStreamingPayload reads model + usage from a non-streaming Responses API body.
// Same shape as the nested "response" object inside response.completed.
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

// openaiChatNonStreamingPayload reads model + usage from a non-streaming Chat Completions body.
type openaiChatNonStreamingPayload struct {
	Model string                 `json:"model"`
	Usage openaiChatUsagePayload `json:"usage"`
}

// ---------------------------------------------------------------------------
// ExtractOpenAITokens — three-format token extractor
// ---------------------------------------------------------------------------

// ExtractOpenAITokens reads an OpenAI API response body (Responses API SSE,
// Chat Completions SSE, or non-streaming JSON) and returns the model ID, input/output
// token counts, cached input token counts, and reasoning output token counts.
//
// Returns ("", 0, 0, 0, 0, nil) on empty or unrecognized body.
// Caps the read at maxResponseBodySize (10 MB) and returns best-effort counts on overflow.
//
// Format detection order:
//  1. SSE (scan for "data: " prefix lines) — primary for Codex (Responses API) and Chat Completions
//  2. Non-streaming Responses API JSON (top-level "usage.input_tokens")
//  3. Non-streaming Chat Completions JSON (top-level "usage.prompt_tokens")
//
// Important semantics:
//   - cachedInputTokens is a SUBSET of inputTokens (not additive — OpenAI semantics).
//     Call CalculateOpenAICost to bill correctly (subtracts cached before applying full rate).
//   - reasoningOutputTokens is INCLUDED in outputTokens (not a separate counter).
//     Surface in logs for observability only — never pass to CalculateOpenAICost.
func ExtractOpenAITokens(body io.Reader) (modelID string, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens int, err error) {
	lr := io.LimitReader(body, int64(maxResponseBodySize))
	data, _ := io.ReadAll(lr)
	if len(data) == 0 {
		return "", 0, 0, 0, 0, nil
	}

	// Try SSE parsing first (line-by-line, look for "data: " prefix).
	hasSSEEvents := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// CRITICAL: SSE lines can exceed the default 64KB scanner buffer.
	// The Responses API response.completed event can carry full output text +
	// reasoning summaries exceeding 64KB. Bump to maxResponseBodySize (10MB):
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseBodySize)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, sseDataPrefix) {
			continue
		}
		jsonData := strings.TrimPrefix(line, sseDataPrefix)
		if jsonData == "[DONE]" {
			// Chat Completions SSE terminator — not JSON; must skip before unmarshal.
			continue
		}

		// Peek at the type field. genericTypePayload reused from bedrock.go.
		var typed genericTypePayload
		if jsonErr := json.Unmarshal([]byte(jsonData), &typed); jsonErr != nil {
			continue
		}

		switch typed.Type {
		case "response.completed":
			// Responses API — usage in this final event; model ID also present.
			hasSSEEvents = true
			var p openaiResponsesCompletedPayload
			if jsonErr := json.Unmarshal([]byte(jsonData), &p); jsonErr == nil {
				modelID = p.Response.Model
				inputTokens = p.Response.Usage.InputTokens
				outputTokens = p.Response.Usage.OutputTokens
				cachedInputTokens = p.Response.Usage.InputTokensDetails.CachedTokens
				reasoningOutputTokens = p.Response.Usage.OutputTokensDetails.ReasoningTokens
			}
		case "response.created":
			// Optional — model ID also appears in response.completed.response.model.
			// Capture as a defensive fallback if response.completed is truncated.
			hasSSEEvents = true
			var p openaiResponsesCreatedPayload
			if jsonErr := json.Unmarshal([]byte(jsonData), &p); jsonErr == nil && modelID == "" {
				modelID = p.Response.Model
			}
		case "":
			// No type field — likely a Chat Completions chunk.
			var p openaiChatCompletionsChunkPayload
			if jsonErr := json.Unmarshal([]byte(jsonData), &p); jsonErr == nil {
				hasSSEEvents = true
				if p.Model != "" && modelID == "" {
					modelID = p.Model
				}
				// Usage populated only on final chunk (when stream_options.include_usage=true).
				if p.Usage != nil {
					inputTokens = p.Usage.PromptTokens
					outputTokens = p.Usage.CompletionTokens
					if p.Usage.PromptTokensDetails != nil {
						cachedInputTokens = p.Usage.PromptTokensDetails.CachedTokens
					}
					if p.Usage.CompletionTokensDetails != nil {
						reasoningOutputTokens = p.Usage.CompletionTokensDetails.ReasoningTokens
					}
				}
			}
		}
	}

	if hasSSEEvents {
		// Pitfall 2 (RESEARCH.md): Chat Completions without include_usage yields zero counts.
		// Emit a WARN so operators know the client needs stream_options.include_usage=true.
		if inputTokens == 0 && outputTokens == 0 && modelID != "" {
			log.Warn().
				Str("event_type", "openai_chat_completions_missing_usage").
				Str("model", modelID).
				Msg("openai chat completions response missing usage field — set stream_options.include_usage=true to enable metering")
		}
		return modelID, inputTokens, outputTokens, cachedInputTokens, reasoningOutputTokens, nil
	}

	// No SSE events — try non-streaming Responses API (input_tokens field).
	var respP openaiResponsesNonStreamingPayload
	if jsonErr := json.Unmarshal(data, &respP); jsonErr == nil && (respP.Usage.InputTokens > 0 || respP.Usage.OutputTokens > 0) {
		return respP.Model, respP.Usage.InputTokens, respP.Usage.OutputTokens,
			respP.Usage.InputTokensDetails.CachedTokens, respP.Usage.OutputTokensDetails.ReasoningTokens, nil
	}

	// Fall through to non-streaming Chat Completions (prompt_tokens field).
	var chatP openaiChatNonStreamingPayload
	if jsonErr := json.Unmarshal(data, &chatP); jsonErr == nil && (chatP.Usage.PromptTokens > 0 || chatP.Usage.CompletionTokens > 0) {
		cached := 0
		reasoning := 0
		if chatP.Usage.PromptTokensDetails != nil {
			cached = chatP.Usage.PromptTokensDetails.CachedTokens
		}
		if chatP.Usage.CompletionTokensDetails != nil {
			reasoning = chatP.Usage.CompletionTokensDetails.ReasoningTokens
		}
		return chatP.Model, chatP.Usage.PromptTokens, chatP.Usage.CompletionTokens, cached, reasoning, nil
	}

	// Unparseable but non-empty — return zero counts, no error (best-effort).
	return "", 0, 0, 0, 0, nil
}

// ---------------------------------------------------------------------------
// CalculateOpenAICost — cache-subtract arithmetic
// ---------------------------------------------------------------------------

// CalculateOpenAICost returns the USD cost for the given OpenAI token counts.
//
// OpenAI cache semantics differ from Anthropic:
//   - cachedInputTokens is a SUBSET of inputTokens (not additive).
//   - Uncached input = inputTokens - cachedInputTokens (billed at full input rate).
//   - Cached input billed at rate.CachedInputPricePer1KTokens (explicit per-model rate).
//
// Note: reasoningOutputTokens is NOT a parameter — reasoning is already counted inside
// outputTokens (Pitfall 3 in RESEARCH.md). Pass reasoningOutputTokens to logs for
// observability only; never sum it into cost.
func CalculateOpenAICost(inputTokens, outputTokens, cachedInputTokens int, rate aws.BedrockModelRate) float64 {
	// Subtract cached from input: bill cached at cheaper cached rate, remainder at full rate.
	uncachedInput := inputTokens - cachedInputTokens
	if uncachedInput < 0 {
		uncachedInput = 0 // defensive: anomalous response where cached > input
	}
	inputCost := float64(uncachedInput) * rate.InputPricePer1KTokens / 1000.0
	cachedCost := float64(cachedInputTokens) * rate.CachedInputPricePer1KTokens / 1000.0
	outputCost := float64(outputTokens) * rate.OutputPricePer1KTokens / 1000.0
	return inputCost + cachedCost + outputCost
}

// ---------------------------------------------------------------------------
// staticOpenAIRates — price table
// ---------------------------------------------------------------------------

// staticOpenAIRates maps OpenAI API model IDs to their per-1K-token USD rates.
//
// Source: https://developers.openai.com/api/docs/pricing (primary) +
//
//	https://pricepertoken.com/pricing-page/provider/openai (cross-verified)
//
// Verified 2026-05-24. Prices in USD per 1,000 tokens (converted from /1M by dividing by 1000).
// Update cadence: OpenAI pricing changes ~quarterly; re-verify every 90 days and on new model launch.
//
// Note: response.model echoes back the model ID exactly as sent in the request.
// Both alias forms (e.g. "gpt-4o") and dated variants (e.g. "gpt-4o-2024-08-06") appear
// in real traffic — include both. Add dated variants reactively when real traffic surfaces
// unknown-model warnings in km status / proxy logs.
//
// Note: the unknown-model fallback is handled by the proxy/transparent handlers (not here).
// When a modelID is not in this table, the handler logs WARN openai_unknown_model and still
// writes a DynamoDB row with costUSD=0 so the model ID surfaces in km status.
var staticOpenAIRates = map[string]aws.BedrockModelRate{
	// GPT-5.5 family (current flagship as of 2026-05-24)
	"gpt-5.5": {
		InputPricePer1KTokens:       0.005,
		CachedInputPricePer1KTokens: 0.0005,
		OutputPricePer1KTokens:      0.030,
	},
	"gpt-5.5-pro": {
		InputPricePer1KTokens:       0.030,
		CachedInputPricePer1KTokens: 0.030, // NOTE: o-series-style — no cache discount verified
		OutputPricePer1KTokens:      0.180,
	},

	// GPT-5.4 family
	"gpt-5.4": {
		InputPricePer1KTokens:       0.0025,
		CachedInputPricePer1KTokens: 0.00025,
		OutputPricePer1KTokens:      0.015,
	},
	"gpt-5.4-mini": {
		InputPricePer1KTokens:       0.00075,
		CachedInputPricePer1KTokens: 0.000075,
		OutputPricePer1KTokens:      0.0045,
	},
	"gpt-5.4-nano": {
		InputPricePer1KTokens:       0.0002,
		CachedInputPricePer1KTokens: 0.00002,
		OutputPricePer1KTokens:      0.00125,
	},
	"gpt-5.4-pro": {
		InputPricePer1KTokens:       0.030,
		CachedInputPricePer1KTokens: 0.030, // NOTE: verified — no cache discount for pro tier
		OutputPricePer1KTokens:      0.180,
	},

	// GPT-5.3 Codex family (most important for Phase 88 — Codex CLI default as of 2026-05-24)
	"gpt-5.3-codex": {
		InputPricePer1KTokens:       0.00175,
		CachedInputPricePer1KTokens: 0.000175,
		OutputPricePer1KTokens:      0.014,
	},
	"gpt-5.3-codex-spark": {
		// NOTE: gpt-5.3-codex-spark pricing not in main pricing table as of 2026-05-24.
		// Using same rates as gpt-5.3-codex (placeholder — verify against authoritative source).
		InputPricePer1KTokens:       0.00175,
		CachedInputPricePer1KTokens: 0.000175,
		OutputPricePer1KTokens:      0.014,
	},

	// GPT-4o family (legacy but still in use)
	"gpt-4o": {
		InputPricePer1KTokens:       0.0025,
		CachedInputPricePer1KTokens: 0.00125,
		OutputPricePer1KTokens:      0.010,
	},
	"gpt-4o-2024-08-06": {
		InputPricePer1KTokens:       0.0025,
		CachedInputPricePer1KTokens: 0.00125,
		OutputPricePer1KTokens:      0.010,
	},
	"gpt-4o-2024-11-20": {
		InputPricePer1KTokens:       0.0025,
		CachedInputPricePer1KTokens: 0.00125,
		OutputPricePer1KTokens:      0.010,
	},
	"gpt-4o-mini": {
		InputPricePer1KTokens:       0.00015,
		CachedInputPricePer1KTokens: 0.000075,
		OutputPricePer1KTokens:      0.0006,
	},
	"gpt-4o-mini-2024-07-18": {
		InputPricePer1KTokens:       0.00015,
		CachedInputPricePer1KTokens: 0.000075,
		OutputPricePer1KTokens:      0.0006,
	},

	// GPT-4.1 family
	"gpt-4.1": {
		InputPricePer1KTokens:       0.002,
		CachedInputPricePer1KTokens: 0.0005,
		OutputPricePer1KTokens:      0.008,
	},
	"gpt-4.1-mini": {
		InputPricePer1KTokens:       0.0004,
		CachedInputPricePer1KTokens: 0.0001,
		OutputPricePer1KTokens:      0.0016,
	},

	// O-series reasoning (still occasionally invoked)
	"o1": {
		InputPricePer1KTokens:       0.015,
		CachedInputPricePer1KTokens: 0.0075,
		OutputPricePer1KTokens:      0.060,
	},
	// NOTE: o1-mini and o3-mini — pricepertoken.com shows cached = input (no discount).
	// Set CachedInputPricePer1KTokens == InputPricePer1KTokens per Open Questions #5 in RESEARCH.md.
	// Update if authoritative OpenAI docs show a different rate.
	"o1-mini": {
		InputPricePer1KTokens:       0.00055,
		CachedInputPricePer1KTokens: 0.00055, // no cache discount for o1-mini
		OutputPricePer1KTokens:      0.0022,
	},
	"o3-mini": {
		InputPricePer1KTokens:       0.00055,
		CachedInputPricePer1KTokens: 0.00055, // no cache discount for o3-mini
		OutputPricePer1KTokens:      0.0022,
	},
}

// StaticOpenAIRates returns a copy of the static OpenAI model rate table.
// Exported for use in tests and logging. The proxy handler uses the unexported
// staticOpenAIRates directly.
func StaticOpenAIRates() map[string]aws.BedrockModelRate {
	out := make(map[string]aws.BedrockModelRate, len(staticOpenAIRates))
	for k, v := range staticOpenAIRates {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// OpenAIBlockedResponse — 403 response builder
// ---------------------------------------------------------------------------

// OpenAIBlockedResponse returns a goproxy-compatible 403 http.Response for the
// given request. The body contains a parseable JSON object with error, spent, limit,
// model, and topUp fields — identical shape to BedrockBlockedResponse and
// AnthropicBlockedResponse. Reuses the shared blockedResponseBody struct from bedrock.go.
func OpenAIBlockedResponse(req *http.Request, sandboxID, modelID string, spent, limit float64) *http.Response {
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

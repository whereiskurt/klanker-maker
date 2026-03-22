// Package httpproxy — bedrock.go
// Bedrock MITM interception: SSE token extractor, cost calculator, DynamoDB
// spend incrementer, and 403 response builder for AI budget enforcement.
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
)

// maxResponseBodySize is the read cap to prevent runaway memory on huge responses.
const maxResponseBodySize = 10 * 1024 * 1024 // 10 MB

// sseDataPrefix is the SSE line prefix we extract JSON from.
const sseDataPrefix = "data: "

// modelPathRegex matches /model/{model-id}/invoke or /model/{model-id}/invoke-with-response-stream
var modelPathRegex = regexp.MustCompile(`/model/([^/]+)/invoke(?:-with-response-stream)?$`)

// messageStartPayload is used to unmarshal message_start SSE events.
type messageStartPayload struct {
	Type    string `json:"type"`
	Message struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// messageDeltaPayload is used to unmarshal message_delta SSE events.
type messageDeltaPayload struct {
	Type  string `json:"type"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// nonStreamingPayload is used to unmarshal non-streaming JSON response bodies.
type nonStreamingPayload struct {
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// genericTypePayload is used to peek at the "type" field of an SSE data line.
type genericTypePayload struct {
	Type string `json:"type"`
}

// AIBudgetExhaustedError is returned when the sandbox AI budget is fully spent.
type AIBudgetExhaustedError struct {
	SandboxID string
	ModelID   string
	Spent     float64
	Limit     float64
}

func (e *AIBudgetExhaustedError) Error() string {
	return fmt.Sprintf("AI budget exhausted for sandbox %s (spent=%.4f limit=%.4f model=%s)",
		e.SandboxID, e.Spent, e.Limit, e.ModelID)
}

// ExtractBedrockTokens reads a Bedrock response body (SSE or plain JSON) and
// returns the input and output token counts. Returns (0, 0, nil) on empty body.
// Caps the read at 10 MB and returns best-effort partial counts on overflow.
func ExtractBedrockTokens(body io.Reader) (inputTokens, outputTokens int, err error) {
	// Read up to maxResponseBodySize.
	lr := io.LimitReader(body, int64(maxResponseBodySize))
	data, readErr := io.ReadAll(lr)
	if len(data) == 0 {
		return 0, 0, nil
	}

	// Try SSE parsing first.
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
			var payload messageStartPayload
			if jsonErr := json.Unmarshal([]byte(jsonData), &payload); jsonErr == nil {
				inputTokens = payload.Message.Usage.InputTokens
			}
		case "message_delta":
			hasSSEEvents = true
			var payload messageDeltaPayload
			if jsonErr := json.Unmarshal([]byte(jsonData), &payload); jsonErr == nil {
				// message_delta carries the final cumulative output_tokens — use it directly.
				outputTokens = payload.Usage.OutputTokens
			}
		}
	}

	if hasSSEEvents {
		_ = readErr // best-effort on partial read
		return inputTokens, outputTokens, nil
	}

	// No SSE events — try non-streaming JSON body.
	var payload nonStreamingPayload
	if jsonErr := json.Unmarshal(data, &payload); jsonErr == nil && (payload.Usage.InputTokens > 0 || payload.Usage.OutputTokens > 0) {
		return payload.Usage.InputTokens, payload.Usage.OutputTokens, nil
	}

	// Unparseable but non-empty — return zero counts, no error (best-effort).
	return 0, 0, nil
}

// CalculateCost returns the USD cost for the given token counts and per-1K rates.
func CalculateCost(inputTokens, outputTokens int, inputPricePer1K, outputPricePer1K float64) float64 {
	return float64(inputTokens)*inputPricePer1K/1000.0 + float64(outputTokens)*outputPricePer1K/1000.0
}

// ExtractModelID parses the Bedrock model ID from a URL path of the form
// /model/{model-id}/invoke or /model/{model-id}/invoke-with-response-stream.
// Returns an empty string if the path does not match.
func ExtractModelID(urlPath string) string {
	m := modelPathRegex.FindStringSubmatch(urlPath)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// blockedResponseBody is the JSON structure sent in 403 budget-exhausted responses.
type blockedResponseBody struct {
	Error string  `json:"error"`
	Spent float64 `json:"spent"`
	Limit float64 `json:"limit"`
	Model string  `json:"model"`
	TopUp string  `json:"topUp"`
}

// BedrockBlockedResponse returns a goproxy-compatible 403 http.Response for the
// given request. The body contains a parseable JSON object with spent/limit/model
// and the km budget add command for self-service top-up.
func BedrockBlockedResponse(req *http.Request, sandboxID, modelID string, spent, limit float64) *http.Response {
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

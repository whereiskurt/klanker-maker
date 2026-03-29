// Package httpproxy — bedrock.go
// Bedrock MITM interception: SSE token extractor, cost calculator, DynamoDB
// spend incrementer, and 403 response builder for AI budget enforcement.
package httpproxy

import (
	"bufio"
	"bytes"
	"encoding/base64"
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

// ExtractBedrockTokens reads a Bedrock response body and returns the input and
// output token counts. Handles three formats:
//   - SSE text streams (data: {"type":"message_start",...}) — Anthropic SDK format
//   - AWS event-stream binary (application/vnd.amazon.eventstream) — Bedrock streaming
//   - Non-streaming JSON ({"usage":{"input_tokens":...}}) — Bedrock invoke
//
// Returns (0, 0, nil) on empty body.
// Caps the read at 10 MB and returns best-effort partial counts on overflow.
func ExtractBedrockTokens(body io.Reader) (inputTokens, outputTokens int, err error) {
	// Read up to maxResponseBodySize.
	lr := io.LimitReader(body, int64(maxResponseBodySize))
	data, readErr := io.ReadAll(lr)
	if len(data) == 0 {
		return 0, 0, nil
	}

	// Try SSE parsing first (text-based streams from Anthropic SDK).
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
				outputTokens = payload.Usage.OutputTokens
			}
		}
	}

	if hasSSEEvents {
		_ = readErr // best-effort on partial read
		return inputTokens, outputTokens, nil
	}

	// Try non-streaming JSON body.
	var payload nonStreamingPayload
	if jsonErr := json.Unmarshal(data, &payload); jsonErr == nil && (payload.Usage.InputTokens > 0 || payload.Usage.OutputTokens > 0) {
		return payload.Usage.InputTokens, payload.Usage.OutputTokens, nil
	}

	// Try embedded JSON extraction (AWS event-stream binary or any binary-framed format).
	// Bedrock invoke-with-response-stream wraps the same message_start/message_delta
	// JSON payloads inside binary event-stream frames. Scan for JSON objects by finding
	// opening braces and attempting to unmarshal balanced JSON from each position.
	in, out, found := extractEmbeddedTokens(data)
	if found {
		_ = readErr
		return in, out, nil
	}

	// Unparseable but non-empty — return zero counts, no error (best-effort).
	return 0, 0, nil
}

// extractEmbeddedTokens scans binary data for JSON objects containing
// message_start and message_delta payloads. This handles AWS event-stream
// binary encoding where JSON payloads are embedded within binary frames.
// Works provider-agnostically — any framing that embeds these JSON structures.
// bedrockEventPayload wraps a Bedrock event-stream JSON chunk.
// Bedrock encodes the actual Anthropic event as base64 in the "bytes" field.
type bedrockEventPayload struct {
	Bytes string `json:"bytes"`
}

func extractEmbeddedTokens(data []byte) (inputTokens, outputTokens int, found bool) {
	// Scan for JSON objects by locating opening braces.
	for i := 0; i < len(data); i++ {
		if data[i] != '{' {
			continue
		}

		// Find the balanced closing brace.
		jsonBytes := extractBalancedJSON(data[i:])
		if jsonBytes == nil {
			continue
		}

		// Try direct type match first (plain embedded JSON).
		if in, out, ok := tryParseTokenEvent(jsonBytes); ok {
			found = true
			if in > 0 {
				inputTokens = in
			}
			if out > 0 {
				outputTokens = out
			}
			i += len(jsonBytes) - 1
			continue
		}

		// Try Bedrock event-stream format: {"bytes":"<base64>"}
		var wrapper bedrockEventPayload
		if json.Unmarshal(jsonBytes, &wrapper) == nil && wrapper.Bytes != "" {
			decoded, decErr := base64.StdEncoding.DecodeString(wrapper.Bytes)
			if decErr == nil && len(decoded) > 0 {
				if in, out, ok := tryParseTokenEvent(decoded); ok {
					found = true
					if in > 0 {
						inputTokens = in
					}
					if out > 0 {
						outputTokens = out
					}
				}
			}
		}

		// Skip past this JSON object.
		i += len(jsonBytes) - 1
	}
	return inputTokens, outputTokens, found
}

// tryParseTokenEvent checks if jsonBytes is a message_start or message_delta
// event and extracts token counts. Returns (0, 0, false) if not a token event.
func tryParseTokenEvent(jsonBytes []byte) (inputTokens, outputTokens int, found bool) {
	var typed genericTypePayload
	if json.Unmarshal(jsonBytes, &typed) != nil {
		return 0, 0, false
	}

	switch typed.Type {
	case "message_start":
		var payload messageStartPayload
		if json.Unmarshal(jsonBytes, &payload) == nil {
			return payload.Message.Usage.InputTokens, 0, true
		}
	case "message_delta":
		var payload messageDeltaPayload
		if json.Unmarshal(jsonBytes, &payload) == nil {
			return 0, payload.Usage.OutputTokens, true
		}
	}
	return 0, 0, false
}

// extractBalancedJSON returns the first balanced JSON object starting at data[0],
// or nil if no balanced object is found within 64 KB.
func extractBalancedJSON(data []byte) []byte {
	if len(data) == 0 || data[0] != '{' {
		return nil
	}
	depth := 0
	inString := false
	escaped := false
	limit := len(data)
	if limit > 65536 {
		limit = 65536 // cap scan to prevent runaway on large binary blobs
	}
	for i := 0; i < limit; i++ {
		b := data[i]
		if escaped {
			escaped = false
			continue
		}
		if b == '\\' && inString {
			escaped = true
			continue
		}
		if b == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if b == '{' {
			depth++
		} else if b == '}' {
			depth--
			if depth == 0 {
				return data[:i+1]
			}
		}
	}
	return nil
}

// CalculateCost returns the USD cost for the given token counts and per-1K rates.
func CalculateCost(inputTokens, outputTokens int, inputPricePer1K, outputPricePer1K float64) float64 {
	return float64(inputTokens)*inputPricePer1K/1000.0 + float64(outputTokens)*outputPricePer1K/1000.0
}

// ExtractModelID parses the Bedrock model ID from a URL path of the form
// /model/{model-id}/invoke or /model/{model-id}/invoke-with-response-stream.
// Returns an empty string if the path does not match.
// Strips cross-region inference prefixes (e.g. "us.", "eu.", "ap.") so that
// "us.anthropic.claude-sonnet-4-6" becomes "anthropic.claude-sonnet-4-6",
// matching the keys in the static rate table.
func ExtractModelID(urlPath string) string {
	m := modelPathRegex.FindStringSubmatch(urlPath)
	if len(m) < 2 {
		return ""
	}
	modelID := m[1]
	// Strip cross-region prefix: "us.", "eu.", "ap." etc.
	if idx := strings.Index(modelID, ".anthropic."); idx >= 0 && idx <= 3 {
		modelID = modelID[idx+1:]
	}
	return modelID
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

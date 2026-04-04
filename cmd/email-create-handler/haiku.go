package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// BedrockRuntimeAPI is a narrow, mockable interface for invoking Bedrock models.
type BedrockRuntimeAPI interface {
	InvokeModel(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error)
}

// InterpretedCommand holds the structured output from Haiku's email interpretation.
type InterpretedCommand struct {
	// Command is the km command to execute: create, destroy, status, list, extend, pause, resume.
	Command string `json:"command"`
	// Type classifies the command: "info" (reply immediately) or "action" (requires confirmation).
	Type string `json:"type"`
	// Profile is the sandbox profile name (for create commands).
	Profile string `json:"profile"`
	// Overrides are profile field overrides (ttl, instance type, etc.).
	Overrides map[string]interface{} `json:"overrides"`
	// Confidence is Haiku's self-reported confidence in [0, 1].
	Confidence float64 `json:"confidence"`
	// Reasoning is Haiku's plain-language explanation.
	Reasoning string `json:"reasoning"`
}

// haikuRequest is the Bedrock Messages API request body.
type haikuRequest struct {
	AnthropicVersion string         `json:"anthropic_version"`
	MaxTokens        int            `json:"max_tokens"`
	Temperature      float64        `json:"temperature"`
	System           string         `json:"system"`
	Messages         []haikuMessage `json:"messages"`
}

// haikuMessage is a single message in the Messages API conversation.
type haikuMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// haikuResponse is the Bedrock Messages API response body.
type haikuResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// callHaiku invokes the Haiku model via Bedrock and returns a parsed InterpretedCommand.
func callHaiku(ctx context.Context, client BedrockRuntimeAPI, modelID, systemPrompt, userMessage string) (*InterpretedCommand, error) {
	reqBody, err := json.Marshal(haikuRequest{
		AnthropicVersion: "bedrock-2023-05-31",
		MaxTokens:        512,
		Temperature:      0.1,
		System:           systemPrompt,
		Messages: []haikuMessage{
			{Role: "user", Content: userMessage},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal haiku request: %w", err)
	}

	out, err := client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     awssdk.String(modelID),
		ContentType: awssdk.String("application/json"),
		Accept:      awssdk.String("application/json"),
		Body:        reqBody,
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock InvokeModel: %w", err)
	}

	var resp haikuResponse
	if err := json.Unmarshal(out.Body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal haiku response: %w", err)
	}
	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("haiku returned empty content array")
	}

	return parseHaikuResponse(resp.Content[0].Text)
}

// parseHaikuResponse parses the text content from Haiku's response into an InterpretedCommand.
// It handles type coercions (confidence as string, null overrides) for robustness.
func parseHaikuResponse(text string) (*InterpretedCommand, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("haiku returned empty response text")
	}

	// Strip markdown code fences — Haiku often wraps JSON in ```json ... ```
	if strings.HasPrefix(text, "```") {
		// Remove opening fence (```json or ```)
		if idx := strings.Index(text, "\n"); idx != -1 {
			text = text[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(text, "```"); idx != -1 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}

	// Use a raw map with json.Number to handle lenient numeric types.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("parse haiku JSON: %w", err)
	}

	cmd := &InterpretedCommand{
		Overrides: make(map[string]interface{}),
	}

	// Extract command (string)
	if v, ok := raw["command"]; ok {
		_ = json.Unmarshal(v, &cmd.Command)
	}

	// Extract type (string); default to "action" for backward compat
	if v, ok := raw["type"]; ok {
		_ = json.Unmarshal(v, &cmd.Type)
	}
	if cmd.Type == "" {
		cmd.Type = "action"
	}

	// Extract profile (string)
	if v, ok := raw["profile"]; ok {
		_ = json.Unmarshal(v, &cmd.Profile)
	}

	// Extract reasoning (string)
	if v, ok := raw["reasoning"]; ok {
		_ = json.Unmarshal(v, &cmd.Reasoning)
	}

	// Extract confidence — lenient: handle both float64 and quoted string
	if v, ok := raw["confidence"]; ok {
		var num json.Number
		if err := json.Unmarshal(v, &num); err == nil {
			if f, err := num.Float64(); err == nil {
				cmd.Confidence = f
			}
		} else {
			// Try string form: "0.85"
			var s string
			if err := json.Unmarshal(v, &s); err == nil {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					cmd.Confidence = f
				}
			}
		}
	}

	// Extract overrides — default to empty map if null or missing
	if v, ok := raw["overrides"]; ok {
		var overrides map[string]interface{}
		if err := json.Unmarshal(v, &overrides); err == nil && overrides != nil {
			cmd.Overrides = overrides
		}
	}

	return cmd, nil
}

// buildSystemPrompt constructs the system prompt for Haiku email interpretation.
// It includes the available profiles, currently running sandboxes, command descriptions,
// and the required JSON output format.
func buildSystemPrompt(profiles []string, sandboxes []string) string {
	var sb strings.Builder

	sb.WriteString("You are an operator assistant for a sandbox management platform (km).\n")
	sb.WriteString("Interpret the operator's email and return a JSON command object.\n\n")

	sb.WriteString("## Command Classification\n\n")
	sb.WriteString("### Info commands (type: \"info\") — reply immediately, no confirmation needed:\n")
	sb.WriteString("- `status <sandbox-id>` — get sandbox status\n")
	sb.WriteString("- `list` — list all running sandboxes\n\n")
	sb.WriteString("### Action commands (type: \"action\") — require confirmation before execution:\n")
	sb.WriteString("- `create <profile>` — create a new sandbox from the named profile\n")
	sb.WriteString("- `destroy <sandbox-id>` — destroy (teardown) a running sandbox\n")
	sb.WriteString("- `extend <sandbox-id>` — extend the TTL of a running sandbox\n")
	sb.WriteString("- `pause <sandbox-id>` — pause/hibernate a running sandbox\n")
	sb.WriteString("- `resume <sandbox-id>` — resume a paused sandbox\n\n")

	if len(profiles) > 0 {
		sb.WriteString("## Available Profiles\n\n")
		for _, p := range profiles {
			sb.WriteString(fmt.Sprintf("- %s\n", p))
		}
		sb.WriteString("\n")
	}

	if len(sandboxes) > 0 {
		sb.WriteString("## Currently Running Sandboxes\n\n")
		for _, id := range sandboxes {
			sb.WriteString(fmt.Sprintf("- %s\n", id))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Response Format\n\n")
	sb.WriteString("Always respond with ONLY a JSON object (no markdown, no prose):\n")
	sb.WriteString(`{
  "command": "<command-name>",
  "type": "<info|action>",
  "profile": "<profile-name or empty>",
  "overrides": {},
  "confidence": 0.95,
  "reasoning": "<brief explanation>"
}`)
	sb.WriteString("\n\n")
	sb.WriteString("Set confidence < 0.7 if the request is ambiguous or unclear.\n")
	sb.WriteString("Action commands require confirmation from the operator before execution.\n")
	sb.WriteString("Info commands (status, list) do not require confirmation.\n")

	return sb.String()
}

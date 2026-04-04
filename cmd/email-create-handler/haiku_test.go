package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// ---- mock BedrockRuntimeAPI ----

type mockBedrock struct {
	response []byte
	err      error
}

func (m *mockBedrock) InvokeModel(_ context.Context, _ *bedrockruntime.InvokeModelInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &bedrockruntime.InvokeModelOutput{Body: m.response}, nil
}

// buildHaikuResponseBody builds a Bedrock Messages API response with the given text content.
func buildHaikuResponseBody(text string) []byte {
	resp := map[string]interface{}{
		"id":      "msg_01test",
		"type":    "message",
		"role":    "assistant",
		"content": []map[string]string{{"type": "text", "text": text}},
		"model":   "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		"stop_reason": "end_turn",
		"usage":   map[string]int{"input_tokens": 100, "output_tokens": 50},
	}
	b, _ := json.Marshal(resp)
	return b
}

// ---- TestCallHaiku ----

func TestCallHaiku_Success(t *testing.T) {
	cmdJSON := `{"command":"create","type":"action","profile":"open-dev","overrides":{"ttl":"2h"},"confidence":0.92,"reasoning":"User wants a sandbox"}`
	client := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}

	cmd, err := callHaiku(context.Background(), client, "us.anthropic.claude-haiku-4-5-20251001-v1:0", "system prompt", "create a sandbox")
	if err != nil {
		t.Fatalf("callHaiku error: %v", err)
	}
	if cmd.Command != "create" {
		t.Errorf("Command = %q, want %q", cmd.Command, "create")
	}
	if cmd.Type != "action" {
		t.Errorf("Type = %q, want %q", cmd.Type, "action")
	}
	if cmd.Profile != "open-dev" {
		t.Errorf("Profile = %q, want %q", cmd.Profile, "open-dev")
	}
	if cmd.Confidence != 0.92 {
		t.Errorf("Confidence = %v, want 0.92", cmd.Confidence)
	}
	if cmd.Reasoning != "User wants a sandbox" {
		t.Errorf("Reasoning = %q, want %q", cmd.Reasoning, "User wants a sandbox")
	}
	if cmd.Overrides == nil {
		t.Error("Overrides should not be nil")
	}
}

func TestCallHaiku_LowConfidence(t *testing.T) {
	cmdJSON := `{"command":"create","type":"action","profile":"","overrides":{},"confidence":0.4,"reasoning":"Ambiguous request"}`
	client := &mockBedrock{response: buildHaikuResponseBody(cmdJSON)}

	cmd, err := callHaiku(context.Background(), client, "us.anthropic.claude-haiku-4-5-20251001-v1:0", "system prompt", "do something")
	if err != nil {
		t.Fatalf("callHaiku error: %v", err)
	}
	if cmd.Confidence != 0.4 {
		t.Errorf("Confidence = %v, want 0.4", cmd.Confidence)
	}
}

func TestCallHaiku_InvokeError(t *testing.T) {
	client := &mockBedrock{err: io.ErrUnexpectedEOF}

	_, err := callHaiku(context.Background(), client, "us.anthropic.claude-haiku-4-5-20251001-v1:0", "system prompt", "message")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---- TestParseHaikuResponse ----

func TestParseHaikuResponse_ValidJSON(t *testing.T) {
	text := `{"command":"destroy","type":"action","profile":"","overrides":{"force":true},"confidence":0.88,"reasoning":"User wants to destroy sandbox"}`
	cmd, err := parseHaikuResponse(text)
	if err != nil {
		t.Fatalf("parseHaikuResponse error: %v", err)
	}
	if cmd.Command != "destroy" {
		t.Errorf("Command = %q, want %q", cmd.Command, "destroy")
	}
	if cmd.Type != "action" {
		t.Errorf("Type = %q, want %q", cmd.Type, "action")
	}
	if cmd.Confidence != 0.88 {
		t.Errorf("Confidence = %v, want 0.88", cmd.Confidence)
	}
}

func TestParseHaikuResponse_StringConfidence(t *testing.T) {
	text := `{"command":"list","type":"info","profile":"","overrides":null,"confidence":"0.85","reasoning":"Listing sandboxes"}`
	cmd, err := parseHaikuResponse(text)
	if err != nil {
		t.Fatalf("parseHaikuResponse error: %v", err)
	}
	if cmd.Confidence != 0.85 {
		t.Errorf("Confidence = %v, want 0.85", cmd.Confidence)
	}
}

func TestParseHaikuResponse_NullOverrides(t *testing.T) {
	text := `{"command":"status","type":"info","profile":"","overrides":null,"confidence":0.9,"reasoning":"Status check"}`
	cmd, err := parseHaikuResponse(text)
	if err != nil {
		t.Fatalf("parseHaikuResponse error: %v", err)
	}
	if cmd.Overrides == nil {
		t.Error("Overrides should default to empty map, not nil")
	}
}

func TestParseHaikuResponse_MalformedJSON(t *testing.T) {
	_, err := parseHaikuResponse("not json at all {{{")
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestParseHaikuResponse_EmptyContent(t *testing.T) {
	_, err := parseHaikuResponse("")
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
}

// ---- TestBuildSystemPrompt ----

func TestBuildSystemPrompt_ContainsProfiles(t *testing.T) {
	profiles := []string{"open-dev", "restricted-dev", "hardened"}
	prompt := buildSystemPrompt(profiles, nil)
	for _, p := range profiles {
		if !strings.Contains(prompt, p) {
			t.Errorf("prompt does not contain profile %q", p)
		}
	}
}

func TestBuildSystemPrompt_ContainsCommands(t *testing.T) {
	prompt := buildSystemPrompt(nil, nil)
	commands := []string{"create", "destroy", "status", "list", "extend", "pause", "resume"}
	for _, cmd := range commands {
		if !strings.Contains(prompt, cmd) {
			t.Errorf("prompt does not contain command %q", cmd)
		}
	}
}

func TestBuildSystemPrompt_CommandDescriptions(t *testing.T) {
	prompt := buildSystemPrompt(nil, nil)
	// Info commands — reply immediately, no confirmation
	infoCommands := []string{"status", "list"}
	for _, cmd := range infoCommands {
		if !strings.Contains(prompt, cmd) {
			t.Errorf("prompt does not contain info command %q", cmd)
		}
	}
	// Action commands — require confirmation
	actionCommands := []string{"create", "destroy", "extend", "pause", "resume"}
	for _, cmd := range actionCommands {
		if !strings.Contains(prompt, cmd) {
			t.Errorf("prompt does not contain action command %q", cmd)
		}
	}
	// Should describe type classification
	if !strings.Contains(prompt, "info") || !strings.Contains(prompt, "action") {
		t.Error("prompt should describe command type classification (info/action)")
	}
	// Should mention confirmation for action commands
	if !strings.Contains(strings.ToLower(prompt), "confirm") {
		t.Error("prompt should mention confirmation requirement for action commands")
	}
}

func TestBuildSystemPrompt_ContainsSandboxes(t *testing.T) {
	sandboxes := []string{"sb-abc12345", "goose-def67890"}
	prompt := buildSystemPrompt(nil, sandboxes)
	for _, sb := range sandboxes {
		if !strings.Contains(prompt, sb) {
			t.Errorf("prompt does not contain sandbox ID %q", sb)
		}
	}
}

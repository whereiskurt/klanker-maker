package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ---- mockS3WithBody extends mockS3 with PutObject body capture for saveConversation tests ----

type mockS3WithBody struct {
	objects   map[string][]byte
	putKeys   []string
	putBodies map[string][]byte
}

func (m *mockS3WithBody) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := awssdk.ToString(input.Key)
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("NoSuchKey: %s", key)
	}
	return &s3.GetObjectOutput{Body: nopCloser(bytes.NewReader(data))}, nil
}

func (m *mockS3WithBody) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	key := awssdk.ToString(input.Key)
	m.putKeys = append(m.putKeys, key)
	if m.putBodies == nil {
		m.putBodies = make(map[string][]byte)
	}
	body, _ := io.ReadAll(input.Body)
	m.putBodies[key] = body
	return &s3.PutObjectOutput{}, nil
}

// ---- TestExtractThreadID ----

func buildMailMessage(headers map[string]string) *mail.Message {
	var buf strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
	}
	buf.WriteString("\r\n")
	buf.WriteString("body")
	msg, _ := mail.ReadMessage(strings.NewReader(buf.String()))
	return msg
}

func TestExtractThreadID_InReplyTo(t *testing.T) {
	msg := buildMailMessage(map[string]string{
		"From":       "op@example.com",
		"In-Reply-To": "<parent-msg-id@example.com>",
		"Message-ID": "<new-msg-id@example.com>",
	})
	got := extractThreadID(msg)
	want := "parent-msg-id@example.com"
	if got != want {
		t.Errorf("extractThreadID = %q, want %q", got, want)
	}
}

func TestExtractThreadID_References(t *testing.T) {
	msg := buildMailMessage(map[string]string{
		"From":       "op@example.com",
		"References": "<root-msg@example.com> <second-msg@example.com>",
		"Message-ID": "<new-msg-id@example.com>",
	})
	got := extractThreadID(msg)
	want := "root-msg@example.com"
	if got != want {
		t.Errorf("extractThreadID = %q, want %q", got, want)
	}
}

func TestExtractThreadID_MessageIDOnly(t *testing.T) {
	msg := buildMailMessage(map[string]string{
		"From":       "op@example.com",
		"Message-ID": "<new-thread-root@example.com>",
	})
	got := extractThreadID(msg)
	want := "new-thread-root@example.com"
	if got != want {
		t.Errorf("extractThreadID = %q, want %q", got, want)
	}
}

func TestExtractThreadID_AngleBrackets(t *testing.T) {
	msg := buildMailMessage(map[string]string{
		"From":       "op@example.com",
		"In-Reply-To": "  <brackets-stripped@example.com>  ",
	})
	got := extractThreadID(msg)
	want := "brackets-stripped@example.com"
	if got != want {
		t.Errorf("extractThreadID = %q, want %q", got, want)
	}
}

func TestExtractThreadID_Empty(t *testing.T) {
	msg := buildMailMessage(map[string]string{
		"From": "op@example.com",
	})
	got := extractThreadID(msg)
	if got != "" {
		t.Errorf("extractThreadID = %q, want empty string", got)
	}
}

// ---- TestConversationState ----

func TestConversationState_JSONRoundTrip(t *testing.T) {
	cmd := &InterpretedCommand{
		Command:    "create",
		Type:       "action",
		Profile:    "open-dev",
		Overrides:  map[string]interface{}{"ttl": "2h"},
		Confidence: 0.92,
		Reasoning:  "sandbox for testing",
	}
	state := ConversationState{
		ThreadID:    "thread-abc@example.com",
		Sender:      "op@corp.com",
		Started:     time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		Updated:     time.Date(2026, 4, 3, 10, 1, 0, 0, time.UTC),
		State:       "awaiting_confirmation",
		ResolvedCmd: cmd,
		Messages: []ConversationMsg{
			{Role: "operator", Content: "Create a sandbox", At: time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)},
		},
	}

	b, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ConversationState
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ThreadID != state.ThreadID {
		t.Errorf("ThreadID = %q, want %q", decoded.ThreadID, state.ThreadID)
	}
	if decoded.State != state.State {
		t.Errorf("State = %q, want %q", decoded.State, state.State)
	}
	if decoded.ResolvedCmd == nil {
		t.Fatal("ResolvedCmd should not be nil after round-trip")
	}
	if decoded.ResolvedCmd.Command != "create" {
		t.Errorf("ResolvedCmd.Command = %q, want %q", decoded.ResolvedCmd.Command, "create")
	}
	if len(decoded.Messages) != 1 {
		t.Errorf("Messages len = %d, want 1", len(decoded.Messages))
	}
}

func TestConversationState_AllStates(t *testing.T) {
	states := []string{"new", "interpreted", "awaiting_confirmation", "confirmed", "revised", "cancelled"}
	for _, s := range states {
		cs := ConversationState{State: s}
		b, _ := json.Marshal(cs)
		var out ConversationState
		_ = json.Unmarshal(b, &out)
		if out.State != s {
			t.Errorf("state %q did not round-trip; got %q", s, out.State)
		}
	}
}

// ---- TestLoadConversation ----

func TestLoadConversation_Success(t *testing.T) {
	state := ConversationState{
		ThreadID: "test-thread@example.com",
		Sender:   "op@corp.com",
		State:    "awaiting_confirmation",
		Messages: []ConversationMsg{},
	}
	data, _ := json.Marshal(state)

	mock := &mockS3WithBody{
		objects: map[string][]byte{
			"mail/conversations/test-thread@example.com.json": data,
		},
	}

	loaded, err := loadConversation(context.Background(), mock, "test-bucket", "test-thread@example.com")
	if err != nil {
		t.Fatalf("loadConversation error: %v", err)
	}
	if loaded.ThreadID != "test-thread@example.com" {
		t.Errorf("ThreadID = %q, want %q", loaded.ThreadID, "test-thread@example.com")
	}
	if loaded.State != "awaiting_confirmation" {
		t.Errorf("State = %q, want %q", loaded.State, "awaiting_confirmation")
	}
}

func TestLoadConversation_NotFound(t *testing.T) {
	mock := &mockS3WithBody{objects: map[string][]byte{}}

	_, err := loadConversation(context.Background(), mock, "test-bucket", "nonexistent-thread@example.com")
	if err == nil {
		t.Fatal("expected error for missing conversation, got nil")
	}
	if !strings.Contains(err.Error(), "NoSuchKey") {
		t.Errorf("expected NoSuchKey error, got: %v", err)
	}
}

// ---- TestSaveConversation ----

func TestSaveConversation_WritesToCorrectKey(t *testing.T) {
	state := &ConversationState{
		ThreadID: "save-thread@example.com",
		Sender:   "op@corp.com",
		State:    "interpreted",
		Messages: []ConversationMsg{},
	}

	mock := &mockS3WithBody{objects: map[string][]byte{}}

	if err := saveConversation(context.Background(), mock, "test-bucket", state); err != nil {
		t.Fatalf("saveConversation error: %v", err)
	}

	expectedKey := "mail/conversations/save-thread@example.com.json"
	if len(mock.putKeys) != 1 || mock.putKeys[0] != expectedKey {
		t.Errorf("putKeys = %v, want [%q]", mock.putKeys, expectedKey)
	}

	// Verify the body is valid JSON and contains correct thread_id
	body, ok := mock.putBodies[expectedKey]
	if !ok {
		t.Fatalf("no put body for key %q", expectedKey)
	}
	var saved ConversationState
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatalf("put body is not valid JSON: %v", err)
	}
	if saved.ThreadID != "save-thread@example.com" {
		t.Errorf("saved ThreadID = %q, want %q", saved.ThreadID, "save-thread@example.com")
	}
	if saved.Updated.IsZero() {
		t.Error("saveConversation should update the Updated timestamp")
	}
}

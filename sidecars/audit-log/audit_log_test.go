package auditlog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	auditlog "github.com/whereiskurt/klankrmkr/sidecars/audit-log"
)

// ---- Mock Destinations ----

type mockDest struct {
	events  []auditlog.AuditEvent
	flushed bool
}

func (m *mockDest) Write(_ context.Context, event auditlog.AuditEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockDest) Flush(_ context.Context) error {
	m.flushed = true
	return nil
}

// ---- Mock CloudWatch backend for CloudWatchDest ----

type mockCWBackend struct {
	putMessages []string
}

func (m *mockCWBackend) PutLogMessages(_ context.Context, _ string, _ string, messages []string) error {
	m.putMessages = append(m.putMessages, messages...)
	return nil
}

func (m *mockCWBackend) EnsureLogGroup(_ context.Context, _ string, _ string) error {
	return nil
}

// ---- Tests ----

func TestAuditLogFormat_ShellCommand(t *testing.T) {
	input := `{"timestamp":"2026-03-21T12:00:00Z","sandbox_id":"sb-a1b2c3d4","event_type":"shell_command","source":"audit-log","detail":{"command":"ls -la"}}` + "\n"

	reader := strings.NewReader(input)
	dest := &mockDest{}
	ctx := context.Background()

	err := auditlog.Process(ctx, reader, dest)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	if len(dest.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(dest.events))
	}

	ev := dest.events[0]
	if ev.SandboxID != "sb-a1b2c3d4" {
		t.Errorf("SandboxID = %q, want %q", ev.SandboxID, "sb-a1b2c3d4")
	}
	if ev.EventType != "shell_command" {
		t.Errorf("EventType = %q, want %q", ev.EventType, "shell_command")
	}
	if ev.Source != "audit-log" {
		t.Errorf("Source = %q, want %q", ev.Source, "audit-log")
	}
	cmd, _ := ev.Detail["command"].(string)
	if cmd != "ls -la" {
		t.Errorf("detail.command = %q, want %q", cmd, "ls -la")
	}
}

func TestAuditLogFormat_NetworkEvent(t *testing.T) {
	input := `{"timestamp":"2026-03-21T12:00:00Z","sandbox_id":"sb-a1b2c3d4","event_type":"dns_query","source":"dns-proxy","detail":{"domain":"example.com","allowed":true}}` + "\n"

	reader := strings.NewReader(input)
	dest := &mockDest{}
	ctx := context.Background()

	err := auditlog.Process(ctx, reader, dest)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	if len(dest.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(dest.events))
	}

	ev := dest.events[0]
	if ev.EventType != "dns_query" {
		t.Errorf("EventType = %q, want %q", ev.EventType, "dns_query")
	}
	if ev.Source != "dns-proxy" {
		t.Errorf("Source = %q, want %q", ev.Source, "dns-proxy")
	}
	domain, _ := ev.Detail["domain"].(string)
	if domain != "example.com" {
		t.Errorf("detail.domain = %q, want %q", domain, "example.com")
	}
	allowed, _ := ev.Detail["allowed"].(bool)
	if !allowed {
		t.Errorf("detail.allowed = false, want true")
	}
}

func TestAuditLogDest_Stdout(t *testing.T) {
	input := `{"timestamp":"2026-03-21T12:00:00Z","sandbox_id":"sb-a1b2c3d4","event_type":"shell_command","source":"audit-log","detail":{"command":"echo hello"}}` + "\n"

	reader := strings.NewReader(input)
	var buf bytes.Buffer
	dest := auditlog.NewStdoutDest(&buf)
	ctx := context.Background()

	err := auditlog.Process(ctx, reader, dest)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "shell_command") {
		t.Errorf("stdout output does not contain event_type: %q", out)
	}
	if !strings.Contains(out, "sb-a1b2c3d4") {
		t.Errorf("stdout output does not contain sandbox_id: %q", out)
	}

	// Verify it's valid JSON
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 JSON line, got %d", len(lines))
	}
	var ev auditlog.AuditEvent
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Errorf("stdout output is not valid JSON: %v — %q", err, lines[0])
	}
}

func TestAuditLogDest_CloudWatch(t *testing.T) {
	input := `{"timestamp":"2026-03-21T12:00:00Z","sandbox_id":"sb-a1b2c3d4","event_type":"shell_command","source":"audit-log","detail":{"command":"pwd"}}` + "\n"

	reader := strings.NewReader(input)
	mockCW := &mockCWBackend{}
	dest := auditlog.NewCloudWatchDest(mockCW, "/km/sandboxes/sb-a1b2c3d4/", "audit")
	ctx := context.Background()

	err := auditlog.Process(ctx, reader, dest)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}

	// Flush explicitly to send buffered events
	if err := dest.Flush(ctx); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	if len(mockCW.putMessages) == 0 {
		t.Error("expected PutLogMessages to be called, but got 0 events")
	}
}

func TestAuditLogFormat_InvalidJSON(t *testing.T) {
	input := "this is not json\n"

	reader := strings.NewReader(input)
	dest := &mockDest{}
	ctx := context.Background()

	// Should NOT return error — just warn and skip
	err := auditlog.Process(ctx, reader, dest)
	if err != nil {
		t.Fatalf("Process should not error on invalid JSON, got: %v", err)
	}

	// No events should have been routed
	if len(dest.events) != 0 {
		t.Errorf("expected 0 events on invalid JSON, got %d", len(dest.events))
	}
}

// ---- AuditEvent struct validation ----

func TestAuditEventSerialization(t *testing.T) {
	ev := auditlog.AuditEvent{
		Timestamp: time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC),
		SandboxID: "sb-a1b2c3d4",
		EventType: "shell_command",
		Source:    "audit-log",
		Detail: map[string]interface{}{
			"command": "ls -la",
		},
	}

	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	fields := []string{"timestamp", "sandbox_id", "event_type", "source", "detail"}
	for _, f := range fields {
		if _, ok := out[f]; !ok {
			t.Errorf("missing field %q in JSON output", f)
		}
	}
}

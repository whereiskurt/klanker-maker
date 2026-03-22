package auditlog_test

import (
	"context"
	"testing"
	"time"

	auditlog "github.com/whereiskurt/klankrmkr/sidecars/audit-log"
)

// mockCaptureDest records written events for assertion.
type mockCaptureDest struct {
	events  []auditlog.AuditEvent
	flushed bool
}

func (m *mockCaptureDest) Write(_ context.Context, event auditlog.AuditEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockCaptureDest) Flush(_ context.Context) error {
	m.flushed = true
	return nil
}

func baseEvent() auditlog.AuditEvent {
	return auditlog.AuditEvent{
		Timestamp: time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC),
		SandboxID: "sb-a1b2c3d4",
		EventType: "shell_command",
		Source:    "audit-log",
		Detail:    map[string]interface{}{},
	}
}

// TestRedactAWSKeyID: AKIA-style access key IDs must be replaced with [REDACTED].
func TestRedactAWSKeyID(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, nil)

	ev := baseEvent()
	ev.Detail["aws_key"] = "AKIAIOSFODNN7EXAMPLE"

	ctx := context.Background()
	if err := rd.Write(ctx, ev); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	if len(inner.events) != 1 {
		t.Fatalf("expected 1 event forwarded, got %d", len(inner.events))
	}
	got, _ := inner.events[0].Detail["aws_key"].(string)
	if got != "[REDACTED]" {
		t.Errorf("expected aws_key to be [REDACTED], got %q", got)
	}
}

// TestRedactBearerToken: Bearer tokens must be replaced with [REDACTED].
func TestRedactBearerToken(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, nil)

	ev := baseEvent()
	ev.Detail["auth"] = "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature"

	ctx := context.Background()
	if err := rd.Write(ctx, ev); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	got, _ := inner.events[0].Detail["auth"].(string)
	if got != "[REDACTED]" {
		t.Errorf("expected auth to be [REDACTED], got %q", got)
	}
}

// TestRedactHexSecret: Hex strings of 40+ characters must be replaced with [REDACTED].
func TestRedactHexSecret(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, nil)

	ev := baseEvent()
	ev.Detail["token"] = "a94a8fe5ccb19ba61c4c0873d391e987982fbbd3" // 40-char hex SHA1

	ctx := context.Background()
	if err := rd.Write(ctx, ev); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	got, _ := inner.events[0].Detail["token"].(string)
	if got != "[REDACTED]" {
		t.Errorf("expected hex token to be [REDACTED], got %q", got)
	}
}

// TestRedactSSMLiteralValue: SSM secret literal passed at construction must be replaced with [REDACTED].
func TestRedactSSMLiteralValue(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, []string{"my-secret-password"})

	ev := baseEvent()
	ev.Detail["env_value"] = "my-secret-password"

	ctx := context.Background()
	if err := rd.Write(ctx, ev); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	got, _ := inner.events[0].Detail["env_value"].(string)
	if got != "[REDACTED]" {
		t.Errorf("expected literal secret to be [REDACTED], got %q", got)
	}
}

// TestRedactPreservesStructuralFields: SandboxID, EventType, Timestamp, Source must NOT be modified.
func TestRedactPreservesStructuralFields(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, nil)

	ev := baseEvent()
	ev.SandboxID = "sb-a1b2c3d4"
	ev.EventType = "shell_command"
	ev.Source = "audit-log"
	ts := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)
	ev.Timestamp = ts
	ev.Detail["safe"] = "harmless-value"

	ctx := context.Background()
	if err := rd.Write(ctx, ev); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	got := inner.events[0]
	if got.SandboxID != "sb-a1b2c3d4" {
		t.Errorf("SandboxID modified: got %q, want %q", got.SandboxID, "sb-a1b2c3d4")
	}
	if got.EventType != "shell_command" {
		t.Errorf("EventType modified: got %q, want %q", got.EventType, "shell_command")
	}
	if got.Source != "audit-log" {
		t.Errorf("Source modified: got %q, want %q", got.Source, "audit-log")
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp modified: got %v, want %v", got.Timestamp, ts)
	}
}

// TestRedactNestedMap: secret in detail["env"]["TOKEN"] must be redacted.
func TestRedactNestedMap(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, []string{"super-secret"})

	ev := baseEvent()
	ev.Detail["env"] = map[string]interface{}{
		"TOKEN": "super-secret",
	}

	ctx := context.Background()
	if err := rd.Write(ctx, ev); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	envMap, ok := inner.events[0].Detail["env"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected detail.env to be a map, got %T", inner.events[0].Detail["env"])
	}
	token, _ := envMap["TOKEN"].(string)
	if token != "[REDACTED]" {
		t.Errorf("expected nested env.TOKEN to be [REDACTED], got %q", token)
	}
}

// TestRedactStringSlice: secret in detail["args"][1] must be redacted.
func TestRedactStringSlice(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, []string{"slice-secret"})

	ev := baseEvent()
	ev.Detail["args"] = []interface{}{"--token", "slice-secret"}

	ctx := context.Background()
	if err := rd.Write(ctx, ev); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	args, ok := inner.events[0].Detail["args"].([]interface{})
	if !ok {
		t.Fatalf("expected detail.args to be a slice, got %T", inner.events[0].Detail["args"])
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[0].(string) != "--token" {
		t.Errorf("expected args[0] to be --token, got %q", args[0])
	}
	if args[1].(string) != "[REDACTED]" {
		t.Errorf("expected args[1] to be [REDACTED], got %q", args[1])
	}
}

// TestRedactDelegatesAfterRedaction: RedactingDestination must forward the (redacted) event to inner.
func TestRedactDelegatesAfterRedaction(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, nil)

	ev := baseEvent()
	ev.Detail["key"] = "value"

	ctx := context.Background()
	if err := rd.Write(ctx, ev); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	if len(inner.events) != 1 {
		t.Errorf("expected inner Destination to receive exactly 1 event, got %d", len(inner.events))
	}
}

// TestRedactFlushDelegates: Flush must delegate to inner.
func TestRedactFlushDelegates(t *testing.T) {
	inner := &mockCaptureDest{}
	rd := auditlog.NewRedactingDestination(inner, nil)

	ctx := context.Background()
	if err := rd.Flush(ctx); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	if !inner.flushed {
		t.Error("expected inner.Flush to be called")
	}
}

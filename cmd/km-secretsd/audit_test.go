package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPipeAudit_EmitsCanonicalSchema(t *testing.T) {
	// A regular file stands in for the FIFO: the writer must not care.
	p := filepath.Join(t.TempDir(), "audit-pipe")
	a := &PipeAudit{Path: p, SandboxID: "sb-a1b2c3d4"}

	if err := a.Emit("secret_unseal", map[string]any{
		"as":   "claude",
		"keys": []string{"ANTHROPIC_API_KEY"},
		"pid":  4242,
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Timestamp string         `json:"timestamp"`
		SandboxID string         `json:"sandbox_id"`
		EventType string         `json:"event_type"`
		Source    string         `json:"source"`
		Detail    map[string]any `json:"detail"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("audit line is not JSON: %v (%q)", err, raw)
	}
	if ev.SandboxID != "sb-a1b2c3d4" || ev.EventType != "secret_unseal" || ev.Source != "km-secretsd" {
		t.Errorf("bad envelope: %+v", ev)
	}
	if ev.Timestamp == "" {
		t.Error("timestamp missing")
	}
	if ev.Detail["as"] != "claude" {
		t.Errorf("detail lost: %+v", ev.Detail)
	}
	if raw[len(raw)-1] != '\n' {
		t.Error("audit line must be newline-terminated JSONL")
	}
}

func TestPipeAudit_MissingPipeIsNotFatal(t *testing.T) {
	// Losing an audit line must never cost an agent turn. The unseal is the
	// product; the record is best-effort, exactly like uploadCapture.
	a := &PipeAudit{Path: filepath.Join(t.TempDir(), "nope", "audit-pipe"), SandboxID: "sb-1"}
	if err := a.Emit("secret_unseal", nil); err != nil {
		t.Errorf("Emit returned an error for an absent pipe: %v", err)
	}
}

func TestPipeAudit_NeverLogsValues(t *testing.T) {
	// Guard against a future refactor passing values instead of names.
	p := filepath.Join(t.TempDir(), "audit-pipe")
	a := &PipeAudit{Path: p, SandboxID: "sb-1"}
	if err := a.Emit("secret_unseal", map[string]any{"keys": []string{"API_KEY"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if len(raw) == 0 {
		t.Fatal("nothing written")
	}
	// The detail map is the only channel; assert the writer adds nothing itself.
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if _, bad := ev["values"]; bad {
		t.Error("audit envelope carries a values field")
	}
}

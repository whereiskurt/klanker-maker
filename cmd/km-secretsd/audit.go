package main

import (
	"encoding/json"
	"os"
	"time"
)

// AuditWriter records what the broker did. Every unseal is logged because the
// broker's value is that theft becomes an active, attributable, recorded act
// rather than a passive scoop that leaves no trace.
type AuditWriter interface {
	Emit(eventType string, detail map[string]any) error
}

// PipeAudit writes JSONL to the km-audit-log sidecar's input FIFO, which ships
// it to the sandbox's CloudWatch audit stream. The envelope is the schema
// locked in sidecars/audit-log/auditlog.go.
type PipeAudit struct {
	Path      string
	SandboxID string
}

// Emit writes one event. It is BEST-EFFORT by construction: an absent or
// blocked pipe returns nil, because losing an audit line must never cost an
// agent turn. Detail carries key NAMES only; values never reach this path.
func (a *PipeAudit) Emit(eventType string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	line, err := json.Marshal(map[string]any{
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"sandbox_id": a.SandboxID,
		"event_type": eventType,
		"source":     "km-secretsd",
		"detail":     detail,
	})
	if err != nil {
		return nil
	}

	f, err := os.OpenFile(a.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
	return nil
}

// NopAudit discards events. Used in tests and when no pipe is configured.
type NopAudit struct{}

// Emit discards the event.
func (NopAudit) Emit(string, map[string]any) error { return nil }

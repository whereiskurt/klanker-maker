// Package cmd — doctor_frozen_test.go
// Unit tests for the Phase 121 doctor checks:
//   - checkFrozenSandboxes: WARN when any sandbox has action_frozen=true
package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ---- Minimal fake lister for doctor tests ----

// frozenFakeLister is a SandboxLister returning a fixed slice of SandboxRecords.
// Used to inject frozen/non-frozen sandboxes into the checkFrozenSandboxes test.
type frozenFakeLister struct {
	records []kmaws.SandboxRecord
	err     error
}

func (f *frozenFakeLister) ListSandboxes(_ context.Context, _ bool) ([]kmaws.SandboxRecord, error) {
	return f.records, f.err
}

// ---- Tests ----

// TestCheckFrozenSandboxes_NoneOK verifies that checkFrozenSandboxes returns OK
// when no sandboxes have action_frozen=true.
func TestCheckFrozenSandboxes_NoneOK(t *testing.T) {
	lister := &frozenFakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-aaa111", Status: "running", ActionFrozen: false},
			{SandboxID: "sb-bbb222", Status: "stopped", ActionFrozen: false},
		},
	}
	result := checkFrozenSandboxes(context.Background(), lister)
	if result.Status != CheckOK {
		t.Errorf("expected CheckOK, got %v (message: %q)", result.Status, result.Message)
	}
}

// TestCheckFrozenSandboxes_WarnWhenFrozen verifies that checkFrozenSandboxes returns
// WARN when at least one sandbox has action_frozen=true, and names the sandbox in
// the message (CLI-03).
func TestCheckFrozenSandboxes_WarnWhenFrozen(t *testing.T) {
	frozenAt := time.Now().UTC().Add(-30 * time.Minute)
	lister := &frozenFakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID:    "sb-frozen1",
				Status:       "running",
				ActionFrozen: true,
				FrozenReason: "quota:push:daily:10",
				FrozenAt:     &frozenAt,
				FrozenBy:     "auto:push:daily",
			},
			{SandboxID: "sb-clean2", Status: "running", ActionFrozen: false},
		},
	}
	result := checkFrozenSandboxes(context.Background(), lister)
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn, got %v (message: %q)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "sb-frozen1") {
		t.Errorf("message should mention frozen sandbox ID, got: %q", result.Message)
	}
	if !strings.Contains(result.Message, "quota:push:daily:10") {
		t.Errorf("message should mention freeze reason, got: %q", result.Message)
	}
	if result.Remediation == "" {
		t.Error("remediation should suggest 'km unlock'")
	}
}

// TestCheckFrozenSandboxes_MultipleFrozen verifies that all frozen sandboxes are
// listed in the WARN message.
func TestCheckFrozenSandboxes_MultipleFrozen(t *testing.T) {
	lister := &frozenFakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-frz1", ActionFrozen: true, FrozenReason: "reason-A"},
			{SandboxID: "sb-frz2", ActionFrozen: true, FrozenReason: "reason-B"},
			{SandboxID: "sb-ok3", ActionFrozen: false},
		},
	}
	result := checkFrozenSandboxes(context.Background(), lister)
	if result.Status != CheckWarn {
		t.Errorf("expected CheckWarn, got %v", result.Status)
	}
	if !strings.Contains(result.Message, "sb-frz1") {
		t.Errorf("message missing sb-frz1: %q", result.Message)
	}
	if !strings.Contains(result.Message, "sb-frz2") {
		t.Errorf("message missing sb-frz2: %q", result.Message)
	}
	if !strings.Contains(result.Message, "2 frozen") {
		t.Errorf("message should say '2 frozen': %q", result.Message)
	}
}

// TestCheckFrozenSandboxes_NilListerSkipped verifies that a nil lister returns
// CheckSkipped (not an error) — mirrors other lister-dependent doctor checks.
func TestCheckFrozenSandboxes_NilListerSkipped(t *testing.T) {
	result := checkFrozenSandboxes(context.Background(), nil)
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for nil lister, got %v", result.Status)
	}
}

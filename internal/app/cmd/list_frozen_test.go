// Package cmd_test — list_frozen_test.go
// CLI-03 test: km list renders a FROZEN marker for sandboxes with action_frozen=true.
package cmd_test

import (
	"strings"
	"testing"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// TestListCmd_FrozenMarker verifies that a frozen sandbox (action_frozen=true)
// shows the FROZEN marker in km list output (CLI-03).
func TestListCmd_FrozenMarker(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID:    "sb-frozen1",
				Alias:        "frozen-box",
				Profile:      "open-dev",
				Substrate:    "ec2",
				Region:       "us-east-1",
				Status:       "running",
				ActionFrozen: true,
				FrozenReason: "quota:push:daily:10",
			},
			{
				SandboxID:    "sb-normal2",
				Alias:        "normal-box",
				Profile:      "open-dev",
				Substrate:    "ec2",
				Region:       "us-east-1",
				Status:       "running",
				ActionFrozen: false,
			},
		},
	}

	// Default (narrow) mode.
	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("km list returned error: %v", err)
	}

	// The frozen sandbox row must contain the FROZEN marker.
	if !strings.Contains(out, "FROZEN") {
		t.Errorf("expected FROZEN marker in list output for frozen sandbox, got:\n%s", out)
	}

	// The non-frozen sandbox must NOT have the FROZEN marker (only on frozen row).
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "sb-normal2") && strings.Contains(line, "FROZEN") {
			t.Errorf("non-frozen sandbox sb-normal2 should not show FROZEN marker, but got: %q", line)
		}
	}
}

// TestListCmd_FrozenMarkerWide verifies that the FROZEN marker also appears in
// --wide output mode (covers both rendering paths).
func TestListCmd_FrozenMarkerWide(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID:    "sb-wfrz1",
				Alias:        "wfrz",
				Profile:      "open-dev",
				Substrate:    "ec2",
				Region:       "us-east-1",
				Status:       "running",
				ActionFrozen: true,
			},
		},
	}

	out, err := runListCmd(t, lister, "--wide")
	if err != nil {
		t.Fatalf("km list --wide returned error: %v", err)
	}

	if !strings.Contains(out, "FROZEN") {
		t.Errorf("expected FROZEN marker in --wide output, got:\n%s", out)
	}
}

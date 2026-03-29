package cmd_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// TestExtendMaxLifetime_WithinCap verifies that extending within MaxLifetime succeeds.
func TestExtendMaxLifetime_WithinCap(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	// MaxLifetime = 48h; sandbox created at 2026-03-20 12:00 UTC
	// max expiry = 2026-03-22 12:00 UTC
	// newExpiry  = 2026-03-21 12:00 UTC (within cap)
	meta := &kmaws.SandboxMetadata{
		SandboxID:   "sb-abc001",
		CreatedAt:   createdAt,
		MaxLifetime: "48h",
	}
	newExpiry := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)

	err := cmd.CheckMaxLifetime(meta, newExpiry)
	if err != nil {
		t.Errorf("expected no error for newExpiry within cap, got: %v", err)
	}
}

// TestExtendMaxLifetime_ExceedsCap verifies that extending beyond MaxLifetime returns error
// with a clear message including the cap duration and sandbox creation time.
func TestExtendMaxLifetime_ExceedsCap(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	// MaxLifetime = 48h; max expiry = 2026-03-22 12:00 UTC
	// newExpiry   = 2026-03-23 12:00 UTC (exceeds cap by 24h)
	meta := &kmaws.SandboxMetadata{
		SandboxID:   "sb-abc002",
		CreatedAt:   createdAt,
		MaxLifetime: "48h",
	}
	newExpiry := time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC)

	err := cmd.CheckMaxLifetime(meta, newExpiry)
	if err == nil {
		t.Fatal("expected error for newExpiry exceeding max lifetime cap, got nil")
	}

	// Error must mention the cap duration, creation time, and max expiry date
	errMsg := err.Error()
	for _, want := range []string{"max lifetime", "48h", "2026-03-22"} {
		if !strings.Contains(errMsg, want) {
			t.Errorf("error message missing %q; got: %s", want, errMsg)
		}
	}
}

// TestExtendMaxLifetime_NoCapSet verifies backward compatibility: no MaxLifetime = no enforcement.
func TestExtendMaxLifetime_NoCapSet(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	meta := &kmaws.SandboxMetadata{
		SandboxID:   "sb-abc003",
		CreatedAt:   createdAt,
		MaxLifetime: "", // no cap
	}
	// newExpiry far in the future — should be allowed when no cap is set
	newExpiry := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	err := cmd.CheckMaxLifetime(meta, newExpiry)
	if err != nil {
		t.Errorf("expected no error when MaxLifetime is empty (no cap), got: %v", err)
	}
}

// TestExtendMaxLifetime_ExpiredSandboxRespectsCap verifies that when TTL has already
// expired and extend computes newExpiry from now, MaxLifetime is still enforced.
func TestExtendMaxLifetime_ExpiredSandboxRespectsCap(t *testing.T) {
	// Sandbox created 100 hours ago, MaxLifetime = 24h (already exceeded by creation)
	// This tests that even when extending from now, the cap is honored.
	createdAt := time.Now().Add(-100 * time.Hour)
	meta := &kmaws.SandboxMetadata{
		SandboxID:   "sb-abc004",
		CreatedAt:   createdAt,
		MaxLifetime: "24h",
	}
	// newExpiry = now + 1h (extends from now because TTL expired)
	newExpiry := time.Now().Add(1 * time.Hour)

	err := cmd.CheckMaxLifetime(meta, newExpiry)
	if err == nil {
		t.Fatal("expected error: sandbox max lifetime (24h from creation 100h ago) is already exceeded")
	}
	if !strings.Contains(err.Error(), "max lifetime") {
		t.Errorf("error message missing 'max lifetime'; got: %s", err.Error())
	}
}

// ---- Remote path tests ----

// runExtendRemote invokes km extend --remote via injected publisher.
func runExtendRemote(t *testing.T, pub cmd.RemoteCommandPublisher, sandboxID, duration string) error {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	extendCmd := cmd.NewExtendCmdWithPublisher(cfg, pub)
	root.AddCommand(extendCmd)
	root.SetArgs([]string{"extend", "--remote", sandboxID, duration})
	return root.Execute()
}

// TestExtendCmd_RemotePublishesCorrectEvent verifies km extend --remote dispatches
// an EventBridge event with eventType "extend" and duration in extra params.
func TestExtendCmd_RemotePublishesCorrectEvent(t *testing.T) {
	pub := &fakePublisher{}
	err := runExtendRemote(t, pub, "sb-aabbccdd", "2h")
	if err != nil {
		t.Fatalf("extend --remote returned error: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.sandboxID != "sb-aabbccdd" {
		t.Errorf("sandboxID = %q, want %q", call.sandboxID, "sb-aabbccdd")
	}
	if call.eventType != "extend" {
		t.Errorf("eventType = %q, want %q", call.eventType, "extend")
	}
	// extra params should contain "duration" key and "2h" value
	foundDuration := false
	for i := 0; i+1 < len(call.extra); i += 2 {
		if call.extra[i] == "duration" && call.extra[i+1] == "2h" {
			foundDuration = true
		}
	}
	if !foundDuration {
		t.Errorf("expected duration=2h in extra params, got: %v", call.extra)
	}
}

// TestExtendCmd_RemotePublishFailure verifies EventBridge publish failure propagates.
func TestExtendCmd_RemotePublishFailure(t *testing.T) {
	pub := &fakePublisher{err: errors.New("eventbridge unavailable")}
	err := runExtendRemote(t, pub, "sb-aabbccdd", "1h")
	if err == nil {
		t.Fatal("expected error when publisher fails, got nil")
	}
}

// TestExtendCmd_RemoteInvalidSandboxID verifies invalid sandbox ID is rejected before publish.
func TestExtendCmd_RemoteInvalidSandboxID(t *testing.T) {
	pub := &fakePublisher{}
	err := runExtendRemote(t, pub, "NOT-VALID", "1h")
	if err == nil {
		t.Fatal("expected error for invalid sandbox ID, got nil")
	}
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publisher calls for invalid ID, got %d", len(pub.calls))
	}
}

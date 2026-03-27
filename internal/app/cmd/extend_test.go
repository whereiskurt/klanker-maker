package cmd_test

import (
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
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

// TestExtendMaxLifetime_ExceedsCap verifies that extending beyond MaxLifetime returns error.
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

	// Error must mention the cap duration
	errMsg := err.Error()
	for _, want := range []string{"max lifetime", "48h", "2026-03-22"} {
		if !containsStr(errMsg, want) {
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
	if !containsStr(err.Error(), "max lifetime") {
		t.Errorf("error message missing 'max lifetime'; got: %s", err.Error())
	}
}

// containsStr is a helper to avoid importing strings in the test.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

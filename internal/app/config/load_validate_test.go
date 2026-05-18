package config_test

// Wave 5 RED scaffolding — Phase 84.3 Plan 06.
// Integration tests for config.Load() rejecting placeholder artifacts_bucket values.
//
// WHY THESE TESTS ARE RED against current code:
//
//  validateArtifactsBucket exists in cmd/configure.go and correctly identifies
//  placeholder values. However, it is NOT called from config.Load(). Plan 09
//  wires validateArtifactsBucket into config.Load() for non-empty bucket values.
//
//  RED tests:
//    TestConfigLoad_RejectsPlaceholderArtifactsBucket — fails because Load() returns nil error
//    TestConfigLoad_RejectsAngleBracketPlaceholder — same, angle-bracket variant
//
//  GREEN tests (must stay GREEN before and after Plan 09):
//    TestConfigLoad_AcceptsRealBucket — a valid bucket name passes Load()
//    TestConfigLoad_AcceptsEmptyBucket — empty is OK (fresh install / km configure path)

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// Note: writeKMConfigDrift and changeToDir are declared in config_load_drift_test.go
// (same package config_test). They are reused here without redeclaration.

// TestConfigLoad_RejectsPlaceholderArtifactsBucket verifies that config.Load()
// returns a non-nil error when km-config.yaml contains the exact placeholder
// sentinel "km-artifacts-12345".
//
// RED against current code: config.Load() returns nil error for any ArtifactsBucket
// value (validateArtifactsBucket is not called from Load). Plan 09 wires it in.
func TestConfigLoad_RejectsPlaceholderArtifactsBucket(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: km-artifacts-12345
resource_prefix: km
`)
	changeToDir(t, dir)

	cfg, err := config.Load()
	if err == nil {
		t.Errorf("config.Load() returned nil error for placeholder bucket km-artifacts-12345; want non-nil error (RED: Plan 09 will fix)")
		t.Logf("cfg.ArtifactsBucket = %q", cfg.ArtifactsBucket)
	} else {
		// Already GREEN (unexpected — only after Plan 09 lands): verify error message quality.
		msg := err.Error()
		if !strings.Contains(msg, "placeholder") && !strings.Contains(msg, "km-artifacts-12345") {
			t.Errorf("error %q should mention 'placeholder' or 'km-artifacts-12345'", msg)
		}
	}
}

// TestConfigLoad_RejectsAngleBracketPlaceholder verifies that config.Load()
// returns a non-nil error when km-config.yaml contains an angle-bracket placeholder
// for artifacts_bucket (e.g. "<prefix>-artifacts-12345678").
//
// RED against current code for the same reason as TestConfigLoad_RejectsPlaceholderArtifactsBucket.
func TestConfigLoad_RejectsAngleBracketPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: "<prefix>-artifacts-12345678"
resource_prefix: km
`)
	changeToDir(t, dir)

	cfg, err := config.Load()
	if err == nil {
		t.Errorf("config.Load() returned nil error for angle-bracket placeholder bucket; want non-nil error (RED: Plan 09 will fix)")
		t.Logf("cfg.ArtifactsBucket = %q", cfg.ArtifactsBucket)
	} else {
		msg := err.Error()
		if !strings.Contains(msg, "placeholder") {
			t.Errorf("error %q should mention 'placeholder'", msg)
		}
	}
}

// TestConfigLoad_AcceptsRealBucket verifies that config.Load() returns nil error
// when km-config.yaml contains a properly derived bucket name
// (format: {prefix}-artifacts-{12-digit-account-id}).
//
// GREEN both before and after Plan 09 (positive baseline — must not regress).
func TestConfigLoad_AcceptsRealBucket(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: km-artifacts-123456789012
resource_prefix: km
`)
	changeToDir(t, dir)

	_, err := config.Load()
	if err != nil {
		t.Errorf("config.Load() returned error for valid bucket km-artifacts-123456789012: %v", err)
	}
}

// TestConfigLoad_AcceptsEmptyBucket verifies that config.Load() returns nil error
// when km-config.yaml omits artifacts_bucket (or sets it to empty string).
//
// Empty is OK — this is the state during km configure (before derivation) and on
// fresh installs. config.Load() must not fail on a fresh system.
//
// GREEN both before and after Plan 09 (positive baseline — must not regress).
func TestConfigLoad_AcceptsEmptyBucket(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: ""
resource_prefix: km
`)
	changeToDir(t, dir)

	_, err := config.Load()
	if err != nil {
		t.Errorf("config.Load() returned error for empty artifacts_bucket: %v", err)
	}
}

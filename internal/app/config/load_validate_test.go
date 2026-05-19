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

// TestConfigLoad_AcceptsLegacyBucketLiteral documents Phase 84.4.1.1 Gap #4 behavior change:
// ValidateArtifactsBucket now enforces the canonical ${prefix}-artifacts-${12-digit-account-id}
// shape. The legacy bucket "km-artifacts-12345" has a 5-digit account suffix and fails the
// canonical regex.
//
// Phase 84.4-08 UAT originally intended to ACCEPT this name (it was a real legacy bucket).
// Phase 84.4.1.1 Plan 02 introduces the canonical shape enforcement via ValidateArtifactsBucket
// wired into config.Load(). This means "km-artifacts-12345" now returns an error because
// 12345 is 5 digits, not 12.
//
// Operators with pre-Phase-84.3 installs using this exact bucket name will see a config.Load()
// error and should re-run km configure to derive the correct canonical name
// (or add allow_non_canonical_bucket: true to km-config.yaml when that escape hatch is
// implemented in a follow-on plan).
func TestConfigLoad_AcceptsLegacyBucketLiteral(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: km-artifacts-12345
resource_prefix: km
`)
	changeToDir(t, dir)

	_, err := config.Load()
	if err == nil {
		t.Errorf("config.Load() accepted km-artifacts-12345; want error (non-canonical: 5-digit suffix)")
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

// TestConfigLoad_RejectsNonCanonicalBucket verifies Gap #2b + Gap #4 (Phase 84.4.1.1 Plan 02+03):
// config.Load() calls ValidateArtifactsBucket so non-canonical bucket names
// (e.g. tg-km-artifacts-use1-abcd0123) hard-fail before reaching bootstrap.
func TestConfigLoad_RejectsNonCanonicalBucket(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: tg-km-artifacts-use1-abcd0123
resource_prefix: tg
`)
	changeToDir(t, dir)

	cfg, err := config.Load()
	if err == nil {
		t.Errorf("config.Load() returned nil error for non-canonical bucket tg-km-artifacts-use1-abcd0123; want error")
		t.Logf("cfg.ArtifactsBucket = %q", cfg.ArtifactsBucket)
	} else if !strings.Contains(err.Error(), "canonical shape") {
		t.Errorf("error %q should mention 'canonical shape'", err.Error())
	}
}

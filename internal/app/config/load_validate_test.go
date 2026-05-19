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

// TestConfigLoad_AcceptsLegacyBucketLiteral preserves the Phase 84.4-08 UAT behavior:
// the literal "km-artifacts-12345" is a real legacy bucket on at least one operator's
// install (predating Phase 84.3's ${prefix}-artifacts-${account_id} derivation).
//
// Phase 84.4.1.1 Plan 02 originally introduced canonical-shape enforcement at config.Load()
// time. That broke this operator's km commands (every CLI invocation hard-failed on the
// legacy literal). Phase 84.4.1.1 post-UAT softened Load() to placeholder-only — the
// canonical-shape check lives in configure.go (catches typos at config-write time) but
// not at Load() (legacy installs must keep working).
//
// This test verifies Load() accepts the legacy literal. The canonical-shape rejection is
// tested in cmd/configure_84_3_test.go (configure-time only).
func TestConfigLoad_AcceptsLegacyBucketLiteral(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: km-artifacts-12345
resource_prefix: km
`)
	changeToDir(t, dir)

	_, err := config.Load()
	if err != nil {
		t.Errorf("config.Load() rejected legacy literal km-artifacts-12345: %v; want nil (legacy installs must keep working)", err)
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

// TestConfigLoad_AcceptsNonCanonicalBucket documents Phase 84.4.1.1 post-UAT behavior:
// non-canonical-shaped but real bucket names (e.g. tg-km-artifacts-use1-abcd0123) pass
// at Load() time. Rationale: once a bucket is deployed, the config must keep working
// even if the name predates the canonical-shape convention.
//
// The canonical-shape check still applies at configure time (cmd/configure.go's
// cmdCanonicalBucketRE) — that catches typos when an operator writes a new config.
// Configure-time enforcement covered by TestConfigure_ValidateArtifactsBucket_CanonicalShape.
func TestConfigLoad_AcceptsNonCanonicalBucket(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: tg-km-artifacts-use1-abcd0123
resource_prefix: tg
`)
	changeToDir(t, dir)

	_, err := config.Load()
	if err != nil {
		t.Errorf("config.Load() rejected non-canonical bucket tg-km-artifacts-use1-abcd0123: %v; want nil (canonical check lives in configure.go, not Load)", err)
	}
}

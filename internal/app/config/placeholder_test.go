package config

// Phase 84.4.1 regression test for d551bba isPlaceholderBucket fix.
//
// This file lives in package config (not config_test) because isPlaceholderBucket
// is unexported. It is a pure unit test with no external dependencies.

import "testing"

// TestIsPlaceholderBucket_AcceptsLegacyKmArtifacts12345 verifies the d551bba
// regression fix: literal "km-artifacts-12345" is a legitimate operator-chosen
// bucket name (Phase 84.4-08 UAT discovery), not a placeholder.
//
// Prior to d551bba, isPlaceholderBucket rejected "km-artifacts-12345" as the
// km-config.example.yaml sentinel value. That broke every `km` command for the
// operator whose actual bucket name happens to be "km-artifacts-12345" — a valid
// pre-derivation name from before Phase 84.3's ${prefix}-artifacts-${account_id}
// derivation was introduced.
//
// Phase 84.4.1 closes LAMBDA-ZIP-MAKEFILE-PARITY (regression test requirement).
func TestIsPlaceholderBucket_AcceptsLegacyKmArtifacts12345(t *testing.T) {
	// "km-artifacts-12345" must NOT be treated as a placeholder.
	if isPlaceholderBucket("km-artifacts-12345") {
		t.Errorf("km-artifacts-12345 must NOT be treated as a placeholder (d551bba regression); it is a legitimate operator-chosen bucket name")
	}

	// Sanity: angle-bracket tokens ARE placeholders (the post-d551bba contract).
	if !isPlaceholderBucket("<prefix>-artifacts-<account-id>") {
		t.Errorf("expected angle-bracket form to be flagged as placeholder")
	}

	// Empty string is NOT a placeholder (it's unconfigured).
	if isPlaceholderBucket("") {
		t.Errorf("empty string must not be treated as placeholder")
	}

	// Legitimate real bucket names are not placeholders.
	legit := []string{
		"km-artifacts-123456789012",
		"whereiskurt-artifacts-987654321098",
		"tg-artifacts-111122223333",
	}
	for _, name := range legit {
		if isPlaceholderBucket(name) {
			t.Errorf("legitimate bucket %q must not be treated as placeholder", name)
		}
	}
}

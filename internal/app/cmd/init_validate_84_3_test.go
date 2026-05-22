package cmd

// Wave 5 RED scaffolding — Phase 84.3 Plan 06.
// Integration tests for runInitPlan hard-failing on placeholder artifacts_bucket.
//
// WHY THIS TEST IS RED against current code:
//
//  validateArtifactsBucket exists in cmd/configure.go and correctly identifies
//  placeholder values (km-artifacts-12345, angle-bracket patterns, etc.).
//  However, neither runInitPlan nor runInitPlanWithWriter calls
//  validateArtifactsBucket before delegating to RunInitPlanWithRunner.
//  Plan 09 adds the validateArtifactsBucket call early in runInitPlanWithWriter
//  (or a shared pre-validation helper) so operators get an actionable error
//  before any terragrunt invocation.
//
//  Testing strategy: set KM_REPO_ROOT to an empty temp dir so RunInitPlanWithRunner
//  finds no modules and returns nil immediately. This makes the test fast and
//  reveals the gap: runInitPlanWithWriter returns nil for a placeholder bucket
//  (no validation gate exists). After Plan 09, it returns early with a non-nil
//  placeholder error before even reaching RunInitPlanWithRunner.
//
//  RED test:
//    TestInitPlan_HardFailsOnPlaceholderBucket — returns nil today (no validation).
//      Plan 09 fixes by adding validateArtifactsBucket check.
//
//  Also confirms validateArtifactsBucket itself is wired correctly (positive):
//    TestValidateArtifactsBucket_RejectsPlaceholder — unit-level, GREEN now.
//    TestValidateArtifactsBucket_AcceptsValidBucket — unit-level, GREEN now.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// makeEmptyRepoRoot creates a minimal directory tree that looks like a repo root
// (has go.mod) but has no infra/live modules. RunInitPlanWithRunner will find no
// modules and return nil without calling terragrunt — keeping the test fast.
func makeEmptyRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// Write a stub go.mod so findRepoRoot() stops walking at this dir.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\ngo 1.21\n"), 0600); err != nil {
		t.Fatalf("makeEmptyRepoRoot: write go.mod: %v", err)
	}
	// Create the region dir that RunInitPlanWithRunner looks for, but with no module subdirs.
	regionDir := filepath.Join(root, "infra", "live", "use1")
	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		t.Fatalf("makeEmptyRepoRoot: mkdir infra/live/use1: %v", err)
	}
	return root
}

// TestInitPlan_HardFailsOnPlaceholderBucket verifies that runInitPlanWithWriter
// returns a non-nil error containing "placeholder" or "km-artifacts-12345" when
// cfg.ArtifactsBucket is the placeholder sentinel.
//
// WHY THIS IS RED: runInitPlanWithWriter does not call validateArtifactsBucket.
// It goes straight to RunInitPlanWithRunner, which finds no modules in the empty
// repo root and returns nil. Plan 09 adds the validateArtifactsBucket check
// before RunInitPlanWithRunner is called.
//
// Expected failure mode (current code):
//   error is nil — test fails with "expected non-nil error for placeholder bucket, got nil"
//   (RunInitPlanWithRunner returned nil because no modules exist in empty root)
func TestInitPlan_HardFailsOnPlaceholderBucket(t *testing.T) {
	// Use an empty repo root so RunInitPlanWithRunner returns nil without terragrunt.
	emptyRoot := makeEmptyRepoRoot(t)
	t.Setenv("KM_REPO_ROOT", emptyRoot)

	cfg := &config.Config{
		PrimaryRegion:   "us-east-1",
		ArtifactsBucket: "<prefix>-artifacts-<account-id>", // unreplaced km-config.example.yaml placeholder
		ResourcePrefix:  "km",
		AWSProfile:      "klanker-terraform",
	}
	var buf bytes.Buffer

	err := runInitPlanWithWriter(cfg, "klanker-terraform", "us-east-1", &buf, false, false)
	if err == nil {
		t.Errorf("runInitPlanWithWriter returned nil error for angle-bracket placeholder bucket; want non-nil error")
	} else {
		msg := err.Error()
		if !strings.Contains(msg, "placeholder") {
			t.Errorf("error %q should mention 'placeholder'", msg)
		}
	}
}

// TestInitPlan_PassesWithValidBucket verifies that runInitPlanWithWriter does NOT
// return a placeholder validation error for a properly derived bucket name.
// The function may return other errors (module-related, etc.) but must NOT
// fail due to bucket validation.
//
// GREEN both before and after Plan 09 (positive baseline — must not regress).
func TestInitPlan_PassesWithValidBucket(t *testing.T) {
	// Use an empty repo root so RunInitPlanWithRunner returns nil without terragrunt.
	emptyRoot := makeEmptyRepoRoot(t)
	t.Setenv("KM_REPO_ROOT", emptyRoot)

	cfg := &config.Config{
		PrimaryRegion:   "us-east-1",
		ArtifactsBucket: "km-artifacts-123456789012", // valid derived name
		ResourcePrefix:  "km",
		AWSProfile:      "klanker-terraform",
	}
	var buf bytes.Buffer

	err := runInitPlanWithWriter(cfg, "klanker-terraform", "us-east-1", &buf, false, false)

	// Any error here must NOT be a placeholder validation error.
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "placeholder") || strings.Contains(msg, "km-artifacts-12345") {
			t.Errorf("runInitPlanWithWriter returned a placeholder error for a valid bucket: %v", err)
		}
		t.Logf("(expected) non-placeholder error: %v", err)
	}
}

// TestValidateArtifactsBucket_RejectsPlaceholder is a direct unit test of
// validateArtifactsBucket (unexported, accessible in package cmd). Confirms
// the validation logic is correct independently of the wiring.
//
// GREEN before and after Plan 09 (the function already exists — Plan 09 only wires it in).
func TestValidateArtifactsBucket_RejectsPlaceholder(t *testing.T) {
	cases := []struct {
		name   string
		bucket string
	}{
		{"angle-bracket prefix", "<prefix>-artifacts-12345678"},
		{"angle-bracket full", "<my-bucket>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArtifactsBucket(tc.bucket)
			if err == nil {
				t.Errorf("validateArtifactsBucket(%q) returned nil; want non-nil error", tc.bucket)
			}
		})
	}
}

// TestValidateArtifactsBucket_AcceptsValidBucket confirms valid bucket names pass.
//
// GREEN before and after Plan 09 (positive baseline).
func TestValidateArtifactsBucket_AcceptsValidBucket(t *testing.T) {
	err := validateArtifactsBucket("km-artifacts-123456789012")
	if err != nil {
		t.Errorf("validateArtifactsBucket(valid) returned error: %v", err)
	}
}

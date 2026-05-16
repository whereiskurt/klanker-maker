package cmd_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// TestInitSESPreflight verifies that RunInitWithRunner returns an actionable
// error when the shared SES rule set is missing, and succeeds when it exists.
func TestInitSESPreflight(t *testing.T) {
	// Preserve and restore InitSESPreflight after the test.
	origPreflight := cmd.InitSESPreflight
	t.Cleanup(func() { cmd.InitSESPreflight = origPreflight })

	repoRoot := t.TempDir()

	// Create the ses module directory so RunInitWithRunner doesn't skip it.
	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")
	if err := os.MkdirAll(sesDir, 0o755); err != nil {
		t.Fatalf("create ses dir: %v", err)
	}

	// Also create region.hcl location.
	regionDir := filepath.Join(repoRoot, "infra", "live", "use1")
	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		t.Fatalf("create region dir: %v", err)
	}

	// The ses module has KM_ROUTE53_ZONE_ID as envReq — set it so the module
	// isn't skipped before reaching the preflight or apply step.
	origZoneID := os.Getenv("KM_ROUTE53_ZONE_ID")
	os.Setenv("KM_ROUTE53_ZONE_ID", "Z1234567890FAKE")
	t.Cleanup(func() { os.Setenv("KM_ROUTE53_ZONE_ID", origZoneID) })

	t.Run("MissingRuleSet_ReturnsActionableError", func(t *testing.T) {
		// Inject a preflight that returns the "missing" error.
		cmd.InitSESPreflight = func(_ context.Context) error {
			return errors.New("Foundation SES rule set 'sandbox-email-shared' not found. Run 'km bootstrap --shared-ses' first on a fresh account.")
		}

		// Track whether terragrunt apply was called for the ses module.
		runner := &mockRunner{}
		err := cmd.RunInitWithRunner(runner, repoRoot, "us-east-1")
		if err == nil {
			t.Fatal("expected error when shared rule set is missing, got nil")
		}
		if !strings.Contains(err.Error(), "sandbox-email-shared") {
			t.Errorf("expected actionable error mentioning 'sandbox-email-shared', got: %v", err)
		}
		if !strings.Contains(err.Error(), "km bootstrap --shared-ses") {
			t.Errorf("expected error to mention 'km bootstrap --shared-ses', got: %v", err)
		}

		// Verify that the ses module apply was NOT called (fail fast).
		for _, applied := range runner.applied {
			if strings.HasSuffix(applied, "/ses") {
				t.Errorf("expected ses module NOT to be applied when preflight fails, but runner.Apply was called with %s", applied)
			}
		}
	})

	t.Run("RuleSetPresent_SESApplied", func(t *testing.T) {
		// Inject a preflight that passes.
		cmd.InitSESPreflight = func(_ context.Context) error {
			return nil
		}

		runner := &mockRunner{}
		// RunInitWithRunner may fail for other modules that don't exist in the temp dir.
		// We just care that the ses module was reached (i.e. preflight didn't block it).
		_ = cmd.RunInitWithRunner(runner, repoRoot, "us-east-1")

		// The ses module dir exists, so if preflight passes it should be applied.
		sesApplied := false
		for _, applied := range runner.applied {
			if strings.HasSuffix(applied, "/ses") {
				sesApplied = true
				break
			}
		}
		if !sesApplied {
			t.Error("expected ses module to be applied when preflight passes, but it was not")
		}
	})
}

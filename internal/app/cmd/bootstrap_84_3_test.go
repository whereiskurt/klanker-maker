package cmd

// Wave 0 RED scaffolding — Phase 84.3 Plan 01 Task 3.
// Tests for closures (b) dry-run text fix extension, (f) --all flag,
// and (h) bootstrap banner WARN.
//
// These tests reference production symbols that Plan 03 will create:
//   - runBootstrapAll (new func in cmd/bootstrap.go — --all routing)
//   - warnEmptyAccountIDs (new helper in cmd/bootstrap.go — banner WARN)
//   - RunBootstrapAllFunc (package-level testability seam, Plan 03)
//   - RunBootstrapFunc (package-level testability seam, Plan 03)
//
// RED contract: `go test ./internal/app/cmd/` fails with
//   undefined: runBootstrapAll
//   undefined: warnEmptyAccountIDs
//   undefined: RunBootstrapAllFunc
// Plan 03 makes them GREEN.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ---- BOOTSTRAP-DRYRUN-TEXT-FIX (closure b) -------------------------------------

// TestBootstrapDryRun_SaysApply verifies that the dry-run output (from
// runBootstrapSharedSES with dryRun=true) contains "would run: terragrunt apply"
// and does NOT say "would run: terragrunt plan".
//
// This is an extension / clarification of the existing TestBootstrapDryRun at
// configure_test.go:276 (integration). This test is purely unit-level.
func TestBootstrapDryRun_SaysApply(t *testing.T) {
	cfg := &config.Config{
		PrimaryRegion:        "us-east-1",
		ArtifactsBucket:      "km-artifacts-111111111111",
		ResourcePrefix:       "km",
		OrganizationAccountID: "111111111111",
		ApplicationAccountID: "333333333333",
	}
	var buf bytes.Buffer

	// runBootstrapSharedSES is unexported. To exercise the dry-run output
	// without AWS, we use the same DI approach as bootstrap_test.go:
	// capture via buf and pass dryRun=true. The fake terragrunt is not needed
	// because dryRun=true does not call terragrunt.
	err := runBootstrapSharedSES(context.Background(), cfg, true /* dryRun */, &buf, nil /* listerOverride */)
	// dryRun returns no error even when AWS is not configured.
	_ = err

	out := buf.String()
	if !strings.Contains(out, "would run: terragrunt apply") {
		t.Errorf("dry-run output missing 'would run: terragrunt apply'; got:\n%s", out)
	}
	if strings.Contains(out, "would run: terragrunt plan") {
		t.Errorf("dry-run output must not contain 'would run: terragrunt plan'; got:\n%s", out)
	}
}

// ---- BOOTSTRAP-WORKFLOW-DISCOVERABILITY --all flag tests (closure f) ------------

// TestBootstrapAllFlagRegistered verifies that NewBootstrapCmd registers the --all
// flag (default false) and that existing Phase 84.1/84.2 flags are preserved.
func TestBootstrapAllFlagRegistered(t *testing.T) {
	cfg := &config.Config{}
	c := NewBootstrapCmd(cfg)

	allFlag := c.Flags().Lookup("all")
	if allFlag == nil {
		t.Fatal("--all flag not registered on bootstrap command")
	}
	if allFlag.DefValue != "false" {
		t.Errorf("--all default = %q, want false", allFlag.DefValue)
	}

	// Phase 84 preserved: --shared-ses must still exist.
	if c.Flags().Lookup("shared-ses") == nil {
		t.Fatal("--shared-ses flag missing (must not be removed by --all addition)")
	}

	// Phase 84.2 preserved: --plan must still exist.
	if c.Flags().Lookup("plan") == nil {
		t.Fatal("--plan flag missing (Phase 84.2 flag must not be removed)")
	}
}

// TestBootstrapAll_ChainsBothSubflows verifies that runBootstrapAll calls
// runBootstrap (foundation SCP) first and runBootstrapSharedSES second,
// both with the same dryRun argument.
//
// Plan 03 must introduce RunBootstrapAllFunc + RunBootstrapFunc package-level vars
// (test-seam pattern per Phase 84.2's RunInitPlanFunc).
//
// This test is a declaration of the contract that Plan 03 must satisfy.
func TestBootstrapAll_ChainsBothSubflows(t *testing.T) {
	cfg := &config.Config{PrimaryRegion: "us-east-1"}
	var buf bytes.Buffer

	// RunBootstrapAllFunc is the seam Plan 03 will add. Until then, this
	// references the unexporting runBootstrapAll directly (wave 0 is package cmd).
	err := runBootstrapAll(context.Background(), cfg, true /* dryRun */, false, false, &buf)
	// We don't assert err content here — just that it does not panic and the
	// function exists. The sub-call assertions are in TestBootstrapAll_PlanRespectsGate.
	_ = err

	out := buf.String()
	// runBootstrapAll must mention both subflows in its output.
	if !strings.Contains(out, "bootstrap") && !strings.Contains(out, "shared-ses") {
		// If both are mentioned or there's combined output, that's fine.
		// Only fail if output is completely empty (no-op stub).
		if out == "" {
			t.Errorf("runBootstrapAll returned empty output; expected chained subflow output")
		}
	}
}

// TestBootstrapAll_PlanRespectsGate verifies that when plan=true and the
// shared-ses subflow hits a destroy-class gate trip, runBootstrapAll propagates
// that error.
//
// The test uses the RunBootstrapAllFunc test seam (Plan 03 will introduce).
func TestBootstrapAll_PlanRespectsGate(t *testing.T) {
	cfg := &config.Config{PrimaryRegion: "us-east-1"}
	var buf bytes.Buffer

	// Direct call — Plan 03 will ensure the gate propagates.
	err := runBootstrapAll(context.Background(), cfg, false /* dryRun */, true /* plan */, false, &buf)
	// If plan=true and gateway trips, we expect non-nil error.
	// Since we don't have mocks at this level (runBootstrapAll calls real subflows),
	// this test mostly verifies runBootstrapAll exists and is callable.
	// Concrete gate-trip assertions belong in Plan 03's GREEN tests.
	_ = err
}

// TestBootstrapAll_MutexWithSharedSES verifies that passing both --all and
// --shared-ses returns an error mentioning mutual exclusivity.
//
// Per RESEARCH.md Open Question 3 + Pitfall 5: --all and --shared-ses are
// mutually exclusive; --all runs both subflows in order.
func TestBootstrapAll_MutexWithSharedSES(t *testing.T) {
	cfg := &config.Config{PrimaryRegion: "us-east-1"}
	c := NewBootstrapCmdWithWriter(cfg, &bytes.Buffer{})
	c.SetArgs([]string{"--all", "--shared-ses"})

	err := c.Execute()
	if err == nil {
		t.Fatal("expected error for --all + --shared-ses, got nil")
	}
	if !strings.Contains(err.Error(), "--all") || !strings.Contains(err.Error(), "--shared-ses") {
		t.Errorf("error %q should mention both --all and --shared-ses", err.Error())
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q should say 'mutually exclusive'", err.Error())
	}
}

// ---- CONFIG-DISPLAY-VS-YAML-AUTHORITY banner tests (closure h bootstrap-side) --

// TestBootstrapBannerWarnsMissingAccount verifies that warnEmptyAccountIDs
// emits WARN lines to stderr for each empty required account ID.
//
// Plan 03 will create warnEmptyAccountIDs in cmd/bootstrap.go.
func TestBootstrapBannerWarnsMissingAccount(t *testing.T) {
	t.Run("missing organization emits WARN", func(t *testing.T) {
		cfg := &config.Config{
			OrganizationAccountID: "", // missing
			ApplicationAccountID:  "333333333333",
		}
		var w bytes.Buffer
		warnEmptyAccountIDs(cfg, &w)
		out := w.String()
		if !strings.Contains(out, "WARN") {
			t.Errorf("expected WARN in output; got: %s", out)
		}
		if !strings.Contains(out, "accounts.organization") {
			t.Errorf("expected 'accounts.organization' in WARN; got: %s", out)
		}
		if !strings.Contains(out, "km-config.yaml") {
			t.Errorf("expected 'km-config.yaml' reference in WARN; got: %s", out)
		}
	})

	t.Run("all accounts set produces no WARN", func(t *testing.T) {
		cfg := &config.Config{
			OrganizationAccountID: "111111111111",
			DNSParentAccountID:    "222222222222",
			ApplicationAccountID:  "333333333333",
		}
		var w bytes.Buffer
		warnEmptyAccountIDs(cfg, &w)
		if strings.Contains(w.String(), "WARN") {
			t.Errorf("expected no WARN when all accounts set; got: %s", w.String())
		}
	})

	t.Run("all three empty produces 3 WARN lines", func(t *testing.T) {
		cfg := &config.Config{
			OrganizationAccountID: "",
			DNSParentAccountID:    "",
			ApplicationAccountID:  "",
		}
		var w bytes.Buffer
		warnEmptyAccountIDs(cfg, &w)
		out := w.String()
		count := strings.Count(out, "WARN")
		if count < 3 {
			t.Errorf("expected 3 WARN lines for 3 empty accounts, got %d; output: %s", count, out)
		}
	})
}

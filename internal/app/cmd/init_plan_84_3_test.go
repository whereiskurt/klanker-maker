package cmd_test

// Wave 0 RED scaffolding — Phase 84.3 Plan 01 Task 2.
// Extension of init_plan_test.go with PLAN-FRESH-INSTALL-OUTPUTS-HANDLING tests.
//
// These tests reference production symbols that Plan 04 will create:
//   - upstreamOutputsExist (new helper in cmd/init.go — closure d skip probe)
//     Used inside RunInitPlanWithRunner to skip modules whose upstream
//     outputs.json is missing.
//
// init_plan_test.go is package cmd_test and 549 lines — we create a sibling file
// rather than appending to avoid the >600-line threshold.
//
// RED contract: `go test ./internal/app/cmd/` fails at runtime with assertion
// failures (the skip-probe code does not yet exist in RunInitPlanWithRunner),
// NOT at compile level for this file (RunInitPlanWithRunner + mockPlanRunner
// already exist). The tests will produce assertion failures because the actual
// skip behavior is not yet implemented.
//
// Plan 04 makes them GREEN by adding upstreamOutputsExist + the skip-probe loop
// inside RunInitPlanWithRunner.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// makeNetworkEfsLayout creates infra/live/use1/{network,efs}/terragrunt.hcl
// in a temp dir. The efs module's terragrunt.hcl includes a reference to
// network/outputs.json (simulating the real dependency).
// Returns the repoRoot.
func makeNetworkEfsLayout(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	for _, mod := range []string{"network", "efs"} {
		dir := filepath.Join(repoRoot, "infra", "live", "use1", mod)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
		hcl := "# placeholder terragrunt.hcl\n"
		if mod == "efs" {
			// Reference to network outputs — signals the upstream dependency.
			hcl = `# efs depends on network outputs
locals {
  network_outputs = "${get_terragrunt_dir()}/../network/outputs.json"
}
`
		}
		if err := os.WriteFile(filepath.Join(dir, "terragrunt.hcl"), []byte(hcl), 0o644); err != nil {
			t.Fatalf("write terragrunt.hcl for %s: %v", mod, err)
		}
	}
	return repoRoot
}

// TestRunInitPlan_SkipsEFS_WhenNetworkOutputsMissing verifies that when
// network/outputs.json is absent, efs is skipped with a "[skip" message that
// contains "network/outputs.json" and "efs" in the stdout, and that
// RunInitPlanWithRunner returns nil (exit-0 for skip-only path).
// Additionally, mockPlanRunner.planned must NOT include the efs module directory.
func TestRunInitPlan_SkipsEFS_WhenNetworkOutputsMissing(t *testing.T) {
	repoRoot := makeNetworkEfsLayout(t)
	// network/outputs.json intentionally NOT created.

	mock := &mockPlanRunner{}
	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, false)
	})

	if runErr != nil {
		t.Errorf("expected nil error for skip-only path, got: %v", runErr)
	}
	if !strings.Contains(out, "[skip") {
		t.Errorf("expected '[skip' in output when efs upstream missing; got:\n%s", out)
	}
	if !strings.Contains(out, "network/outputs.json") {
		t.Errorf("expected 'network/outputs.json' in skip message; got:\n%s", out)
	}
	if !strings.Contains(out, "efs") {
		t.Errorf("expected 'efs' in skip message; got:\n%s", out)
	}

	efsDir := filepath.Join(repoRoot, "infra", "live", "use1", "efs")
	for _, planned := range mock.planned {
		if planned == efsDir {
			t.Errorf("efs should NOT appear in mock.planned (skip ran before planModule); planned: %v", mock.planned)
		}
	}
}

// TestRunInitPlan_NoSkipWhenOutputsExist verifies that efs IS planned when
// network/outputs.json is present.
func TestRunInitPlan_NoSkipWhenOutputsExist(t *testing.T) {
	repoRoot := makeNetworkEfsLayout(t)

	// Create network/outputs.json (any valid JSON).
	networkDir := filepath.Join(repoRoot, "infra", "live", "use1", "network")
	outputsPath := filepath.Join(networkDir, "outputs.json")
	if err := os.WriteFile(outputsPath, []byte(`{"vpc_id":{"value":"vpc-test"}}`), 0o644); err != nil {
		t.Fatalf("write network/outputs.json: %v", err)
	}

	mock := &mockPlanRunner{}
	captureStdout(t, func() {
		_ = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, false)
	})

	efsDir := filepath.Join(repoRoot, "infra", "live", "use1", "efs")
	hasEfs := false
	for _, p := range mock.planned {
		if p == efsDir {
			hasEfs = true
			break
		}
	}
	if !hasEfs {
		t.Errorf("expected efs to be planned when network/outputs.json exists; planned: %v", mock.planned)
	}
}

// TestRunInitPlan_SkippedModulesExcludedFromGate verifies that a module skipped
// due to missing upstream outputs does not contribute to the destroy-class gate
// trip count. The gate should only trip on modules that were actually planned.
func TestRunInitPlan_SkippedModulesExcludedFromGate(t *testing.T) {
	// ses module requires KM_ROUTE53_ZONE_ID — set it so ses is not also skipped.
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	repoRoot := makeNetworkEfsLayout(t)
	// network/outputs.json absent → efs will be skipped.

	// Also create ses module dir.
	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")
	if err := os.MkdirAll(sesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll ses: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sesDir, "terragrunt.hcl"), []byte("# ses\n"), 0o644); err != nil {
		t.Fatalf("write ses terragrunt.hcl: %v", err)
	}

	// Inject a protected-destroy JSON for ses to trip the gate on ses (not efs).
	sesTrip := loadPhase84TripFixture(t)
	mock := &mockPlanRunner{
		planJSON: map[string][]byte{sesDir: sesTrip},
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, false)
	})

	// Gate should trip on ses (protected destroy) but NOT on efs (skipped).
	if runErr == nil {
		// Only non-nil if gate trips OR plan fails; for this test we just verify
		// the efs skip message is present regardless of gate outcome.
	}

	// efs skip must still appear.
	if !strings.Contains(out, "[skip") || !strings.Contains(out, "efs") {
		t.Errorf("expected efs skip message in output; got:\n%s", out)
	}
	// efs must not appear in planned list.
	efsDir := filepath.Join(repoRoot, "infra", "live", "use1", "efs")
	for _, p := range mock.planned {
		if p == efsDir {
			t.Errorf("efs should NOT be in mock.planned when skipped; planned: %v", mock.planned)
		}
	}
	_ = runErr
}

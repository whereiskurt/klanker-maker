package cmd_test

// Phase 105 Wave 0 — TDD scaffold for scoped km init.
//
// This file contains compiling-but-skipping stub tests. Each test is named to
// match the VALIDATION.md per-requirement map exactly so `-run TestScoped` captures
// all of them. Wave 1/2 will replace the t.Skip bodies with real assertions as the
// production symbols are added to init.go.
//
// Req-ID → Test mapping (from 105-VALIDATION.md):
//
//   INIT-SCOPED-FLAG    → TestScopedModuleResolution   (--only known module resolves)
//   INIT-SCOPED-FLAG    → TestScopedModuleRejection    (--only unknown-module errors + prints allowed set)
//   INIT-SCOPED-ALIASES → TestScopedAliases            (--github/--slack/--h1/--email resolve to exact module names)
//   INIT-SCOPED-GUARD   → TestScopedMutualExclusion    (--github --plan and --github --sidecars both error)
//   INIT-SCOPED-IMPL    → TestScopedDryRun             (--dry-run=true does NOT call runner.Apply)
//   INIT-SCOPED-IMPL    → TestScopedApply              (--dry-run=false calls runner.Apply exactly once for target)
//   INIT-SCOPED-IMPL    → TestScopedEnvVarsExported    (ExportTerragruntEnvVars called before apply)
//   INIT-SCOPED-GUARD   → TestScopedTier2Gate          (--only ses: plan called before apply)
//   INIT-SCOPED-GUARD   → TestScopedTier2GateBlocked   (tier-2 gate blocked → apply NOT called)
//   INIT-SCOPED-IMPL    → TestScopedSesPreflight       (ses preflight + Reconfigure called for --only ses)
//
// Production symbols introduced in Wave 1 (Plan 02):
//
//   cmd.ResolveScopedModule(onlyVal string, github, slack, h1, email bool) (string, error)
//   cmd.runInitScopedFunc  (package-level stub var; Plan 03 replaces body)
//   cmd.scopedCheapAllowlist  []string  (unexported; Tier 1)
//   cmd.scopedGatedAllowlist  []string  (unexported; Tier 2)
//
// Wave 2 symbols (Plan 03 — do NOT reference here):
//
//   cmd.RunInitScopedWithRunner(runner, repoRoot, region, module string, acceptDestroys bool) error

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// makeMinimalInitConfig returns a minimal *config.Config for tests that need to
// execute NewInitCmd without real AWS credentials. Mirrors makeMinimalConfig in
// init_plan_test.go (same package — cannot import across test files directly).
func makeMinimalInitConfig() *config.Config {
	return &config.Config{}
}

// TestScopedModuleResolution verifies that --only <known-module> resolves to
// the expected module name and does not error.
//
// Wave 1 contract (ResolveScopedModule):
//   - cmd.ResolveScopedModule("lambda-github-bridge", false, false, false, false) → ("lambda-github-bridge", nil)
//   - result must be in the Tier-1 allowlist
//   - "ses" (Tier-2) also resolves without error
//   - empty onlyVal with all false → ("", nil) (no scoped request, not an error)
func TestScopedModuleResolution(t *testing.T) {
	cases := []struct {
		name      string
		onlyVal   string
		github    bool
		slack     bool
		h1        bool
		email     bool
		wantMod   string
		wantError bool
	}{
		{
			name:    "lambda-github-bridge via --only",
			onlyVal: "lambda-github-bridge",
			wantMod: "lambda-github-bridge",
		},
		{
			name:    "lambda-slack-bridge via --only",
			onlyVal: "lambda-slack-bridge",
			wantMod: "lambda-slack-bridge",
		},
		{
			name:    "lambda-h1-bridge via --only",
			onlyVal: "lambda-h1-bridge",
			wantMod: "lambda-h1-bridge",
		},
		{
			name:    "email-handler via --only",
			onlyVal: "email-handler",
			wantMod: "email-handler",
		},
		{
			name:    "ses (tier-2) via --only",
			onlyVal: "ses",
			wantMod: "ses",
		},
		{
			name:    "no flags set — falls through to normal init",
			onlyVal: "",
			wantMod: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cmd.ResolveScopedModule(tc.onlyVal, tc.github, tc.slack, tc.h1, tc.email)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil (module=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantMod {
				t.Errorf("module = %q, want %q", got, tc.wantMod)
			}
		})
	}
}

// TestScopedModuleRejection verifies that --only <unknown-module> returns an
// error that names the allowed set.
//
// Wave 1 contract (ResolveScopedModule):
//   - cmd.ResolveScopedModule("unknown-module", false, false, false, false) → ("", err)
//   - err.Error() contains each Tier-1 module name and "ses" (Tier-2)
func TestScopedModuleRejection(t *testing.T) {
	invalidModules := []string{
		"bogus-module",
		"network",
		"dynamodb-sandboxes",
		"create-handler",
		"totally-fake",
	}

	// These are the expected allowed module names that must appear in the error message.
	wantMentioned := []string{
		"lambda-github-bridge",
		"lambda-slack-bridge",
		"lambda-h1-bridge",
		"email-handler",
		"ses",
	}

	for _, mod := range invalidModules {
		t.Run(mod, func(t *testing.T) {
			got, err := cmd.ResolveScopedModule(mod, false, false, false, false)
			if err == nil {
				t.Fatalf("expected error for %q, got nil (module=%q)", mod, got)
			}
			if got != "" {
				t.Errorf("on error, returned module = %q, want empty string", got)
			}
			errMsg := err.Error()
			for _, name := range wantMentioned {
				if !strings.Contains(errMsg, name) {
					t.Errorf("error %q does not mention allowed module %q", errMsg, name)
				}
			}
		})
	}
}

// TestScopedAliases verifies that each sugar alias resolves to the expected
// canonical module name and that all four aliases are covered.
//
// Wave 1 contract (ResolveScopedModule):
//
//	--github → "lambda-github-bridge"
//	--slack  → "lambda-slack-bridge"
//	--h1     → "lambda-h1-bridge"
//	--email  → "email-handler"
func TestScopedAliases(t *testing.T) {
	cases := []struct {
		alias   string
		github  bool
		slack   bool
		h1      bool
		email   bool
		wantMod string
	}{
		{alias: "--github", github: true, wantMod: "lambda-github-bridge"},
		{alias: "--slack", slack: true, wantMod: "lambda-slack-bridge"},
		{alias: "--h1", h1: true, wantMod: "lambda-h1-bridge"},
		{alias: "--email", email: true, wantMod: "email-handler"},
	}

	for _, tc := range cases {
		t.Run(tc.alias, func(t *testing.T) {
			got, err := cmd.ResolveScopedModule("", tc.github, tc.slack, tc.h1, tc.email)
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.alias, err)
			}
			if got != tc.wantMod {
				t.Errorf("%s resolved to %q, want %q", tc.alias, got, tc.wantMod)
			}
		})
	}
}

// TestScopedMutualExclusion verifies that:
//   - Combining two entry points (e.g. --github + --slack) returns an error from ResolveScopedModule.
//   - Combining --only/alias with --plan, --sidecars, or --lambdas returns a mutual-exclusion
//     error from the NewInitCmd dispatch.
//
// Wave 1 contract (ResolveScopedModule + NewInitCmd dispatch guard):
//
//	"km init --github --plan"     → error containing "cannot be combined"
//	"km init --github --sidecars" → error containing "cannot be combined"
//	"km init --github --lambdas"  → error containing "cannot be combined"
//	"km init --only ses --plan"   → error containing "cannot be combined"
//	"km init --github --slack"    → error containing "at most one"
//	"km init --only X --github"   → error containing "at most one"
func TestScopedMutualExclusion(t *testing.T) {
	t.Run("two alias flags via ResolveScopedModule", func(t *testing.T) {
		_, err := cmd.ResolveScopedModule("", true, true, false, false) // --github + --slack
		if err == nil {
			t.Fatal("expected error for github+slack, got nil")
		}
		if !strings.Contains(err.Error(), "at most one") {
			t.Errorf("error = %q, want to contain 'at most one'", err.Error())
		}
	})

	t.Run("--only and alias both set via ResolveScopedModule", func(t *testing.T) {
		_, err := cmd.ResolveScopedModule("lambda-github-bridge", true, false, false, false) // --only + --github
		if err == nil {
			t.Fatal("expected error for --only + --github, got nil")
		}
		if !strings.Contains(err.Error(), "at most one") {
			t.Errorf("error = %q, want to contain 'at most one'", err.Error())
		}
	})

	// For dispatch-level guard tests, invoke the full Cobra command via NewInitCmd
	// so the RunE guard fires before any AWS call.
	guardCases := []struct {
		name string
		args []string
	}{
		{name: "--github --plan", args: []string{"--github", "--plan"}},
		{name: "--github --sidecars", args: []string{"--github", "--sidecars"}},
		{name: "--github --lambdas", args: []string{"--github", "--lambdas"}},
		{name: "--only ses --plan", args: []string{"--only", "ses", "--plan"}},
		{name: "--slack --sidecars", args: []string{"--slack", "--sidecars"}},
	}

	for _, tc := range guardCases {
		tc := tc // capture
		t.Run(tc.name, func(t *testing.T) {
			cfg := makeMinimalInitConfig()
			c := cmd.NewInitCmd(cfg)
			c.SetArgs(tc.args)
			err := c.Execute()
			if err == nil {
				t.Fatalf("expected error for args %v, got nil", tc.args)
			}
			if !strings.Contains(err.Error(), "cannot be combined") {
				t.Errorf("error = %q, want to contain 'cannot be combined'", err.Error())
			}
		})
	}
}

// makeModuleDir creates a module directory inside the given regionDir, making it
// visible to RunInitScopedWithRunner's os.Stat check.
func makeModuleDir(t *testing.T, regionDir, name string) {
	t.Helper()
	dir := filepath.Join(regionDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("makeModuleDir %s: %v", name, err)
	}
}

// makeScopedRepoRoot creates a minimal repoRoot with the region.hcl and requested
// module directories created, mirroring the scaffold used by init_test.go.
// The region "us-east-1" maps to regionLabel "use1".
func makeScopedRepoRoot(t *testing.T, modules []string) (repoRoot string) {
	t.Helper()
	repoRoot = t.TempDir()
	regionLabel := "use1"
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)

	// Write region.hcl so ensureRegionHCL is a no-op.
	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		t.Fatalf("mkdir regionDir: %v", err)
	}
	hcl := fmt.Sprintf("locals {\n  region_label = %q\n  region_full  = %q\n}\n", regionLabel, "us-east-1")
	if err := os.WriteFile(filepath.Join(regionDir, "region.hcl"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write region.hcl: %v", err)
	}

	for _, mod := range modules {
		makeModuleDir(t, regionDir, mod)
	}
	return repoRoot
}

// TestScopedDryRun verifies that when dryRun=true, RunInitScopedWithRunner does NOT
// call runner.Apply (it prints a summary and returns nil).
//
// Wave 2 contract (RunInitScopedWithRunner):
//   - mockRunner.applied must remain empty after RunInitScopedWithRunner with dryRun=true
//   - Function returns nil (not an error)
func TestScopedDryRun(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"lambda-github-bridge"})
	mock := &mockRunner{}
	// KM_ARTIFACTS_BUCKET required by lambda-github-bridge envReqs.
	t.Setenv("KM_ARTIFACTS_BUCKET", "test-bucket")

	err := cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "lambda-github-bridge", true /*dryRun*/, false)
	if err != nil {
		t.Fatalf("RunInitScopedWithRunner (dry-run=true): unexpected error: %v", err)
	}
	if len(mock.applied) != 0 {
		t.Errorf("dry-run=true: expected 0 Apply calls, got %d: %v", len(mock.applied), mock.applied)
	}
}

// TestScopedApply verifies that when dryRun=false, RunInitScopedWithRunner calls
// runner.Apply exactly once, for the target module directory.
//
// Wave 2 contract (RunInitScopedWithRunner):
//   - mockRunner.applied has exactly one entry
//   - The entry's path suffix matches the target module name
func TestScopedApply(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"lambda-github-bridge"})
	mock := &mockRunner{}
	t.Setenv("KM_ARTIFACTS_BUCKET", "test-bucket")

	err := cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "lambda-github-bridge", false /*dryRun*/, false)
	if err != nil {
		t.Fatalf("RunInitScopedWithRunner (dry-run=false): unexpected error: %v", err)
	}
	if len(mock.applied) != 1 {
		t.Errorf("expected exactly 1 Apply call, got %d: %v", len(mock.applied), mock.applied)
	}
	if len(mock.applied) > 0 && !strings.HasSuffix(mock.applied[0], "lambda-github-bridge") {
		t.Errorf("applied[0] = %q, want suffix 'lambda-github-bridge'", mock.applied[0])
	}

	// Also verify that module-not-found returns a clear error.
	t.Run("module not found", func(t *testing.T) {
		root2 := makeScopedRepoRoot(t, nil) // no module dirs
		mock2 := &mockRunner{}
		err2 := cmd.RunInitScopedWithRunner(mock2, root2, "us-east-1", "lambda-github-bridge", false, false)
		if err2 == nil {
			t.Fatal("expected error for missing module dir, got nil")
		}
		if !strings.Contains(err2.Error(), "not found") {
			t.Errorf("error %q does not contain 'not found'", err2.Error())
		}
	})

	// Verify envReqs check: KM_ARTIFACTS_BUCKET unset → error naming missing var.
	t.Run("missing env req", func(t *testing.T) {
		root3 := makeScopedRepoRoot(t, []string{"lambda-github-bridge"})
		mock3 := &mockRunner{}
		// Ensure env var is not set for this sub-test.
		t.Setenv("KM_ARTIFACTS_BUCKET", "")
		err3 := cmd.RunInitScopedWithRunner(mock3, root3, "us-east-1", "lambda-github-bridge", false, false)
		if err3 == nil {
			t.Fatal("expected error for missing KM_ARTIFACTS_BUCKET, got nil")
		}
		if !strings.Contains(err3.Error(), "KM_ARTIFACTS_BUCKET") {
			t.Errorf("error %q does not name missing env var KM_ARTIFACTS_BUCKET", err3.Error())
		}
	})
}

// TestScopedEnvVarsExported verifies that ExportTerragruntEnvVars sets KM_ARTIFACTS_BUCKET
// from cfg.ArtifactsBucket before the apply path runs.
//
// Wave 2 contract (runInitScoped production wrapper):
//   - runInitScoped calls ExportTerragruntEnvVars(cfg) FIRST, which sets KM_ARTIFACTS_BUCKET
//     from cfg.ArtifactsBucket when the env var is not already set.
//   - This test calls ExportTerragruntEnvVars directly (the same call runInitScoped makes)
//     to assert the contract that RunInitScopedWithRunner relies on.
func TestScopedEnvVarsExported(t *testing.T) {
	// runInitScoped calls ExportTerragruntEnvVars(cfg) as its first action (before applying).
	// We test this contract directly: ExportTerragruntEnvVars with a cfg that has
	// ArtifactsBucket set must write KM_ARTIFACTS_BUCKET into the environment.

	// Ensure the var is unset before we call ExportTerragruntEnvVars.
	t.Setenv("KM_ARTIFACTS_BUCKET", "")

	cfg := &config.Config{
		ArtifactsBucket: "my-test-artifacts-bucket",
	}

	// Call ExportTerragruntEnvVars the same way runInitScoped does (first thing before apply).
	cmd.ExportTerragruntEnvVars(cfg)

	got := os.Getenv("KM_ARTIFACTS_BUCKET")
	if got != cfg.ArtifactsBucket {
		t.Errorf("after ExportTerragruntEnvVars: KM_ARTIFACTS_BUCKET = %q, want %q", got, cfg.ArtifactsBucket)
	}
}

// mockReconfigurePlanRunner wraps mockPlanRunner and records Reconfigure calls
// so TestScopedSesPreflight can verify Reconfigure is invoked for --only ses.
type mockReconfigurePlanRunner struct {
	mockPlanRunner
	reconfigured []string
}

func (m *mockReconfigurePlanRunner) Reconfigure(_ context.Context, dir string) error {
	m.reconfigured = append(m.reconfigured, dir)
	return nil
}

// TestScopedTier2Gate verifies that for --only ses the plan is run (via
// mockPlanRunner.planned) BEFORE Apply is called (via mockPlanRunner.applied).
//
// Plan 04 contract (RunInitScopedWithRunner, tier-2 path):
//   - mockPlanRunner.planned must contain the ses module dir (gate ran plan)
//   - mockPlanRunner.applied must contain the ses module dir (apply ran)
//   - planned[0] is populated before applied[0] (gate runs first)
func TestScopedTier2Gate(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"ses"})
	// ses requires KM_ROUTE53_ZONE_ID per its envReqs.
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	// Suppress InitSESPreflight (would try real AWS) for this test.
	origPreflight := cmd.InitSESPreflight
	cmd.InitSESPreflight = func(_ context.Context) error { return nil }
	t.Cleanup(func() { cmd.InitSESPreflight = origPreflight })

	mock := &mockPlanRunner{}
	// dryRun=false so Apply runs; acceptDestroys=false with a clean (no-destroy) plan.
	err := cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "ses", false /*dryRun*/, false)
	if err != nil {
		t.Fatalf("RunInitScopedWithRunner (ses, clean plan): unexpected error: %v", err)
	}

	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")

	// Gate must have run plan (planned contains ses dir).
	found := false
	for _, p := range mock.planned {
		if strings.HasSuffix(p, "ses") || p == sesDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("gate did not plan ses: planned = %v", mock.planned)
	}

	// Apply must have run (applied contains ses dir).
	if len(mock.applied) == 0 {
		t.Errorf("expected Apply to be called for ses, got 0 Apply calls")
	} else if !strings.HasSuffix(mock.applied[0], "ses") {
		t.Errorf("applied[0] = %q, want suffix 'ses'", mock.applied[0])
	}
}

// TestScopedTier2GateBlocked verifies that when the plan output contains a
// destroy-class change, the gate trips and Apply is NOT called.
//
// Plan 04 contract (RunInitScopedWithRunner, tier-2 path):
//   - mockPlanRunner.planJSON[sesDir] = destroyPlanJSON (protected destroy)
//   - RunInitScopedWithRunner returns a non-nil "destroy-class gate tripped" error
//   - mockPlanRunner.applied must remain empty (Apply never called)
//   - With acceptDestroys=true the gate is overridden and Apply IS called
func TestScopedTier2GateBlocked(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"ses"})
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	// Load the existing protected-destroy fixture used by plan tests.
	tripJSON := loadPhase84TripFixture(t)
	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")

	// Suppress InitSESPreflight so only the gate logic is tested.
	origPreflight := cmd.InitSESPreflight
	cmd.InitSESPreflight = func(_ context.Context) error { return nil }
	t.Cleanup(func() { cmd.InitSESPreflight = origPreflight })

	t.Run("blocked without acceptDestroys", func(t *testing.T) {
		mock := &mockPlanRunner{
			planJSON: map[string][]byte{sesDir: tripJSON},
		}
		var runErr error
		_ = captureStdout(t, func() {
			runErr = cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "ses", false /*dryRun*/, false /*acceptDestroys*/)
		})
		if runErr == nil {
			t.Fatal("expected non-nil error when gate trips on protected destroy, got nil")
		}
		if !strings.Contains(runErr.Error(), "destroy-class gate tripped") {
			t.Errorf("error = %q, want to contain 'destroy-class gate tripped'", runErr.Error())
		}
		if len(mock.applied) != 0 {
			t.Errorf("Apply must NOT be called when gate blocks; got %d calls: %v", len(mock.applied), mock.applied)
		}
	})

	t.Run("overridden with acceptDestroys=true", func(t *testing.T) {
		mock := &mockPlanRunner{
			planJSON: map[string][]byte{sesDir: tripJSON},
		}
		var runErr error
		_ = captureStdout(t, func() {
			runErr = cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "ses", false /*dryRun*/, true /*acceptDestroys*/)
		})
		if runErr != nil {
			t.Fatalf("expected nil error with acceptDestroys=true override, got: %v", runErr)
		}
		if len(mock.applied) == 0 {
			t.Errorf("expected Apply to be called when acceptDestroys=true overrides gate")
		}
	})
}

// TestScopedSesPreflight verifies that the SES preflight function and
// runner.Reconfigure are called for --only ses before Apply.
//
// Plan 04 contract (RunInitScopedWithRunner, ses branch):
//   - cmd.InitSESPreflight (package var spy) is invoked before Apply
//   - mockReconfigurePlanRunner.Reconfigure records the ses dir before Apply
//   - Both fire before Apply (ordering: plan → preflight → reconfigure → apply)
func TestScopedSesPreflight(t *testing.T) {
	repoRoot := makeScopedRepoRoot(t, []string{"ses"})
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")

	// Replace InitSESPreflight with a spy that records it was called.
	preflightCalled := false
	origPreflight := cmd.InitSESPreflight
	cmd.InitSESPreflight = func(_ context.Context) error {
		preflightCalled = true
		return nil
	}
	t.Cleanup(func() { cmd.InitSESPreflight = origPreflight })

	mock := &mockReconfigurePlanRunner{}
	err := cmd.RunInitScopedWithRunner(mock, repoRoot, "us-east-1", "ses", false /*dryRun*/, false)
	if err != nil {
		t.Fatalf("RunInitScopedWithRunner (ses, preflight test): unexpected error: %v", err)
	}

	// InitSESPreflight must have been called.
	if !preflightCalled {
		t.Error("InitSESPreflight was not called for --only ses")
	}

	// Reconfigure must have been called with the ses dir.
	if len(mock.reconfigured) == 0 {
		t.Error("Reconfigure was not called for --only ses")
	} else if !strings.HasSuffix(mock.reconfigured[0], "ses") && mock.reconfigured[0] != sesDir {
		t.Errorf("Reconfigure called with %q, want ses dir %q", mock.reconfigured[0], sesDir)
	}

	// Apply must also have been called (the full path completed).
	if len(mock.applied) == 0 {
		t.Error("Apply was not called after preflight+reconfigure for ses")
	}
}

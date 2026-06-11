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

// TestScopedDryRun verifies that when --dry-run=true (the default), the scoped
// path does NOT call runner.Apply.
//
// Wave 2 contract (RunInitScopedWithRunner):
//   - mockRunner.applied must remain empty after RunInitScopedWithRunner with dryRun=true
//   - Function returns nil (not an error)
func TestScopedDryRun(t *testing.T) {
	t.Skip("Phase 105 Wave 2: pending Plan 03 implementation")
}

// TestScopedApply verifies that when --dry-run=false, the scoped path calls
// runner.Apply exactly once for the target module directory.
//
// Wave 2 contract (RunInitScopedWithRunner):
//   - mockRunner.applied has exactly one entry after RunInitScopedWithRunner
//   - The entry's path suffix matches the target module name
func TestScopedApply(t *testing.T) {
	t.Skip("Phase 105 Wave 2: pending Plan 03 implementation")
}

// TestScopedEnvVarsExported verifies that ExportTerragruntEnvVars (which sets
// KM_ARTIFACTS_BUCKET, KM_RESOURCE_PREFIX, etc.) is called before runner.Apply.
//
// Wave 2 contract (runInitScoped):
//   - After RunInitScopedWithRunner returns, t.Setenv-injected KM_* vars must be
//     visible (i.e. ExportTerragruntEnvVars ran and did not clear them).
//   - Alternatively: wrap ExportTerragruntEnvVars via a package-level var and
//     assert it was called (mirroring BuildLambdaZipsFunc pattern).
func TestScopedEnvVarsExported(t *testing.T) {
	t.Skip("Phase 105 Wave 2: pending Plan 03 implementation")
}

// TestScopedTier2Gate verifies that for --only ses the plan is run (via
// mockPlanRunner.planned) BEFORE Apply is called (via mockPlanRunner.applied).
//
// Wave 2/3 contract (RunInitScopedWithRunner, tier-2 path):
//   - mockPlanRunner.planned must contain the ses module dir
//   - mockPlanRunner.applied must contain the ses module dir
//   - planned[0] comes before applied[0] in call order (gate runs first)
func TestScopedTier2Gate(t *testing.T) {
	t.Skip("Phase 105 Wave 2: pending Plan 03 implementation")
}

// TestScopedTier2GateBlocked verifies that when the plan output contains a
// destroy-class change, the gate trips and Apply is NOT called.
//
// Wave 2/3 contract (RunInitScopedWithRunner, tier-2 path):
//   - mockPlanRunner.planJSON["ses"] = destroyPlanJSON (a plan with a destroy action)
//   - RunInitScopedWithRunner returns a non-nil error (gate tripped)
//   - mockPlanRunner.applied must remain empty (Apply never called)
func TestScopedTier2GateBlocked(t *testing.T) {
	t.Skip("Phase 105 Wave 2: pending Plan 03 implementation")
}

// TestScopedSesPreflight verifies that the SES preflight function and
// runner.Reconfigure are called for --only ses before Apply.
//
// Wave 2/3 contract (RunInitScopedWithRunner, ses branch):
//   - Override cmd.InitSESPreflight to record it was called
//   - mockRunner.Reconfigure records the ses dir
//   - Both must fire before mockRunner.Apply records the ses dir
func TestScopedSesPreflight(t *testing.T) {
	t.Skip("Phase 105 Wave 2: pending Plan 03 implementation")
}

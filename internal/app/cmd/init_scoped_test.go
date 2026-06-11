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
// Production symbols that Wave 1/2 must introduce (do NOT reference here — stubs
// must compile at Wave 0 before these exist):
//
//   cmd.resolveScopedModule(onlyVal, github, slack, h1, email bool) (string, error)
//   cmd.RunInitScopedWithRunner(runner, repoRoot, region, module string, acceptDestroys bool) error
//   cmd.scopedCheapAllowlist  []string  (Tier 1: lambda-github-bridge, lambda-slack-bridge, lambda-h1-bridge, email-handler)
//   cmd.scopedGatedAllowlist  []string  (Tier 2: ses)

import "testing"

// TestScopedModuleResolution verifies that --only <known-module> resolves to
// the expected module name and does not error.
//
// Wave 1 contract (resolveScopedModule):
//   - cmd.resolveScopedModule("lambda-github-bridge", false, false, false, false) → ("lambda-github-bridge", nil)
//   - result must be in cmd.scopedCheapAllowlist or cmd.scopedGatedAllowlist
func TestScopedModuleResolution(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

// TestScopedModuleRejection verifies that --only <unknown-module> returns an
// error that names the allowed set.
//
// Wave 1 contract (resolveScopedModule):
//   - cmd.resolveScopedModule("unknown-module", false, false, false, false) → ("", err)
//   - err.Error() contains each entry from cmd.scopedCheapAllowlist and cmd.scopedGatedAllowlist
func TestScopedModuleRejection(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

// TestScopedAliases verifies that each sugar alias resolves to the expected
// canonical module name and that all four aliases are covered.
//
// Wave 1 contract (resolveScopedModule):
//   --github → "lambda-github-bridge"
//   --slack  → "lambda-slack-bridge"
//   --h1     → "lambda-h1-bridge"
//   --email  → "email-handler"
//
// Implemented by calling cmd.resolveScopedModule with the respective bool set.
func TestScopedAliases(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

// TestScopedMutualExclusion verifies that combining --only / any alias with
// --plan, --sidecars, or --lambdas returns an error.
//
// Wave 1 contract (NewInitCmd dispatch guard):
//   "km init --github --plan"     → error containing "cannot be combined"
//   "km init --github --sidecars" → error containing "cannot be combined"
//   "km init --github --lambdas"  → error containing "cannot be combined"
//   "km init --only ses --plan"   → error containing "cannot be combined"
func TestScopedMutualExclusion(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

// TestScopedDryRun verifies that when --dry-run=true (the default), the scoped
// path does NOT call runner.Apply.
//
// Wave 2 contract (RunInitScopedWithRunner):
//   - mockRunner.applied must remain empty after RunInitScopedWithRunner with dryRun=true
//   - Function returns nil (not an error)
func TestScopedDryRun(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

// TestScopedApply verifies that when --dry-run=false, the scoped path calls
// runner.Apply exactly once for the target module directory.
//
// Wave 2 contract (RunInitScopedWithRunner):
//   - mockRunner.applied has exactly one entry after RunInitScopedWithRunner
//   - The entry's path suffix matches the target module name
func TestScopedApply(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
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
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

// TestScopedTier2Gate verifies that for --only ses the plan is run (via
// mockPlanRunner.planned) BEFORE Apply is called (via mockPlanRunner.applied).
//
// Wave 2/3 contract (RunInitScopedWithRunner, tier-2 path):
//   - mockPlanRunner.planned must contain the ses module dir
//   - mockPlanRunner.applied must contain the ses module dir
//   - planned[0] comes before applied[0] in call order (gate runs first)
func TestScopedTier2Gate(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

// TestScopedTier2GateBlocked verifies that when the plan output contains a
// destroy-class change, the gate trips and Apply is NOT called.
//
// Wave 2/3 contract (RunInitScopedWithRunner, tier-2 path):
//   - mockPlanRunner.planJSON["ses"] = destroyPlanJSON (a plan with a destroy action)
//   - RunInitScopedWithRunner returns a non-nil error (gate tripped)
//   - mockPlanRunner.applied must remain empty (Apply never called)
func TestScopedTier2GateBlocked(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

// TestScopedSesPreflight verifies that the SES preflight function and
// runner.Reconfigure are called for --only ses before Apply.
//
// Wave 2/3 contract (RunInitScopedWithRunner, ses branch):
//   - Override cmd.InitSESPreflight to record it was called
//   - mockRunner.Reconfigure records the ses dir
//   - Both must fire before mockRunner.Apply records the ses dir
func TestScopedSesPreflight(t *testing.T) {
	t.Skip("Phase 105 Wave 1/2: pending implementation")
}

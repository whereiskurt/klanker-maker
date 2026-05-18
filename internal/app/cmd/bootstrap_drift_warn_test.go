package cmd

// Wave 5 RED scaffolding — Phase 84.3 Plan 06.
// Integration tests for drift WARN emission via runBootstrap.
//
// WHY THESE TESTS ARE RED against current code:
//
//  runBootstrap does NOT call ExportTerragruntEnvVars today.
//  Only warnEmptyAccountIDs is called at the top of runBootstrap, which
//  checks for EMPTY account IDs (not for env/yaml drift). Plan 08 adds the
//  ExportTerragruntEnvVars call to runBootstrap so drift WARNs fire there too.
//
//  RED tests:
//    TestRunBootstrap_DriftWarn_MissingExportCall — WARN absent because
//      ExportTerragruntEnvVars is not called. Plan 08 adds the call.
//
//  SKIP-guarded test (compiles but does not run until Plan 07):
//    TestRunBootstrap_DriftWarn_KM_REGION — needs cfg.YAMLDefaults field (Plan 07)
//
//  GREEN test (must stay GREEN before and after):
//    TestRunBootstrap_NoDriftWarn_WhenEnvMatchesConfig — no drift when values agree

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestRunBootstrap_DriftWarn_MissingExportCall verifies that when
// KM_ACCOUNTS_ORGANIZATION in the shell differs from cfg.OrganizationAccountID,
// runBootstrap emits a "WARN: KM_ACCOUNTS_ORGANIZATION=..." drift warning to stderr.
//
// WHY THIS IS RED: runBootstrap does NOT call ExportTerragruntEnvVars today.
// The warnEmptyAccountIDs call only warns about empty values, not about env/yaml
// drift. Plan 08 adds ExportTerragruntEnvVars(loadedCfg) to runBootstrap so the
// drift WARN fires via warnAndSetEnv (the same mechanism as init/configure paths).
//
// Expected failure mode:
//   "expected stderr to contain 'WARN: KM_ACCOUNTS_ORGANIZATION=999999999999' but got (empty)"
func TestRunBootstrap_DriftWarn_MissingExportCall(t *testing.T) {
	// Simulate operator with a different org account ID in their shell.
	t.Setenv("KM_ACCOUNTS_ORGANIZATION", "999999999999")

	// cfg represents what km-config.yaml says (yaml value = "111111111111").
	cfg := &config.Config{
		Domain:                "example.com",
		PrimaryRegion:         "us-east-1",
		OrganizationAccountID: "111111111111", // yaml value
		ApplicationAccountID:  "333333333333",
		ResourcePrefix:        "km",
		ArtifactsBucket:       "km-artifacts-333333333333",
	}

	// dryRun=true: runBootstrap prints its banner without calling terragrunt.
	// This keeps the test fast and credential-free.
	var out bytes.Buffer
	stderr := captureStderr(t, func() {
		_ = RunBootstrapFunc(context.Background(), cfg, true /* dryRun */, &out)
	})

	// After Plan 08: ExportTerragruntEnvVars must be called from runBootstrap,
	// triggering warnAndSetEnv which emits the drift WARN.
	if !strings.Contains(stderr, "WARN: KM_ACCOUNTS_ORGANIZATION=999999999999") {
		t.Errorf("expected stderr to contain 'WARN: KM_ACCOUNTS_ORGANIZATION=999999999999' (Plan 08 will fix); got:\n%s", stderr)
	}
}

// TestRunBootstrap_DriftWarn_KM_REGION verifies drift WARN for the top-level
// region key when KM_REGION in the shell differs from cfg.PrimaryRegion.
//
// SKIP-GUARDED: This test requires cfg.YAMLDefaults, a field added by Plan 07.
// Without YAMLDefaults, ExportTerragruntEnvVars cannot distinguish yaml vs env
// values for PrimaryRegion (both appear equal in cfg after viper baking).
// Plan 07's Task 2 action MUST remove this t.Skip call.
func TestRunBootstrap_DriftWarn_KM_REGION(t *testing.T) {
	t.Setenv("KM_REGION", "us-west-2")

	cfg := &config.Config{
		Domain:          "example.com",
		PrimaryRegion:   "us-east-1", // yaml value (post-Plan-07: also stored in YAMLDefaults["region"])
		ResourcePrefix:  "km",
		ArtifactsBucket: "km-artifacts-111111111111",
	}

	var out bytes.Buffer
	stderr := captureStderr(t, func() {
		_ = RunBootstrapFunc(context.Background(), cfg, true /* dryRun */, &out)
	})

	if !strings.Contains(stderr, "WARN: KM_REGION=us-west-2") {
		t.Errorf("expected 'WARN: KM_REGION=us-west-2' in stderr; got:\n%s", stderr)
	}
}

// TestRunBootstrap_NoDriftWarn_WhenEnvMatchesConfig verifies that when
// KM_ACCOUNTS_ORGANIZATION in the shell equals cfg.OrganizationAccountID,
// no drift WARN is emitted (values agree → no operator action needed).
//
// GREEN both before and after Plan 08 (negative test — must not regress).
func TestRunBootstrap_NoDriftWarn_WhenEnvMatchesConfig(t *testing.T) {
	// Same value in env as cfg — no drift.
	t.Setenv("KM_ACCOUNTS_ORGANIZATION", "111111111111")

	cfg := &config.Config{
		Domain:                "example.com",
		PrimaryRegion:         "us-east-1",
		OrganizationAccountID: "111111111111", // matches env
		ApplicationAccountID:  "333333333333",
		ResourcePrefix:        "km",
		ArtifactsBucket:       "km-artifacts-333333333333",
	}

	var out bytes.Buffer
	stderr := captureStderr(t, func() {
		_ = RunBootstrapFunc(context.Background(), cfg, true /* dryRun */, &out)
	})

	// Matching values → no WARN for KM_ACCOUNTS_ORGANIZATION.
	if strings.Contains(stderr, "WARN: KM_ACCOUNTS_ORGANIZATION") {
		t.Errorf("unexpected WARN for KM_ACCOUNTS_ORGANIZATION when env matches config; got:\n%s", stderr)
	}
}

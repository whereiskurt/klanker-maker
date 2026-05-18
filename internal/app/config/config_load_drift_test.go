package config_test

// Wave 5 RED scaffolding — Phase 84.3 Plan 06.
// Integration tests for drift WARN behavior via ExportTerragruntEnvVars.
//
// WHY THESE TESTS ARE RED against current code:
//
//  The current code path:
//    1. config.Load() calls viper.AutomaticEnv(), which bakes KM_REGION into v.
//    2. v.GetString("region") returns the env value (e.g. "us-west-2").
//    3. cfg.PrimaryRegion == "us-west-2" (env value).
//    4. ExportTerragruntEnvVars calls warnAndSetEnv("KM_REGION", "region", cfg.PrimaryRegion).
//    5. warnAndSetEnv checks: envVal != cfgVal → "us-west-2" != "us-west-2" → FALSE → no WARN.
//
//  Plan 07 adds cfg.YAMLDefaults (a snapshot of the yaml-loaded values before env
//  baking), so ExportTerragruntEnvVars compares envVal against the YAML value
//  instead of the (already-baked) cfg field.
//
//  RED tests:
//    TestConfigLoad_DriftWarn_KM_REGION — fails with no WARN on stderr (Plan 07 fixes)
//    TestConfigLoad_DriftWarn_KM_ARTIFACTS_BUCKET — same pattern, different key
//
//  GREEN tests (must stay GREEN before and after Plan 07):
//    TestConfigLoad_NoDriftWarn_WhenNoEnvSet — negative: no env set → no WARN
//    TestConfigLoad_NoDriftWarn_WhenEnvMatchesYaml — negative: env == yaml → no WARN

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// captureStderrOS redirects os.Stderr through a pipe for the duration of fn,
// then restores the original stderr and returns whatever fn wrote to stderr.
// Used in config_test package (no captureStderr helper from testhelpers_test.go available here).
func captureStderrOS(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	io.Copy(&buf, r) //nolint:errcheck
	return buf.String()
}

// exportEnvVarNames lists all env vars that ExportTerragruntEnvVars may set via
// os.Setenv. We must pre-register these with t.Setenv in tests that call
// ExportTerragruntEnvVars to prevent test pollution across subtests.
// Without this, os.Setenv calls inside ExportTerragruntEnvVars persist beyond
// the test's cleanup (since only t.Setenv'd vars are restored by testing.T).
var exportEnvVarNames = []string{
	"KM_ROUTE53_ZONE_ID",
	"KM_REGION_LABEL",
	"KM_ARTIFACTS_BUCKET",
	"KM_ACCOUNTS_ORGANIZATION",
	"KM_ACCOUNTS_DNS_PARENT",
	"KM_ACCOUNTS_APPLICATION",
	"KM_DOMAIN",
	"KM_REGION",
	"KM_OPERATOR_EMAIL",
	"KM_SCHEDULER_ROLE_ARN",
	"KM_RESOURCE_PREFIX",
	"KM_EMAIL_SUBDOMAIN",
}

// isolateExportEnvVars pre-registers all ExportTerragruntEnvVars output env vars
// with t.Setenv so testing.T cleanup restores them after the test.
// Call this at the top of any test that calls ExportTerragruntEnvVars.
func isolateExportEnvVars(t *testing.T) {
	t.Helper()
	for _, k := range exportEnvVarNames {
		t.Setenv(k, os.Getenv(k)) // register current value for cleanup
	}
}

// writeKMConfigDrift writes a km-config.yaml into dir with the given content.
// Uses a distinct name from writeKMConfig84 (config_84_3_test.go) to avoid
// redeclaration within the config_test package.
func writeKMConfigDrift(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("writeKMConfigDrift: %v", err)
	}
}

// changeToDir is already defined in config_84_3_test.go (same package).
// We reuse it here without redeclaring.

// TestConfigLoad_DriftWarn_KM_REGION verifies that when KM_REGION in the
// environment differs from the yaml region, ExportTerragruntEnvVars emits a
// "WARN: KM_REGION=..." line to stderr.
//
// RED against current code: cfg.PrimaryRegion == env value after config.Load()
// because viper bakes the env in. ExportTerragruntEnvVars then sees
// envVal == cfgVal → no WARN. Plan 07 fixes by adding YAMLDefaults snapshot.
func TestConfigLoad_DriftWarn_KM_REGION(t *testing.T) {
	isolateExportEnvVars(t)
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
resource_prefix: km
`)
	changeToDir(t, dir)

	// Simulate operator with a different region in their shell.
	t.Setenv("KM_REGION", "us-west-2")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load(): %v", err)
	}

	// env wins for PrimaryRegion (standard viper behavior — unchanged by Plan 07).
	if cfg.PrimaryRegion != "us-west-2" {
		t.Errorf("PrimaryRegion = %q, want us-west-2 (env wins)", cfg.PrimaryRegion)
	}

	// After Plan 07: ExportTerragruntEnvVars must emit a WARN comparing env vs yaml.
	stderr := captureStderrOS(t, func() {
		cmd.ExportTerragruntEnvVars(cfg)
	})

	if !strings.Contains(stderr, "KM_REGION=us-west-2") {
		t.Errorf("expected WARN containing 'KM_REGION=us-west-2' on stderr; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "us-east-1") {
		t.Errorf("expected WARN to mention yaml value 'us-east-1'; got:\n%s", stderr)
	}
}

// TestConfigLoad_DriftWarn_KM_ARTIFACTS_BUCKET verifies drift WARN for the
// artifacts_bucket key: when KM_ARTIFACTS_BUCKET in env differs from yaml value,
// ExportTerragruntEnvVars emits a WARN to stderr.
//
// RED for the same reason as TestConfigLoad_DriftWarn_KM_REGION.
func TestConfigLoad_DriftWarn_KM_ARTIFACTS_BUCKET(t *testing.T) {
	isolateExportEnvVars(t)
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
artifacts_bucket: km-artifacts-123456789012
resource_prefix: km
`)
	changeToDir(t, dir)

	// Operator has an override in their shell — different from yaml.
	t.Setenv("KM_ARTIFACTS_BUCKET", "env-override-bucket")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load(): %v", err)
	}

	// KM_ARTIFACTS_BUCKET env wins for ArtifactsBucket (standard viper behavior).
	// Note: the yaml-authoritative logic only applies to accounts.* keys.
	if cfg.ArtifactsBucket != "env-override-bucket" {
		t.Errorf("ArtifactsBucket = %q, want env-override-bucket (env wins)", cfg.ArtifactsBucket)
	}

	// After Plan 07: ExportTerragruntEnvVars must emit a WARN.
	stderr := captureStderrOS(t, func() {
		cmd.ExportTerragruntEnvVars(cfg)
	})

	if !strings.Contains(stderr, "KM_ARTIFACTS_BUCKET=env-override-bucket") {
		t.Errorf("expected WARN containing 'KM_ARTIFACTS_BUCKET=env-override-bucket' on stderr; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "km-artifacts-123456789012") {
		t.Errorf("expected WARN to mention yaml value 'km-artifacts-123456789012'; got:\n%s", stderr)
	}
}

// TestConfigLoad_NoDriftWarn_WhenNoEnvSet verifies that when KM_REGION is NOT
// set in the environment, ExportTerragruntEnvVars emits NO drift WARN for region.
//
// GREEN both before and after Plan 07 (negative test — must not regress).
func TestConfigLoad_NoDriftWarn_WhenNoEnvSet(t *testing.T) {
	isolateExportEnvVars(t)
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
resource_prefix: km
`)
	changeToDir(t, dir)

	// Explicitly unset to avoid test environment pollution.
	t.Setenv("KM_REGION", "")
	os.Unsetenv("KM_REGION") //nolint:errcheck

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load(): %v", err)
	}

	stderr := captureStderrOS(t, func() {
		cmd.ExportTerragruntEnvVars(cfg)
	})

	// No env override → no WARN for KM_REGION.
	if strings.Contains(stderr, "WARN: KM_REGION") {
		t.Errorf("unexpected WARN for KM_REGION when no env set; got:\n%s", stderr)
	}
}

// TestConfigLoad_NoDriftWarn_WhenEnvMatchesYaml verifies that when KM_REGION is
// set in the environment to the SAME value as the yaml region, no drift WARN is
// emitted (values agree → no operator action needed).
//
// GREEN both before and after Plan 07 (negative test — must not regress).
func TestConfigLoad_NoDriftWarn_WhenEnvMatchesYaml(t *testing.T) {
	isolateExportEnvVars(t)
	dir := t.TempDir()
	writeKMConfigDrift(t, dir, `
region: us-east-1
resource_prefix: km
`)
	changeToDir(t, dir)

	// Same value in env as yaml — no drift.
	t.Setenv("KM_REGION", "us-east-1")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load(): %v", err)
	}

	stderr := captureStderrOS(t, func() {
		cmd.ExportTerragruntEnvVars(cfg)
	})

	// Matching values → no WARN.
	if strings.Contains(stderr, "WARN: KM_REGION") {
		t.Errorf("unexpected WARN when env matches yaml; got:\n%s", stderr)
	}
}

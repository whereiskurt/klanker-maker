package config_test

// Wave 0 RED scaffolding — Phase 84.3 Plan 01 Task 3.
// Tests for closure (h) CONFIG-DISPLAY-VS-YAML-AUTHORITY:
//   - accounts.organization, dns_parent, application: yaml values must win
//     even when KM_ACCOUNTS_* env vars are set.
//   - accounts.terraform: preserves env-var precedence (asymmetry per CONTEXT.md).
//
// TestConfig_AccountsYamlAuthoritative MUST fail against current code because
// viper's AutomaticEnv (with isSetByEnv guard) lets env vars override yaml for
// ALL keys. Plan 02 adds accountsYamlAuthoritativeKeys logic to config.Load().
//
// TestConfig_AccountsTerraformEnvWins MUST pass both before and after Plan 02
// (env wins for accounts.terraform is the intentional asymmetry).
//
// TestConfig_NoEnvSet_YamlLoaded is a smoke baseline that should pass now.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// writeKMConfig84 writes a km-config.yaml into dir with the given content.
// Uses a distinct name from writeKMConfig in config_test.go to avoid redeclaration.
func writeKMConfig84(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("writeKMConfig84: %v", err)
	}
}

// changeToDir changes the working directory to dir and restores on cleanup.
// viper's AddConfigPath(".") picks up km-config.yaml from the cwd.
func changeToDir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// TestConfig_AccountsYamlAuthoritative verifies that yaml values for
// accounts.organization, accounts.dns_parent, accounts.application WIN
// even when the corresponding KM_ACCOUNTS_* env vars are set.
//
// This test FAILS against current code (closure-h bug: env wins via isSetByEnv).
// Plan 02 adds accountsYamlAuthoritativeKeys to config.Load() to make it GREEN.
func TestConfig_AccountsYamlAuthoritative(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig84(t, dir, `
accounts:
  organization: "111111111111"
  dns_parent: "222222222222"
  application: "333333333333"
  terraform: "444444444444"
region: us-east-1
`)
	changeToDir(t, dir)

	// These env vars simulate the pathological case: operator ran
	// `export KM_ACCOUNTS_ORGANIZATION=...` in their shell before km init.
	t.Setenv("KM_ACCOUNTS_ORGANIZATION", "999999999999")
	t.Setenv("KM_ACCOUNTS_DNS_PARENT", "888888888888")
	t.Setenv("KM_ACCOUNTS_APPLICATION", "777777777777")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load(): %v", err)
	}

	// After Plan 02: yaml WINS for these three keys.
	if cfg.OrganizationAccountID != "111111111111" {
		t.Errorf("OrganizationAccountID = %q, want %q (yaml must win; got env value)", cfg.OrganizationAccountID, "111111111111")
	}
	if cfg.DNSParentAccountID != "222222222222" {
		t.Errorf("DNSParentAccountID = %q, want %q (yaml must win; got env value)", cfg.DNSParentAccountID, "222222222222")
	}
	if cfg.ApplicationAccountID != "333333333333" {
		t.Errorf("ApplicationAccountID = %q, want %q (yaml must win; got env value)", cfg.ApplicationAccountID, "333333333333")
	}
}

// TestConfig_AccountsTerraformEnvWins codifies the intentional asymmetry:
// accounts.terraform DOES retain env-var precedence (i.e., KM_ACCOUNTS_TERRAFORM wins).
// This is by design — Terraform account is less dangerous to override via env.
//
// This test should PASS both before and after Plan 02 (it documents the exception,
// not the bug).
func TestConfig_AccountsTerraformEnvWins(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig84(t, dir, `
accounts:
  organization: "111111111111"
  dns_parent: "222222222222"
  application: "333333333333"
  terraform: "444444444444"
region: us-east-1
`)
	changeToDir(t, dir)

	t.Setenv("KM_ACCOUNTS_TERRAFORM", "555555555555")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load(): %v", err)
	}

	// env WINS for accounts.terraform — intentional asymmetry.
	if cfg.TerraformAccountID != "555555555555" {
		t.Errorf("TerraformAccountID = %q, want %q (env must win for terraform account)", cfg.TerraformAccountID, "555555555555")
	}
}

// TestConfig_NoEnvSet_YamlLoaded is a smoke baseline: when no KM_ACCOUNTS_* are
// set in the environment, all four yaml account values load correctly.
func TestConfig_NoEnvSet_YamlLoaded(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig84(t, dir, `
accounts:
  organization: "111111111111"
  dns_parent: "222222222222"
  application: "333333333333"
  terraform: "444444444444"
region: us-east-1
`)
	changeToDir(t, dir)

	// Ensure no KM_ACCOUNTS_* env vars leak in from the environment.
	for _, k := range []string{"KM_ACCOUNTS_ORGANIZATION", "KM_ACCOUNTS_DNS_PARENT", "KM_ACCOUNTS_APPLICATION", "KM_ACCOUNTS_TERRAFORM"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load(): %v", err)
	}

	if cfg.OrganizationAccountID != "111111111111" {
		t.Errorf("OrganizationAccountID = %q, want %q", cfg.OrganizationAccountID, "111111111111")
	}
	if cfg.DNSParentAccountID != "222222222222" {
		t.Errorf("DNSParentAccountID = %q, want %q", cfg.DNSParentAccountID, "222222222222")
	}
	if cfg.ApplicationAccountID != "333333333333" {
		t.Errorf("ApplicationAccountID = %q, want %q", cfg.ApplicationAccountID, "333333333333")
	}
	if cfg.TerraformAccountID != "444444444444" {
		t.Errorf("TerraformAccountID = %q, want %q", cfg.TerraformAccountID, "444444444444")
	}
}

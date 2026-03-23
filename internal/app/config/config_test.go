package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// writeKMConfig writes a km-config.yaml file into dir with the given content.
func writeKMConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("writeKMConfig: %v", err)
	}
}

// TestLoadPlatformFields verifies that Load() reads the new platform fields from km-config.yaml.
func TestLoadPlatformFields(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: klankermaker.ai
accounts:
  management: "111111111111"
  terraform: "222222222222"
  application: "333333333333"
sso:
  start_url: https://my-sso.awsapps.com/start
  region: us-east-1
region: us-east-1
budget_table_name: my-budgets
`)

	// Change to dir so viper picks up km-config.yaml
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Domain != "klankermaker.ai" {
		t.Errorf("Domain: got %q, want %q", cfg.Domain, "klankermaker.ai")
	}
	if cfg.ManagementAccountID != "111111111111" {
		t.Errorf("ManagementAccountID: got %q, want %q", cfg.ManagementAccountID, "111111111111")
	}
	if cfg.TerraformAccountID != "222222222222" {
		t.Errorf("TerraformAccountID: got %q, want %q", cfg.TerraformAccountID, "222222222222")
	}
	if cfg.ApplicationAccountID != "333333333333" {
		t.Errorf("ApplicationAccountID: got %q, want %q", cfg.ApplicationAccountID, "333333333333")
	}
	if cfg.SSOStartURL != "https://my-sso.awsapps.com/start" {
		t.Errorf("SSOStartURL: got %q, want %q", cfg.SSOStartURL, "https://my-sso.awsapps.com/start")
	}
	if cfg.SSORegion != "us-east-1" {
		t.Errorf("SSORegion: got %q, want %q", cfg.SSORegion, "us-east-1")
	}
	if cfg.PrimaryRegion != "us-east-1" {
		t.Errorf("PrimaryRegion: got %q, want %q", cfg.PrimaryRegion, "us-east-1")
	}
	if cfg.BudgetTableName != "my-budgets" {
		t.Errorf("BudgetTableName: got %q, want %q", cfg.BudgetTableName, "my-budgets")
	}
}

// TestLoadBackwardCompat verifies that Load() without km-config.yaml still returns
// defaults for existing fields.
func TestLoadBackwardCompat(t *testing.T) {
	dir := t.TempDir()

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Existing fields should still have defaults
	if len(cfg.ProfileSearchPaths) == 0 {
		t.Error("ProfileSearchPaths should have defaults")
	}
	if cfg.LogLevel == "" {
		t.Error("LogLevel should have a default value")
	}
	// New fields should be empty (no km-config.yaml)
	if cfg.Domain != "" {
		t.Errorf("Domain: expected empty, got %q", cfg.Domain)
	}
	if cfg.ManagementAccountID != "" {
		t.Errorf("ManagementAccountID: expected empty, got %q", cfg.ManagementAccountID)
	}
}

// TestLoadBudgetTableDefault verifies that BudgetTableName defaults to "km-budgets" when not set.
func TestLoadBudgetTableDefault(t *testing.T) {
	dir := t.TempDir()
	// Write a km-config.yaml without budget_table_name
	writeKMConfig(t, dir, `
domain: test.example.com
`)

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.BudgetTableName != "km-budgets" {
		t.Errorf("BudgetTableName: expected default %q, got %q", "km-budgets", cfg.BudgetTableName)
	}
}

// TestLoadIdentityTableDefault verifies that IdentityTableName defaults to "km-identities" when not set.
func TestLoadIdentityTableDefault(t *testing.T) {
	dir := t.TempDir()
	// Write a km-config.yaml without identity_table_name
	writeKMConfig(t, dir, `
domain: test.example.com
`)

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.IdentityTableName != "km-identities" {
		t.Errorf("IdentityTableName: expected default %q, got %q", "km-identities", cfg.IdentityTableName)
	}
}

// TestLoadIdentityTableFromConfig verifies that IdentityTableName loads from km-config.yaml.
func TestLoadIdentityTableFromConfig(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: test.example.com
identity_table_name: my-custom-identities
`)

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.IdentityTableName != "my-custom-identities" {
		t.Errorf("IdentityTableName: expected %q, got %q", "my-custom-identities", cfg.IdentityTableName)
	}
}

// TestLoadEnvOverride verifies that KM_DOMAIN env var overrides km-config.yaml.
func TestLoadEnvOverride(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: from-file.example.com
`)

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Setenv("KM_DOMAIN", "from-env.example.com")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Domain != "from-env.example.com" {
		t.Errorf("Domain: expected env override %q, got %q", "from-env.example.com", cfg.Domain)
	}
}

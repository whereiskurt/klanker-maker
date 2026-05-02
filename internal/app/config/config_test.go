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
  dns_parent: "111111111111"
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
	if cfg.DNSParentAccountID != "111111111111" {
		t.Errorf("DNSParentAccountID: got %q, want %q", cfg.DNSParentAccountID, "111111111111")
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
	if cfg.DNSParentAccountID != "" {
		t.Errorf("DNSParentAccountID: expected empty, got %q", cfg.DNSParentAccountID)
	}
	if cfg.OrganizationAccountID != "" {
		t.Errorf("OrganizationAccountID: expected empty, got %q", cfg.OrganizationAccountID)
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

// TestMaxSandboxesDefault verifies that MaxSandboxes defaults to 10 when not set.
func TestMaxSandboxesDefault(t *testing.T) {
	dir := t.TempDir()
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

	if cfg.MaxSandboxes != 10 {
		t.Errorf("MaxSandboxes: expected default 10, got %d", cfg.MaxSandboxes)
	}
}

// TestMaxSandboxesFromConfig verifies that max_sandboxes: 5 in km-config.yaml returns 5.
func TestMaxSandboxesFromConfig(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: test.example.com
max_sandboxes: 5
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

	if cfg.MaxSandboxes != 5 {
		t.Errorf("MaxSandboxes: expected 5 from config, got %d", cfg.MaxSandboxes)
	}
}

// TestMaxSandboxesEnvOverride verifies that KM_MAX_SANDBOXES=3 overrides config value.
func TestMaxSandboxesEnvOverride(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: test.example.com
max_sandboxes: 5
`)

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Setenv("KM_MAX_SANDBOXES", "3")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.MaxSandboxes != 3 {
		t.Errorf("MaxSandboxes: expected env override 3, got %d", cfg.MaxSandboxes)
	}
}

// TestConfig_DoctorStaleAMIDays_Default verifies that DoctorStaleAMIDays defaults to 30
// when no km-config.yaml is present and no env var is set.
func TestConfig_DoctorStaleAMIDays_Default(t *testing.T) {
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

	if cfg.DoctorStaleAMIDays != 30 {
		t.Errorf("DoctorStaleAMIDays: got %d, want 30 (default)", cfg.DoctorStaleAMIDays)
	}
}

// TestConfig_DoctorStaleAMIDays_EnvOverride verifies that KM_DOCTOR_STALE_AMI_DAYS=7
// overrides the default value.
func TestConfig_DoctorStaleAMIDays_EnvOverride(t *testing.T) {
	dir := t.TempDir()

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Setenv("KM_DOCTOR_STALE_AMI_DAYS", "7")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.DoctorStaleAMIDays != 7 {
		t.Errorf("DoctorStaleAMIDays: got %d, want 7 (env override)", cfg.DoctorStaleAMIDays)
	}
}

// TestConfig_DoctorStaleAMIDays_FileOverride verifies that doctor_stale_ami_days: 14
// in km-config.yaml is honored by Load().
func TestConfig_DoctorStaleAMIDays_FileOverride(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: test.example.com
doctor_stale_ami_days: 14
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

	if cfg.DoctorStaleAMIDays != 14 {
		t.Errorf("DoctorStaleAMIDays: got %d, want 14 (file override)", cfg.DoctorStaleAMIDays)
	}
}

// TestConfig_DoctorStaleAMIDays_ZeroFallsBackToDefault verifies that a zero value
// is clamped back to 30 (guards against operator misconfiguration).
func TestConfig_DoctorStaleAMIDays_ZeroFallsBackToDefault(t *testing.T) {
	dir := t.TempDir()

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Setenv("KM_DOCTOR_STALE_AMI_DAYS", "0")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.DoctorStaleAMIDays != 30 {
		t.Errorf("DoctorStaleAMIDays: got %d, want 30 (zero clamped to default)", cfg.DoctorStaleAMIDays)
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

// TestLoadOrganizationAndDNSParentFields verifies that accounts.organization and
// accounts.dns_parent in km-config.yaml are loaded into their respective struct fields.
func TestLoadOrganizationAndDNSParentFields(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
accounts:
  organization: "111111111111"
  dns_parent: "222222222222"
  application: "333333333333"
  terraform: "333333333333"
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

	if cfg.OrganizationAccountID != "111111111111" {
		t.Errorf("OrganizationAccountID: got %q, want %q", cfg.OrganizationAccountID, "111111111111")
	}
	if cfg.DNSParentAccountID != "222222222222" {
		t.Errorf("DNSParentAccountID: got %q, want %q", cfg.DNSParentAccountID, "222222222222")
	}
}

// TestConfig_GetResourcePrefix_FallbackKM verifies that GetResourcePrefix returns "km"
// when ResourcePrefix is empty, and returns the configured value when set.
func TestConfig_GetResourcePrefix_FallbackKM(t *testing.T) {
	c := &config.Config{}
	if got := c.GetResourcePrefix(); got != "km" {
		t.Fatalf("expected fallback 'km', got %q", got)
	}
	c.ResourcePrefix = "stg"
	if got := c.GetResourcePrefix(); got != "stg" {
		t.Fatalf("expected 'stg' from set field, got %q", got)
	}
}

// TestConfig_GetSlackThreadsTableName_DefaultAndPrefix verifies table name derivation.
func TestConfig_GetSlackThreadsTableName_DefaultAndPrefix(t *testing.T) {
	c := &config.Config{}
	if got := c.GetSlackThreadsTableName(); got != "km-slack-threads" {
		t.Fatalf("expected default 'km-slack-threads', got %q", got)
	}
	c.ResourcePrefix = "stg"
	if got := c.GetSlackThreadsTableName(); got != "stg-slack-threads" {
		t.Fatalf("expected 'stg-slack-threads', got %q", got)
	}
	c.SlackThreadsTableName = "explicit-name"
	if got := c.GetSlackThreadsTableName(); got != "explicit-name" {
		t.Fatalf("explicit field should win, got %q", got)
	}
}

// TestConfig_NilReceiverSafe verifies that nil Config receivers return safe defaults.
func TestConfig_NilReceiverSafe(t *testing.T) {
	var c *config.Config
	if got := c.GetResourcePrefix(); got != "km" {
		t.Fatalf("nil receiver: want 'km', got %q", got)
	}
	if got := c.GetSlackThreadsTableName(); got != "km-slack-threads" {
		t.Fatalf("nil receiver: want 'km-slack-threads', got %q", got)
	}
}

// TestLoadBlankOrganizationIsValid verifies that km-config.yaml without
// accounts.organization loads without error and yields an empty OrganizationAccountID.
func TestLoadBlankOrganizationIsValid(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
accounts:
  dns_parent: "444444444444"
  application: "333333333333"
  terraform: "222222222222"
`)

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}
	if cfg.OrganizationAccountID != "" {
		t.Errorf("OrganizationAccountID: expected empty string for missing key, got %q", cfg.OrganizationAccountID)
	}
}

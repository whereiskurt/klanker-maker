package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
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

// TestLoadBudgetTableDefault verifies that GetBudgetTableName() returns "km-budgets" by default.
// Phase 66: the raw field defaults to "" so the prefix-aware helper can derive the name from
// resource_prefix; for default prefix "km" the helper returns "km-budgets" preserving behavior.
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

	if got := cfg.GetBudgetTableName(); got != "km-budgets" {
		t.Errorf("GetBudgetTableName(): expected default %q, got %q", "km-budgets", got)
	}
}

// TestLoadIdentityTableDefault verifies that GetIdentityTableName() returns "km-identities" by default.
// Phase 66: see TestLoadBudgetTableDefault for the same field/helper rationale.
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

	if got := cfg.GetIdentityTableName(); got != "km-identities" {
		t.Errorf("GetIdentityTableName(): expected default %q, got %q", "km-identities", got)
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

// TestConfig_DoctorIgnorePrefixes_FileOverride verifies that
// doctor_ignore_prefixes in km-config.yaml is parsed and surfaced via the getter.
func TestConfig_DoctorIgnorePrefixes_FileOverride(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: test.example.com
doctor_ignore_prefixes:
  - km2
  - rg
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

	got := cfg.GetDoctorIgnorePrefixes()
	if len(got) != 2 || got[0] != "km2" || got[1] != "rg" {
		t.Errorf("GetDoctorIgnorePrefixes() = %v, want [km2 rg]", got)
	}
}

// TestConfig_DoctorIgnorePrefixes_DefaultNil verifies the getter returns nil when
// the key is absent (no siblings ignored by default).
func TestConfig_DoctorIgnorePrefixes_DefaultNil(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, "domain: test.example.com\n")

	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if got := cfg.GetDoctorIgnorePrefixes(); len(got) != 0 {
		t.Errorf("GetDoctorIgnorePrefixes() = %v, want empty/nil", got)
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

// TestGetResourcePrefix_Custom verifies that GetResourcePrefix returns the configured value.
func TestGetResourcePrefix_Custom(t *testing.T) {
	c := &config.Config{ResourcePrefix: "alt"}
	if got := c.GetResourcePrefix(); got != "alt" {
		t.Fatalf("expected 'alt', got %q", got)
	}
}

// TestGetEmailDomain_Default verifies that GetEmailDomain returns "sandboxes.{domain}"
// when EmailSubdomain is unset.
func TestGetEmailDomain_Default(t *testing.T) {
	c := &config.Config{Domain: "example.com"}
	if got := c.GetEmailDomain(); got != "sandboxes.example.com" {
		t.Fatalf("expected 'sandboxes.example.com', got %q", got)
	}
}

// TestGetEmailDomain_Custom verifies that GetEmailDomain returns "{subdomain}.{domain}"
// when EmailSubdomain is set.
func TestGetEmailDomain_Custom(t *testing.T) {
	c := &config.Config{Domain: "example.com", EmailSubdomain: "mail"}
	if got := c.GetEmailDomain(); got != "mail.example.com" {
		t.Fatalf("expected 'mail.example.com', got %q", got)
	}
}

// TestGetEmailDomain_NilSafe verifies that a nil Config receiver returns the hardcoded
// fallback "sandboxes.klankermaker.ai" (mirrors Phase 67's nil-safety pattern).
func TestGetEmailDomain_NilSafe(t *testing.T) {
	var c *config.Config
	if got := c.GetEmailDomain(); got != "sandboxes.klankermaker.ai" {
		t.Fatalf("nil receiver: expected 'sandboxes.klankermaker.ai', got %q", got)
	}
}

// TestGetSsmPrefix_Default verifies that GetSsmPrefix returns "/km/" with empty config.
func TestGetSsmPrefix_Default(t *testing.T) {
	c := &config.Config{}
	if got := c.GetSsmPrefix(); got != "/km/" {
		t.Fatalf("expected '/km/', got %q", got)
	}
}

// TestGetSsmPrefix_Custom verifies that GetSsmPrefix returns "/{prefix}/" when prefix is set.
func TestGetSsmPrefix_Custom(t *testing.T) {
	c := &config.Config{ResourcePrefix: "alt"}
	if got := c.GetSsmPrefix(); got != "/alt/" {
		t.Fatalf("expected '/alt/', got %q", got)
	}
}

// TestLoadEmailSubdomain verifies that km-config.yaml with email_subdomain sets
// cfg.EmailSubdomain AND that GetEmailDomain() returns "{subdomain}.{domain}".
func TestLoadEmailSubdomain(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
email_subdomain: mail
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

	if cfg.EmailSubdomain != "mail" {
		t.Errorf("EmailSubdomain: got %q, want %q", cfg.EmailSubdomain, "mail")
	}
	if got := cfg.GetEmailDomain(); got != "mail.example.com" {
		t.Errorf("GetEmailDomain(): got %q, want %q", got, "mail.example.com")
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

// TestContainerSubstratesEnabled_DefaultUnset verifies that when the operator
// hasn't set container_substrates_enabled, ShouldBuildContainerImages returns
// true (back-compat: existing installs continue building images).
func TestContainerSubstratesEnabled_DefaultUnset(t *testing.T) {
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

	if cfg.ContainerSubstratesEnabled != nil {
		t.Errorf("ContainerSubstratesEnabled: expected nil pointer when unset, got %v", *cfg.ContainerSubstratesEnabled)
	}
	if !cfg.ShouldBuildContainerImages() {
		t.Error("ShouldBuildContainerImages: expected true (back-compat default) when unset, got false")
	}
}

// TestContainerSubstratesEnabled_ExplicitFalse verifies that
// container_substrates_enabled: false in km-config.yaml is honored — operators
// who only use EC2 sandboxes can disable ECR builds and skip ~2-10 min/init.
func TestContainerSubstratesEnabled_ExplicitFalse(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: test.example.com
container_substrates_enabled: false
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

	if cfg.ContainerSubstratesEnabled == nil {
		t.Fatal("ContainerSubstratesEnabled: expected non-nil pointer when explicitly set, got nil")
	}
	if *cfg.ContainerSubstratesEnabled != false {
		t.Errorf("ContainerSubstratesEnabled: expected false, got %v", *cfg.ContainerSubstratesEnabled)
	}
	if cfg.ShouldBuildContainerImages() {
		t.Error("ShouldBuildContainerImages: expected false when container_substrates_enabled=false, got true")
	}
}

// TestContainerSubstratesEnabled_ExplicitTrue verifies that
// container_substrates_enabled: true in km-config.yaml round-trips and is the
// same as the default — distinguishes "explicitly opted in" from "unset".
func TestContainerSubstratesEnabled_ExplicitTrue(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: test.example.com
container_substrates_enabled: true
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

	if cfg.ContainerSubstratesEnabled == nil {
		t.Fatal("ContainerSubstratesEnabled: expected non-nil pointer when explicitly set, got nil")
	}
	if *cfg.ContainerSubstratesEnabled != true {
		t.Errorf("ContainerSubstratesEnabled: expected true, got %v", *cfg.ContainerSubstratesEnabled)
	}
	if !cfg.ShouldBuildContainerImages() {
		t.Error("ShouldBuildContainerImages: expected true when container_substrates_enabled=true, got false")
	}
}

// TestShouldBuildContainerImages_NilReceiver verifies the helper is safe on a
// nil *Config (defensive — callers occasionally use it on optional config).
func TestShouldBuildContainerImages_NilReceiver(t *testing.T) {
	var cfg *config.Config
	if !cfg.ShouldBuildContainerImages() {
		t.Error("ShouldBuildContainerImages on nil *Config: expected true (default), got false")
	}
}

// TestGetSandboxSessionDocumentName_Default verifies fallback to "km" prefix.
// Phase 84.4.1: GetSandboxSessionDocumentName() replaces 5 hardcoded callsites.
func TestGetSandboxSessionDocumentName_Default(t *testing.T) {
	cfg := &config.Config{}
	got := cfg.GetSandboxSessionDocumentName()
	if got != "km-Sandbox-Session" {
		t.Errorf("expected km-Sandbox-Session, got %q", got)
	}
}

// TestGetSandboxSessionDocumentName_Custom verifies per-install rename.
func TestGetSandboxSessionDocumentName_Custom(t *testing.T) {
	cfg := &config.Config{ResourcePrefix: "tg"}
	got := cfg.GetSandboxSessionDocumentName()
	if got != "tg-Sandbox-Session" {
		t.Errorf("expected tg-Sandbox-Session, got %q", got)
	}
}

// TestGetSandboxSessionDocumentName_NilSafe matches the GetEmailDomain nil-safety pattern.
// A nil Config receiver must not panic and must return the "km" default.
func TestGetSandboxSessionDocumentName_NilSafe(t *testing.T) {
	var cfg *config.Config
	got := cfg.GetSandboxSessionDocumentName()
	if got != "km-Sandbox-Session" {
		t.Errorf("expected km-Sandbox-Session (nil-safe), got %q", got)
	}
}

// TestLoadSlackMentionOnly_True verifies Phase 91.1 nested key slack.mention_only
// loads from yaml end-to-end (catches the merge-loop allowlist bug).
func TestLoadSlackMentionOnly_True(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    mention_only: true
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
	if cfg.Slack.MentionOnly == nil {
		t.Fatal("Slack.MentionOnly is nil; expected non-nil from yaml load (merge-loop must include slack.mention_only)")
	}
	if *cfg.Slack.MentionOnly != true {
		t.Errorf("Slack.MentionOnly: got %v, want true", *cfg.Slack.MentionOnly)
	}
}

// TestLoadSlackMentionOnly_False — explicit false in yaml.
func TestLoadSlackMentionOnly_False(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    mention_only: false
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
	if cfg.Slack.MentionOnly == nil {
		t.Fatal("Slack.MentionOnly is nil; expected non-nil &false")
	}
	if *cfg.Slack.MentionOnly != false {
		t.Errorf("Slack.MentionOnly: got %v, want false", *cfg.Slack.MentionOnly)
	}
}

// TestLoadSlackMentionOnly_Absent — yaml omits the slack block; pointer stays nil.
func TestLoadSlackMentionOnly_Absent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
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
	if cfg.Slack.MentionOnly != nil {
		t.Errorf("Slack.MentionOnly: got &%v, want nil (key absent)", *cfg.Slack.MentionOnly)
	}
}

// TestLoadSlackReactAlways_True verifies Phase 91.4 nested key slack.react_always
// loads from yaml end-to-end (same merge-loop coverage as slack.mention_only).
func TestLoadSlackReactAlways_True(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    react_always: true
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
	if cfg.Slack.ReactAlways == nil {
		t.Fatal("Slack.ReactAlways is nil; expected non-nil")
	}
	if *cfg.Slack.ReactAlways != true {
		t.Errorf("Slack.ReactAlways: got %v, want true", *cfg.Slack.ReactAlways)
	}
}

func TestLoadSlackReactAlways_False(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    react_always: false
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
	if cfg.Slack.ReactAlways == nil {
		t.Fatal("Slack.ReactAlways is nil")
	}
	if *cfg.Slack.ReactAlways != false {
		t.Errorf("Slack.ReactAlways: got %v, want false", *cfg.Slack.ReactAlways)
	}
}

func TestLoadSlackReactAlways_Absent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
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
	if cfg.Slack.ReactAlways != nil {
		t.Errorf("Slack.ReactAlways: got &%v, want nil", *cfg.Slack.ReactAlways)
	}
}

// TestLoadSlackPeerBridges_Set verifies Phase 95 nested key slack.peer_bridges
// loads from yaml end-to-end (catches the merge-loop allowlist footgun).
// Asserts len==2 specifically — not just non-nil — to make the merge-list
// footgun visible: if "slack.peer_bridges" is missing from the v2→v merge-list,
// cfg.Slack.PeerBridges stays nil even when the key is present in km-config.yaml.
func TestLoadSlackPeerBridges_Set(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    peer_bridges:
      - https://abc123.lambda-url.us-east-1.on.aws/events
      - https://def456.lambda-url.us-east-1.on.aws/events
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
	if cfg.Slack.PeerBridges == nil {
		t.Fatal("Slack.PeerBridges is nil; expected non-nil from yaml load (merge-loop must include slack.peer_bridges)")
	}
	if len(cfg.Slack.PeerBridges) != 2 {
		t.Errorf("Slack.PeerBridges: got len=%d, want 2; values=%v", len(cfg.Slack.PeerBridges), cfg.Slack.PeerBridges)
	}
	if cfg.Slack.PeerBridges[0] != "https://abc123.lambda-url.us-east-1.on.aws/events" {
		t.Errorf("Slack.PeerBridges[0]: got %q, want abc123 URL", cfg.Slack.PeerBridges[0])
	}
	if cfg.Slack.PeerBridges[1] != "https://def456.lambda-url.us-east-1.on.aws/events" {
		t.Errorf("Slack.PeerBridges[1]: got %q, want def456 URL", cfg.Slack.PeerBridges[1])
	}
}

// TestLoadGithubPeerBridges_Set verifies Phase 100 nested key github.peer_bridges
// round-trips into cfg.Github.PeerBridges end-to-end.
//
// DEVIATION (100-RESEARCH.md Pitfall 2): unlike slack.peer_bridges — which needed
// its OWN "slack.peer_bridges" merge-list entry because Slack config is decoded
// field-by-field via GetStringSlice — the github: block is decoded as a single
// structured v.UnmarshalKey("github", &cfg.Github) and "github" is ALREADY in the
// v2→v merge-list (config.go ~line 551). Adding PeerBridges []string to GithubConfig
// is picked up automatically — NO new merge-list entry is required. This test
// PASSING with ONLY the struct field added (and the existing "github" merge entry)
// is the proof that no redundant "github.peer_bridges" merge entry is needed.
func TestLoadGithubPeerBridges_Set(t *testing.T) {
	dir := t.TempDir()
	// Synthetic on.aws Lambda Function URLs — generic placeholders, never real accounts.
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
github:
    peer_bridges:
      - https://gh-abc123.lambda-url.us-east-1.on.aws/
      - https://gh-def456.lambda-url.us-east-1.on.aws/
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
	if cfg.Github.PeerBridges == nil {
		t.Fatal("Github.PeerBridges is nil; expected non-nil from yaml load via the existing UnmarshalKey(\"github\",…) — proves no separate merge entry is needed")
	}
	if len(cfg.Github.PeerBridges) != 2 {
		t.Errorf("Github.PeerBridges: got len=%d, want 2; values=%v", len(cfg.Github.PeerBridges), cfg.Github.PeerBridges)
	}
	if cfg.Github.PeerBridges[0] != "https://gh-abc123.lambda-url.us-east-1.on.aws/" {
		t.Errorf("Github.PeerBridges[0]: got %q, want gh-abc123 URL", cfg.Github.PeerBridges[0])
	}
	if cfg.Github.PeerBridges[1] != "https://gh-def456.lambda-url.us-east-1.on.aws/" {
		t.Errorf("Github.PeerBridges[1]: got %q, want gh-def456 URL", cfg.Github.PeerBridges[1])
	}
}

// TestLoadGithubPeerBridges_Absent verifies that omitting github.peer_bridges
// (even with a github: block present carrying other keys) yields a nil/empty
// slice — the "federation off" dormancy sentinel for the GitHub relayer.
func TestLoadGithubPeerBridges_Absent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
github:
    default_profile: github-review
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
	if len(cfg.Github.PeerBridges) != 0 {
		t.Errorf("Github.PeerBridges: got %v (len=%d), want empty/nil when github.peer_bridges absent", cfg.Github.PeerBridges, len(cfg.Github.PeerBridges))
	}
}

// TestLoadSlackPeerBridges_Absent verifies that omitting slack.peer_bridges from
// yaml yields a nil slice — the "federation off" sentinel for EventsHandler.Relayer.
func TestLoadSlackPeerBridges_Absent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
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
	if cfg.Slack.PeerBridges != nil {
		t.Errorf("Slack.PeerBridges: got %v, want nil (key absent => federation off)", cfg.Slack.PeerBridges)
	}
}

// TestLoadSlackDefaultRouter_True verifies Phase 96 nested key slack.default_router
// loads from yaml end-to-end (catches the merge-loop allowlist footgun).
// If "slack.default_router" is missing from the v2→v merge-list, cfg.Slack.DefaultRouter
// stays nil even when the key is present in km-config.yaml (project_config_key_merge_list).
func TestLoadSlackDefaultRouter_True(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    default_router: true
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
	if cfg.Slack.DefaultRouter == nil {
		t.Fatal("Slack.DefaultRouter is nil; expected non-nil from yaml load (merge-loop must include slack.default_router)")
	}
	if *cfg.Slack.DefaultRouter != true {
		t.Errorf("Slack.DefaultRouter: got %v, want true", *cfg.Slack.DefaultRouter)
	}
}

// TestLoadSlackDefaultRouter_False verifies explicit false in yaml loads correctly.
func TestLoadSlackDefaultRouter_False(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    default_router: false
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
	if cfg.Slack.DefaultRouter == nil {
		t.Fatal("Slack.DefaultRouter is nil; expected non-nil &false")
	}
	if *cfg.Slack.DefaultRouter != false {
		t.Errorf("Slack.DefaultRouter: got %v, want false", *cfg.Slack.DefaultRouter)
	}
}

// TestLoadSlackDefaultRouter_Absent verifies that omitting slack.default_router
// from yaml yields a nil pointer — the "router off" sentinel.
func TestLoadSlackDefaultRouter_Absent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
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
	if cfg.Slack.DefaultRouter != nil {
		t.Errorf("Slack.DefaultRouter: got &%v, want nil (key absent => router off)", *cfg.Slack.DefaultRouter)
	}
}

// TestLoadSlackDefaultRouter_MergeListRegression is the merge-list footgun regression
// test (project_config_key_merge_list): a config that sets ONLY slack.default_router:true
// must still surface the value — must NOT be silently dropped by an absent merge-list entry.
func TestLoadSlackDefaultRouter_MergeListRegression(t *testing.T) {
	dir := t.TempDir()
	// Only set slack.default_router — no other slack.* keys — to isolate the merge-list path.
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
slack:
    default_router: true
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
	// If this assertion fails, the most likely cause is that "slack.default_router"
	// is missing from the v2→v merge-list in config.go (the known silent-drop footgun).
	if cfg.Slack.DefaultRouter == nil {
		t.Fatal("Slack.DefaultRouter is nil after loading a config that explicitly sets slack.default_router: true — " +
			"check that \"slack.default_router\" is in the v2→v merge-list in config.go (project_config_key_merge_list)")
	}
	if *cfg.Slack.DefaultRouter != true {
		t.Errorf("Slack.DefaultRouter: got %v, want true", *cfg.Slack.DefaultRouter)
	}
}

// TestDoctorRetentionAndExpireDays verifies the five-touchpoint pattern for the two
// Phase 94 config knobs: doctor_log_retention_days and doctor_s3_expire_days.
func TestDoctorRetentionAndExpireDays(t *testing.T) {
	t.Run("default 30 when unset", func(t *testing.T) {
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
		if cfg.DoctorLogRetentionDays != 30 {
			t.Errorf("DoctorLogRetentionDays: got %d, want 30 (default)", cfg.DoctorLogRetentionDays)
		}
		if cfg.DoctorS3ExpireDays != 30 {
			t.Errorf("DoctorS3ExpireDays: got %d, want 30 (default)", cfg.DoctorS3ExpireDays)
		}
	})

	t.Run("yaml file setting both keys to 7 loads 7 (proves merge-list wiring)", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfig(t, dir, `
domain: test.example.com
doctor_log_retention_days: 7
doctor_s3_expire_days: 7
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
		if cfg.DoctorLogRetentionDays != 7 {
			t.Errorf("DoctorLogRetentionDays: got %d, want 7 (yaml override)", cfg.DoctorLogRetentionDays)
		}
		if cfg.DoctorS3ExpireDays != 7 {
			t.Errorf("DoctorS3ExpireDays: got %d, want 7 (yaml override)", cfg.DoctorS3ExpireDays)
		}
	})

	t.Run("<=0 clamps to 30", func(t *testing.T) {
		dir := t.TempDir()
		orig, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(orig) })
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		t.Setenv("KM_DOCTOR_LOG_RETENTION_DAYS", "0")
		t.Setenv("KM_DOCTOR_S3_EXPIRE_DAYS", "-1")
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		if cfg.DoctorLogRetentionDays != 30 {
			t.Errorf("DoctorLogRetentionDays: got %d, want 30 (zero clamped to default)", cfg.DoctorLogRetentionDays)
		}
		if cfg.DoctorS3ExpireDays != 30 {
			t.Errorf("DoctorS3ExpireDays: got %d, want 30 (negative clamped to default)", cfg.DoctorS3ExpireDays)
		}
	})
}

// ---- Phase 101: GithubConfig.DefaultRouter *bool round-trip tests ----

// TestLoadGithubDefaultRouter_Set verifies Phase 101 nested key github.default_router
// round-trips into cfg.Github.DefaultRouter end-to-end for all three cases:
// true, false, and absent (nil).
//
// NO-MERGE-ENTRY PROOF: the github: block is decoded as a single structured
// v.UnmarshalKey("github", &cfg.Github) call and "github" is ALREADY in the
// v2→v merge-list (config.go ~line 566). Adding DefaultRouter *bool to GithubConfig
// is picked up automatically — NO new "github.default_router" merge-list entry is
// required. This test PASSING with ONLY the struct field added (and the existing
// "github" merge entry) is the proof that no redundant merge entry is needed.
// Mirrors TestLoadGithubPeerBridges_Set precedent (Phase 100, Research Pitfall 2).
func TestLoadGithubDefaultRouter_Set(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
github:
    default_router: true
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
		if cfg.Github.DefaultRouter == nil {
			t.Fatal("Github.DefaultRouter is nil; expected non-nil from yaml load via the existing UnmarshalKey(\"github\",…) — proves no separate merge entry is needed")
		}
		if *cfg.Github.DefaultRouter != true {
			t.Errorf("Github.DefaultRouter: got %v, want true", *cfg.Github.DefaultRouter)
		}
	})

	t.Run("false", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
github:
    default_router: false
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
		if cfg.Github.DefaultRouter == nil {
			t.Fatal("Github.DefaultRouter is nil; expected non-nil (explicit false) from yaml load")
		}
		if *cfg.Github.DefaultRouter != false {
			t.Errorf("Github.DefaultRouter: got %v, want false", *cfg.Github.DefaultRouter)
		}
	})

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
github:
    default_profile: github-review
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
		// nil is the tri-state dormancy sentinel: absent key ⇒ DefaultRouter nil ⇒
		// km init does NOT export KM_GITHUB_DEFAULT_ROUTER ⇒ terragrunt default "false"
		// applies ⇒ router dormant (Phase 100 byte-identical).
		if cfg.Github.DefaultRouter != nil {
			t.Errorf("Github.DefaultRouter: got %v, want nil (tri-state dormancy when absent)", *cfg.Github.DefaultRouter)
		}
	})
}

// ---- Phase 115 Wave 0 RED scaffold — GH-EVENT-CONFIG ----

// TestLoadGithubEvents verifies that github.events: in km-config.yaml round-trips
// into cfg.Github.Events via the existing UnmarshalKey("github", &cfg.Github) call.
//
// GH-EVENT-CONFIG: cfg.Github.Events field (GithubEventRule) does not exist yet →
// compile-fail on cfg.Github.Events reference = genuine RED Wave 0.
// Implemented in Phase 115 Plan 02.
//
// NO-MERGE-ENTRY PROOF (mirrors TestLoadGithubPeerBridges_Set): the "github" block is
// decoded atomically; adding Events []GithubEventRule to GithubConfig is picked up
// automatically — NO separate "github.events" merge-list entry is required.
func TestLoadGithubEvents(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
github:
    events:
      - on: repository
        actions:
          - created
        match: "myorg/*"
        exclude:
          - "myorg/archive-*"
        profile: profiles/onboard.yaml
        cooldown_seconds: 0
        prompt: "A new repo {{repo}} was created."
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
	// cfg.Github.Events does not exist yet → compile-fail = RED
	if len(cfg.Github.Events) != 1 {
		t.Fatalf("Github.Events: got len=%d, want 1 (check UnmarshalKey wiring + GithubEventRule struct)", len(cfg.Github.Events))
	}
	rule := cfg.Github.Events[0]
	if rule.On != "repository" {
		t.Errorf("Events[0].On: got %q, want %q", rule.On, "repository")
	}
	if len(rule.Actions) != 1 || rule.Actions[0] != "created" {
		t.Errorf("Events[0].Actions: got %v, want [created]", rule.Actions)
	}
	if rule.Match != "myorg/*" {
		t.Errorf("Events[0].Match: got %q, want %q", rule.Match, "myorg/*")
	}
	if len(rule.Exclude) != 1 || rule.Exclude[0] != "myorg/archive-*" {
		t.Errorf("Events[0].Exclude: got %v, want [myorg/archive-*]", rule.Exclude)
	}
	if rule.Profile != "profiles/onboard.yaml" {
		t.Errorf("Events[0].Profile: got %q, want %q", rule.Profile, "profiles/onboard.yaml")
	}
	if rule.Prompt != "A new repo {{repo}} was created." {
		t.Errorf("Events[0].Prompt: got %q", rule.Prompt)
	}
	if rule.CooldownSeconds != 0 {
		t.Errorf("Events[0].CooldownSeconds: got %d, want 0", rule.CooldownSeconds)
	}
}

// TestLoadGithubEvents_Absent verifies that omitting github.events yields an empty
// slice — the "event routing off" dormancy sentinel.
func TestLoadGithubEvents_Absent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfig(t, dir, `
domain: example.com
region: us-east-1
github:
    default_profile: github-review
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
	// cfg.Github.Events does not exist yet → compile-fail = RED
	if len(cfg.Github.Events) != 0 {
		t.Errorf("Github.Events: got %v (len=%d), want empty/nil when github.events absent", cfg.Github.Events, len(cfg.Github.Events))
	}
}

// TestGetSlackChannelsTableName verifies the three derivation cases for
// GetSlackChannelsTableName (Phase 104.3):
//
//   - nil receiver → default "km-slack-channels"
//   - explicit SlackChannelsTableName override → that value wins
//   - ResourcePrefix set, no override → "{prefix}-slack-channels"
func TestGetSlackChannelsTableName(t *testing.T) {
	t.Run("nil receiver returns default", func(t *testing.T) {
		var c *config.Config
		got := c.GetSlackChannelsTableName()
		want := "km-slack-channels"
		if got != want {
			t.Errorf("nil receiver: got %q, want %q", got, want)
		}
	})

	t.Run("explicit override wins", func(t *testing.T) {
		c := &config.Config{SlackChannelsTableName: "custom-tbl"}
		got := c.GetSlackChannelsTableName()
		want := "custom-tbl"
		if got != want {
			t.Errorf("explicit override: got %q, want %q", got, want)
		}
	})

	t.Run("prefix-derived name", func(t *testing.T) {
		c := &config.Config{ResourcePrefix: "sec"}
		got := c.GetSlackChannelsTableName()
		want := "sec-slack-channels"
		if got != want {
			t.Errorf("prefix-derived: got %q, want %q", got, want)
		}
	})

	t.Run("slack_channels_table_name from km-config.yaml is not ignored", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfig(t, dir, "slack_channels_table_name: myorg-slack-channels\n")
		orig, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(orig) })
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		got := cfg.GetSlackChannelsTableName()
		want := "myorg-slack-channels"
		if got != want {
			t.Errorf("km-config.yaml merge: got %q, want %q (merge-list entry missing?)", got, want)
		}
	})
}

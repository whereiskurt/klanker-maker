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

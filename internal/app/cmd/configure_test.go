package cmd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// kmConfigYAML reads and parses a km-config.yaml from dir.
func kmConfigYAML(t *testing.T, dir string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "km-config.yaml"))
	if err != nil {
		t.Fatalf("km-config.yaml not found in %s: %v", dir, err)
	}
	var out map[string]interface{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse km-config.yaml: %v", err)
	}
	return out
}

// runKMArgs runs the km binary with the given args and optional stdin text.
// Returns combined output and any error.
func runKMArgs(km, stdinText string, args ...string) (string, error) {
	c := exec.Command(km, args...)
	if stdinText != "" {
		c.Stdin = strings.NewReader(stdinText)
	}
	out, err := c.CombinedOutput()
	return string(out), err
}

// runKMArgsInDir runs the km binary from the given directory.
func runKMArgsInDir(km, dir, stdinText string, args ...string) (string, error) {
	c := exec.Command(km, args...)
	c.Dir = dir
	if stdinText != "" {
		c.Stdin = strings.NewReader(stdinText)
	}
	out, err := c.CombinedOutput()
	return string(out), err
}

// TestConfigureNonInteractiveWritesConfig verifies that --non-interactive writes a valid km-config.yaml.
func TestConfigureNonInteractiveWritesConfig(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	out, err := runKMArgs(km, "",
		"configure",
		"--non-interactive",
		"--output-dir", dir,
		"--domain", "test.example.com",
		"--management-account", "111111111111",
		"--terraform-account", "222222222222",
		"--application-account", "333333333333",
		"--sso-start-url", "https://sso.example.com/start",
		"--sso-region", "us-east-1",
		"--region", "us-east-1",
	)
	if err != nil {
		t.Fatalf("km configure --non-interactive: %v\noutput: %s", err, out)
	}

	cfg := kmConfigYAML(t, dir)

	if cfg["domain"] != "test.example.com" {
		t.Errorf("domain: got %v, want test.example.com", cfg["domain"])
	}

	accounts, ok := cfg["accounts"].(map[string]interface{})
	if !ok {
		t.Fatalf("accounts key missing or wrong type: %T", cfg["accounts"])
	}
	if accounts["management"] != "111111111111" {
		t.Errorf("accounts.management: got %v, want 111111111111", accounts["management"])
	}
	if accounts["terraform"] != "222222222222" {
		t.Errorf("accounts.terraform: got %v, want 222222222222", accounts["terraform"])
	}
	if accounts["application"] != "333333333333" {
		t.Errorf("accounts.application: got %v, want 333333333333", accounts["application"])
	}

	sso, ok := cfg["sso"].(map[string]interface{})
	if !ok {
		t.Fatalf("sso key missing or wrong type: %T", cfg["sso"])
	}
	if sso["start_url"] != "https://sso.example.com/start" {
		t.Errorf("sso.start_url: got %v", sso["start_url"])
	}
	if sso["region"] != "us-east-1" {
		t.Errorf("sso.region: got %v", sso["region"])
	}

	if cfg["region"] != "us-east-1" {
		t.Errorf("region: got %v, want us-east-1", cfg["region"])
	}
}

// TestConfigureTwoAccountTopology verifies that when terraform == application,
// DNS delegation guidance is NOT shown.
func TestConfigureTwoAccountTopology(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	out, err := runKMArgs(km, "",
		"configure",
		"--non-interactive",
		"--output-dir", dir,
		"--domain", "test.example.com",
		"--management-account", "111111111111",
		"--terraform-account", "333333333333", // same as application
		"--application-account", "333333333333",
		"--sso-start-url", "https://sso.example.com/start",
		"--sso-region", "us-east-1",
		"--region", "us-east-1",
	)
	if err != nil {
		t.Fatalf("km configure 2-account topology: %v\noutput: %s", err, out)
	}

	if strings.Contains(strings.ToLower(out), "dns delegation") {
		t.Errorf("2-account topology should NOT show DNS delegation guidance; output: %s", out)
	}
}

// TestConfigureThreeAccountTopology verifies that when management != application,
// DNS delegation guidance IS shown.
func TestConfigureThreeAccountTopology(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	out, err := runKMArgs(km, "",
		"configure",
		"--non-interactive",
		"--output-dir", dir,
		"--domain", "test.example.com",
		"--management-account", "111111111111",
		"--terraform-account", "222222222222",
		"--application-account", "333333333333",
		"--sso-start-url", "https://sso.example.com/start",
		"--sso-region", "us-east-1",
		"--region", "us-east-1",
	)
	if err != nil {
		t.Fatalf("km configure 3-account topology: %v\noutput: %s", err, out)
	}

	if !strings.Contains(strings.ToLower(out), "dns") {
		t.Errorf("3-account topology should show DNS delegation guidance; output: %s", out)
	}
}

// TestConfigureStateBucketFlag verifies that --state-bucket is written to km-config.yaml.
func TestConfigureStateBucketFlag(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	out, err := runKMArgs(km, "",
		"configure",
		"--non-interactive",
		"--output-dir", dir,
		"--domain", "test.example.com",
		"--management-account", "111111111111",
		"--terraform-account", "222222222222",
		"--application-account", "333333333333",
		"--sso-start-url", "https://sso.example.com/start",
		"--sso-region", "us-east-1",
		"--region", "us-east-1",
		"--state-bucket", "my-sandbox-state-bucket",
	)
	if err != nil {
		t.Fatalf("km configure --state-bucket: %v\noutput: %s", err, out)
	}

	cfg := kmConfigYAML(t, dir)
	if cfg["state_bucket"] != "my-sandbox-state-bucket" {
		t.Errorf("state_bucket: got %v, want my-sandbox-state-bucket", cfg["state_bucket"])
	}
}

// TestConfigureStateBucketOmittedWhenEmpty verifies that state_bucket is absent
// from km-config.yaml when --state-bucket is not provided (omitempty behavior).
func TestConfigureStateBucketOmittedWhenEmpty(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	out, err := runKMArgs(km, "",
		"configure",
		"--non-interactive",
		"--output-dir", dir,
		"--domain", "test.example.com",
		"--management-account", "111111111111",
		"--terraform-account", "222222222222",
		"--application-account", "333333333333",
		"--sso-start-url", "https://sso.example.com/start",
		"--sso-region", "us-east-1",
		"--region", "us-east-1",
		// No --state-bucket flag
	)
	if err != nil {
		t.Fatalf("km configure without --state-bucket: %v\noutput: %s", err, out)
	}

	cfg := kmConfigYAML(t, dir)
	if _, present := cfg["state_bucket"]; present {
		t.Errorf("state_bucket should be absent when not provided; got: %v", cfg["state_bucket"])
	}
}

// TestBootstrapDryRun verifies that km bootstrap --dry-run validates config and
// prints what would be provisioned without making any AWS calls.
func TestBootstrapDryRun(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	// Write a minimal km-config.yaml so bootstrap can validate it exists
	cfgContent := `domain: test.example.com
accounts:
  management: "111111111111"
  terraform: "222222222222"
  application: "333333333333"
sso:
  start_url: https://sso.example.com/start
  region: us-east-1
region: us-east-1
`
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(cfgContent), 0600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}

	out, err := runKMArgsInDir(km, dir, "", "bootstrap", "--dry-run")
	if err != nil {
		t.Fatalf("km bootstrap --dry-run: %v\noutput: %s", err, out)
	}

	lc := strings.ToLower(out)
	// Should describe what would be created
	if !strings.Contains(lc, "s3") && !strings.Contains(lc, "dynamodb") &&
		!strings.Contains(lc, "kms") && !strings.Contains(lc, "would") {
		t.Errorf("bootstrap --dry-run should describe what would be created; output: %s", out)
	}
}

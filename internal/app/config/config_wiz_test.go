package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadWizYAML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Chdir(dir)
	return Load()
}

func TestLoad_WizOutpostBlockDecoded(t *testing.T) {
	cfg, err := loadWizYAML(t, `
domain: example.com
resource_prefix: km
wiz:
  outpost:
    trust_role_arn: arn:aws:iam::444455556666:role/wiz-trust
    orchestrator_account_id: "111122223333"
    external_id: abcdefgh-abcd-abcd-abcd-abcdefghijkl
    role_suffix: km
    scanning:
      eks: true
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Wiz.Outpost == nil {
		t.Fatal("Wiz.Outpost nil (merge-list entry \"wiz\" missing?)")
	}
	if cfg.Wiz.Outpost.RoleSuffix != "km" {
		t.Errorf("RoleSuffix = %q", cfg.Wiz.Outpost.RoleSuffix)
	}
	if e := cfg.Wiz.Outpost.Scanning.EKS; e == nil || !*e {
		t.Errorf("Scanning.EKS = %v, want true", e)
	}
}

func TestLoad_NoWizBlock_NilOutpost(t *testing.T) {
	cfg, err := loadWizYAML(t, "domain: example.com\nresource_prefix: km\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Wiz.Outpost != nil {
		t.Errorf("Outpost = %+v, want nil", cfg.Wiz.Outpost)
	}
}

func TestLoad_WizOutpostInvalid_Errors(t *testing.T) {
	_, err := loadWizYAML(t, `
domain: example.com
resource_prefix: km
wiz:
  outpost:
    trust_role_arn: arn:aws:iam::444455556666:role/wiz-trust
    orchestrator_account_id: "111122223333"
    external_id: nope
    role_suffix: km
`)
	if err == nil || !strings.Contains(err.Error(), "wiz.outpost.external_id") {
		t.Fatalf("err = %v, want mention of wiz.outpost.external_id", err)
	}
}

// Regression guard for the project_config_key_merge_list footgun: a "wiz" key
// missing from the v2->v merge-list silently drops the whole block.
func TestLoad_WizIsInMergeList(t *testing.T) {
	cfg, err := loadWizYAML(t, `
domain: example.com
resource_prefix: km
wiz:
  outpost:
    trust_role_arn: arn:aws:iam::444455556666:role/wiz-trust
    orchestrator_account_id: "111122223333"
    external_id: abcdefgh-abcd-abcd-abcd-abcdefghijkl
    role_suffix: km
`)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Wiz.Outpost == nil {
		t.Fatal("Wiz.Outpost nil: add \"wiz\" to the v2->v merge-list in config.go (project_config_key_merge_list footgun)")
	}
}

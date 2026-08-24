package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// writeEnvShadowConfig drops a km-config.yaml into a temp dir and chdirs there so
// Load() finds it, mirroring the helper in config_launch_accounts_test.go.
func writeEnvShadowConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

const envShadowBase = "domain: example.com\nregion: us-east-1\nresource_prefix: km\n"

// KM_LAUNCH_ACCOUNTS is WRITE-ONLY (config → env → terragrunt), but viper's
// AutomaticEnv binds that JSON string over the map-typed launch_accounts key. Once
// the variable exists — and `km init` sets it in-process via os.Setenv — every
// later config.Load() failed outright with:
//
//	unmarshal launch_accounts: '' expected a map, got 'string'
//
// taking down km commands with nothing to do with cross-account launches.
func TestLoad_LaunchAccountsEnvDoesNotBreakLoad(t *testing.T) {
	writeEnvShadowConfig(t, envShadowBase)
	t.Setenv("KM_LAUNCH_ACCOUNTS", `{"gpuman":{"launcher_role_arn":"arn:aws:iam::481723467561:role/km-gpu-launcher","external_id_ssm":"/km/launch-accounts/gpuman/external-id","region":"us-east-1"}}`)

	if _, err := config.Load(); err != nil {
		t.Fatalf("Load with KM_LAUNCH_ACCOUNTS set must not error: %v", err)
	}
}

// The env payload is deliberately minimal — launcher_role_arn, external_id_ssm and
// region only. Reading it back would yield links with no account_id, no subnet list
// and no box role, which would silently relocate a cross-account sandbox. yaml stays
// authoritative even when the env var is present and names a DIFFERENT link.
func TestLoad_LaunchAccountsYAMLWinsOverEnv(t *testing.T) {
	writeEnvShadowConfig(t, envShadowBase+`launch_accounts:
  gpuman:
    account_id: "481723467561"
    launcher_role_arn: arn:aws:iam::481723467561:role/km-gpu-launcher
    box_role_arn: arn:aws:iam::481723467561:role/km-gpu-box
    external_id_ssm: /km/launch-accounts/gpuman/external-id
    region: us-east-1
    security_group_id: sg-0c4d21c992351eb71
    subnet_ids:
      - subnet-aaa
      - subnet-bbb
`)
	// A different link name, and a payload missing every field but three.
	t.Setenv("KM_LAUNCH_ACCOUNTS", `{"someotherlink":{"launcher_role_arn":"arn:aws:iam::999999999999:role/evil","region":"us-west-2"}}`)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.GetLaunchAccount("someotherlink"); ok {
		t.Error("the env payload must never become a config source — it is write-only")
	}
	link, ok := cfg.GetLaunchAccount("gpuman")
	if !ok {
		t.Fatalf("yaml link must survive the env shadow; got %v", cfg.LaunchAccounts)
	}
	// The fields the env payload does NOT carry are exactly the ones that matter
	// for placement — prove they survived.
	if link.AccountID != "481723467561" {
		t.Errorf("AccountID = %q, want 481723467561", link.AccountID)
	}
	if link.SecurityGroupID != "sg-0c4d21c992351eb71" {
		t.Errorf("SecurityGroupID = %q", link.SecurityGroupID)
	}
	if len(link.SubnetIDs) != 2 {
		t.Errorf("SubnetIDs = %v, want 2 entries", link.SubnetIDs)
	}
	if link.BoxRoleARN != "arn:aws:iam::481723467561:role/km-gpu-box" {
		t.Errorf("BoxRoleARN = %q", link.BoxRoleARN)
	}
}

// A dormant install with the variable exported empty must load cleanly.
func TestLoad_LaunchAccountsEmptyEnvIsDormant(t *testing.T) {
	writeEnvShadowConfig(t, envShadowBase)
	t.Setenv("KM_LAUNCH_ACCOUNTS", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load with empty KM_LAUNCH_ACCOUNTS must not error: %v", err)
	}
	if len(cfg.LaunchAccounts) != 0 {
		t.Errorf("expected no links, got %v", cfg.LaunchAccounts)
	}
}

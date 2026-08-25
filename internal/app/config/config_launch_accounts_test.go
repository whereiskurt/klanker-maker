// Package config_test provides launch_accounts config round-trip tests.
// Phase 126 Plan 01 Task 1: LaunchAccountConfig struct + merge-list registration +
// UnmarshalKey load + GetLaunchAccount(s)() getters.
//
// These mirror config_limits_test.go — the limits: block is the structural template
// for the launch_accounts: block. The merge-list regression test is the load-bearing
// one: without "launch_accounts" in the v2→v merge-loop the whole launch_accounts:
// block is silently dropped (project_config_key_merge_list footgun).
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// writeKMConfigLaunchAccounts writes a km-config.yaml to dir. Self-contained helper
// mirroring writeKMConfigLimits so this file reads independently.
func writeKMConfigLaunchAccounts(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}
}

// chdirLaunchAccounts changes the working directory for the duration of the test.
// Uses a distinct name so it doesn't shadow chdirLimits/chdirChecks in a parallel build.
func chdirLaunchAccounts(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// TestLaunchAccountsConfigLoaded is the primary gate test for Phase 126 Plan 01 Task 1.
//
// Test 1 (populated) is ALSO the merge-list regression guard: deleting the
// "launch_accounts" entry from the v2→v merge-loop in config.go makes this test
// fail, because cfg.LaunchAccounts stays empty even though the yaml has content.
func TestLaunchAccountsConfigLoaded(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigLaunchAccounts(t, dir, `
domain: example.com
region: us-east-1
launch_accounts:
  mgmt-gpu:
    account_id: "111122223333"
    launcher_role_arn: "arn:aws:iam::111122223333:role/km-launcher"
    box_role_arn: "arn:aws:iam::111122223333:role/km-box"
    external_id_ssm: "/km/launch-accounts/mgmt-gpu/external-id"
    region: us-west-2
    subnet_ids: ["subnet-aaa", "subnet-bbb"]
    availability_zones: ["us-west-2a", "us-west-2b"]
    security_group_id: sg-0123456789
    results_bucket: km-mgmt-gpu-results
    efs_id: fs-0123456789
    instance_types: ["g6e.12xlarge", "g6e.48xlarge"]
    state_bucket: km-mgmt-gpu-tfstate
    lock_table: km-mgmt-gpu-tflock
    state_key: launch-accounts/mgmt-gpu/terraform.tfstate
`)
		chdirLaunchAccounts(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		// Merge-list guard: this is the primary regression signal (Test 1).
		link, ok := cfg.LaunchAccounts["mgmt-gpu"]
		if !ok {
			t.Fatal("MERGE-LIST GUARD: cfg.LaunchAccounts[\"mgmt-gpu\"] missing; expected non-nil from yaml load (merge-loop must include \"launch_accounts\")")
		}

		if link.AccountID != "111122223333" {
			t.Errorf("AccountID: got %q, want %q", link.AccountID, "111122223333")
		}
		if link.LauncherRoleARN != "arn:aws:iam::111122223333:role/km-launcher" {
			t.Errorf("LauncherRoleARN: got %q", link.LauncherRoleARN)
		}
		if link.BoxRoleARN != "arn:aws:iam::111122223333:role/km-box" {
			t.Errorf("BoxRoleARN: got %q", link.BoxRoleARN)
		}
		if link.ExternalIDSSM != "/km/launch-accounts/mgmt-gpu/external-id" {
			t.Errorf("ExternalIDSSM: got %q", link.ExternalIDSSM)
		}
		if link.Region != "us-west-2" {
			t.Errorf("Region: got %q, want %q", link.Region, "us-west-2")
		}
		if link.SecurityGroupID != "sg-0123456789" {
			t.Errorf("SecurityGroupID: got %q", link.SecurityGroupID)
		}
		if link.ResultsBucket != "km-mgmt-gpu-results" {
			t.Errorf("ResultsBucket: got %q", link.ResultsBucket)
		}
		if link.EFSID != "fs-0123456789" {
			t.Errorf("EFSID: got %q", link.EFSID)
		}

		// Test 4: subnet_ids decodes as a multi-element list (guards C5 — a
		// single-subnet link collapses the Phase-124 AZ sweep to one attempt).
		if len(link.SubnetIDs) != 2 || link.SubnetIDs[0] != "subnet-aaa" || link.SubnetIDs[1] != "subnet-bbb" {
			t.Errorf("SubnetIDs: got %v, want [subnet-aaa subnet-bbb]", link.SubnetIDs)
		}
		if len(link.AvailabilityZones) != 2 || link.AvailabilityZones[0] != "us-west-2a" || link.AvailabilityZones[1] != "us-west-2b" {
			t.Errorf("AvailabilityZones: got %v, want [us-west-2a us-west-2b]", link.AvailabilityZones)
		}
		if len(link.InstanceTypes) != 2 || link.InstanceTypes[0] != "g6e.12xlarge" {
			t.Errorf("InstanceTypes: got %v", link.InstanceTypes)
		}

		// Test 3b: state_bucket, lock_table and state_key round-trip through the
		// config load, so teardown can rediscover the remote backend.
		if link.StateBucket != "km-mgmt-gpu-tfstate" {
			t.Errorf("StateBucket: got %q, want %q", link.StateBucket, "km-mgmt-gpu-tfstate")
		}
		if link.LockTable != "km-mgmt-gpu-tflock" {
			t.Errorf("LockTable: got %q, want %q", link.LockTable, "km-mgmt-gpu-tflock")
		}
		if link.StateKey != "launch-accounts/mgmt-gpu/terraform.tfstate" {
			t.Errorf("StateKey: got %q, want %q", link.StateKey, "launch-accounts/mgmt-gpu/terraform.tfstate")
		}
	})

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigLaunchAccounts(t, dir, `
domain: example.com
region: us-east-1
`)
		chdirLaunchAccounts(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error when launch_accounts: absent: %v", err)
		}

		// Test 2: absent launch_accounts: key loads with a non-nil, zero-length map.
		if cfg.LaunchAccounts == nil {
			t.Fatal("LaunchAccounts: got nil, want non-nil zero-length map (key absent => dormant)")
		}
		if len(cfg.LaunchAccounts) != 0 {
			t.Errorf("LaunchAccounts: got %d entries, want 0", len(cfg.LaunchAccounts))
		}
	})
}

// TestGetLaunchAccount verifies the GetLaunchAccount/GetLaunchAccounts getters
// (Test 3): a known name returns the record with ok=true; an unknown name returns
// ok=false. Also verifies GetLaunchAccounts() tolerates the dormant zero-value case.
func TestGetLaunchAccount(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigLaunchAccounts(t, dir, `
domain: example.com
region: us-east-1
launch_accounts:
  mgmt-gpu:
    account_id: "111122223333"
    launcher_role_arn: "arn:aws:iam::111122223333:role/km-launcher"
    box_role_arn: "arn:aws:iam::111122223333:role/km-box"
    external_id_ssm: "/km/launch-accounts/mgmt-gpu/external-id"
    region: us-west-2
    subnet_ids: ["subnet-aaa"]
    security_group_id: sg-0123456789
    results_bucket: km-mgmt-gpu-results
    state_bucket: km-mgmt-gpu-tfstate
    lock_table: km-mgmt-gpu-tflock
    state_key: launch-accounts/mgmt-gpu/terraform.tfstate
`)
		chdirLaunchAccounts(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		link, ok := cfg.GetLaunchAccount("mgmt-gpu")
		if !ok {
			t.Fatal("GetLaunchAccount(\"mgmt-gpu\"): got ok=false, want true")
		}
		if link.AccountID != "111122223333" {
			t.Errorf("GetLaunchAccount(\"mgmt-gpu\").AccountID: got %q", link.AccountID)
		}

		if _, ok := cfg.GetLaunchAccount("does-not-exist"); ok {
			t.Error("GetLaunchAccount(\"does-not-exist\"): got ok=true, want false")
		}

		all := cfg.GetLaunchAccounts()
		if len(all) != 1 {
			t.Errorf("GetLaunchAccounts(): got %d entries, want 1", len(all))
		}
	})

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigLaunchAccounts(t, dir, `
domain: example.com
region: us-east-1
`)
		chdirLaunchAccounts(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		if _, ok := cfg.GetLaunchAccount("mgmt-gpu"); ok {
			t.Error("GetLaunchAccount(\"mgmt-gpu\"): got ok=true against dormant config, want false")
		}

		all := cfg.GetLaunchAccounts()
		if all == nil {
			t.Fatal("GetLaunchAccounts(): got nil, want non-nil empty map")
		}
		if len(all) != 0 {
			t.Errorf("GetLaunchAccounts(): got %d entries, want 0", len(all))
		}
	})
}

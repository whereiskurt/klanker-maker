// PrivateSubnetCount: NetworkConfig field + merge-list registration + tri-state load.
//
// The merge-list regression test below is the load-bearing one. A new nested
// km-config.yaml key must be added to the v2→v merge-list in Load(), not just to
// the struct — otherwise the file value is silently ignored and every unit test
// that constructs a Config directly still passes (project_config_key_merge_list).
package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// loadFromYAML writes a km-config.yaml into a temp dir, chdirs there, and loads it.
// Mirrors the TestLoadNetworkNATGateway_* pattern — Load() discovers the file from
// the working directory rather than taking a path.
func loadFromYAML(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	writeKMConfig(t, dir, body)
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return cfg
}

// TestLoadPrivateSubnetCount_MergeListGuard is the footgun guard: a config that
// sets ONLY network.private_subnet_count (no other network.* key) must populate
// the field. If "network.private_subnet_count" is missing from the v2→v
// merge-list, cfg.Network.PrivateSubnetCount stays nil and the yaml is silently
// ignored.
func TestLoadPrivateSubnetCount_MergeListGuard(t *testing.T) {
	cfg := loadFromYAML(t, `
domain: example.com
region: us-east-1
network:
    private_subnet_count: 1
`)

	if cfg.Network.PrivateSubnetCount == nil {
		t.Fatal("Network.PrivateSubnetCount is nil; merge-list footgun " +
			"(project_config_key_merge_list) — ensure \"network.private_subnet_count\" " +
			"is in the v2→v merge-loop in config.go")
	}
	if got := *cfg.Network.PrivateSubnetCount; got != 1 {
		t.Errorf("PrivateSubnetCount = %d, want 1", got)
	}
	if got := cfg.GetPrivateSubnetCount(); got != 1 {
		t.Errorf("GetPrivateSubnetCount() = %d, want 1", got)
	}
}

// TestLoadPrivateSubnetCount_CoexistsWithNATGateway proves the two network.* keys
// do not shadow each other in the merge-list (each nested key needs its own entry).
func TestLoadPrivateSubnetCount_CoexistsWithNATGateway(t *testing.T) {
	cfg := loadFromYAML(t, `
domain: example.com
region: us-east-1
network:
    nat_gateway: true
    private_subnet_count: 2
`)

	if cfg.Network.NATGateway == nil || !*cfg.Network.NATGateway {
		t.Error("Network.NATGateway did not survive alongside private_subnet_count")
	}
	if cfg.Network.PrivateSubnetCount == nil || *cfg.Network.PrivateSubnetCount != 2 {
		t.Errorf("PrivateSubnetCount = %v, want 2", cfg.Network.PrivateSubnetCount)
	}
}

// TestLoadPrivateSubnetCount_AbsentIsDormant pins the dormancy contract: an absent
// key leaves the pointer nil so no KM_PRIVATE_SUBNET_COUNT is exported and the
// terragrunt get_env() default (the full CIDR list length) applies.
func TestLoadPrivateSubnetCount_AbsentIsDormant(t *testing.T) {
	cfg := loadFromYAML(t, `
domain: example.com
region: us-east-1
`)

	if cfg.Network.PrivateSubnetCount != nil {
		t.Errorf("PrivateSubnetCount = %d, want nil for an absent key",
			*cfg.Network.PrivateSubnetCount)
	}
	if got := cfg.GetPrivateSubnetCount(); got != config.DefaultPrivateSubnetCount {
		t.Errorf("GetPrivateSubnetCount() = %d, want the %d default",
			got, config.DefaultPrivateSubnetCount)
	}
}

// TestGetPrivateSubnetCount_NilSafe covers the getter's three states, including a
// nil receiver — downstream callers must not each re-implement the nil check.
func TestGetPrivateSubnetCount_NilSafe(t *testing.T) {
	two := 2

	tests := []struct {
		name string
		cfg  *config.Config
		want int
	}{
		{"nil receiver", nil, config.DefaultPrivateSubnetCount},
		{"nil pointer (key absent)", &config.Config{}, config.DefaultPrivateSubnetCount},
		{"explicit value", &config.Config{
			Network: config.NetworkConfig{PrivateSubnetCount: &two},
		}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.GetPrivateSubnetCount(); got != tc.want {
				t.Errorf("GetPrivateSubnetCount() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestValidatePrivateSubnetCount_Bounds guards the range. slice() in the network
// terragrunt.hcl fails deep inside terraform on an out-of-range bound, so the
// error must surface early and name the km-config.yaml key.
func TestValidatePrivateSubnetCount_Bounds(t *testing.T) {
	tests := []struct {
		name    string
		count   int
		wantErr bool
	}{
		{"zero is rejected", 0, true},
		{"negative is rejected", -1, true},
		{"one is the documented minimum", 1, false},
		{"max equals the CIDR list length", config.MaxPrivateSubnetCount, false},
		{"above the CIDR list length is rejected", config.MaxPrivateSubnetCount + 1, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidatePrivateSubnetCount(tc.count)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidatePrivateSubnetCount(%d) = nil, want error", tc.count)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidatePrivateSubnetCount(%d) = %v, want nil", tc.count, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "network.private_subnet_count") {
				// The operator has to know which yaml key to edit.
				t.Errorf("error %q does not name network.private_subnet_count", err.Error())
			}
		})
	}
}

// Package config_test provides the Clusters field round-trip test.
// Plan 80-01 scaffolded this file with a t.Skip; Plan 80-04 adds ClusterConfig +
// Config.Clusters to config.go and wires viper so the assertions below pass.
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestClustersField writes a temp km-config.yaml containing a clusters: list, loads it
// via config.Load(), and asserts that the ClusterConfig fields round-trip correctly.
//
// YAML the test exercises:
//
//	clusters:
//	  - name: dev-use1-0
//	    oidc_provider_arn: arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE
//	    namespace: "*"
//	    service_account: km
//	    role_arn: arn:aws:iam::850919910932:role/km-cluster-dev-use1-0
func TestClustersField(t *testing.T) {
	t.Run("single cluster entry round-trips", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "km-config.yaml")

		// Write km-config.yaml with a clusters: list entry.
		// config.Load() has no required-field validation — domain + clusters is sufficient.
		content := `domain: example.com
clusters:
  - name: dev-use1-0
    oidc_provider_arn: arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE
    namespace: "*"
    service_account: km
    role_arn: arn:aws:iam::850919910932:role/km-cluster-dev-use1-0
`
		if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
			t.Fatalf("write km-config.yaml: %v", err)
		}

		// Change to dir so config.Load() picks up km-config.yaml (mirrors config_test.go pattern).
		orig, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(orig) })
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir: %v", err)
		}

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("config.Load() error: %v", err)
		}

		if len(cfg.Clusters) != 1 {
			t.Fatalf("expected 1 cluster entry, got %d", len(cfg.Clusters))
		}
		if cfg.Clusters[0].Name != "dev-use1-0" {
			t.Errorf("Name: got %q, want %q", cfg.Clusters[0].Name, "dev-use1-0")
		}
		if cfg.Clusters[0].OIDCProviderARN != "arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE" {
			t.Errorf("OIDCProviderARN: got %q", cfg.Clusters[0].OIDCProviderARN)
		}
		if cfg.Clusters[0].Namespace != "*" {
			t.Errorf("Namespace: got %q, want *", cfg.Clusters[0].Namespace)
		}
		if cfg.Clusters[0].ServiceAccount != "km" {
			t.Errorf("ServiceAccount: got %q, want km", cfg.Clusters[0].ServiceAccount)
		}
		if cfg.Clusters[0].RoleARN != "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0" {
			t.Errorf("RoleARN: got %q", cfg.Clusters[0].RoleARN)
		}
	})

	t.Run("absent clusters key yields empty slice with no error", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "km-config.yaml")

		// Write km-config.yaml WITHOUT a clusters: key at all.
		content := `domain: example.com
`
		if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
			t.Fatalf("write km-config.yaml: %v", err)
		}

		orig, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(orig) })
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir: %v", err)
		}

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("config.Load() returned unexpected error when clusters: absent: %v", err)
		}
		if len(cfg.Clusters) != 0 {
			t.Errorf("expected empty Clusters slice when key absent, got %d entries", len(cfg.Clusters))
		}
	})
}

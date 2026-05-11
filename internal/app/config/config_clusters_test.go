// Wave 0 scaffold — see Plan 80-04 for implementation.
// Package config_test provides the Clusters field round-trip test skeleton.
// Plan 80-04 adds ClusterConfig + Clusters []ClusterConfig to Config and wires viper.
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
// YAML the test will exercise (Plan 80-04 must support this exact structure):
//
//	clusters:
//	  - name: dev-use1-0
//	    oidc_provider_arn: arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE
//	    namespace: "*"
//	    service_account: km
//	    role_arn: arn:aws:iam::850919910932:role/km-cluster-dev-use1-0
//
// Expected assertions (Plan 80-04 will unskip and implement):
//   - cfg.Clusters has length 1
//   - cfg.Clusters[0].Name == "dev-use1-0"
//   - cfg.Clusters[0].OIDCProviderARN == "arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE"
//   - cfg.Clusters[0].Namespace == "*"
//   - cfg.Clusters[0].ServiceAccount == "km"
//   - cfg.Clusters[0].RoleARN == "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0"
func TestClustersField(t *testing.T) {
	t.Skip("pending — Plan 80-04 adds Clusters field + viper merge key")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "km-config.yaml")

	// Write km-config.yaml with a clusters: list entry
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

	// Change to dir so config.Load() picks up km-config.yaml (mirrors config_test.go pattern)
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}

	// Plan 80-04 adds Clusters []ClusterConfig to Config; at Wave 0 this field does not exist.
	// Unskip these assertions when Plan 80-04 ships:
	//
	//   if len(cfg.Clusters) != 1 {
	//       t.Fatalf("expected 1 cluster entry, got %d", len(cfg.Clusters))
	//   }
	//   if cfg.Clusters[0].Name != "dev-use1-0" {
	//       t.Errorf("Name: got %q, want %q", cfg.Clusters[0].Name, "dev-use1-0")
	//   }
	//   if cfg.Clusters[0].OIDCProviderARN != "arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE" {
	//       t.Errorf("OIDCProviderARN: got %q", cfg.Clusters[0].OIDCProviderARN)
	//   }
	//   if cfg.Clusters[0].Namespace != "*" {
	//       t.Errorf("Namespace: got %q, want *", cfg.Clusters[0].Namespace)
	//   }
	//   if cfg.Clusters[0].ServiceAccount != "km" {
	//       t.Errorf("ServiceAccount: got %q, want km", cfg.Clusters[0].ServiceAccount)
	//   }
	//   if cfg.Clusters[0].RoleARN != "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0" {
	//       t.Errorf("RoleARN: got %q", cfg.Clusters[0].RoleARN)
	//   }

	// Silence unused-variable error until the skip is lifted
	_ = cfg
}

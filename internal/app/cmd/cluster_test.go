// Wave 0 scaffold — see Plan 80-05 for implementations
// Package cmd_test provides test skeletons for the km cluster subcommand.
// These skeletons define the contract that Plan 80-04 (config) and Plan 80-05 (CLI) must satisfy.
package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// mockClusterRunner is a test double for the ClusterRunner interface that Plan 80-05 will declare.
// It records all method invocations so tests can assert which operations were performed.
// Plan 80-05 will wire this via the newClusterRunner and persistClustersConfigFunc seams.
//
// Struct definition:
//
//	type ClusterRunner interface {
//	    Plan(ctx context.Context, dir string) error
//	    Apply(ctx context.Context, dir string) error
//	    Destroy(ctx context.Context, dir string) error
//	    Reconfigure(ctx context.Context, dir string) error
//	    Output(ctx context.Context, dir string) (map[string]interface{}, error)
//	}
type mockClusterRunner struct {
	PlanCalled     bool
	Applied        []string
	Destroyed      []string
	Reconfigured   []string
	OutputCalled   bool
	OutputResult   map[string]interface{}
	ApplyErr       error
	PlanErr        error
	DestroyErr     error
	ReconfigureErr error
	OutputErr      error
}

func (m *mockClusterRunner) Plan(_ context.Context, _ string) error {
	m.PlanCalled = true
	return m.PlanErr
}

func (m *mockClusterRunner) Apply(_ context.Context, dir string) error {
	if m.ApplyErr != nil {
		return m.ApplyErr
	}
	m.Applied = append(m.Applied, dir)
	return nil
}

func (m *mockClusterRunner) Destroy(_ context.Context, dir string) error {
	if m.DestroyErr != nil {
		return m.DestroyErr
	}
	m.Destroyed = append(m.Destroyed, dir)
	return nil
}

func (m *mockClusterRunner) Reconfigure(_ context.Context, dir string) error {
	if m.ReconfigureErr != nil {
		return m.ReconfigureErr
	}
	m.Reconfigured = append(m.Reconfigured, dir)
	return nil
}

func (m *mockClusterRunner) Output(_ context.Context, _ string) (map[string]interface{}, error) {
	m.OutputCalled = true
	if m.OutputErr != nil {
		return nil, m.OutputErr
	}
	if m.OutputResult != nil {
		return m.OutputResult, nil
	}
	return map[string]interface{}{
		"role_arn": map[string]interface{}{"value": "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0"},
	}, nil
}

// TestGenerateClusterHCL verifies that generateClusterHCL substitutes all four
// placeholders in the HCL template (CLUSTER_NAME, OIDC_PROVIDER_ARN, NAMESPACE,
// SERVICE_ACCOUNT) so no literal brace-wrapped token remains in the output.
//
// Plan 80-05 will expose cmd.GenerateClusterHCL for this test to call.
//
// Expected assertions (Plan 80-05 will unskip and implement):
//   - Call cmd.GenerateClusterHCL("dev-use1-0", "arn:aws:iam::123:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE", "*", "km")
//   - Assert output does not contain "{CLUSTER_NAME}", "{OIDC_PROVIDER_ARN}", "{NAMESPACE}", "{SERVICE_ACCOUNT}"
//   - Assert output contains "dev-use1-0", "arn:aws:iam::123:oidc-provider/", "*", "km"
func TestGenerateClusterHCL(t *testing.T) {
	t.Skip("pending implementation — Plan 80-04/80-05")

	// Placeholder reference to avoid "imported and not used" for strings
	_ = strings.Contains
}

// TestClusterAdd verifies the km cluster add command wires the ClusterRunner correctly.
//
// Plan 80-05 will expose newClusterRunner and persistClustersConfigFunc package-level seams.
//
// Expected assertions (Plan 80-05 will unskip and implement):
//   - With dryRun=false: mockClusterRunner.Apply was called with the cluster stack dir;
//     Output() was called; role ARN appears in cfg.Clusters[0].RoleARN
//   - With dryRun=true: mockClusterRunner.Plan was called (NOT Apply);
//     Output() was NOT called; cfg.Clusters remains unchanged
func TestClusterAdd(t *testing.T) {
	t.Skip("pending implementation — Plan 80-04/80-05")

	repoRoot := t.TempDir()
	mock := &mockClusterRunner{
		OutputResult: map[string]interface{}{
			"role_arn": map[string]interface{}{"value": "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0"},
		},
	}
	cfg := &config.Config{}

	_ = mock
	_ = cfg
	_ = repoRoot

	// Non-dry-run path injection (Plan 80-05):
	//   orig := cmd.NewClusterRunnerFunc
	//   cmd.NewClusterRunnerFunc = func(_, _ string) cmd.ClusterRunner { return mock }
	//   t.Cleanup(func() { cmd.NewClusterRunnerFunc = orig })
	//   err := cmd.RunClusterAdd(cfg, "dev-use1-0", oidcARN, "*", "km", false, repoRoot)
	//   if err != nil { t.Fatalf("RunClusterAdd: %v", err) }
	//
	// Assertions:
	//   len(mock.Applied) == 1
	//   strings.HasSuffix(mock.Applied[0], "km-cluster-dev-use1-0")
	//   mock.OutputCalled == true
	//   len(cfg.Clusters) == 1
	//   cfg.Clusters[0].RoleARN == "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0"
	//
	// Dry-run path assertions:
	//   mock.PlanCalled == true
	//   len(mock.Applied) == 0
	//   mock.OutputCalled == false
	//   len(cfg.Clusters) == 0
	fmt.Println("placeholder to satisfy import — removed at unskip time")
}

// TestClusterList verifies the km cluster list command prints all registered clusters.
//
// Plan 80-05 will expose cmd.NewClusterCmd for building the cobra command.
//
// Expected assertions (Plan 80-05 will unskip and implement):
//   - Seed cfg.Clusters with two entries (dev-use1-0, prod-use1-0)
//   - Invoke list cobra command with bytes.Buffer capture
//   - Assert both NAME values appear in output
func TestClusterList(t *testing.T) {
	t.Skip("pending implementation — Plan 80-04/80-05")

	cfg := &config.Config{}
	_ = cfg

	var buf bytes.Buffer
	_ = buf

	// Plan 80-05 injection:
	//   cfg.Clusters = []config.ClusterConfig{
	//       {Name: "dev-use1-0",  RoleARN: "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0"},
	//       {Name: "prod-use1-0", RoleARN: "arn:aws:iam::850919910932:role/km-cluster-prod-use1-0"},
	//   }
	//   listCmd := cmd.NewClusterCmd(cfg)
	//   listCmd.SetOut(&buf)
	//   listCmd.SetArgs([]string{"list"})
	//   listCmd.Execute()
	//
	// Assertions:
	//   strings.Contains(buf.String(), "dev-use1-0")
	//   strings.Contains(buf.String(), "prod-use1-0")
}

// TestClusterRm verifies the km cluster rm command removes the entry from cfg.Clusters
// and runs Destroy against the cluster stack directory.
//
// Plan 80-05 will expose newClusterRunner seam and RunClusterRm.
//
// Expected assertions (Plan 80-05 will unskip and implement):
//   - Seed cfg.Clusters with one entry ("dev-use1-0")
//   - Inject mockClusterRunner via newClusterRunner seam
//   - Run "km cluster rm dev-use1-0"
//   - Assert len(mock.Destroyed) == 1 AND mock.Destroyed[0] ends with "km-cluster-dev-use1-0"
//   - Assert len(cfg.Clusters) == 0
func TestClusterRm(t *testing.T) {
	t.Skip("pending implementation — Plan 80-04/80-05")

	mock := &mockClusterRunner{}
	cfg := &config.Config{}
	_ = mock
	_ = cfg

	// Plan 80-05 injection:
	//   orig := cmd.NewClusterRunnerFunc
	//   cmd.NewClusterRunnerFunc = func(_, _ string) cmd.ClusterRunner { return mock }
	//   t.Cleanup(func() { cmd.NewClusterRunnerFunc = orig })
	//   cfg.Clusters = []config.ClusterConfig{{Name: "dev-use1-0"}}
	//   err := cmd.RunClusterRm(cfg, "dev-use1-0", repoRoot)
	//   if err != nil { t.Fatalf("RunClusterRm: %v", err) }
	//
	// Assertions:
	//   len(mock.Destroyed) == 1
	//   strings.HasSuffix(mock.Destroyed[0], "km-cluster-dev-use1-0")
	//   len(cfg.Clusters) == 0
}

// TestPersistClusters verifies persistClustersConfig merges clusters into an existing
// km-config.yaml without clobbering other top-level keys.
//
// Plan 80-05 will expose cmd.PersistClustersConfig (or via seam).
//
// Expected assertions (Plan 80-05 will unskip and implement):
//   - Write temp km-config.yaml with domain: example.com
//   - Call persistClustersConfig with two ClusterConfig entries
//   - Re-read file; assert domain: still present AND clusters: list has two entries
func TestPersistClusters(t *testing.T) {
	t.Skip("pending implementation — Plan 80-04/80-05")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "km-config.yaml")

	// Seed with one existing key
	if err := os.WriteFile(cfgPath, []byte("domain: example.com\n"), 0600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}
	_ = cfgPath

	// Plan 80-05 injection:
	//   clusters := []config.ClusterConfig{
	//       {Name: "dev-use1-0",  OIDCProviderARN: "arn:...", Namespace: "*", ServiceAccount: "km"},
	//       {Name: "prod-use1-0", OIDCProviderARN: "arn:...", Namespace: "*", ServiceAccount: "km"},
	//   }
	//   err := cmd.PersistClustersConfig(cfgPath, clusters)
	//   if err != nil { t.Fatalf("PersistClustersConfig: %v", err) }
	//   content, _ := os.ReadFile(cfgPath)
	//
	// Assertions:
	//   strings.Contains(string(content), "domain: example.com")   — key preserved
	//   strings.Contains(string(content), "clusters:")             — clusters section written
	//   count of "name:" in content >= 2
}

// TestClusterAddPersistFailure verifies the rollback contract: when persistClustersConfigFunc
// returns an error AFTER Apply succeeds, runClusterAdd must:
//
//  1. return a non-nil error
//  2. the error message contains "km cluster rm" (instructs operator on remediation)
//  3. mockClusterRunner.Destroyed remains empty (no auto-destroy — cluster may be partial)
//
// Injection contract (Plan 80-05 wires these package-level seams):
//   - Inject newClusterRunner returning a mockClusterRunner whose Apply returns nil
//     and Output returns role_arn "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0"
//   - Inject persistClustersConfigFunc returning fmt.Errorf("disk full") via t.Cleanup
//   - Call runClusterAdd with dryRun:false
//   - Assert err != nil AND strings.Contains(err.Error(), "km cluster rm") AND len(mock.Destroyed) == 0
func TestClusterAddPersistFailure(t *testing.T) {
	t.Skip("pending — Plan 80-05 wires rollback path")

	mock := &mockClusterRunner{
		OutputResult: map[string]interface{}{
			"role_arn": map[string]interface{}{"value": "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0"},
		},
	}
	_ = mock

	// Plan 80-05 implementation contract:
	//
	// 1. Inject newClusterRunner returning mock via t.Cleanup:
	//    orig := cmd.NewClusterRunnerFunc
	//    cmd.NewClusterRunnerFunc = func(_, _ string) cmd.ClusterRunner { return mock }
	//    t.Cleanup(func() { cmd.NewClusterRunnerFunc = orig })
	//
	// 2. Inject persistClustersConfigFunc returning an error:
	//    origP := cmd.PersistClustersConfigFunc
	//    cmd.PersistClustersConfigFunc = func(_ []config.ClusterConfig) error {
	//        return fmt.Errorf("disk full")
	//    }
	//    t.Cleanup(func() { cmd.PersistClustersConfigFunc = origP })
	//
	// 3. Call runClusterAdd(cfg, "dev-use1-0", oidcARN, "*", "km", false, repoRoot)
	//
	// 4. Assert:
	//    if err == nil { t.Fatal("expected error from persist failure, got nil") }
	//    if !strings.Contains(err.Error(), "km cluster rm") {
	//        t.Errorf("error should mention 'km cluster rm'; got: %v", err)
	//    }
	//    if len(mock.Destroyed) != 0 {
	//        t.Errorf("expected no auto-destroy on persist failure; Destroyed=%v", mock.Destroyed)
	//    }
}

// Package cmd_test provides tests for the km cluster subcommand.
package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// mockClusterRunner is a test double for the ClusterRunner interface declared in cluster.go.
// It records all method invocations so tests can assert which operations were performed.
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

// mockOidcLister is a test double for the OidcProviderLister interface in cluster.go.
// Shape mirrors mockClusterRunner above.
type mockOidcLister struct {
	Providers []iamtypes.OpenIDConnectProviderListEntry
	Err       error
}

func (m *mockOidcLister) ListOpenIDConnectProviders(
	_ context.Context,
	_ *iam.ListOpenIDConnectProvidersInput,
	_ ...func(*iam.Options),
) (*iam.ListOpenIDConnectProvidersOutput, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return &iam.ListOpenIDConnectProvidersOutput{
		OpenIDConnectProviderList: m.Providers,
	}, nil
}

// makeTestRepoRoot creates a minimal temp directory tree that satisfies:
//   - findRepoRoot()-style discovery (go.mod or CLAUDE.md anchor)
//   - km-config.yaml at root
//   - infra/live/{regionLabel}/ directory
//   - WriteFile for region.hcl
func makeTestRepoRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Anchor file so region.hcl writes don't fail
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# anchor\n"), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}
	// Valid km-config.yaml
	kmcfg := "domain: test.example.com\nclusters: []\n"
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(kmcfg), 0o600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}
	// Create infra/live/use1 so region.hcl writes have a parent
	if err := os.MkdirAll(filepath.Join(dir, "infra", "live", "use1"), 0o755); err != nil {
		t.Fatalf("mkdir infra/live/use1: %v", err)
	}
	return dir
}

// TestGenerateClusterHCL verifies that GenerateClusterHCL substitutes all four
// {PLACEHOLDER} markers and leaves HCL ${...} interpolations intact.
func TestGenerateClusterHCL(t *testing.T) {
	clusterName := "dev-use1-0"
	oidcARN := "arn:aws:iam::123:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE"
	namespace := "*"
	serviceAccount := "km"

	out := cmd.GenerateClusterHCL(clusterName, oidcARN, namespace, serviceAccount)

	// All four placeholders must be replaced.
	for _, placeholder := range []string{"{CLUSTER_NAME}", "{OIDC_PROVIDER_ARN}", "{NAMESPACE}", "{SERVICE_ACCOUNT_NAME}"} {
		if strings.Contains(out, placeholder) {
			t.Errorf("output still contains placeholder %q", placeholder)
		}
	}

	// Substituted values must appear.
	if !strings.Contains(out, clusterName) {
		t.Errorf("output missing cluster name %q", clusterName)
	}
	if !strings.Contains(out, oidcARN) {
		t.Errorf("output missing oidc ARN")
	}
	if !strings.Contains(out, namespace) {
		t.Errorf("output missing namespace %q", namespace)
	}
	if !strings.Contains(out, serviceAccount) {
		t.Errorf("output missing service account %q", serviceAccount)
	}

	// HCL ${...} interpolations must remain literal (Go replacement must not touch them).
	if !strings.Contains(out, "${local.repo_root}") {
		t.Errorf("HCL interpolation ${local.repo_root} was incorrectly modified; got:\n%s", out)
	}

	// Double-slash source pattern must be present.
	if !strings.Contains(out, "infra/modules//cluster-irsa/v1.0.0") {
		t.Errorf("double-slash // source pattern missing in output")
	}
}

// TestClusterAdd verifies the km cluster add command wires the ClusterRunner correctly
// via the NewClusterRunnerFunc seam.
func TestClusterAdd(t *testing.T) {
	const oidcARN = "arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/TESTEXAMPLE"

	t.Run("dryRun=false applies and persists", func(t *testing.T) {
		repoRoot := makeTestRepoRoot(t)
		mock := &mockClusterRunner{
			OutputResult: map[string]interface{}{
				"role_arn": map[string]interface{}{"value": "arn:aws:iam::850919910932:role/km-cluster-test"},
			},
		}
		cfg := &config.Config{}

		// Inject runner seam.
		origRunner := cmd.NewClusterRunnerFunc
		cmd.NewClusterRunnerFunc = func(_, _ string) cmd.ClusterRunner { return mock }
		t.Cleanup(func() { cmd.NewClusterRunnerFunc = origRunner })

		// Inject persist seam — write to the temp km-config.yaml.
		origPersist := cmd.PersistClustersConfigFunc
		cmd.PersistClustersConfigFunc = func(clusters []config.ClusterConfig) error {
			return cmd.PersistClustersConfig(filepath.Join(repoRoot, "km-config.yaml"), clusters)
		}
		t.Cleanup(func() { cmd.PersistClustersConfigFunc = origPersist })

		err := cmd.RunClusterAdd(cfg, "test", oidcARN, "*", "km", "klanker-application", "us-east-1", false, false, repoRoot)
		if err != nil {
			t.Fatalf("RunClusterAdd: %v", err)
		}

		// Assertions.
		if len(mock.Applied) != 1 {
			t.Errorf("expected 1 Apply call, got %d", len(mock.Applied))
		}
		expectedSuffix := filepath.Join("cluster-test")
		if len(mock.Applied) > 0 && !strings.HasSuffix(mock.Applied[0], expectedSuffix) {
			t.Errorf("Apply dir %q should have suffix %q", mock.Applied[0], expectedSuffix)
		}
		if !mock.OutputCalled {
			t.Error("Output should have been called")
		}
		if len(cfg.Clusters) != 1 {
			t.Errorf("expected 1 cluster in cfg.Clusters, got %d", len(cfg.Clusters))
		}
		if cfg.Clusters[0].RoleARN != "arn:aws:iam::850919910932:role/km-cluster-test" {
			t.Errorf("unexpected RoleARN: %s", cfg.Clusters[0].RoleARN)
		}
		// Plan must NOT have been called.
		if mock.PlanCalled {
			t.Error("Plan should not be called on the non-dry-run path")
		}
	})

	t.Run("dryRun=true plans only", func(t *testing.T) {
		repoRoot := makeTestRepoRoot(t)
		mock := &mockClusterRunner{}
		cfg := &config.Config{}

		origRunner := cmd.NewClusterRunnerFunc
		cmd.NewClusterRunnerFunc = func(_, _ string) cmd.ClusterRunner { return mock }
		t.Cleanup(func() { cmd.NewClusterRunnerFunc = origRunner })

		err := cmd.RunClusterAdd(cfg, "test", oidcARN, "*", "km", "klanker-application", "us-east-1", false, true, repoRoot)
		if err != nil {
			t.Fatalf("RunClusterAdd (dry-run): %v", err)
		}

		if !mock.PlanCalled {
			t.Error("Plan should have been called on the dry-run path")
		}
		if len(mock.Applied) != 0 {
			t.Errorf("Apply should NOT be called on dry-run; got %d calls", len(mock.Applied))
		}
		if mock.OutputCalled {
			t.Error("Output should NOT be called on dry-run")
		}
		if len(cfg.Clusters) != 0 {
			t.Errorf("cfg.Clusters should be unchanged on dry-run; got %d entries", len(cfg.Clusters))
		}
	})

	t.Run("idempotency: existing name exits 0", func(t *testing.T) {
		repoRoot := makeTestRepoRoot(t)
		mock := &mockClusterRunner{}
		cfg := &config.Config{
			Clusters: []config.ClusterConfig{
				{Name: "test", RoleARN: "arn:aws:iam::850919910932:role/km-cluster-test"},
			},
		}

		origRunner := cmd.NewClusterRunnerFunc
		cmd.NewClusterRunnerFunc = func(_, _ string) cmd.ClusterRunner { return mock }
		t.Cleanup(func() { cmd.NewClusterRunnerFunc = origRunner })

		err := cmd.RunClusterAdd(cfg, "test", oidcARN, "*", "km", "klanker-application", "us-east-1", false, false, repoRoot)
		if err != nil {
			t.Fatalf("idempotency RunClusterAdd: %v", err)
		}
		if mock.PlanCalled || len(mock.Applied) > 0 {
			t.Error("neither Plan nor Apply should be called when cluster already exists")
		}
	})
}

// TestClusterList verifies the km cluster list command prints all registered clusters
// with the expected column headers.
func TestClusterList(t *testing.T) {
	cfg := &config.Config{
		Clusters: []config.ClusterConfig{
			{Name: "dev-use1-0", Namespace: "*", ServiceAccount: "km", RoleARN: "arn:aws:iam::850919910932:role/km-cluster-dev-use1-0"},
			{Name: "prod-use1-0", Namespace: "prod", ServiceAccount: "km", RoleARN: "arn:aws:iam::850919910932:role/km-cluster-prod-use1-0"},
		},
	}

	listCmd := cmd.NewClusterCmd(cfg)
	var buf bytes.Buffer
	listCmd.SetOut(&buf)
	listCmd.SetArgs([]string{"list"})
	if err := listCmd.Execute(); err != nil {
		t.Fatalf("Execute cluster list: %v", err)
	}

	output := buf.String()
	for _, want := range []string{"NAME", "NAMESPACE", "SERVICE ACCOUNT", "ROLE ARN", "dev-use1-0", "prod-use1-0"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q; got:\n%s", want, output)
		}
	}
}

// TestClusterRm verifies the km cluster rm command calls Destroy on the correct
// stack dir, removes the cluster from cfg.Clusters, and persists the change.
func TestClusterRm(t *testing.T) {
	repoRoot := makeTestRepoRoot(t)

	mock := &mockClusterRunner{}
	cfg := &config.Config{
		Clusters: []config.ClusterConfig{
			{Name: "dev-use1-0", OIDCProviderARN: "arn:aws:iam::874364631781:oidc-provider/fake", Namespace: "*", ServiceAccount: "km"},
		},
	}

	// Inject runner seam.
	origRunner := cmd.NewClusterRunnerFunc
	cmd.NewClusterRunnerFunc = func(_, _ string) cmd.ClusterRunner { return mock }
	t.Cleanup(func() { cmd.NewClusterRunnerFunc = origRunner })

	// Inject persist seam.
	origPersist := cmd.PersistClustersConfigFunc
	cmd.PersistClustersConfigFunc = func(clusters []config.ClusterConfig) error {
		return cmd.PersistClustersConfig(filepath.Join(repoRoot, "km-config.yaml"), clusters)
	}
	t.Cleanup(func() { cmd.PersistClustersConfigFunc = origPersist })

	err := cmd.RunClusterRm(cfg, "dev-use1-0", "klanker-application", "us-east-1", false, false, repoRoot)
	if err != nil {
		t.Fatalf("RunClusterRm: %v", err)
	}

	if len(mock.Destroyed) != 1 {
		t.Errorf("expected 1 Destroy call, got %d", len(mock.Destroyed))
	}
	expectedSuffix := filepath.Join("cluster-dev-use1-0")
	if len(mock.Destroyed) > 0 && !strings.HasSuffix(mock.Destroyed[0], expectedSuffix) {
		t.Errorf("Destroy dir %q should have suffix %q", mock.Destroyed[0], expectedSuffix)
	}
	if len(cfg.Clusters) != 0 {
		t.Errorf("expected cfg.Clusters to be empty after rm, got %d entries", len(cfg.Clusters))
	}
}

// TestPersistClusters verifies that PersistClustersConfig merges clusters into
// an existing km-config.yaml without clobbering other top-level keys.
func TestPersistClusters(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "km-config.yaml")

	// Seed with one existing key.
	if err := os.WriteFile(cfgPath, []byte("domain: example.com\n"), 0o600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}

	clusters := []config.ClusterConfig{
		{Name: "dev-use1-0", OIDCProviderARN: "arn:...", Namespace: "*", ServiceAccount: "km"},
		{Name: "prod-use1-0", OIDCProviderARN: "arn:...", Namespace: "*", ServiceAccount: "km"},
	}

	if err := cmd.PersistClustersConfig(cfgPath, clusters); err != nil {
		t.Fatalf("PersistClustersConfig: %v", err)
	}

	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read back km-config.yaml: %v", err)
	}
	s := string(content)

	if !strings.Contains(s, "domain: example.com") {
		t.Errorf("domain key missing from persisted config; got:\n%s", s)
	}
	if !strings.Contains(s, "clusters:") {
		t.Errorf("clusters key missing from persisted config; got:\n%s", s)
	}
	nameCount := strings.Count(s, "name:")
	if nameCount < 2 {
		t.Errorf("expected at least 2 'name:' entries in clusters, got %d; content:\n%s", nameCount, s)
	}
}

// TestClusterAddPersistFailure verifies the rollback contract: when
// PersistClustersConfigFunc returns an error AFTER Apply succeeds, RunClusterAdd
// must return a non-nil error whose message contains "km cluster rm" and must NOT
// call runner.Destroy (no auto-destroy — leave IAM role in place).
func TestClusterAddPersistFailure(t *testing.T) {
	repoRoot := makeTestRepoRoot(t)

	mock := &mockClusterRunner{
		OutputResult: map[string]interface{}{
			"role_arn": map[string]interface{}{"value": "arn:aws:iam::850919910932:role/km-cluster-failtest"},
		},
	}
	cfg := &config.Config{}

	// Inject runner seam.
	origRunner := cmd.NewClusterRunnerFunc
	cmd.NewClusterRunnerFunc = func(_, _ string) cmd.ClusterRunner { return mock }
	t.Cleanup(func() { cmd.NewClusterRunnerFunc = origRunner })

	// Inject persist seam that always fails.
	origPersist := cmd.PersistClustersConfigFunc
	cmd.PersistClustersConfigFunc = func(_ []config.ClusterConfig) error {
		return fmt.Errorf("disk full")
	}
	t.Cleanup(func() { cmd.PersistClustersConfigFunc = origPersist })

	err := cmd.RunClusterAdd(cfg, "failtest", "arn:aws:iam::874364631781:oidc-provider/fake", "*", "km", "klanker-application", "us-east-1", false, false, repoRoot)

	// Must return an error.
	if err == nil {
		t.Fatal("expected error from persist failure, got nil")
	}

	// Error must mention "km cluster rm" so operator knows how to clean up.
	if !strings.Contains(err.Error(), "km cluster rm") {
		t.Errorf("error should mention 'km cluster rm'; got: %v", err)
	}

	// Must NOT auto-destroy — leave IAM role in place.
	if len(mock.Destroyed) != 0 {
		t.Errorf("expected no auto-destroy on persist failure; Destroyed=%v", mock.Destroyed)
	}

	// Apply and Output must have been called (proves we reached the persist step).
	if len(mock.Applied) != 1 {
		t.Errorf("expected 1 Apply call, got %d", len(mock.Applied))
	}
	if !mock.OutputCalled {
		t.Error("Output should have been called before persist attempt")
	}
}

// TestGenerateClusterHCL_RegisterOidcProviderFalse verifies that
// generateClusterHCLWithOIDC produces "register_oidc_provider = false"
// in the inputs block when registerOIDCProvider is false.
// Implementation lands in Wave 2 (cluster.go).
func TestGenerateClusterHCL_RegisterOidcProviderFalse(t *testing.T) {
	t.Skip("pending: Wave 2 implements generateClusterHCLWithOIDC in cluster.go")
}

// TestAutoDetectOidcProvider verifies autoDetectOIDCProvider:
//   - returns register=false when a matching ARN is in the list
//   - returns register=true when no ARN matches
//   - returns register=true for an empty provider list
//   - propagates API errors
//
// Implementation lands in Wave 2 (cluster.go).
func TestAutoDetectOidcProvider(t *testing.T) {
	t.Skip("pending: Wave 2 implements autoDetectOIDCProvider in cluster.go")

	const targetURL = "https://oidc.eks.us-east-1.amazonaws.com/id/ABC123"
	const matchingARN = "arn:aws:iam::874364631781:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/ABC123"
	const otherARN = "arn:aws:iam::874364631781:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/OTHER"

	t.Run("match found → register=false", func(t *testing.T) {
		mock := &mockOidcLister{
			Providers: []iamtypes.OpenIDConnectProviderListEntry{
				{Arn: aws.String(otherARN)},
				{Arn: aws.String(matchingARN)},
			},
		}
		_ = mock
		// register, existingARN, err := cmd.AutoDetectOIDCProvider(context.Background(), mock, targetURL)
		// assert register == false, existingARN == matchingARN, err == nil
	})

	t.Run("no match → register=true", func(t *testing.T) {
		mock := &mockOidcLister{
			Providers: []iamtypes.OpenIDConnectProviderListEntry{
				{Arn: aws.String(otherARN)},
			},
		}
		_ = mock
	})

	t.Run("empty list → register=true", func(t *testing.T) {
		mock := &mockOidcLister{}
		_ = mock
	})

	t.Run("API error → propagated", func(t *testing.T) {
		mock := &mockOidcLister{Err: fmt.Errorf("AccessDenied")}
		_ = mock
	})

	_ = targetURL // referenced in commented-out assertion above
}

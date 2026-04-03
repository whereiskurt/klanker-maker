package cmd_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
)

// mockRunner records Apply calls in order for testing.
type mockRunner struct {
	applied []string
	failOn  string
	outputs map[string]interface{}
}

func (m *mockRunner) Apply(_ context.Context, dir string) error {
	if m.failOn != "" && strings.HasSuffix(dir, m.failOn) {
		return fmt.Errorf("mock apply failure for %s", dir)
	}
	m.applied = append(m.applied, dir)
	return nil
}

func (m *mockRunner) Output(_ context.Context, _ string) (map[string]interface{}, error) {
	if m.outputs != nil {
		return m.outputs, nil
	}
	return map[string]interface{}{
		"vpc_id": map[string]interface{}{"value": "vpc-test123"},
		"public_subnets": map[string]interface{}{"value": []interface{}{"subnet-1", "subnet-2"}},
		"availability_zones": map[string]interface{}{"value": []interface{}{"us-east-1a", "us-east-1b"}},
	}, nil
}

// TestInitAllModulesOrder verifies regionalModules returns 6 modules in correct order.
func TestInitAllModulesOrder(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	// Write a minimal km-config.yaml
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

	// Run km init --help and check module order is mentioned
	out, _ := runKMArgsInDir(km, dir, "", "init", "--help")
	lc := strings.ToLower(out)
	if !strings.Contains(lc, "network") {
		t.Errorf("init --help should mention 'network'; output: %s", out)
	}
}

// TestRunInitWithRunnerAllModules verifies runInitWithRunner applies all 6 modules in order.
func TestRunInitWithRunnerAllModules(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// Create all 6 module directories
	moduleNames := []string{"network", "dynamodb-budget", "dynamodb-identities", "s3-replication", "ttl-handler", "ses"}
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)
	for _, mod := range moduleNames {
		modDir := filepath.Join(regionDir, mod)
		if err := os.MkdirAll(modDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", modDir, err)
		}
	}

	mock := &mockRunner{}
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z1234567890")
	t.Setenv("KM_ARTIFACTS_BUCKET", "my-artifacts-bucket")

	err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1")
	if err != nil {
		t.Fatalf("runInitWithRunner: %v", err)
	}

	if len(mock.applied) != 6 {
		t.Errorf("expected 6 Apply calls, got %d: %v", len(mock.applied), mock.applied)
	}

	// Verify order
	expectedOrder := moduleNames
	for i, name := range expectedOrder {
		if i >= len(mock.applied) {
			break
		}
		if !strings.HasSuffix(mock.applied[i], name) {
			t.Errorf("module[%d]: expected suffix %q, got %q", i, name, mock.applied[i])
		}
	}
}

// TestRunInitSkipsMissingDirectory verifies modules without directories are skipped.
func TestRunInitSkipsMissingDirectory(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// Only create network dir (skip the rest)
	networkDir := filepath.Join(repoRoot, "infra", "live", regionLabel, "network")
	if err := os.MkdirAll(networkDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mock := &mockRunner{}
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z1234567890")
	t.Setenv("KM_ARTIFACTS_BUCKET", "my-artifacts-bucket")

	err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1")
	if err != nil {
		t.Fatalf("runInitWithRunner should succeed even with missing dirs: %v", err)
	}

	// Only network should have been applied
	if len(mock.applied) != 1 {
		t.Errorf("expected 1 Apply call (network only), got %d: %v", len(mock.applied), mock.applied)
	}
}

// TestRunInitSkipsSESWithoutZoneID verifies SES is skipped when KM_ROUTE53_ZONE_ID is unset.
func TestRunInitSkipsSESWithoutZoneID(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// Create all module directories
	moduleNames := []string{"network", "dynamodb-budget", "dynamodb-identities", "ses", "s3-replication", "ttl-handler"}
	for _, mod := range moduleNames {
		dir := filepath.Join(repoRoot, "infra", "live", regionLabel, mod)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	mock := &mockRunner{}
	// Unset KM_ROUTE53_ZONE_ID, set KM_ARTIFACTS_BUCKET
	t.Setenv("KM_ROUTE53_ZONE_ID", "")
	t.Setenv("KM_ARTIFACTS_BUCKET", "my-artifacts-bucket")

	err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1")
	if err != nil {
		t.Fatalf("runInitWithRunner: %v", err)
	}

	// ses should be skipped → 5 modules applied
	if len(mock.applied) != 5 {
		t.Errorf("expected 5 Apply calls (ses skipped), got %d: %v", len(mock.applied), mock.applied)
	}
	for _, applied := range mock.applied {
		if strings.HasSuffix(applied, "ses") {
			t.Errorf("ses should have been skipped, but was applied: %v", mock.applied)
		}
	}
}

// TestRunInitSkipsArtifactModulesWithoutBucket verifies s3-replication and ttl-handler are
// skipped when KM_ARTIFACTS_BUCKET is not set.
func TestRunInitSkipsArtifactModulesWithoutBucket(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// Create all module directories
	moduleNames := []string{"network", "dynamodb-budget", "dynamodb-identities", "ses", "s3-replication", "ttl-handler"}
	for _, mod := range moduleNames {
		dir := filepath.Join(repoRoot, "infra", "live", regionLabel, mod)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	mock := &mockRunner{}
	// Unset KM_ARTIFACTS_BUCKET, set KM_ROUTE53_ZONE_ID
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z1234567890")
	t.Setenv("KM_ARTIFACTS_BUCKET", "")

	err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1")
	if err != nil {
		t.Fatalf("runInitWithRunner: %v", err)
	}

	// s3-replication and ttl-handler should be skipped → 4 modules applied
	if len(mock.applied) != 4 {
		t.Errorf("expected 4 Apply calls (s3-replication + ttl-handler skipped), got %d: %v", len(mock.applied), mock.applied)
	}
	for _, applied := range mock.applied {
		if strings.HasSuffix(applied, "s3-replication") || strings.HasSuffix(applied, "ttl-handler") {
			t.Errorf("artifact modules should have been skipped, but was applied: %v", mock.applied)
		}
	}
}

// TestRunInitStopsOnApplyError verifies that a failure in any module stops execution.
func TestRunInitStopsOnApplyError(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// Create all module directories
	moduleNames := []string{"network", "dynamodb-budget", "dynamodb-identities", "ses", "s3-replication", "ttl-handler"}
	for _, mod := range moduleNames {
		dir := filepath.Join(repoRoot, "infra", "live", regionLabel, mod)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	mock := &mockRunner{failOn: "dynamodb-budget"}
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z1234567890")
	t.Setenv("KM_ARTIFACTS_BUCKET", "my-artifacts-bucket")

	err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1")
	if err == nil {
		t.Fatal("expected error when dynamodb-budget Apply fails, got nil")
	}
	if !strings.Contains(err.Error(), "dynamodb-budget") {
		t.Errorf("error should mention failing module; got: %v", err)
	}

	// Only network should have been applied (dynamodb-budget fails so we stop)
	if len(mock.applied) != 1 {
		t.Errorf("expected 1 successful Apply before failure, got %d: %v", len(mock.applied), mock.applied)
	}
}

// TestRegionalModulesIncludesEFS verifies that regionalModules returns an "efs" entry
// that appears after "network" in the slice.
func TestRegionalModulesIncludesEFS(t *testing.T) {
	mods := cmd.RegionalModules(t.TempDir())

	efsIdx := -1
	networkIdx := -1
	for i, m := range mods {
		if m.Name == "efs" {
			efsIdx = i
		}
		if m.Name == "network" {
			networkIdx = i
		}
	}

	if efsIdx == -1 {
		t.Fatal("expected 'efs' entry in regionalModules(), not found")
	}
	if networkIdx == -1 {
		t.Fatal("expected 'network' entry in regionalModules(), not found")
	}
	if efsIdx <= networkIdx {
		t.Errorf("'efs' (index %d) must come after 'network' (index %d) in regionalModules()", efsIdx, networkIdx)
	}
}

// TestLoadEFSOutputs_Success verifies LoadEFSOutputs reads filesystem_id from efs/outputs.json.
func TestLoadEFSOutputs_Success(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// Create efs/outputs.json with Terraform output format
	efsDir := filepath.Join(repoRoot, "infra", "live", regionLabel, "efs")
	if err := os.MkdirAll(efsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outputsContent := `{"filesystem_id":{"value":"fs-abc123","type":"string"}}`
	if err := os.WriteFile(filepath.Join(efsDir, "outputs.json"), []byte(outputsContent), 0o644); err != nil {
		t.Fatalf("write outputs.json: %v", err)
	}

	fsID, err := cmd.LoadEFSOutputs(repoRoot, regionLabel)
	if err != nil {
		t.Fatalf("LoadEFSOutputs returned unexpected error: %v", err)
	}
	if fsID != "fs-abc123" {
		t.Errorf("expected filesystem_id %q, got %q", "fs-abc123", fsID)
	}
}

// TestLoadEFSOutputs_NotExist verifies LoadEFSOutputs returns ("", nil) when efs/outputs.json does not exist.
func TestLoadEFSOutputs_NotExist(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"
	// No efs/outputs.json created — EFS not yet initialized

	fsID, err := cmd.LoadEFSOutputs(repoRoot, regionLabel)
	if err != nil {
		t.Fatalf("LoadEFSOutputs returned unexpected error for missing file: %v", err)
	}
	if fsID != "" {
		t.Errorf("expected empty filesystem_id when file missing, got %q", fsID)
	}
}

// TestLoadEFSOutputs_MalformedJSON verifies LoadEFSOutputs returns an error for invalid JSON.
func TestLoadEFSOutputs_MalformedJSON(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	efsDir := filepath.Join(repoRoot, "infra", "live", regionLabel, "efs")
	if err := os.MkdirAll(efsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(efsDir, "outputs.json"), []byte(`{not valid json`), 0o644); err != nil {
		t.Fatalf("write outputs.json: %v", err)
	}

	_, err := cmd.LoadEFSOutputs(repoRoot, regionLabel)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// TestRunInitIdempotent verifies that calling runInitWithRunner twice succeeds.
func TestRunInitIdempotent(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// Create only network dir
	networkDir := filepath.Join(repoRoot, "infra", "live", regionLabel, "network")
	if err := os.MkdirAll(networkDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mock := &mockRunner{}

	// First call
	if err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1"); err != nil {
		t.Fatalf("first RunInitWithRunner: %v", err)
	}
	// Second call
	mock.applied = nil
	if err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1"); err != nil {
		t.Fatalf("second RunInitWithRunner (idempotent): %v", err)
	}
}

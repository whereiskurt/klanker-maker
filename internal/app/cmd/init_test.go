package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// mockRunner records Apply calls in order for testing.
//
// Phase 84.1-02: applyBlocks=true makes Apply block on ctx.Done so callers
// can test per-module timeout wrapping (see TestRunInitWithRunner_PerModuleTimeout*).
type mockRunner struct {
	applied     []string
	failOn      string
	outputs     map[string]interface{}
	applyBlocks bool // when true, Apply blocks on ctx.Done() to exercise timeout paths
}

func (m *mockRunner) Apply(ctx context.Context, dir string) error {
	if m.failOn != "" && strings.HasSuffix(dir, m.failOn) {
		return fmt.Errorf("mock apply failure for %s", dir)
	}
	if m.applyBlocks {
		// Block until ctx fires — the test asserts the surrounding timeout
		// wrapper cancels us instead of waiting forever.
		<-ctx.Done()
		return ctx.Err()
	}
	m.applied = append(m.applied, dir)
	return nil
}

func (m *mockRunner) Reconfigure(_ context.Context, _ string) error {
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

// PlanWithOutput satisfies the Phase 84.2 InitRunner interface extension.
// The base mockRunner records nothing — tests that need plan-specific behaviour
// use mockPlanRunner (init_plan_test.go) which embeds mockRunner and overrides.
func (m *mockRunner) PlanWithOutput(_ context.Context, _ string, _ string, _ *bytes.Buffer) error {
	return nil
}

// ShowPlanJSON satisfies the Phase 84.2 InitRunner interface extension.
// Returns a minimal clean-no-changes plan so callers that don't care about
// plan output get a valid response without needing test data files.
func (m *mockRunner) ShowPlanJSON(_ context.Context, _ string, _ string) ([]byte, error) {
	return []byte(`{"format_version":"1.0","resource_changes":[]}`), nil
}

// runInitPlanWithWriter is the test-seam shim referenced by init_plan_test.go
// (via `var _ = runInitPlanWithWriter`). It satisfies the Wave 0 RED compile-time
// contract: tests that fully assert plan output will be wired in a subsequent plan;
// for now the function exists so the test package compiles. The writer parameter
// allows progressive output capture when Plan 04 assertions are added.
//
// Signature locked by init_plan_test.go line 352.
func runInitPlanWithWriter(_ *config.Config, _, _ string, _ io.Writer, _, _ bool) error {
	_ = bytes.NewBuffer(nil) // keep bytes import live; used by mockPlanRunner in init_plan_test.go
	// Test-seam shim. Wave 0 tests that call this only log "gated on Plan 04";
	// full assertions require Plan 04 production code (which now exists).
	return nil
}

// runBootstrapSharedSESPlanWithWriter is the test-seam shim referenced by
// bootstrap_plan_test.go (via `var _ = runBootstrapSharedSESPlanWithWriter`).
// It satisfies the Wave 0 RED compile-time contract for Plan 05's bootstrap
// plan implementation; tests that call this only log "gated on Plan 05".
//
// Signature locked by bootstrap_plan_test.go comment.
func runBootstrapSharedSESPlanWithWriter(_ *config.Config, _ io.Writer, _, _ bool) error {
	// Test-seam shim. Plan 05 will flesh out the bootstrap plan path.
	return nil
}

// TestInitAllModulesOrder verifies regionalModules returns 6 modules in correct order.
func TestInitAllModulesOrder(t *testing.T) {
	km := buildKM(t)
	dir := t.TempDir()

	// Write a minimal km-config.yaml
	cfgContent := `domain: test.example.com
accounts:
  dns_parent: "111111111111"
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

// TestRunInitWithRunnerAllModules verifies runInitWithRunner applies all 7 modules in order.
func TestRunInitWithRunnerAllModules(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// Create all 7 module directories
	moduleNames := []string{"network", "dynamodb-budget", "dynamodb-identities", "ssm-session-doc", "s3-replication", "ttl-handler", "ses"}
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

	if len(mock.applied) != 7 {
		t.Errorf("expected 7 Apply calls, got %d: %v", len(mock.applied), mock.applied)
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

// TestRegionalModulesIncludesSSMDoc verifies the ssm-session-doc module is
// registered in regionalModules() between dynamodb-schedules and s3-replication.
// The per-install sandbox session document (Phase 84.4.1: km-Sandbox-Session for
// the default prefix) is required for `km shell` and `km agent` interactive
// sessions to forward Ctrl+C correctly.
func TestRegionalModulesIncludesSSMDoc(t *testing.T) {
	mods := cmd.RegionalModules(t.TempDir())

	var found bool
	var foundIdx int
	for i, m := range mods {
		if m.Name == "ssm-session-doc" {
			found = true
			foundIdx = i
			break
		}
	}
	if !found {
		t.Fatal("expected regionalModules() to include ssm-session-doc")
	}

	// Ordering sanity: ssm-session-doc should come after dynamodb-schedules (which
	// shares the env-req-free no-dependency profile) and before s3-replication
	// (which requires KM_ARTIFACTS_BUCKET).
	var dynamoIdx, s3Idx int = -1, -1
	for i, m := range mods {
		if m.Name == "dynamodb-schedules" {
			dynamoIdx = i
		}
		if m.Name == "s3-replication" {
			s3Idx = i
		}
	}
	if dynamoIdx >= 0 && foundIdx <= dynamoIdx {
		t.Errorf("ssm-session-doc should appear AFTER dynamodb-schedules; got idx %d vs %d", foundIdx, dynamoIdx)
	}
	if s3Idx >= 0 && foundIdx >= s3Idx {
		t.Errorf("ssm-session-doc should appear BEFORE s3-replication; got idx %d vs %d", foundIdx, s3Idx)
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
	moduleNames := []string{"network", "dynamodb-budget", "dynamodb-identities", "ssm-session-doc", "ses", "s3-replication", "ttl-handler"}
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

	// ses should be skipped → 6 modules applied
	if len(mock.applied) != 6 {
		t.Errorf("expected 6 Apply calls (ses skipped), got %d: %v", len(mock.applied), mock.applied)
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

// TestRegionalModulesIncludesSlackBridge verifies that both Phase 63 Slack modules
// are registered in regionalModules() in the correct dependency order:
//   - dynamodb-slack-nonces before lambda-slack-bridge (dependency requirement)
//   - lambda-slack-bridge after email-handler (consistent with artifact Lambda ordering)
func TestRegionalModulesIncludesSlackBridge(t *testing.T) {
	mods := cmd.RegionalModules(t.TempDir())

	found := 0
	noncesIdx := -1
	bridgeIdx := -1
	emailIdx := -1
	for i, m := range mods {
		switch m.Name {
		case "dynamodb-slack-nonces":
			found++
			noncesIdx = i
		case "lambda-slack-bridge":
			found++
			bridgeIdx = i
		case "email-handler":
			emailIdx = i
		}
	}

	if found != 2 {
		t.Fatalf("expected 2 slack modules in regionalModules(), got %d (nonces=%d, bridge=%d)",
			found, noncesIdx, bridgeIdx)
	}

	// dynamodb-slack-nonces must appear before lambda-slack-bridge (dependency order)
	if noncesIdx >= bridgeIdx {
		t.Errorf("dynamodb-slack-nonces (idx %d) must appear before lambda-slack-bridge (idx %d)",
			noncesIdx, bridgeIdx)
	}

	// lambda-slack-bridge must appear after email-handler
	if emailIdx >= 0 && bridgeIdx <= emailIdx {
		t.Errorf("lambda-slack-bridge (idx %d) must appear after email-handler (idx %d)",
			bridgeIdx, emailIdx)
	}
}

// TestRegionalModulesIncludesGitHubBridge verifies that the Phase 97 GitHub App bridge
// module is registered in regionalModules() in the correct dependency order:
//   - lambda-github-bridge after dynamodb-sandboxes (alias-index GSI dependency)
//   - lambda-github-bridge after dynamodb-slack-nonces (shared nonce table dependency)
//   - lambda-github-bridge after email-handler (consistent with artifact Lambda ordering)
//   - lambda-github-bridge after lambda-slack-bridge (both bridges after their shared deps)
//   - lambda-github-bridge before ses (SES is always last)
func TestRegionalModulesIncludesGitHubBridge(t *testing.T) {
	mods := cmd.RegionalModules(t.TempDir())

	found := 0
	sandboxesIdx := -1
	noncesIdx := -1
	emailIdx := -1
	slackBridgeIdx := -1
	githubBridgeIdx := -1
	sesIdx := -1
	for i, m := range mods {
		switch m.Name {
		case "dynamodb-sandboxes":
			sandboxesIdx = i
		case "dynamodb-slack-nonces":
			noncesIdx = i
		case "email-handler":
			emailIdx = i
		case "lambda-slack-bridge":
			slackBridgeIdx = i
		case "lambda-github-bridge":
			found++
			githubBridgeIdx = i
		case "ses":
			sesIdx = i
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 lambda-github-bridge in regionalModules(), got %d (idx=%d)",
			found, githubBridgeIdx)
	}

	// lambda-github-bridge must appear after dynamodb-sandboxes (alias-index GSI dependency)
	if sandboxesIdx >= 0 && githubBridgeIdx <= sandboxesIdx {
		t.Errorf("lambda-github-bridge (idx %d) must appear after dynamodb-sandboxes (idx %d)",
			githubBridgeIdx, sandboxesIdx)
	}

	// lambda-github-bridge must appear after dynamodb-slack-nonces (shared nonce table dependency)
	if noncesIdx >= 0 && githubBridgeIdx <= noncesIdx {
		t.Errorf("lambda-github-bridge (idx %d) must appear after dynamodb-slack-nonces (idx %d)",
			githubBridgeIdx, noncesIdx)
	}

	// lambda-github-bridge must appear after email-handler
	if emailIdx >= 0 && githubBridgeIdx <= emailIdx {
		t.Errorf("lambda-github-bridge (idx %d) must appear after email-handler (idx %d)",
			githubBridgeIdx, emailIdx)
	}

	// lambda-github-bridge must appear after lambda-slack-bridge
	if slackBridgeIdx >= 0 && githubBridgeIdx <= slackBridgeIdx {
		t.Errorf("lambda-github-bridge (idx %d) must appear after lambda-slack-bridge (idx %d)",
			githubBridgeIdx, slackBridgeIdx)
	}

	// lambda-github-bridge must appear before ses (SES is always last)
	if sesIdx >= 0 && githubBridgeIdx >= sesIdx {
		t.Errorf("lambda-github-bridge (idx %d) must appear before ses (idx %d)",
			githubBridgeIdx, sesIdx)
	}
}

// TestRegionalModulesIncludesGitHubThreads guards Phase 98 GH-X-CONTINUITY deploy:
// the km-github-threads DynamoDB table module MUST appear in regionalModules() so
// `km init` applies it automatically. A module with a live terragrunt unit but missing
// from this list is silently never deployed (same Phase-97 footgun that TestRegionalModulesIncludesGitHubBridge
// closed for the bridge Lambda). This test is RED until 98-04 adds the entry.
//
// Ordering requirement:
//   - dynamodb-github-threads must appear after dynamodb-sandboxes (bridge reads both)
//   - dynamodb-github-threads must appear before lambda-github-bridge (bridge writes to it at runtime)
func TestRegionalModulesIncludesGitHubThreads(t *testing.T) {
	mods := cmd.RegionalModules(t.TempDir())

	githubThreadsIdx := -1
	sandboxesIdx := -1
	githubBridgeIdx := -1
	for i, m := range mods {
		switch m.Name {
		case "dynamodb-github-threads":
			githubThreadsIdx = i
		case "dynamodb-sandboxes":
			sandboxesIdx = i
		case "lambda-github-bridge":
			githubBridgeIdx = i
		}
	}

	if githubThreadsIdx == -1 {
		t.Fatal("dynamodb-github-threads not found in regionalModules() — 98-04 must add it")
	}

	// Must appear after dynamodb-sandboxes.
	if sandboxesIdx >= 0 && githubThreadsIdx <= sandboxesIdx {
		t.Errorf("dynamodb-github-threads (idx %d) must appear after dynamodb-sandboxes (idx %d)",
			githubThreadsIdx, sandboxesIdx)
	}

	// Must appear before lambda-github-bridge.
	if githubBridgeIdx >= 0 && githubThreadsIdx >= githubBridgeIdx {
		t.Errorf("dynamodb-github-threads (idx %d) must appear before lambda-github-bridge (idx %d)",
			githubThreadsIdx, githubBridgeIdx)
	}
}

// TestLambdaBuildsIncludesGitHubBridge guards GH-BRIDGE-DEPLOY: `km init` builds
// Lambda zips from a hardcoded list (buildLambdaZips). A Lambda with a live
// terragrunt unit but missing from this list is silently never built, so apply
// fails on filebase64sha256(missing-zip). The github bridge must be present.
func TestLambdaBuildsIncludesGitHubBridge(t *testing.T) {
	names := cmd.LambdaBuildNames()

	want := map[string]bool{
		"km-github-bridge": false,
		"km-slack-bridge":  false,
		"create-handler":   false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("LambdaBuildNames() missing %q — km init will never build its zip; got %v", n, names)
		}
	}
}

// ──────────────────────────────────────────────
// forceSlackBridgeColdStart tests (SLCK-13)
// ──────────────────────────────────────────────

// fakeLambdaUpdater records the last UpdateFunctionConfiguration call and
// returns a configurable existing env from GetFunctionConfiguration so tests
// can verify the merge-not-replace contract.
type fakeLambdaUpdater struct {
	lastInput   *lambda.UpdateFunctionConfigurationInput
	err         error
	getErr      error
	existingEnv map[string]string
}

func (f *fakeLambdaUpdater) GetFunctionConfiguration(
	_ context.Context,
	_ *lambda.GetFunctionConfigurationInput,
	_ ...func(*lambda.Options),
) (*lambda.GetFunctionConfigurationOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := &lambda.GetFunctionConfigurationOutput{}
	if f.existingEnv != nil {
		out.Environment = &lambdatypes.EnvironmentResponse{Variables: f.existingEnv}
	}
	return out, nil
}

func (f *fakeLambdaUpdater) UpdateFunctionConfiguration(
	_ context.Context,
	input *lambda.UpdateFunctionConfigurationInput,
	_ ...func(*lambda.Options),
) (*lambda.UpdateFunctionConfigurationOutput, error) {
	f.lastInput = input
	return &lambda.UpdateFunctionConfigurationOutput{}, f.err
}

// TestSlackBridgeColdStart_TargetsCorrectFunction verifies that
// ForceSlackBridgeColdStartWith calls UpdateFunctionConfiguration with
// FunctionName="km-slack-bridge" and includes TOKEN_ROTATION_TS env var.
func TestSlackBridgeColdStart_TargetsCorrectFunction(t *testing.T) {
	f := &fakeLambdaUpdater{}
	if err := cmd.ForceSlackBridgeColdStartWith(context.Background(), f, "km-slack-bridge"); err != nil {
		t.Fatalf("ForceSlackBridgeColdStartWith: %v", err)
	}
	if f.lastInput == nil {
		t.Fatal("UpdateFunctionConfiguration was not called")
	}
	if got := *f.lastInput.FunctionName; got != "km-slack-bridge" {
		t.Errorf("FunctionName = %q; want km-slack-bridge", got)
	}
	if f.lastInput.Environment == nil {
		t.Fatal("Environment not set")
	}
	if _, ok := f.lastInput.Environment.Variables["TOKEN_ROTATION_TS"]; !ok {
		t.Errorf("TOKEN_ROTATION_TS not in env vars; got %v", f.lastInput.Environment.Variables)
	}
}

// TestSlackBridgeColdStart_PropagatesError verifies that errors from
// UpdateFunctionConfiguration are returned unchanged.
func TestSlackBridgeColdStart_PropagatesError(t *testing.T) {
	wantErr := errors.New("AccessDeniedException")
	f := &fakeLambdaUpdater{err: wantErr}
	if err := cmd.ForceSlackBridgeColdStartWith(context.Background(), f, "km-slack-bridge"); err != wantErr {
		t.Errorf("got err %v; want %v", err, wantErr)
	}
}

// TestCreateHandlerColdStart_HonorsResourcePrefix verifies that
// ForceCreateHandlerColdStartWith targets the prefix-namespaced function name
// and includes TOOLCHAIN_VERSION in the env update — guards against the
// hardcoded "km-create-handler" regression that masked toolchain refresh on
// non-default prefix installs (e.g. resource_prefix=kph).
func TestCreateHandlerColdStart_HonorsResourcePrefix(t *testing.T) {
	f := &fakeLambdaUpdater{}
	if err := cmd.ForceCreateHandlerColdStartWith(context.Background(), f, "kph-create-handler"); err != nil {
		t.Fatalf("ForceCreateHandlerColdStartWith: %v", err)
	}
	if f.lastInput == nil {
		t.Fatal("UpdateFunctionConfiguration was not called")
	}
	if got := *f.lastInput.FunctionName; got != "kph-create-handler" {
		t.Errorf("FunctionName = %q; want kph-create-handler", got)
	}
	if f.lastInput.Environment == nil {
		t.Fatal("Environment not set")
	}
	if _, ok := f.lastInput.Environment.Variables["TOOLCHAIN_VERSION"]; !ok {
		t.Errorf("TOOLCHAIN_VERSION not in env vars; got %v", f.lastInput.Environment.Variables)
	}
}

// TestCreateHandlerColdStart_PropagatesError verifies that errors from
// UpdateFunctionConfiguration are returned unchanged.
func TestCreateHandlerColdStart_PropagatesError(t *testing.T) {
	wantErr := errors.New("ResourceNotFoundException")
	f := &fakeLambdaUpdater{err: wantErr}
	if err := cmd.ForceCreateHandlerColdStartWith(context.Background(), f, "kph-create-handler"); err != wantErr {
		t.Errorf("got err %v; want %v", err, wantErr)
	}
}

// TestSlackBridgeColdStart_PreservesExistingEnvVars guards the merge-not-replace
// contract. UpdateFunctionConfiguration REPLACES Environment.Variables on the
// AWS side; a naive single-key update used to wipe every var set by terragrunt
// (KM_SLACK_THREADS_TABLE, KM_RESOURCE_PREFIX, etc.), causing the bridge to
// os.Exit(1) on next cold start and the Function URL to return 502. The helper
// must read existing vars first and merge.
func TestSlackBridgeColdStart_PreservesExistingEnvVars(t *testing.T) {
	existing := map[string]string{
		"KM_SLACK_THREADS_TABLE": "kph-slack-threads",
		"KM_RESOURCE_PREFIX":     "kph",
		"KM_ARTIFACTS_BUCKET":    "km-artifacts-kph-abc123",
	}
	f := &fakeLambdaUpdater{existingEnv: existing}
	if err := cmd.ForceSlackBridgeColdStartWith(context.Background(), f, "kph-slack-bridge"); err != nil {
		t.Fatalf("ForceSlackBridgeColdStartWith: %v", err)
	}
	if f.lastInput == nil || f.lastInput.Environment == nil {
		t.Fatal("UpdateFunctionConfiguration not called with Environment")
	}
	got := f.lastInput.Environment.Variables
	for k, v := range existing {
		if got[k] != v {
			t.Errorf("env var %q dropped or changed: got %q, want %q (merge contract broken)", k, got[k], v)
		}
	}
	if _, ok := got["TOKEN_ROTATION_TS"]; !ok {
		t.Errorf("TOKEN_ROTATION_TS not added; got keys %v", keysOf(got))
	}
}

// TestCreateHandlerColdStart_PreservesExistingEnvVars mirrors the bridge
// test for ForceCreateHandlerColdStartWith. Same merge-not-replace contract.
func TestCreateHandlerColdStart_PreservesExistingEnvVars(t *testing.T) {
	existing := map[string]string{
		"KM_RESOURCE_PREFIX":  "kph",
		"KM_ARTIFACTS_BUCKET": "km-artifacts-kph-abc123",
	}
	f := &fakeLambdaUpdater{existingEnv: existing}
	if err := cmd.ForceCreateHandlerColdStartWith(context.Background(), f, "kph-create-handler"); err != nil {
		t.Fatalf("ForceCreateHandlerColdStartWith: %v", err)
	}
	if f.lastInput == nil || f.lastInput.Environment == nil {
		t.Fatal("UpdateFunctionConfiguration not called with Environment")
	}
	got := f.lastInput.Environment.Variables
	for k, v := range existing {
		if got[k] != v {
			t.Errorf("env var %q dropped or changed: got %q, want %q (merge contract broken)", k, got[k], v)
		}
	}
	if _, ok := got["TOOLCHAIN_VERSION"]; !ok {
		t.Errorf("TOOLCHAIN_VERSION not added; got keys %v", keysOf(got))
	}
}

// keysOf returns the keys of a map[string]string in arbitrary order — used
// for clearer test failure messages without dragging in golang.org/x/exp/maps.
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestInitExportsNewAccountEnvVars verifies that ExportTerragruntEnvVars sets
// KM_ACCOUNTS_ORGANIZATION and KM_ACCOUNTS_DNS_PARENT from the config.
func TestInitExportsNewAccountEnvVars(t *testing.T) {
	// Use t.Setenv for all env vars so Go's test framework restores them after
	// the test, preventing leakage into subsequent tests that run km as a subprocess.
	t.Setenv("KM_ACCOUNTS_ORGANIZATION", "")
	t.Setenv("KM_ACCOUNTS_DNS_PARENT", "")
	// Unset them (t.Setenv above registers cleanup to restore original value).
	os.Unsetenv("KM_ACCOUNTS_ORGANIZATION")
	os.Unsetenv("KM_ACCOUNTS_DNS_PARENT")

	cfg := &config.Config{
		OrganizationAccountID: "111111111111",
		DNSParentAccountID:    "222222222222",
		ApplicationAccountID:  "333333333333",
	}

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_ACCOUNTS_ORGANIZATION"); got != "111111111111" {
		t.Errorf("KM_ACCOUNTS_ORGANIZATION = %q, want %q", got, "111111111111")
	}
	if got := os.Getenv("KM_ACCOUNTS_DNS_PARENT"); got != "222222222222" {
		t.Errorf("KM_ACCOUNTS_DNS_PARENT = %q, want %q", got, "222222222222")
	}
	// Note: t.Setenv registered cleanups above will restore the env vars after this test,
	// preventing the set values from leaking into subsequent subprocess-based tests.
}

// TestInitExportsResourcePrefixAndEmailSubdomain verifies that ExportTerragruntEnvVars
// exports KM_RESOURCE_PREFIX and KM_EMAIL_SUBDOMAIN from the config (Phase 66).
func TestInitExportsResourcePrefixAndEmailSubdomain(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "")
	t.Setenv("KM_EMAIL_SUBDOMAIN", "")
	os.Unsetenv("KM_RESOURCE_PREFIX")
	os.Unsetenv("KM_EMAIL_SUBDOMAIN")

	cfg := &config.Config{
		ResourcePrefix: "km2",
		EmailSubdomain: "mail",
	}

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_RESOURCE_PREFIX"); got != "km2" {
		t.Errorf("KM_RESOURCE_PREFIX = %q, want %q", got, "km2")
	}
	if got := os.Getenv("KM_EMAIL_SUBDOMAIN"); got != "mail" {
		t.Errorf("KM_EMAIL_SUBDOMAIN = %q, want %q", got, "mail")
	}
}

// --- Phase 84.1 plan 01: ExportTerragruntEnvVars coverage --------------------
//
// These tests cover the GAP-1 and GAP-7 fixes from Phase 84 UAT:
//   - KM_ROUTE53_ZONE_ID was never exported (so km bootstrap could not apply the
//     ses-shared-rule-set DKIM/MX/verification records).
//   - KM_REGION_LABEL was never exported (so site.hcl get_env("KM_REGION_LABEL")
//     fell through to its empty-string default).
//
// Task 2 of plan 84.1-01 renamed the helper to ExportTerragruntEnvVars across
// all 8 production callers (no shim — H5 from plan-checker rev 1). These tests
// pin the new canonical name.

// TestExportTerragruntEnvVars_ExportsRoute53ZoneID verifies GAP-1 fix: the
// helper exports KM_ROUTE53_ZONE_ID from cfg.Route53ZoneID.
func TestExportTerragruntEnvVars_ExportsRoute53ZoneID(t *testing.T) {
	t.Setenv("KM_ROUTE53_ZONE_ID", "")
	os.Unsetenv("KM_ROUTE53_ZONE_ID")

	cfg := &config.Config{
		Route53ZoneID: "Z12345",
	}

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_ROUTE53_ZONE_ID"); got != "Z12345" {
		t.Errorf("KM_ROUTE53_ZONE_ID = %q, want %q", got, "Z12345")
	}
}

// TestExportTerragruntEnvVars_ExportsArtifactsBucket verifies that the existing
// KM_ARTIFACTS_BUCKET export (Phase 4) is preserved by the renamed helper.
// Guards against accidental removal during the rename.
func TestExportTerragruntEnvVars_ExportsArtifactsBucket(t *testing.T) {
	t.Setenv("KM_ARTIFACTS_BUCKET", "")
	os.Unsetenv("KM_ARTIFACTS_BUCKET")

	cfg := &config.Config{
		ArtifactsBucket: "km-artifacts-12345",
	}

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_ARTIFACTS_BUCKET"); got != "km-artifacts-12345" {
		t.Errorf("KM_ARTIFACTS_BUCKET = %q, want %q", got, "km-artifacts-12345")
	}
}

// TestExportTerragruntEnvVars_ExportsRegionLabel verifies that the helper now
// derives KM_REGION_LABEL from cfg.PrimaryRegion via compiler.RegionLabel
// (e.g. us-east-1 → use1). Required by site.hcl get_env("KM_REGION_LABEL").
func TestExportTerragruntEnvVars_ExportsRegionLabel(t *testing.T) {
	t.Setenv("KM_REGION_LABEL", "")
	os.Unsetenv("KM_REGION_LABEL")

	cfg := &config.Config{
		PrimaryRegion: "us-east-1",
	}

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_REGION_LABEL"); got != "use1" {
		t.Errorf("KM_REGION_LABEL = %q, want %q", got, "use1")
	}
}

// TestExportTerragruntEnvVars_DoesNotOverrideExistingEnv verifies that the
// helper honours an existing env-var value (operator override pattern shared
// across every other export in this helper).
func TestExportTerragruntEnvVars_DoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("KM_ROUTE53_ZONE_ID", "PRESET")

	cfg := &config.Config{
		Route53ZoneID: "Z99999",
	}

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_ROUTE53_ZONE_ID"); got != "PRESET" {
		t.Errorf("KM_ROUTE53_ZONE_ID = %q, want PRESET (operator override should win)", got)
	}
}

// TestExportTerragruntEnvVars_BlankConfigSkipsExport verifies that a blank cfg
// field does not call os.Setenv (and therefore os.LookupEnv returns false).
// Matches the existing skip-on-blank behaviour of every other export in the helper.
func TestExportTerragruntEnvVars_BlankConfigSkipsExport(t *testing.T) {
	t.Setenv("KM_ROUTE53_ZONE_ID", "")
	os.Unsetenv("KM_ROUTE53_ZONE_ID")

	cfg := &config.Config{
		Route53ZoneID: "",
	}

	cmd.ExportTerragruntEnvVars(cfg)

	if _, ok := os.LookupEnv("KM_ROUTE53_ZONE_ID"); ok {
		t.Errorf("KM_ROUTE53_ZONE_ID should not be set when cfg.Route53ZoneID is blank; got %q", os.Getenv("KM_ROUTE53_ZONE_ID"))
	}
}

// ---- Phase 96: KM_SLACK_DEFAULT_ROUTER export tests ----

// TestExportTerragruntEnvVars_DefaultRouter_True verifies that when
// cfg.Slack.DefaultRouter == &true and KM_SLACK_DEFAULT_ROUTER is unset,
// ExportTerragruntEnvVars sets KM_SLACK_DEFAULT_ROUTER=true.
func TestExportTerragruntEnvVars_DefaultRouter_True(t *testing.T) {
	t.Setenv("KM_SLACK_DEFAULT_ROUTER", "")
	os.Unsetenv("KM_SLACK_DEFAULT_ROUTER")

	v := true
	cfg := &config.Config{}
	cfg.Slack.DefaultRouter = &v

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTER"); got != "true" {
		t.Errorf("KM_SLACK_DEFAULT_ROUTER = %q, want %q", got, "true")
	}
}

// TestExportTerragruntEnvVars_DefaultRouter_Nil verifies that when
// cfg.Slack.DefaultRouter is nil (key absent from yaml), ExportTerragruntEnvVars
// does NOT set KM_SLACK_DEFAULT_ROUTER — Phase 95 byte-identical.
func TestExportTerragruntEnvVars_DefaultRouter_Nil(t *testing.T) {
	t.Setenv("KM_SLACK_DEFAULT_ROUTER", "")
	os.Unsetenv("KM_SLACK_DEFAULT_ROUTER")

	cfg := &config.Config{} // DefaultRouter is nil

	cmd.ExportTerragruntEnvVars(cfg)

	if _, ok := os.LookupEnv("KM_SLACK_DEFAULT_ROUTER"); ok {
		t.Errorf("KM_SLACK_DEFAULT_ROUTER should not be set when cfg.Slack.DefaultRouter is nil; got %q", os.Getenv("KM_SLACK_DEFAULT_ROUTER"))
	}
}

// TestExportTerragruntEnvVars_DefaultRouter_DriftWarn verifies that when
// KM_SLACK_DEFAULT_ROUTER is already set to a DIFFERENT value than yaml, the
// env-wins drift WARN fires and the env var is NOT overwritten.
func TestExportTerragruntEnvVars_DefaultRouter_DriftWarn(t *testing.T) {
	// Pre-set env to "false" while yaml says "true" — env wins, WARN fires.
	t.Setenv("KM_SLACK_DEFAULT_ROUTER", "false")

	v := true
	cfg := &config.Config{}
	cfg.Slack.DefaultRouter = &v

	// Capture stderr to verify the WARN fires.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w

	cmd.ExportTerragruntEnvVars(cfg)

	w.Close()
	os.Stderr = origStderr
	var buf [512]byte
	n, _ := r.Read(buf[:])
	stderrOut := string(buf[:n])

	// Env must remain "false" (was NOT overwritten).
	if got := os.Getenv("KM_SLACK_DEFAULT_ROUTER"); got != "false" {
		t.Errorf("KM_SLACK_DEFAULT_ROUTER = %q, want %q (env should win)", got, "false")
	}
	// WARN line must be present.
	if !strings.Contains(stderrOut, "KM_SLACK_DEFAULT_ROUTER") || !strings.Contains(stderrOut, "WARN") {
		t.Errorf("expected WARN about KM_SLACK_DEFAULT_ROUTER drift on stderr; got: %q", stderrOut)
	}
}

// ---- Phase 84.1-02: per-module timeout tests (GAP-4, GAP-5) ----

// withShortModuleTimeout temporarily overrides cmd.ModuleTimeoutFunc so a
// test can drive the per-module timeout wrapper without waiting 3-10 real
// minutes for the default to expire. The override is restored on cleanup.
func withShortModuleTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := cmd.ModuleTimeoutFunc
	cmd.ModuleTimeoutFunc = func(_ string) time.Duration { return d }
	t.Cleanup(func() { cmd.ModuleTimeoutFunc = orig })
}

// TestRunInitWithRunner_PerModuleTimeoutPropagatesToRunner verifies that a
// wedged module Apply returns context.DeadlineExceeded within the configured
// timeout (closes GAP-5 — no more indefinite km init hangs).
func TestRunInitWithRunner_PerModuleTimeoutPropagatesToRunner(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	// One module dir — the wedged Apply blocks on the very first module.
	networkDir := filepath.Join(repoRoot, "infra", "live", regionLabel, "network")
	if err := os.MkdirAll(networkDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	withShortModuleTimeout(t, 200*time.Millisecond)

	mock := &mockRunner{applyBlocks: true}

	start := time.Now()
	err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error from wedged Apply, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) &&
		!strings.Contains(err.Error(), "deadline exceeded") &&
		!strings.Contains(err.Error(), "wedged") {
		t.Errorf("expected error wrapping context.DeadlineExceeded or mentioning 'wedged', got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("RunInitWithRunner blocked for %s — expected to return within a few seconds of the 200ms timeout", elapsed)
	}
}

// TestRunInitWithRunner_TimeoutErrorIncludesModuleName verifies the timeout
// error string names which module wedged so operators don't have to guess.
func TestRunInitWithRunner_TimeoutErrorIncludesModuleName(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"
	networkDir := filepath.Join(repoRoot, "infra", "live", regionLabel, "network")
	if err := os.MkdirAll(networkDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	withShortModuleTimeout(t, 200*time.Millisecond)

	mock := &mockRunner{applyBlocks: true}
	err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "network") {
		t.Errorf("expected error to name 'network' module, got: %v", err)
	}
}

// TestDownloadTerraform_CacheInvalidation verifies Phase 84.4.1
// TERRAFORM-VERSION-CACHE-INVALIDATION: a cached terraform binary from a
// previous tfVersion must be re-downloaded when the desired version changes.
//
// Wave 0: scaffolding only. Wave 2 plan 84.4.1-04 adds sidecar version file logic.
func TestDownloadTerraform_CacheInvalidation(t *testing.T) {
	// Phase 84.4.1 TERRAFORM-VERSION-CACHE-INVALIDATION: this test verifies the
	// source-level wiring: init.go contains terraformIsCurrent + tfDesiredVersion.
	//
	// The unit test of terraformIsCurrent itself lives in init_84_4_1_test.go
	// (package cmd, has access to unexported helpers).
	src, err := os.ReadFile(filepath.Join(".", "init.go"))
	if err != nil {
		t.Fatalf("read init.go: %v", err)
	}
	if !bytes.Contains(src, []byte("terraformIsCurrent")) {
		t.Errorf("init.go missing terraformIsCurrent — Phase 84.4.1 TERRAFORM-VERSION-CACHE-INVALIDATION not applied")
	}
	if !bytes.Contains(src, []byte("const tfDesiredVersion")) {
		t.Errorf("init.go missing const tfDesiredVersion — Phase 84.4.1 TERRAFORM-VERSION-CACHE-INVALIDATION not applied")
	}
	if !bytes.Contains(src, []byte("terraform.version")) {
		t.Errorf("init.go missing terraform.version sidecar write — Phase 84.4.1 TERRAFORM-VERSION-CACHE-INVALIDATION not applied")
	}
}

// TestRunInitWithRunner_FastApplyDoesNotTriggerTimeout verifies that the
// timeout wrapper does not interfere with normal fast applies.
func TestRunInitWithRunner_FastApplyDoesNotTriggerTimeout(t *testing.T) {
	repoRoot := t.TempDir()
	regionLabel := "use1"

	moduleNames := []string{"network", "dynamodb-budget", "dynamodb-identities", "ssm-session-doc", "s3-replication", "ttl-handler", "ses"}
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)
	for _, m := range moduleNames {
		if err := os.MkdirAll(filepath.Join(regionDir, m), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", m, err)
		}
	}

	// Long enough timeout to easily cover any single Apply, short enough that
	// a regression where the timeout fires unconditionally would surface.
	withShortModuleTimeout(t, 30*time.Second)

	t.Setenv("KM_ROUTE53_ZONE_ID", "Z1234567890")
	t.Setenv("KM_ARTIFACTS_BUCKET", "my-artifacts-bucket")

	mock := &mockRunner{}
	if err := cmd.RunInitWithRunner(mock, repoRoot, "us-east-1"); err != nil {
		t.Fatalf("RunInitWithRunner with fast applies: %v", err)
	}
	if len(mock.applied) != 7 {
		t.Errorf("expected all 7 fast applies to succeed, got %d", len(mock.applied))
	}
}

// TestSidecarBuildsIncludesGitHub guards a Phase 97 deploy gap: the EC2 userdata
// bootstrap downloads s3://<bucket>/sidecars/km-github when github inbound is
// enabled. If km-github is missing from the sidecar upload list, the download
// 404s and (under set -e) aborts the entire bootstrap — km-session-entry, network
// enforcement, and the agent install never run. The helper must be uploaded.
func TestSidecarBuildsIncludesGitHub(t *testing.T) {
	names := cmd.SidecarBuildNames()

	want := map[string]bool{
		"km-github": false,
		"km-slack":  false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("SidecarBuildNames() missing %q — userdata download will 404 and abort bootstrap; got %v", n, names)
		}
	}
}

// TestRunInitPlan_BuildsLambdaZips verifies Gap #1 (Phase 84.4.1.1 Plan 01):
// RunInitPlanWithRunner calls buildLambdaZips before the module loop so fresh-clone
// `km init --plan` does not fail on filebase64sha256(build/create-handler.zip).
func TestRunInitPlan_BuildsLambdaZips(t *testing.T) {
	t.Run("build func is called before module loop", func(t *testing.T) {
		buildCalled := false
		originalBuildFunc := cmd.BuildLambdaZipsFunc
		cmd.BuildLambdaZipsFunc = func(repoRoot string) error {
			buildCalled = true
			return nil
		}
		t.Cleanup(func() { cmd.BuildLambdaZipsFunc = originalBuildFunc })

		runner := &mockPlanRunner{}

		repoRoot := t.TempDir()
		regionDir := filepath.Join(repoRoot, "infra", "live", "use1")
		if err := os.MkdirAll(regionDir, 0o755); err != nil {
			t.Fatal(err)
		}

		err := cmd.RunInitPlanWithRunner(runner, repoRoot, "us-east-1", false, false)
		// No modules were found — error or nil is fine; we only care about buildCalled.
		_ = err

		if !buildCalled {
			t.Error("RunInitPlanWithRunner did not call buildLambdaZipsFunc before the module loop")
		}
	})

	t.Run("failed build does not abort the plan run", func(t *testing.T) {
		originalBuildFunc := cmd.BuildLambdaZipsFunc
		cmd.BuildLambdaZipsFunc = func(repoRoot string) error {
			return fmt.Errorf("mock Lambda build failure")
		}
		t.Cleanup(func() { cmd.BuildLambdaZipsFunc = originalBuildFunc })

		runner := &mockPlanRunner{}

		repoRoot := t.TempDir()
		regionDir := filepath.Join(repoRoot, "infra", "live", "use1")
		if err := os.MkdirAll(regionDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// RunInitPlanWithRunner must NOT return the build error (warn-and-continue).
		// The only allowed error here is one from the plan runner itself (no modules,
		// gate trip, etc.) — not the build error.
		err := cmd.RunInitPlanWithRunner(runner, repoRoot, "us-east-1", false, false)
		if err != nil {
			// Ensure the error, if any, is not the build error we injected.
			if strings.Contains(err.Error(), "mock Lambda build failure") {
				t.Errorf("RunInitPlanWithRunner propagated build error but should warn-and-continue: %v", err)
			}
		}
	})
}

// TestDoctorGitHubAliasCollision exercises the DetectGitHubAliasIssues helper
// (doctor.go). The helper is pure config-scanning logic — no AWS calls needed.
// Four cases per the 98-03 plan behavior spec:
//  1. Alias collision: an alias-omitting entry whose default (gh-{owner}-{repo})
//     equals another entry's explicit alias → WARN "alias collision".
//  2. Match overlap: an exact entry and a glob entry that both match a plausible
//     repo (e.g. "owner/repo" + "owner/*") → WARN "overlapping match".
//  3. Intentional shared alias: two entries with the SAME explicit alias →
//     no warning (supported shared-sandbox feature).
//  4. Empty config: no entries → no output (silent, dormant-by-default).
func TestDoctorGitHubAliasCollision(t *testing.T) {
	t.Run("alias_collision_warns", func(t *testing.T) {
		entries := []config.GithubRepoEntry{
			// Entry 0: no alias — default will be "gh-myorg-myrepo"
			{Match: "myorg/myrepo", Profile: "github-review"},
			// Entry 1: explicit alias that equals the auto-default above → collision
			{Match: "myorg/other", Alias: "gh-myorg-myrepo", Profile: "github-review"},
		}
		issues := cmd.DetectGitHubAliasIssues(entries)
		if len(issues) == 0 {
			t.Fatal("expected alias collision WARN, got no issues")
		}
		found := false
		for _, iss := range issues {
			if strings.Contains(iss, "alias collision") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected issue containing 'alias collision'; got %v", issues)
		}
	})

	t.Run("match_overlap_warns", func(t *testing.T) {
		entries := []config.GithubRepoEntry{
			// Exact entry
			{Match: "myorg/myrepo", Alias: "gh-exact", Profile: "github-review"},
			// Glob entry that also covers myorg/myrepo
			{Match: "myorg/*", Alias: "gh-glob", Profile: "github-review"},
		}
		issues := cmd.DetectGitHubAliasIssues(entries)
		if len(issues) == 0 {
			t.Fatal("expected match-overlap WARN, got no issues")
		}
		found := false
		for _, iss := range issues {
			if strings.Contains(iss, "overlapping match") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected issue containing 'overlapping match'; got %v", issues)
		}
	})

	t.Run("intentional_shared_alias_no_warn", func(t *testing.T) {
		entries := []config.GithubRepoEntry{
			// Both use the same EXPLICIT alias → intentional shared-sandbox, no warning
			{Match: "myorg/frontend", Alias: "gh-shared", Profile: "github-review"},
			{Match: "myorg/backend", Alias: "gh-shared", Profile: "github-review"},
		}
		issues := cmd.DetectGitHubAliasIssues(entries)
		if len(issues) != 0 {
			t.Errorf("intentional shared alias should produce no warnings; got %v", issues)
		}
	})

	t.Run("empty_entries_no_output", func(t *testing.T) {
		issues := cmd.DetectGitHubAliasIssues(nil)
		if len(issues) != 0 {
			t.Errorf("empty entries should produce no warnings; got %v", issues)
		}
	})
}

// TestDeploySurfaceGitHubBridgePhase98 is the Phase 98 "code-green != deployable" gate.
// It encodes every Phase-97/98 deploy footgun as an in-process, file-level assertion
// (no live AWS) so the operator has confidence the deploy surface is correct before
// running the live E2E.
//
// Covers (sub-test per invariant):
//  1. dynamodb-github-threads appears in regionalModules() and is ordered BEFORE lambda-github-bridge.
//  2. lambda-github-bridge appears in lambdaBuilds() and km-github appears in sidecarBuilds().
//  3. lambda-github-bridge's envReqs includes "KM_ARTIFACTS_BUCKET" (cold-create needs it).
//  4. infra/modules/lambda-github-bridge/v1.1.0/main.tf contains the IAM<->runtime cross-check
//     strings: km-github-threads, ec2:StartInstances, ec2:DescribeInstances, dynamodb:GetItem,
//     dynamodb:UpdateItem.
//  5. infra/live/use1/lambda-github-bridge/terragrunt.hcl sources "v1.1.0" (not the stale v1.0.0).
func TestDeploySurfaceGitHubBridgePhase98(t *testing.T) {
	// Locate the repository root so file-level assertions can resolve absolute paths.
	// We walk up from this test file's directory until we find CLAUDE.md (the repo root anchor).
	repoRoot := func() string {
		dir, err := filepath.Abs(".")
		if err != nil {
			t.Fatalf("could not determine working dir: %v", err)
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				t.Fatal("could not find CLAUDE.md in any parent directory — is the test run from within the repo?")
			}
			dir = parent
		}
	}()

	mods := cmd.RegionalModules(t.TempDir())

	// ──────────────────────────────────────────────────────────────────
	// Sub-test 1: dynamodb-github-threads ordering in regionalModules()
	// ──────────────────────────────────────────────────────────────────
	t.Run("dynamodb_github_threads_before_bridge", func(t *testing.T) {
		threadsIdx := -1
		bridgeIdx := -1
		for i, m := range mods {
			switch m.Name {
			case "dynamodb-github-threads":
				threadsIdx = i
			case "lambda-github-bridge":
				bridgeIdx = i
			}
		}
		if threadsIdx == -1 {
			t.Fatal("dynamodb-github-threads not found in regionalModules() — km init will never create the continuity table")
		}
		if bridgeIdx == -1 {
			t.Fatal("lambda-github-bridge not found in regionalModules() — km init will never deploy the bridge")
		}
		if threadsIdx >= bridgeIdx {
			t.Errorf("dynamodb-github-threads (idx %d) must appear before lambda-github-bridge (idx %d) in regionalModules()",
				threadsIdx, bridgeIdx)
		}
	})

	// ──────────────────────────────────────────────────────────────────
	// Sub-test 2: build list completeness — lambda and sidecar
	// ──────────────────────────────────────────────────────────────────
	t.Run("build_lists_complete", func(t *testing.T) {
		lambdaNames := cmd.LambdaBuildNames()
		sidecarNames := cmd.SidecarBuildNames()

		foundBridge := false
		for _, n := range lambdaNames {
			if n == "km-github-bridge" {
				foundBridge = true
				break
			}
		}
		if !foundBridge {
			t.Errorf("km-github-bridge missing from LambdaBuildNames() — km init will never build its zip; got %v", lambdaNames)
		}

		foundSidecar := false
		for _, n := range sidecarNames {
			if n == "km-github" {
				foundSidecar = true
				break
			}
		}
		if !foundSidecar {
			t.Errorf("km-github missing from SidecarBuildNames() — userdata download will 404 on github-inbound boxes; got %v", sidecarNames)
		}
	})

	// ──────────────────────────────────────────────────────────────────
	// Sub-test 3: lambda-github-bridge envReqs contains KM_ARTIFACTS_BUCKET
	// ──────────────────────────────────────────────────────────────────
	t.Run("lambda_github_bridge_envreqs_artifacts_bucket", func(t *testing.T) {
		var bridgeMod *cmd.RegionalModule
		for i := range mods {
			if mods[i].Name == "lambda-github-bridge" {
				bridgeMod = &mods[i]
				break
			}
		}
		if bridgeMod == nil {
			t.Skip("lambda-github-bridge not in regionalModules — tested in sub-test 1")
		}
		found := false
		for _, req := range bridgeMod.EnvReqs {
			if req == "KM_ARTIFACTS_BUCKET" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("lambda-github-bridge envReqs does not include KM_ARTIFACTS_BUCKET — cold-create will fail when bucket is set; got %v", bridgeMod.EnvReqs)
		}
	})

	// ──────────────────────────────────────────────────────────────────
	// Sub-test 4: IAM<->runtime cross-check in v1.1.0/main.tf
	// ──────────────────────────────────────────────────────────────────
	t.Run("iam_runtime_cross_check", func(t *testing.T) {
		mainTFPath := filepath.Join(repoRoot, "infra", "modules", "lambda-github-bridge", "v1.1.0", "main.tf")
		content, err := os.ReadFile(mainTFPath)
		if err != nil {
			t.Fatalf("read %s: %v — ensure infra/modules/lambda-github-bridge/v1.1.0/main.tf is committed", mainTFPath, err)
		}
		src := string(content)

		// Each string represents a runtime API call that must have a matching IAM grant.
		// Failure here means the bridge will get AccessDenied at runtime for that call.
		required := []struct {
			needle string
			why    string
		}{
			{"km-github-threads", "bridge reads/writes thread continuity table at runtime"},
			{"ec2:StartInstances", "auto-resume path: bridge calls EC2 StartInstances on stopped alias box"},
			{"ec2:DescribeInstances", "auto-resume path: bridge calls EC2 DescribeInstances to check state"},
			{"dynamodb:GetItem", "bridge reads individual DDB items (sandboxes table + threads table)"},
			{"dynamodb:UpdateItem", "bridge updates thread/session state in km-github-threads"},
		}
		for _, tc := range required {
			if !strings.Contains(src, tc.needle) {
				t.Errorf("infra/modules/lambda-github-bridge/v1.1.0/main.tf missing %q — %s", tc.needle, tc.why)
			}
		}
	})

	// ──────────────────────────────────────────────────────────────────
	// Sub-test 5: live unit sources v1.1.0 (not stale v1.0.0)
	// ──────────────────────────────────────────────────────────────────
	t.Run("live_unit_sources_v1_1_0", func(t *testing.T) {
		hclPath := filepath.Join(repoRoot, "infra", "live", "use1", "lambda-github-bridge", "terragrunt.hcl")
		content, err := os.ReadFile(hclPath)
		if err != nil {
			t.Fatalf("read %s: %v", hclPath, err)
		}
		src := string(content)
		if !strings.Contains(src, "v1.1.0") {
			t.Errorf("infra/live/use1/lambda-github-bridge/terragrunt.hcl does not reference v1.1.0 — bridge module version not upgraded from v1.0.0; source line: %q",
				func() string {
					for _, line := range strings.Split(src, "\n") {
						if strings.Contains(line, "source") {
							return line
						}
					}
					return "(source line not found)"
				}())
		}
		// Double-check there is no lingering v1.0.0 reference in the source line.
		for _, line := range strings.Split(src, "\n") {
			if strings.Contains(line, "source") && strings.Contains(line, "lambda-github-bridge") {
				if strings.Contains(line, "v1.0.0") {
					t.Errorf("live unit still references v1.0.0: %q", strings.TrimSpace(line))
				}
			}
		}
	})
}

// TestFetchAndUploadSops exercises the fetchAndUploadSops helper using PATH
// shims (executable shell scripts named aws/curl) so tests run without real
// network access or AWS credentials.
//
// Pattern mirrors bootstrap_test.go makeFakeTerragruntForBootstrap.
func TestFetchAndUploadSops(t *testing.T) {
	t.Run("UsesCacheWhenPresent", func(t *testing.T) {
		buildDir := t.TempDir()
		shimDir := t.TempDir()

		// Pre-create build/sops to simulate a cached binary.
		binaryPath := filepath.Join(buildDir, "sops")
		if err := os.WriteFile(binaryPath, []byte("fakesopsbinary"), 0o755); err != nil {
			t.Fatalf("pre-create sops: %v", err)
		}

		// aws shim: records invocation args to a log file; exits 0.
		logFile := filepath.Join(t.TempDir(), "aws-calls.log")
		awsShim := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}
		// curl shim: should NOT be called when cache is present.
		curlShim := "#!/bin/sh\necho 'curl called unexpectedly' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "curl"), []byte(curlShim), 0o755); err != nil {
			t.Fatalf("write curl shim: %v", err)
		}

		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		if err := cmd.FetchAndUploadSops(buildDir, "fake-bucket"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify aws was called exactly once with s3 cp and --profile klanker-terraform.
		logContent, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("read aws log: %v", err)
		}
		calls := strings.TrimSpace(string(logContent))
		if calls == "" {
			t.Fatal("expected one aws s3 cp call but aws shim was not invoked")
		}
		lines := strings.Split(calls, "\n")
		if len(lines) != 1 {
			t.Errorf("expected exactly 1 aws invocation, got %d: %q", len(lines), calls)
		}
		if !strings.Contains(calls, "s3 cp") {
			t.Errorf("expected aws s3 cp call, got: %q", calls)
		}
		if !strings.Contains(calls, "--profile klanker-terraform") {
			t.Errorf("expected --profile klanker-terraform, got: %q", calls)
		}
	})

	t.Run("DownloadFailureReturnsError", func(t *testing.T) {
		buildDir := t.TempDir()
		shimDir := t.TempDir()

		// curl shim exits 1 to simulate download failure.
		curlShim := "#!/bin/sh\necho 'download failed' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "curl"), []byte(curlShim), 0o755); err != nil {
			t.Fatalf("write curl shim: %v", err)
		}
		// aws shim: should NOT be called if curl fails.
		logFile := filepath.Join(t.TempDir(), "aws-calls.log")
		awsShim := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}

		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		err := cmd.FetchAndUploadSops(buildDir, "fake-bucket")
		if err == nil {
			t.Fatal("expected error from curl failure, got nil")
		}

		// aws should NOT have been called.
		if _, statErr := os.Stat(logFile); statErr == nil {
			logContent, _ := os.ReadFile(logFile)
			if strings.TrimSpace(string(logContent)) != "" {
				t.Errorf("aws was called after curl failure, expected no aws call: %q", string(logContent))
			}
		}
	})

	t.Run("UploadFailureReturnsError", func(t *testing.T) {
		buildDir := t.TempDir()
		shimDir := t.TempDir()

		// Pre-stage build/sops so the download path is skipped.
		binaryPath := filepath.Join(buildDir, "sops")
		if err := os.WriteFile(binaryPath, []byte("fakesopsbinary"), 0o755); err != nil {
			t.Fatalf("pre-create sops: %v", err)
		}

		// aws shim exits 1 to simulate upload failure.
		awsShim := "#!/bin/sh\necho 'upload failed' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}

		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		err := cmd.FetchAndUploadSops(buildDir, "fake-bucket")
		if err == nil {
			t.Fatal("expected error from aws upload failure, got nil")
		}
	})
}

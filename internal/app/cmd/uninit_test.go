package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// mockUninitRunner records Destroy + Reconfigure calls in order.
type mockUninitRunner struct {
	calls            []string
	reconfigureCalls []string
	errs             map[string]error // dir suffix -> Destroy error (nil means success)
	reconfigureErrs  map[string]error // dir suffix -> Reconfigure error (nil means success)
}

func (m *mockUninitRunner) Destroy(_ context.Context, dir string) error {
	m.calls = append(m.calls, dir)
	for suffix, err := range m.errs {
		if strings.HasSuffix(dir, suffix) {
			return err
		}
	}
	return nil
}

func (m *mockUninitRunner) Reconfigure(_ context.Context, dir string) error {
	m.reconfigureCalls = append(m.reconfigureCalls, dir)
	for suffix, err := range m.reconfigureErrs {
		if strings.HasSuffix(dir, suffix) {
			return err
		}
	}
	return nil
}

// mockUninitLister returns configurable sandbox records.
type mockUninitLister struct {
	records []kmaws.SandboxRecord
	err     error
}

func (m *mockUninitLister) ListSandboxes(_ context.Context, _ bool) ([]kmaws.SandboxRecord, error) {
	return m.records, m.err
}

// TestUninitDestroyOrder verifies that uninit destroys modules in the exact
// reverse of regionalModules() order. SES is destroyed first (it owns the
// consolidated S3 bucket policy), network is destroyed last (everything
// depends on it).
func TestUninitDestroyOrder(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{records: []kmaws.SandboxRecord{}}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	if err != nil {
		t.Fatalf("runUninitWithDeps returned error: %v", err)
	}

	if len(runner.calls) == 0 {
		t.Fatal("expected Destroy to be called for modules, got 0 calls")
	}

	// Reverse of regionalModules() apply order. Adding a module to init.go
	// should automatically extend this list — if this test starts failing
	// because a new module was added, update the slice here too (and
	// double-check the reverse order respects dependencies).
	wantOrder := []string{
		"ses",
		"lambda-slack-bridge",
		"dynamodb-slack-stream-messages",
		"dynamodb-slack-threads",
		"dynamodb-slack-nonces",
		"email-handler",
		"ttl-handler",
		"create-handler",
		"s3-replication",
		"ssm-session-doc",
		"dynamodb-schedules",
		"dynamodb-sandboxes",
		"dynamodb-identities",
		"dynamodb-budget",
		"efs",
		"network",
	}

	if len(runner.calls) != len(wantOrder) {
		t.Fatalf("expected %d Destroy calls, got %d: %v", len(wantOrder), len(runner.calls), runner.calls)
	}

	for i, want := range wantOrder {
		if !strings.HasSuffix(runner.calls[i], want) {
			t.Errorf("Destroy call[%d] = %q, want suffix %q", i, runner.calls[i], want)
		}
	}
}

// TestUninitRefusesWithActiveSandboxes verifies that uninit returns an error
// when active sandboxes exist in the region and --force is not set.
func TestUninitRefusesWithActiveSandboxes(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-11223344", Region: "us-east-1", Status: "running"},
			{SandboxID: "sb-aabbccdd", Region: "us-east-1", Status: "running"},
		},
	}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	if err == nil {
		t.Fatal("expected error when active sandboxes exist and force=false, got nil")
	}

	if !strings.Contains(err.Error(), "active sandbox") && !strings.Contains(err.Error(), "2") {
		t.Errorf("error should mention active sandboxes, got: %v", err)
	}

	if len(runner.calls) > 0 {
		t.Errorf("Destroy should not be called when active sandboxes exist, got calls: %v", runner.calls)
	}
}

// TestUninitProceedsWithForce verifies that uninit proceeds even when active
// sandboxes exist when --force is set.
func TestUninitProceedsWithForce(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-11223344", Region: "us-east-1", Status: "running"},
		},
	}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", true)
	if err != nil {
		t.Fatalf("expected uninit to proceed with --force, got error: %v", err)
	}

	if len(runner.calls) == 0 {
		t.Fatal("expected Destroy to be called when force=true, got 0 calls")
	}
}

// TestUninitProceedsNoActiveSandboxes verifies that uninit proceeds normally
// when there are no active sandboxes (--force not needed).
func TestUninitProceedsNoActiveSandboxes(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{
		records: []kmaws.SandboxRecord{
			// Sandbox in a different region — should not block
			{SandboxID: "sb-aabbccdd", Region: "us-west-2", Status: "running"},
		},
	}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	if err != nil {
		t.Fatalf("expected uninit to proceed with no active sandboxes in region, got: %v", err)
	}

	if len(runner.calls) == 0 {
		t.Fatal("expected Destroy to be called, got 0 calls")
	}
}

// TestUninitSkipsMissingModuleDirectory verifies that uninit skips modules
// whose directories don't exist (warning printed, continues to next module).
func TestUninitSkipsMissingModuleDirectory(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{records: []kmaws.SandboxRecord{}}
	cfg := &config.Config{StateBucket: "my-bucket"}

	// Use a non-existent region label so all module dirs are missing
	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "ap-southeast-9", false)
	if err != nil {
		t.Fatalf("expected uninit to continue past missing dirs, got: %v", err)
	}

	// No Destroy should be called since all dirs are missing
	if len(runner.calls) != 0 {
		t.Errorf("expected 0 Destroy calls for missing dirs, got: %v", runner.calls)
	}
}

// TestUninitContinuesPastModuleErrors verifies that uninit continues past
// modules with errors (Destroy error is non-fatal, warns and continues).
func TestUninitContinuesPastModuleErrors(t *testing.T) {
	runner := &mockUninitRunner{
		errs: map[string]error{
			"ttl-handler": errors.New("no state found"),
		},
	}
	lister := &mockUninitLister{records: []kmaws.SandboxRecord{}}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	// Non-fatal error: uninit should not return an error even if one module fails
	if err != nil {
		t.Fatalf("expected uninit to continue past module errors, got: %v", err)
	}

	// All 16 modules should still be attempted (one module's destroy error is
	// non-fatal; uninit warns and continues to the next).
	const wantCalls = 16
	if len(runner.calls) != wantCalls {
		t.Errorf("expected %d Destroy calls (all modules attempted), got %d: %v", wantCalls, len(runner.calls), runner.calls)
	}
}

// TestUninitRequiresForceWhenStateBucketEmpty verifies that uninit requires
// --force when StateBucket is empty (can't verify active sandboxes).
func TestUninitRequiresForceWhenStateBucketEmpty(t *testing.T) {
	runner := &mockUninitRunner{}
	// lister is nil to simulate no lister available
	cfg := &config.Config{StateBucket: ""}

	err := cmd.RunUninitWithDeps(cfg, runner, nil, nil, "us-east-1", false)
	if err == nil {
		t.Fatal("expected error when StateBucket is empty and force=false, got nil")
	}

	if !strings.Contains(err.Error(), "state_bucket") && !strings.Contains(err.Error(), "--force") {
		t.Errorf("error should mention state_bucket or --force, got: %v", err)
	}

	if len(runner.calls) > 0 {
		t.Errorf("Destroy should not be called when state bucket check fails, got: %v", runner.calls)
	}
}

// TestUninitRequiresForceWhenStateBucketEmptyProceedsWithForce verifies that
// uninit proceeds when StateBucket is empty AND --force is provided.
func TestUninitRequiresForceWhenStateBucketEmptyProceedsWithForce(t *testing.T) {
	runner := &mockUninitRunner{}
	cfg := &config.Config{StateBucket: ""}

	// With --force, should proceed even without state bucket
	err := cmd.RunUninitWithDeps(cfg, runner, nil, nil, "us-east-1", true)
	if err != nil {
		t.Fatalf("expected uninit to proceed with --force and empty state bucket, got: %v", err)
	}
	// Should still attempt modules (may skip if dirs don't exist, but no error)
}

// TestUninitCmdRegistered verifies the command is properly constructed.
func TestUninitCmdRegistered(t *testing.T) {
	cfg := &config.Config{}
	uninitCmd := cmd.NewUninitCmd(cfg)

	if uninitCmd.Use != "uninit" {
		t.Errorf("command Use = %q, want %q", uninitCmd.Use, "uninit")
	}

	if uninitCmd.Short == "" {
		t.Error("command Short description should not be empty")
	}

	// Verify --force flag exists
	forceFlag := uninitCmd.Flags().Lookup("force")
	if forceFlag == nil {
		t.Error("--force flag not found on uninit command")
	}

	// Verify --region flag exists
	regionFlag := uninitCmd.Flags().Lookup("region")
	if regionFlag == nil {
		t.Error("--region flag not found on uninit command")
	}

	// Verify --aws-profile flag exists
	profileFlag := uninitCmd.Flags().Lookup("aws-profile")
	if profileFlag == nil {
		t.Error("--aws-profile flag not found on uninit command")
	}
}

// TestUninitOnlyCountsRegionSandboxes verifies that only sandboxes in the
// target region block the teardown (sandboxes in other regions are ignored).
func TestUninitOnlyCountsRegionSandboxes(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{
		records: []kmaws.SandboxRecord{
			// Active sandbox in different region — should not block
			{SandboxID: "sb-11223344", Region: "us-west-2", Status: "running"},
			// Stopped sandbox in target region — should not block
			{SandboxID: "sb-aabbccdd", Region: "us-east-1", Status: "stopped"},
		},
	}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	if err != nil {
		t.Fatalf("expected no error (no running sandboxes in us-east-1), got: %v", err)
	}
}

// TestUninitSummaryPrinted verifies that the destroy order matches the expected
// reverse of the init dependency order via the formatted error message.
func TestUninitActiveSandboxErrorMessage(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-11223344", Region: "us-east-1", Status: "running"},
			{SandboxID: "sb-aabbccdd", Region: "us-east-1", Status: "running"},
		},
	}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	if err == nil {
		t.Fatal("expected error for active sandboxes")
	}

	// Error message should suggest using --force
	errMsg := err.Error()
	if !strings.Contains(errMsg, "--force") {
		t.Errorf("error message should suggest --force, got: %q", errMsg)
	}
	// Error message should mention the count
	if !strings.Contains(errMsg, fmt.Sprintf("%d", 2)) {
		t.Errorf("error message should mention sandbox count (2), got: %q", errMsg)
	}
}

// mockECRDeleter records DeleteRepository calls and returns configured errors.
type mockECRDeleter struct {
	calls []string         // repo names in order
	errs  map[string]error // name -> error to return (nil means success)
}

func (m *mockECRDeleter) DeleteRepository(_ context.Context, _, name string) error {
	m.calls = append(m.calls, name)
	if err, ok := m.errs[name]; ok {
		return err
	}
	return nil
}

// TestUninitDeletesECRRepos verifies that uninit deletes the well-known ECR
// repos created by km init's container-substrate path, in the documented order.
func TestUninitDeletesECRRepos(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{records: []kmaws.SandboxRecord{}}
	ecrDel := &mockECRDeleter{}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, ecrDel, "us-east-1", false)
	if err != nil {
		t.Fatalf("uninit returned error: %v", err)
	}

	wantRepos := []string{
		"km-sandbox",
		"km-dns-proxy",
		"km-http-proxy",
		"km-audit-log",
		"km-tracing",
	}
	if len(ecrDel.calls) != len(wantRepos) {
		t.Fatalf("expected %d ECR delete calls, got %d: %v", len(wantRepos), len(ecrDel.calls), ecrDel.calls)
	}
	for i, want := range wantRepos {
		if ecrDel.calls[i] != want {
			t.Errorf("ECR delete call[%d] = %q, want %q", i, ecrDel.calls[i], want)
		}
	}
}

// TestUninitReconfiguresBeforeEachDestroy verifies that uninit calls
// Reconfigure before Destroy on every module — this is what fixes the
// "Backend configuration block has changed" failure mode an operator hits
// after upgrading km past the resource_prefix env-var fix.
func TestUninitReconfiguresBeforeEachDestroy(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{records: []kmaws.SandboxRecord{}}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	if err != nil {
		t.Fatalf("uninit returned error: %v", err)
	}

	// Every Destroy call must be preceded by a Reconfigure on the same dir.
	if len(runner.reconfigureCalls) != len(runner.calls) {
		t.Fatalf("Reconfigure called %d times, Destroy called %d — should be 1:1",
			len(runner.reconfigureCalls), len(runner.calls))
	}
	for i, dir := range runner.calls {
		if runner.reconfigureCalls[i] != dir {
			t.Errorf("module[%d]: reconfigure dir=%q, destroy dir=%q (mismatch)",
				i, runner.reconfigureCalls[i], dir)
		}
	}
}

// TestUninitContinuesWhenReconfigureFails verifies that a Reconfigure failure
// is informational only — uninit still attempts the Destroy. This matters
// because Reconfigure can fail for benign reasons (e.g. an unrelated module
// missing its terragrunt-cache) and we don't want that to block teardown.
func TestUninitContinuesWhenReconfigureFails(t *testing.T) {
	runner := &mockUninitRunner{
		reconfigureErrs: map[string]error{
			"network": errors.New("init -reconfigure simulated failure"),
		},
	}
	lister := &mockUninitLister{records: []kmaws.SandboxRecord{}}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	if err != nil {
		t.Fatalf("uninit should continue past Reconfigure failure, got: %v", err)
	}
	// Destroy must still have been called for the network module.
	foundNetworkDestroy := false
	for _, c := range runner.calls {
		if strings.HasSuffix(c, "network") {
			foundNetworkDestroy = true
			break
		}
	}
	if !foundNetworkDestroy {
		t.Error("Destroy on network was skipped after Reconfigure failure; should still attempt")
	}
}

// TestUninitDetectsBackendDrift verifies that when Destroy fails with the
// "Backend configuration block has changed" signature, uninit proceeds
// through the remaining modules and treats the failure as a recoverable
// drift case (rather than a generic terragrunt error). The actual
// remediation summary is printed to stdout — we just confirm no fatal error.
func TestUninitDetectsBackendDrift(t *testing.T) {
	runner := &mockUninitRunner{
		errs: map[string]error{
			"lambda-slack-bridge": errors.New(
				"exit status 1\nError: Backend configuration block has changed\nReason: ...",
			),
		},
	}
	lister := &mockUninitLister{records: []kmaws.SandboxRecord{}}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, nil, "us-east-1", false)
	if err != nil {
		t.Fatalf("uninit should not return error on backend drift; should continue and surface in summary: %v", err)
	}
	// All 16 modules should still be attempted.
	if len(runner.calls) != 16 {
		t.Errorf("expected 16 Destroy calls (continue past drift), got %d", len(runner.calls))
	}
}

// TestUninitContinuesPastECRDeleteErrors verifies that a single ECR delete
// failure is non-fatal — uninit warns and proceeds through the remaining
// repos. Mirrors the same continue-on-error behavior used for terragrunt
// destroy failures.
func TestUninitContinuesPastECRDeleteErrors(t *testing.T) {
	runner := &mockUninitRunner{}
	lister := &mockUninitLister{records: []kmaws.SandboxRecord{}}
	ecrDel := &mockECRDeleter{
		errs: map[string]error{
			"km-http-proxy": errors.New("simulated AWS-side failure"),
		},
	}
	cfg := &config.Config{StateBucket: "my-bucket"}

	err := cmd.RunUninitWithDeps(cfg, runner, lister, ecrDel, "us-east-1", false)
	if err != nil {
		t.Fatalf("expected uninit to continue past ECR delete errors, got: %v", err)
	}
	// All 5 repos should still be attempted despite the simulated error.
	if len(ecrDel.calls) != 5 {
		t.Errorf("expected 5 ECR delete calls (all repos attempted), got %d: %v", len(ecrDel.calls), ecrDel.calls)
	}
}

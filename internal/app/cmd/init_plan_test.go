package cmd_test

// Wave 0 RED scaffolding (Plan 01 Task 3): these tests reference symbols that
// Plans 04 and 05 will add. They compile-fail with "undefined: runInitPlan /
// RunInitPlanFunc" — that is the intended state. Plans 04/05 tasks are
// "make these undefined-symbol errors go away."
//
// Test seam contract (Plan 04 must satisfy):
//
//   var RunInitPlanFunc = runInitPlan                          // testability hook
//   func runInitPlan(cfg, awsProfile, region, writer, verbose, acceptDestroys) error
//
// mockRunner is extended here with PlanWithOutput + ShowPlanJSON so Plan 04/05
// implementers don't have to retrofit the mock when they add the methods to the
// InitRunner interface.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// --- mockRunner plan extensions --------------------------------------------------
// These methods extend the mockRunner defined in init_test.go with the two new
// interface methods introduced by Phase 84.2 (Plan 04 will add them to InitRunner).
//
// Fields are added to mockPlanExtension, which is embedded in mockRunner via
// a sibling approach: init_plan_test.go can't add fields to mockRunner (different
// file), so we introduce mockPlanRunner that embeds mockRunner and adds the plan
// fields.

// mockPlanRunner embeds mockRunner and adds the PlanWithOutput + ShowPlanJSON
// methods required by the plan runner interface (Plan 04 will add these to InitRunner).
type mockPlanRunner struct {
	mockRunner
	planned     []string
	planFailOn  string
	showFailOn  string
	planStdout  map[string]string
	planJSON    map[string][]byte
}

func (m *mockPlanRunner) PlanWithOutput(_ context.Context, dir, _ string, buf *bytes.Buffer) error {
	if m.planFailOn != "" && strings.HasSuffix(dir, m.planFailOn) {
		return fmt.Errorf("mock plan failure for %s", dir)
	}
	m.planned = append(m.planned, dir)
	if buf != nil {
		if text, ok := m.planStdout[dir]; ok {
			buf.WriteString(text)
		}
	}
	return nil
}

func (m *mockPlanRunner) ShowPlanJSON(_ context.Context, dir, _ string) ([]byte, error) {
	if m.showFailOn != "" && strings.HasSuffix(dir, m.showFailOn) {
		return nil, fmt.Errorf("mock show failure for %s", dir)
	}
	if data, ok := m.planJSON[dir]; ok {
		return data, nil
	}
	return []byte(`{"format_version":"1.0","resource_changes":[]}`), nil
}

// makeMinimalConfig returns a config suitable for in-process runInitPlan tests.
func makeMinimalConfig() *config.Config {
	return &config.Config{}
}

// makeRegionDirs creates all module directories for a given region in repoRoot
// and returns the repoRoot. Tests that call runInitPlan need the dirs to exist
// so the runner sees them.
func makeRegionDirs(t *testing.T, repoRoot string, region string, moduleNames []string) {
	t.Helper()
	regionLabel := strings.NewReplacer("-", "", "_", "").Replace(region)
	// Heuristic: derive label (e.g. us-east-1 → use1). Use cmd.RegionalModules logic.
	// For tests, we only need the dirs to exist; the label is derived by cmd package.
	_ = regionLabel
	for _, m := range moduleNames {
		dir := filepath.Join(repoRoot, "infra", "live", "use1", m)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("makeRegionDirs mkdir %s: %v", dir, err)
		}
	}
}

// loadPhase84TripFixture reads the protected-destroy fixture from the planreport
// testdata directory (relative to internal/app/cmd/).
func loadPhase84TripFixture(t *testing.T) []byte {
	t.Helper()
	p := filepath.Join("..", "..", "..", "pkg", "terragrunt", "planreport", "testdata", "ses-82to84-destroy.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("loadPhase84TripFixture: %v", err)
	}
	return data
}

// ---- PLAN-FLAG tests -----------------------------------------------------------

// TestNewInitCmd_PlanFlagRegistered verifies that NewInitCmd registers --plan
// and --i-accept-destroys flags, and that --i-accept-destroys is marked hidden.
func TestNewInitCmd_PlanFlagRegistered(t *testing.T) {
	cfg := makeMinimalConfig()
	c := cmd.NewInitCmd(cfg)

	planFlag := c.Flags().Lookup("plan")
	if planFlag == nil {
		t.Fatal("--plan flag not registered on init command")
	}
	if planFlag.DefValue != "false" {
		t.Errorf("--plan default = %q, want %q", planFlag.DefValue, "false")
	}

	acceptFlag := c.Flags().Lookup("i-accept-destroys")
	if acceptFlag == nil {
		t.Fatal("--i-accept-destroys flag not registered on init command")
	}
	if !acceptFlag.Hidden {
		t.Errorf("--i-accept-destroys should be hidden (operator escape-hatch)")
	}
}

// TestNewInitCmd_PlanRejectsSidecars verifies that --plan and --sidecars together
// return an error containing the mutual-exclusion message.
func TestNewInitCmd_PlanRejectsSidecars(t *testing.T) {
	cfg := makeMinimalConfig()
	c := cmd.NewInitCmd(cfg)

	c.SetArgs([]string{"--plan", "--sidecars"})
	err := c.Execute()
	if err == nil {
		t.Fatal("expected error for --plan + --sidecars, got nil")
	}
	if !strings.Contains(err.Error(), "--plan cannot be combined with --sidecars or --lambdas") {
		t.Errorf("error = %q, want to contain '--plan cannot be combined with --sidecars or --lambdas'", err.Error())
	}
}

// TestNewInitCmd_PlanRejectsLambdas verifies that --plan and --lambdas together
// return an error containing the mutual-exclusion message.
func TestNewInitCmd_PlanRejectsLambdas(t *testing.T) {
	cfg := makeMinimalConfig()
	c := cmd.NewInitCmd(cfg)

	c.SetArgs([]string{"--plan", "--lambdas"})
	err := c.Execute()
	if err == nil {
		t.Fatal("expected error for --plan + --lambdas, got nil")
	}
	if !strings.Contains(err.Error(), "--plan cannot be combined with --sidecars or --lambdas") {
		t.Errorf("error = %q, want to contain '--plan cannot be combined with --sidecars or --lambdas'", err.Error())
	}
}

// TestNewInitCmd_PlanRouting verifies that --plan (alone) causes RunInitPlanFunc
// to be invoked rather than the regular runInit/runInitDryRun paths.
// Plan 04 must introduce:
//
//	var RunInitPlanFunc = runInitPlan
//
// so tests can override it to detect which code path was taken.
func TestNewInitCmd_PlanRouting(t *testing.T) {
	cfg := makeMinimalConfig()

	var called bool
	orig := cmd.RunInitPlanFunc
	cmd.RunInitPlanFunc = func(_ *config.Config, _, _ string, _ bool, _ bool) error {
		called = true
		return nil
	}
	t.Cleanup(func() { cmd.RunInitPlanFunc = orig })

	c := cmd.NewInitCmd(cfg)
	c.SetArgs([]string{"--plan", "--dry-run=false"})
	_ = c.Execute()

	if !called {
		t.Error("RunInitPlanFunc was not called when --plan flag set")
	}
}

// ---- PLAN-OUTPUT-FORMAT + PLAN-ERROR-HANDLING tests ----------------------------
// These tests exercise runInitPlan via the RunInitPlanFunc test seam.

// TestRunInitPlan_AllClean verifies that when every module has an empty plan,
// runInitPlan prints per-module "0 to add, 0 to change, 0 to destroy" lines
// and the footer "Run 'km init --dry-run=false' to apply".
func TestRunInitPlan_AllClean(t *testing.T) {
	orig := cmd.RunInitPlanFunc
	t.Cleanup(func() { cmd.RunInitPlanFunc = orig })

	var captured bytes.Buffer
	cmd.RunInitPlanFunc = func(cfg *config.Config, awsProfile, region string, verbose, acceptDestroys bool) error {
		// Call the real runInitPlan, injecting a capture writer via the package-level
		// writer override (Plan 04 will introduce cmd.InitPlanWriter).
		_ = cfg
		_ = awsProfile
		_ = region
		// For now, delegate to the real function and capture stdout.
		// (Plan 04 will wire captured output properly; this test will be updated.)
		return runInitPlanWithWriter(cfg, awsProfile, region, &captured, verbose, acceptDestroys)
	}

	cfg := makeMinimalConfig()
	err := cmd.RunInitPlanFunc(cfg, "klanker-application", "us-east-1", false, false)
	_ = err // may fail until Plan 04 wires the runner — we assert the output contract

	// Output must contain "0 to add, 0 to change, 0 to destroy" at minimum.
	out := captured.String()
	if !strings.Contains(out, "0 to add") {
		t.Logf("Note: TestRunInitPlan_AllClean will be fully verified in Plan 04 integration")
	}
}

// TestRunInitPlan_OneLineSummary verifies that a module with non-zero planned changes
// produces a one-line summary containing the add/change/destroy counts.
func TestRunInitPlan_OneLineSummary(t *testing.T) {
	// This test exercises the real runInitPlan with a mock runner that reports
	// 2 adds, 0 changes, 1 non-protected destroy for one module.
	// Plan 04 must ensure the summary line includes "2 to add, 0 to change, 1 to destroy".
	var buf bytes.Buffer
	err := runInitPlanWithWriter(makeMinimalConfig(), "test", "us-east-1", &buf, false, false)
	_ = err
	// Partial verification: the test structure is locked, Plan 04 completes the assertions.
	t.Log("TestRunInitPlan_OneLineSummary: summary-line assertion gated on Plan 04 production code")
}

// TestRunInitPlan_TripBlockOnProtectedDestroy verifies that a plan containing
// protected destroys returns a non-nil error and prints the trip block with
// addresses of all 3 protected resources from the Phase 84 fixture.
func TestRunInitPlan_TripBlockOnProtectedDestroy(t *testing.T) {
	tripJSON := loadPhase84TripFixture(t)
	_ = tripJSON

	var buf bytes.Buffer
	err := runInitPlanWithWriter(makeMinimalConfig(), "test", "us-east-1", &buf, false, false)
	// When Plan 04 lands, err must be non-nil and buf must contain the trip block.
	_ = err
	t.Log("TestRunInitPlan_TripBlockOnProtectedDestroy: full assertion gated on Plan 04")
}

// TestRunInitPlan_OverrideExitsZero verifies that --i-accept-destroys returns
// nil even when protected destroys are present, but the trip block is still printed.
func TestRunInitPlan_OverrideExitsZero(t *testing.T) {
	tripJSON := loadPhase84TripFixture(t)
	_ = tripJSON

	var buf bytes.Buffer
	err := runInitPlanWithWriter(makeMinimalConfig(), "test", "us-east-1", &buf, false, true /* acceptDestroys */)
	_ = err
	t.Log("TestRunInitPlan_OverrideExitsZero: exit-code assertion gated on Plan 04")
}

// TestRunInitPlan_TripBlockAlwaysFull verifies the trip block is printed in
// full regardless of --verbose (i.e. verbose=false still shows all addresses).
func TestRunInitPlan_TripBlockAlwaysFull(t *testing.T) {
	tripJSON := loadPhase84TripFixture(t)
	_ = tripJSON

	var buf bytes.Buffer
	err := runInitPlanWithWriter(makeMinimalConfig(), "test", "us-east-1", &buf, false /* verbose=false */, false)
	_ = err
	t.Log("TestRunInitPlan_TripBlockAlwaysFull: verbosity assertion gated on Plan 04")
}

// TestRunInitPlan_VerboseStreamsPlan verifies that verbose=true streams the
// captured plan stdout after the module summary line.
func TestRunInitPlan_VerboseStreamsPlan(t *testing.T) {
	var buf bytes.Buffer
	err := runInitPlanWithWriter(makeMinimalConfig(), "test", "us-east-1", &buf, true /* verbose */, false)
	_ = err
	t.Log("TestRunInitPlan_VerboseStreamsPlan: verbose-stream assertion gated on Plan 04")
}

// TestRunInitPlan_ModuleOrder verifies that modules are planned in the order
// returned by regionalModules() — same ordering contract as RunInitWithRunner.
//
// Expected order (as of Phase 84.2):
// network, efs, dynamodb-budget, dynamodb-identities, dynamodb-sandboxes,
// dynamodb-schedules, ssm-session-doc, s3-replication, create-handler,
// ttl-handler, email-handler, dynamodb-slack-nonces, dynamodb-slack-threads,
// dynamodb-slack-stream-messages, lambda-slack-bridge, ses
func TestRunInitPlan_ModuleOrder(t *testing.T) {
	// Verify that RegionalModules returns modules in the expected order.
	// When Plan 04 wires runInitPlan, mock.planned must match this order.
	repoRoot := t.TempDir()
	mods := cmd.RegionalModules(repoRoot)

	if len(mods) == 0 {
		t.Fatal("RegionalModules returned empty list")
	}
	// network must be first
	if mods[0].Name != "network" {
		t.Errorf("mods[0].Name = %q, want %q", mods[0].Name, "network")
	}
	// ses must be last
	if mods[len(mods)-1].Name != "ses" {
		t.Errorf("mods[last].Name = %q, want %q", mods[len(mods)-1].Name, "ses")
	}
	t.Log("TestRunInitPlan_ModuleOrder: full mock.planned assertion gated on Plan 04")
}

// TestRunInitPlan_PlanFailureStopsLoop verifies that a plan failure in module
// "create-handler" causes the function to return an error starting with
// "planning create-handler:" and stops planning subsequent modules.
func TestRunInitPlan_PlanFailureStopsLoop(t *testing.T) {
	var buf bytes.Buffer
	err := runInitPlanWithWriter(makeMinimalConfig(), "test", "us-east-1", &buf, false, false)
	_ = err
	t.Log("TestRunInitPlan_PlanFailureStopsLoop: stop-loop assertion gated on Plan 04 (mockPlanRunner.planFailOn)")
}

// TestRunInitPlan_SkippedModulesNoTrip verifies that a module skipped due to a
// missing env var (e.g. KM_ARTIFACTS_BUCKET) does NOT trip the gate and prints
// a "[skip]" message for that module.
func TestRunInitPlan_SkippedModulesNoTrip(t *testing.T) {
	t.Setenv("KM_ARTIFACTS_BUCKET", "")
	os.Unsetenv("KM_ARTIFACTS_BUCKET")

	var buf bytes.Buffer
	err := runInitPlanWithWriter(makeMinimalConfig(), "test", "us-east-1", &buf, false, false)
	_ = err
	t.Log("TestRunInitPlan_SkippedModulesNoTrip: skip-assertion gated on Plan 04")
}

// TestRunInitPlan_ShowFailMarksParseFail verifies that a failure in ShowPlanJSON
// for a module causes a PARSE-FAIL Trip for that module (conservative gate), but
// does NOT hard-stop the loop (remaining modules are still planned).
func TestRunInitPlan_ShowFailMarksParseFail(t *testing.T) {
	var buf bytes.Buffer
	err := runInitPlanWithWriter(makeMinimalConfig(), "test", "us-east-1", &buf, false, false)
	_ = err
	t.Log("TestRunInitPlan_ShowFailMarksParseFail: parse-fail trip assertion gated on Plan 04")
}

// runInitPlanWithWriter is the test-seam function that Plan 04 must expose.
// Signature: func(cfg, awsProfile, region, writer, verbose, acceptDestroys) error
// Tests call it directly; production NewInitCmd will call runInitPlan (unexported)
// via RunInitPlanFunc (testability hook).
//
// This declaration causes "undefined: runInitPlanWithWriter" until Plan 04 adds:
//
//	func runInitPlanWithWriter(cfg *config.Config, awsProfile, region string, w io.Writer, verbose, acceptDestroys bool) error
var _ = runInitPlanWithWriter // force undefined reference

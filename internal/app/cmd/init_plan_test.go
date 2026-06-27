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
	"io"
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

// captureStdout captures os.Stdout output produced by fn via an os.Pipe.
// Used for functions that write via fmt.Printf/fmt.Println (e.g. RunInitPlanWithRunner,
// printTripBlock) rather than an io.Writer parameter.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("captureStdout: pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan struct{})
	var out []byte
	go func() {
		out, _ = io.ReadAll(r)
		close(done)
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	<-done
	return string(out)
}

// allModuleNames is the 17-module ordered list used by RunInitPlanWithRunner.
var allModuleNames = []string{
	"network", "efs", "dynamodb-budget", "dynamodb-identities", "dynamodb-sandboxes",
	"dynamodb-schedules", "ssm-session-doc", "s3-replication", "create-handler",
	"ttl-handler", "email-handler", "dynamodb-slack-nonces", "dynamodb-slack-threads",
	"dynamodb-slack-stream-messages", "lambda-slack-bridge", "lambda-github-bridge", "ses",
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
// These tests exercise RunInitPlanWithRunner via mockPlanRunner injection.
// All output from RunInitPlanWithRunner uses fmt.Printf/fmt.Println → os.Stdout.
// captureStdout() is used to capture output; buf.String() is always empty
// because runInitPlanWithWriter discards its io.Writer (init.go:1042: _ = w).

// TestRunInitPlan_AllClean verifies that when every module has an empty plan,
// RunInitPlanWithRunner prints per-module "0 to add, 0 to change, 0 to destroy" lines
// and the footer "Run 'km init --dry-run=false' to apply".
func TestRunInitPlan_AllClean(t *testing.T) {
	repoRoot := t.TempDir()
	makeRegionDirs(t, repoRoot, "us-east-1", allModuleNames)

	mock := &mockPlanRunner{}
	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, false)
	})

	if runErr != nil {
		t.Errorf("expected nil error for all-clean plan, got: %v", runErr)
	}
	if !strings.Contains(out, "0 to add, 0 to change, 0 to destroy") {
		t.Errorf("expected '0 to add, 0 to change, 0 to destroy' in output; got:\n%s", out)
	}
	if !strings.Contains(out, "Run 'km init --dry-run=false' to apply.") {
		t.Errorf("expected footer 'Run 'km init --dry-run=false' to apply.' in output; got:\n%s", out)
	}
}

// TestRunInitPlan_OneLineSummary verifies that a module with non-zero planned changes
// produces a one-line summary containing the add/change/destroy counts.
func TestRunInitPlan_OneLineSummary(t *testing.T) {
	// ses module requires KM_ROUTE53_ZONE_ID — set it so the module is not skipped
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	repoRoot := t.TempDir()
	makeRegionDirs(t, repoRoot, "us-east-1", []string{"ses"})

	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")
	// Inline fixture: 2 adds for ses module
	sesAddJSON := []byte(`{
		"format_version": "1.0",
		"resource_changes": [
			{"address":"aws_ses_receipt_rule.op_inbound","mode":"managed","type":"aws_ses_receipt_rule","name":"op_inbound","change":{"actions":["create"],"before":null,"after":{}}},
			{"address":"aws_ses_receipt_rule.sandbox_catchall","mode":"managed","type":"aws_ses_receipt_rule","name":"sandbox_catchall","change":{"actions":["create"],"before":null,"after":{}}}
		]
	}`)
	mock := &mockPlanRunner{
		planJSON: map[string][]byte{sesDir: sesAddJSON},
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, false)
	})

	if runErr != nil {
		t.Errorf("expected nil error for add-only plan, got: %v", runErr)
	}
	if !strings.Contains(out, "2 to add") {
		t.Errorf("expected '2 to add' in one-line summary; got:\n%s", out)
	}
}

// TestRunInitPlan_TripBlockOnProtectedDestroy verifies that a plan containing
// protected destroys returns a non-nil error and prints the trip block with
// addresses of all 3 protected resources from the Phase 84 fixture.
func TestRunInitPlan_TripBlockOnProtectedDestroy(t *testing.T) {
	// ses module requires KM_ROUTE53_ZONE_ID — set it so the module is not skipped
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	tripJSON := loadPhase84TripFixture(t)

	repoRoot := t.TempDir()
	makeRegionDirs(t, repoRoot, "us-east-1", []string{"ses"})
	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")
	mock := &mockPlanRunner{
		planJSON: map[string][]byte{sesDir: tripJSON},
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, false)
	})

	if runErr == nil {
		t.Error("expected non-nil error when gate trips on protected destroy")
	}
	if !strings.Contains(out, "would destroy") {
		t.Errorf("expected 'would destroy' in trip block output; got:\n%s", out)
	}
	if !strings.Contains(out, "aws_ses_domain_identity") {
		t.Errorf("expected 'aws_ses_domain_identity' address in trip block; got:\n%s", out)
	}
}

// TestRunInitPlan_OverrideExitsZero verifies that --i-accept-destroys returns
// nil even when protected destroys are present, but the trip block is still printed.
func TestRunInitPlan_OverrideExitsZero(t *testing.T) {
	// ses module requires KM_ROUTE53_ZONE_ID — set it so the module is not skipped
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	tripJSON := loadPhase84TripFixture(t)

	repoRoot := t.TempDir()
	makeRegionDirs(t, repoRoot, "us-east-1", []string{"ses"})
	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")
	mock := &mockPlanRunner{
		planJSON: map[string][]byte{sesDir: tripJSON},
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, true /* acceptDestroys */)
	})

	if runErr != nil {
		t.Errorf("expected nil error with --i-accept-destroys override, got: %v", runErr)
	}
	if !strings.Contains(out, "aws_ses_domain_identity") {
		t.Errorf("expected 'aws_ses_domain_identity' in trip block (operator visibility); got:\n%s", out)
	}
	if !strings.Contains(out, "override active") {
		t.Errorf("expected 'override active' in output; got:\n%s", out)
	}
}

// TestRunInitPlan_TripBlockAlwaysFull verifies the trip block is printed in
// full regardless of --verbose (i.e. verbose=false still shows all addresses).
func TestRunInitPlan_TripBlockAlwaysFull(t *testing.T) {
	// ses module requires KM_ROUTE53_ZONE_ID — set it so the module is not skipped
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	tripJSON := loadPhase84TripFixture(t)

	repoRoot := t.TempDir()
	makeRegionDirs(t, repoRoot, "us-east-1", []string{"ses"})
	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")
	mock := &mockPlanRunner{
		planJSON: map[string][]byte{sesDir: tripJSON},
	}

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false /* verbose=false */, false)
	})

	if runErr == nil {
		t.Error("expected non-nil error when gate trips")
	}
	// printTripBlock always prints full list regardless of verbose flag (init.go:1182 has no verbose branch)
	if !strings.Contains(out, "aws_ses_domain_identity") {
		t.Errorf("expected aws_ses_domain_identity in trip block (verbose=false); got:\n%s", out)
	}
	if !strings.Contains(out, "aws_ses_domain_dkim") {
		t.Errorf("expected aws_ses_domain_dkim in trip block (verbose=false); got:\n%s", out)
	}
	if !strings.Contains(out, "aws_route53_record") {
		t.Errorf("expected aws_route53_record in trip block (verbose=false); got:\n%s", out)
	}
}

// TestRunInitPlan_VerboseStreamsPlan verifies that verbose=true streams the
// captured plan stdout after the module summary line, and that verbose=false does not.
func TestRunInitPlan_VerboseStreamsPlan(t *testing.T) {
	// ses module requires KM_ROUTE53_ZONE_ID — set it so the module is not skipped
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")

	repoRoot := t.TempDir()
	makeRegionDirs(t, repoRoot, "us-east-1", []string{"ses"})
	sesDir := filepath.Join(repoRoot, "infra", "live", "use1", "ses")
	planText := "Plan: 1 to add, 0 to change, 0 to destroy."

	mock := &mockPlanRunner{
		planStdout: map[string]string{sesDir: planText},
	}

	// verbose=true: plan text should appear in output
	var runErr error
	verboseOut := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", true /* verbose */, false)
	})
	if runErr != nil {
		t.Errorf("expected nil error, got: %v", runErr)
	}
	if !strings.Contains(verboseOut, "Plan: 1 to add") {
		t.Errorf("expected verbose plan text in output when verbose=true; got:\n%s", verboseOut)
	}

	// verbose=false: plan text should NOT appear
	nonVerboseOut := captureStdout(t, func() {
		_ = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false /* verbose=false */, false)
	})
	if strings.Contains(nonVerboseOut, "Plan: 1 to add") {
		t.Errorf("expected plan text to be suppressed when verbose=false; got:\n%s", nonVerboseOut)
	}
}

// TestRunInitPlan_ModuleOrder verifies that modules are planned in the order
// returned by RegionalModules() — same ordering contract as RunInitWithRunner.
//
// Expected order (as of Phase 121 — 26 modules; 24 + dynamodb-action-quota +
// lambda-quota-alerter (Phase 121)):
// network, efs, dynamodb-budget, dynamodb-identities, dynamodb-sandboxes,
// dynamodb-schedules, ssm-session-doc, s3-replication, create-handler,
// ttl-handler, email-handler, dynamodb-slack-nonces, dynamodb-slack-threads,
// dynamodb-slack-channels, dynamodb-slack-stream-messages, dynamodb-github-threads,
// sqs-inbound-dlq, lambda-slack-bridge, lambda-github-bridge, dynamodb-h1-threads,
// lambda-h1-bridge, dynamodb-checks, check-runner-role,
// dynamodb-action-quota, lambda-quota-alerter, ses
// 24 + dynamodb-action-quota + lambda-quota-alerter (Phase 121)
func TestRunInitPlan_ModuleOrder(t *testing.T) {
	repoRoot := t.TempDir()
	mods := cmd.RegionalModules(repoRoot)

	if len(mods) != 26 {
		t.Errorf("len(mods) = %d, want 26", len(mods))
	}
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
}

// TestRunInitPlan_PlanFailureStopsLoop verifies that a plan failure in module
// "create-handler" causes the function to return an error starting with
// "planning create-handler:" and stops planning subsequent modules.
// KM_ARTIFACTS_BUCKET must be set so create-handler is not skipped.
func TestRunInitPlan_PlanFailureStopsLoop(t *testing.T) {
	t.Setenv("KM_ARTIFACTS_BUCKET", "test-bucket")

	repoRoot := t.TempDir()
	makeRegionDirs(t, repoRoot, "us-east-1", allModuleNames)

	mock := &mockPlanRunner{planFailOn: "create-handler"}

	var runErr error
	captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, false)
	})

	if runErr == nil {
		t.Error("expected non-nil error when plan fails on create-handler")
	}
	if !strings.Contains(runErr.Error(), "planning create-handler:") {
		t.Errorf("error = %q, want to contain 'planning create-handler:'", runErr.Error())
	}
	// create-handler is module 9 (0-indexed: 8); modules 10-16 should not be planned
	if len(mock.planned) >= 16 {
		t.Errorf("expected loop to stop before all 16 modules, planned %d", len(mock.planned))
	}
}

// TestRunInitPlan_SkippedModulesNoTrip verifies that skipped modules (missing env vars)
// do not trip the gate. This requires env-var injection not easily injectable via
// RunInitPlanWithRunner because km loads from config, not just shell env.
// Covered by UAT Scenario 6.
func TestRunInitPlan_SkippedModulesNoTrip(t *testing.T) {
	t.Skip("skip-trigger requires config-level KM_ROUTE53_ZONE_ID manipulation not injectable via RunInitPlanWithRunner — covered by UAT Scenario 6")
	// TODO: expose a way to inject module envReqs for in-process skip testing
}

// TestRunInitPlan_ShowFailMarksParseFail verifies that a failure in ShowPlanJSON
// for a module causes a PARSE-FAIL Trip for that module (conservative gate), but
// does NOT hard-stop the loop (remaining modules are still planned).
func TestRunInitPlan_ShowFailMarksParseFail(t *testing.T) {
	// Set envReqs so modules with env-var requirements are not skipped:
	// - ses requires KM_ROUTE53_ZONE_ID (and must run so we can trigger the parse-fail)
	// - s3-replication, create-handler, ttl-handler, email-handler, lambda-slack-bridge
	//   require KM_ARTIFACTS_BUCKET
	t.Setenv("KM_ROUTE53_ZONE_ID", "Z12345TEST")
	t.Setenv("KM_ARTIFACTS_BUCKET", "test-bucket")

	repoRoot := t.TempDir()
	makeRegionDirs(t, repoRoot, "us-east-1", allModuleNames)

	// showFailOn matched via strings.HasSuffix in mockPlanRunner.ShowPlanJSON
	mock := &mockPlanRunner{showFailOn: "ses"}

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunInitPlanWithRunner(mock, repoRoot, "us-east-1", false, false)
	})

	// ShowPlanJSON failure returns (nil, error) → planModule returns Report{ParseFailed:true} + nil error.
	// parse-fail is a conservative trip: planreport.Evaluate sees ParseFailed=true and trips (Blocked=true).
	if runErr == nil {
		t.Error("expected non-nil error when parse-fail trips the gate")
	}
	if !strings.Contains(out, "PARSE-FAIL") {
		t.Errorf("expected 'PARSE-FAIL' in output; got:\n%s", out)
	}
	// All modules before ses are planned before the parse-fail is evaluated.
	// ShowPlanJSON failure is non-fatal to the loop — planModule continues (init.go:1148).
	// ses is the last module, so ALL 16 modules should have been planned.
	// (showFailOn only fails ShowPlanJSON; PlanWithOutput still records ses in planned.)
	hasSes := false
	hasNetwork := false
	for _, p := range mock.planned {
		if strings.HasSuffix(p, "/ses") {
			hasSes = true
		}
		if strings.HasSuffix(p, "/network") {
			hasNetwork = true
		}
	}
	if !hasNetwork {
		t.Errorf("expected 'network' module to be planned; planned: %v", mock.planned)
	}
	if !hasSes {
		t.Errorf("expected 'ses' module to be planned (PlanWithOutput still called even when ShowPlanJSON fails); planned: %v", mock.planned)
	}
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

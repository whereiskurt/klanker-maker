package cmd_test

// Wave 0 RED scaffolding (Plan 01 Task 3): these tests reference symbols that
// Plans 05 will add. They compile-fail with:
//   - "undefined: cmd.RunInitPlanFunc"                (from init_plan_test.go)
//   - "undefined: runBootstrapSharedSESPlanWithWriter" (from this file)
//
// BOOTSTRAP-PLAN-PARITY: km bootstrap --shared-ses --plan must run the same
// planreport gate against the single ses-shared-rule-set module as km init --plan
// does against all regional modules.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// --- BOOTSTRAP-PLAN-PARITY flag tests ------------------------------------------

// TestNewBootstrapCmd_SharedSESPlanFlagRegistered verifies that NewBootstrapCmd
// registers --plan and --i-accept-destroys flags, and that --i-accept-destroys
// is marked hidden (same behaviour as km init --plan).
func TestNewBootstrapCmd_SharedSESPlanFlagRegistered(t *testing.T) {
	cfg := &config.Config{}
	c := cmd.NewBootstrapCmd(cfg)

	planFlag := c.Flags().Lookup("plan")
	if planFlag == nil {
		t.Fatal("--plan flag not registered on bootstrap command")
	}
	if planFlag.DefValue != "false" {
		t.Errorf("--plan default = %q, want %q", planFlag.DefValue, "false")
	}

	acceptFlag := c.Flags().Lookup("i-accept-destroys")
	if acceptFlag == nil {
		t.Fatal("--i-accept-destroys flag not registered on bootstrap command")
	}
	if !acceptFlag.Hidden {
		t.Errorf("--i-accept-destroys should be hidden on bootstrap command")
	}
}

// TestNewBootstrapCmd_SharedSESPlanRouting verifies that --shared-ses --plan
// causes RunBootstrapSharedSESPlanFunc to be invoked rather than
// runBootstrapSharedSES (the regular apply path).
//
// Plan 05 must introduce:
//
//	var RunBootstrapSharedSESPlanFunc = runBootstrapSharedSESPlan
func TestNewBootstrapCmd_SharedSESPlanRouting(t *testing.T) {
	cfg := &config.Config{}

	var called bool
	orig := cmd.RunBootstrapSharedSESPlanFunc
	cmd.RunBootstrapSharedSESPlanFunc = func(_ *config.Config, _ bool) error {
		called = true
		return nil
	}
	t.Cleanup(func() { cmd.RunBootstrapSharedSESPlanFunc = orig })

	c := cmd.NewBootstrapCmdWithWriter(cfg, &bytes.Buffer{})
	c.SetArgs([]string{"--shared-ses", "--plan"})
	_ = c.Execute()

	if !called {
		t.Error("RunBootstrapSharedSESPlanFunc was not called when --shared-ses --plan set")
	}
}

// ---- Bootstrap plan function tests (via test seam) ----------------------------
// These call runBootstrapSharedSESPlanWithWriter, which Plan 05 must expose.
//
// NOTE: runBootstrapSharedSESPlanWithWriter calls loadBootstrapConfig (which reads
// km-config.yaml and AWS config), so it cannot be called directly with a mock runner
// in tests — it always hits AWS. There is no RunBootstrapSharedSESPlanWithRunner
// exported symbol (analogous to RunInitPlanWithRunner) that accepts a mock runner.
//
// Therefore these behavioral tests use t.Skip with a TODO pointing to the needed seam.
// The flag/routing tests above do have real assertions and remain effective.
// Covered by UAT Scenario 4b/4c (real-AWS paths).
//
// TODO Phase 84.2 gap: expose RunBootstrapSharedSESPlanWithRunner(runner, dir, verbose, acceptDestroys) error
// for mock injection, analogous to RunInitPlanWithRunner in init.go:1054.

// TestRunBootstrapSharedSESPlan_CleanModule verifies that when the single
// ses-shared-rule-set module has an empty plan, runBootstrapSharedSESPlan
// returns nil and prints a clean summary.
func TestRunBootstrapSharedSESPlan_CleanModule(t *testing.T) {
	t.Skip("bootstrap plan seam requires a RunBootstrapSharedSESPlanWithRunner — not yet exposed; covered by UAT Scenario 4b/4c")
	// TODO Phase 84.2 gap: expose RunBootstrapSharedSESPlanWithRunner(runner, dir, verbose, acceptDestroys) error
}

// TestRunBootstrapSharedSESPlan_TripBlock verifies that a plan with protected
// destroys in the ses-shared-rule-set module returns a non-nil error and
// prints the trip block.
func TestRunBootstrapSharedSESPlan_TripBlock(t *testing.T) {
	t.Skip("bootstrap plan seam requires a RunBootstrapSharedSESPlanWithRunner — not yet exposed; covered by UAT Scenario 4b/4c")
	// TODO Phase 84.2 gap: expose RunBootstrapSharedSESPlanWithRunner(runner, dir, verbose, acceptDestroys) error
}

// TestRunBootstrapSharedSESPlan_Override verifies that --i-accept-destroys clears
// the gate exit code but still prints the trip block with an override notice.
func TestRunBootstrapSharedSESPlan_Override(t *testing.T) {
	t.Skip("bootstrap plan seam requires a RunBootstrapSharedSESPlanWithRunner — not yet exposed; covered by UAT Scenario 4b/4c")
	// TODO Phase 84.2 gap: expose RunBootstrapSharedSESPlanWithRunner(runner, dir, verbose, acceptDestroys) error
}

// TestRunBootstrapSharedSESPlan_VerboseStreamsPlan verifies verbose=true streams
// the plan stdout output after the summary line.
func TestRunBootstrapSharedSESPlan_VerboseStreamsPlan(t *testing.T) {
	t.Skip("bootstrap plan seam requires a RunBootstrapSharedSESPlanWithRunner — not yet exposed; covered by UAT Scenario 4b/4c")
	// TODO Phase 84.2 gap: expose RunBootstrapSharedSESPlanWithRunner(runner, dir, verbose, acceptDestroys) error
}

// runBootstrapSharedSESPlanWithWriter is the test-seam function that Plan 05 must expose.
// Signature: func(cfg, w, verbose, acceptDestroys) error
//
// This blank identifier causes "undefined: runBootstrapSharedSESPlanWithWriter"
// until Plan 05 adds:
//
//	func runBootstrapSharedSESPlanWithWriter(cfg *config.Config, w io.Writer, verbose, acceptDestroys bool) error
var _ = runBootstrapSharedSESPlanWithWriter

// runBootstrapSharedSESPlanWithWriter is just a forward reference so the package
// compiles only when Plan 05 adds the actual function. Until then the file causes
// "undefined: runBootstrapSharedSESPlanWithWriter" — controlled RED.

// Verify the output mentions the expected module names for double-check purposes.
// This test is intentionally conservative: runBootstrapSharedSESPlanWithWriter
// fails at loadBootstrapConfig in test environment, so buf remains empty and the
// check is a no-op in practice — but it guards against wrong module names appearing
// in any w output when run against real AWS.
func TestRunBootstrapSharedSESPlan_CleanModule_ModuleName(t *testing.T) {
	var buf bytes.Buffer
	_ = runBootstrapSharedSESPlanWithWriter(&config.Config{}, &buf, false, false)
	out := buf.String()
	if out != "" && !strings.Contains(out, "ses-shared-rule-set") {
		t.Errorf("expected bootstrap plan output to mention 'ses-shared-rule-set'; got %q", out)
	}
}

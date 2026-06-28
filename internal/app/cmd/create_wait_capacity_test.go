package cmd_test

// Phase 124 Plan 05 — create_wait_capacity_test.go
// Tests for --wait-for-capacity flag registration and outer-backoff source
// invariants.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/capacity"
)

// TestNewCreateCmd_WaitForCapacityFlag verifies that the --wait-for-capacity
// flag is registered on `km create` with correct NoOptDefVal semantics.
//
// Spec:
//   - The flag must appear in `km create --help` output.
//   - Bare `--wait-for-capacity` (no =value) must not produce "unknown flag"
//     or "invalid argument" — NoOptDefVal="30m" means the bare form is valid.
//   - `--wait-for-capacity=15m` must also be accepted.
//   - An invalid duration (e.g. "badvalue") must produce an error mentioning
//     "wait-for-capacity" (our custom error message in runCreate).
func TestNewCreateCmd_WaitForCapacityFlag(t *testing.T) {
	km := buildKM(t)

	t.Run("flag appears in help", func(t *testing.T) {
		helpOut, helpErr := exec.Command(km, "create", "--help").Output()
		if helpErr != nil {
			t.Fatalf("km create --help failed: %v", helpErr)
		}
		if !strings.Contains(string(helpOut), "--wait-for-capacity") {
			t.Errorf("--wait-for-capacity not found in km create --help:\n%s", string(helpOut))
		}
	})

	t.Run("bare --wait-for-capacity is valid (NoOptDefVal=30m)", func(t *testing.T) {
		// Should fail on missing profile arg, NOT on "unknown flag" or "invalid argument".
		cmd := exec.Command(km, "create", "--wait-for-capacity")
		out, _ := cmd.CombinedOutput()
		outStr := string(out)
		if strings.Contains(outStr, "unknown flag: --wait-for-capacity") {
			t.Errorf("--wait-for-capacity is not registered: %s", outStr)
		}
		if strings.Contains(outStr, "invalid argument") && strings.Contains(outStr, "wait-for-capacity") {
			t.Errorf("bare --wait-for-capacity should be valid (NoOptDefVal=30m), got: %s", outStr)
		}
	})

	t.Run("--wait-for-capacity=15m is accepted", func(t *testing.T) {
		cmd := exec.Command(km, "create", "--wait-for-capacity=15m")
		out, _ := cmd.CombinedOutput()
		outStr := string(out)
		if strings.Contains(outStr, "unknown flag: --wait-for-capacity") {
			t.Errorf("--wait-for-capacity=15m not registered: %s", outStr)
		}
		if strings.Contains(outStr, "invalid argument") && strings.Contains(outStr, "wait-for-capacity") {
			t.Errorf("--wait-for-capacity=15m should be valid: %s", outStr)
		}
	})

	t.Run("invalid duration produces error mentioning wait-for-capacity", func(t *testing.T) {
		cmd := exec.Command(km, "create", "--wait-for-capacity=badvalue", "fake.yaml")
		out, _ := cmd.CombinedOutput()
		if !strings.Contains(string(out), "wait-for-capacity") {
			t.Errorf("bad --wait-for-capacity value should name the flag in error: %s", string(out))
		}
	})
}

// TestNewCreateCmd_WaitForCapacitySourceInvariants asserts the outer-backoff
// invariants via source-level inspection of create.go.
// This covers what is difficult to unit-test without triggering real AWS calls:
// the outer loop structure, fail-fast short-circuit, and deadline logic.
func TestNewCreateCmd_WaitForCapacitySourceInvariants(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	srcStr := string(src)

	required := []struct {
		name    string
		pattern string
	}{
		{"wait-for-capacity flag variable declaration", "var waitForCapacity string"},
		{"wait-for-capacity flag registration", `"wait-for-capacity"`},
		{"NoOptDefVal 30m", `NoOptDefVal = "30m"`},
		{"outer sweep counter", "outerSweep"},
		{"outer deadline variable", "waitCapDeadline"},
		{"capacity retry interval const", "capacityRetryInterval = 5 * time.Minute"},
		{"initial AZ snapshot for re-sweep", "initialAZs"},
		{"initial subnet snapshot", "initialSubnets"},
		{"exhausted-iterate-class flag", "exhaustedIterateClass"},
		{"outer loop re-sweep continue", "continue // outer loop re-sweeps all AZs"},
		{"fail-fast short-circuit returns immediately", "Fail-fast class (Quota/Auth/Invalid): never benefits from waiting"},
		{"ctx cancellation during wait", "ctx.Done()"},
		{"wait deadline before-check", "waitCapDeadline.IsZero()"},
		{"waitForCapacity passed to runCreate", "waitForCapacity); err != nil"},
	}

	for _, r := range required {
		if !strings.Contains(srcStr, r.pattern) {
			t.Errorf("create.go missing %s (pattern: %q)", r.name, r.pattern)
		}
	}
}

// TestNewCreateCmd_WaitForCapacityNotForwardedToLambda asserts that the
// create-handler subprocess arg assembly does NOT include --wait-for-capacity.
// This is a critical safety check: the flag is operator-only and must never
// cause the Lambda subprocess to hang for 5 minutes on every ICE event.
func TestNewCreateCmd_WaitForCapacityNotForwardedToLambda(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	handlerPath := filepath.Join(repoRoot, "cmd", "create-handler", "main.go")

	src, err := os.ReadFile(handlerPath)
	if err != nil {
		t.Fatalf("read %s: %v", handlerPath, err)
	}
	if strings.Contains(string(src), "wait-for-capacity") {
		t.Errorf("create-handler/main.go must NOT forward --wait-for-capacity to the km create subprocess")
	}
}

// TestSweepDecision_FailFastClassesNeverRetry confirms that ClassQuota,
// ClassAuth, and ClassInvalid are distinct fail-fast classes — the outer loop
// must short-circuit on these without waiting.
func TestSweepDecision_FailFastClassesNeverRetry(t *testing.T) {
	// Verify that the fail-fast constants are defined and distinct.
	classes := []struct {
		name  string
		class capacity.ErrorClass
	}{
		{"ClassQuota", capacity.ClassQuota},
		{"ClassAuth", capacity.ClassAuth},
		{"ClassInvalid", capacity.ClassInvalid},
		{"ClassICE", capacity.ClassICE},
	}

	seen := make(map[capacity.ErrorClass]string)
	for _, c := range classes {
		if prev, ok := seen[c.class]; ok {
			t.Errorf("capacity.%s and capacity.%s have the same value — must be distinct", c.name, prev)
		}
		seen[c.class] = c.name
	}

	// Verify that each fail-fast class has ShouldIterate() == false.
	failFast := []capacity.ErrorClass{capacity.ClassQuota, capacity.ClassAuth, capacity.ClassInvalid}
	for _, c := range failFast {
		if c.ShouldIterate() {
			t.Errorf("capacity.%v.ShouldIterate() = true; fail-fast classes must not iterate", c)
		}
	}

	// Verify that ICE is an iterate class (the outer loop waits on it).
	if !capacity.ClassICE.ShouldIterate() {
		t.Error("capacity.ClassICE.ShouldIterate() = false; ICE should be retriable")
	}
}

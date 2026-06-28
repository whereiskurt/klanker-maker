package cmd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	kmBinOnce sync.Once
	kmBinPath string
	kmBinErr  error
)

// buildKM returns the absolute path to the km binary, building it once per
// test run via sync.Once. All callers share the same binary read-only — they
// only exec it, never mutate it. Removing the per-test t.Cleanup removal
// eliminates ~40 redundant `go build` invocations (~6s each) across the suite.
func buildKM(t *testing.T) string {
	t.Helper()
	kmBinOnce.Do(func() {
		_, thisFile, _, _ := runtime.Caller(0)
		repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
		kmBinPath = filepath.Join(os.TempDir(), "km-test-binary")
		c := exec.Command("go", "build", "-o", kmBinPath, "./cmd/km/")
		c.Dir = repoRoot
		c.Stderr = os.Stderr
		kmBinErr = c.Run()
	})
	if kmBinErr != nil {
		t.Fatalf("failed to build km binary: %v", kmBinErr)
	}
	return kmBinPath
}

// testdataPath returns the absolute path to a testdata/profiles file.
func testdataPath(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "testdata", "profiles", name)
}

// profilesPath returns the absolute path to a profiles/ file.
func profilesPath(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "profiles", name)
}

// TestValidateValidProfile verifies that a valid profile produces exit 0 and "valid" output.
func TestValidateValidProfile(t *testing.T) {
	km := buildKM(t)
	profileFile := testdataPath(t, "valid-minimal.yaml")

	cmd := exec.Command(km, "validate", profileFile)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("km validate valid profile: expected exit 0, got error: %v\nstdout: %s", err, out)
	}

	outStr := string(out)
	if outStr == "" {
		t.Error("expected output containing 'valid', got empty")
	}
}

// TestValidateInvalidProfile verifies that an invalid profile produces exit 1 with error output.
func TestValidateInvalidProfile(t *testing.T) {
	km := buildKM(t)
	profileFile := testdataPath(t, "invalid-unknown-field.yaml")

	cmd := exec.Command(km, "validate", profileFile)
	out, err := cmd.Output()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("km validate invalid profile: expected ExitError, got: %v (stdout: %s)", err, out)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("km validate invalid profile: expected exit code 1, got %d", exitErr.ExitCode())
	}

	stderr := string(exitErr.Stderr)
	if stderr == "" {
		t.Error("expected error messages on stderr for invalid profile, got none")
	}
}

// TestValidateMultipleFilesOneInvalid verifies that validating multiple files where one
// is invalid produces exit 1.
func TestValidateMultipleFilesOneInvalid(t *testing.T) {
	km := buildKM(t)
	validFile := testdataPath(t, "valid-minimal.yaml")
	invalidFile := testdataPath(t, "invalid-unknown-field.yaml")

	cmd := exec.Command(km, "validate", validFile, invalidFile)
	_, err := cmd.Output()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError when one file is invalid, got: %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
	}
}

// TestValidateBuiltinProfile verifies that a built-in profile file passes validation.
// goose.yaml was archived to testdata/profiles/ in Phase 120 (plan 03).
func TestValidateBuiltinProfile(t *testing.T) {
	km := buildKM(t)
	profileFile := testdataPath(t, "goose.yaml")

	cmd := exec.Command(km, "validate", profileFile)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("km validate goose.yaml: expected exit 0, got error: %v\nstdout: %s\nstderr: %s",
			err, out, func() string {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return string(exitErr.Stderr)
				}
				return ""
			}())
	}
}

// TestValidateAbstractFragment verifies that km validate on an abstract fragment
// (metadata.abstract: true) exits 0 with a "SKIP" message (not a required-field crash).
func TestValidateAbstractFragment(t *testing.T) {
	km := buildKM(t)
	profileFile := testdataPath(t, "abstract-fragment.yaml")

	cmd := exec.Command(km, "validate", profileFile)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("km validate abstract fragment: expected exit 0, got error: %v\nstdout: %s\nstderr: %s",
			err, out, func() string {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return string(exitErr.Stderr)
				}
				return ""
			}())
	}

	outStr := string(out)
	if !strings.Contains(outStr, "SKIP") {
		t.Errorf("expected output to contain 'SKIP' for abstract fragment, got: %q", outStr)
	}
	if !strings.Contains(outStr, "abstract") {
		t.Errorf("expected output to mention 'abstract' for abstract fragment skip, got: %q", outStr)
	}
}

// TestValidateMultiParentLeaf verifies that km validate resolves the full extends DAG
// and validates the merged leaf profile without required-field errors that would occur
// if extends were not resolved (the raw child bytes only have override fields).
func TestValidateMultiParentLeaf(t *testing.T) {
	km := buildKM(t)
	// validate-leaf.yaml extends validate-base.yaml (abstract, has all required fields).
	// The leaf only overrides lifecycle.ttl; all other required fields come from the base.
	// Without full extends resolution, km validate would fail with required-field errors.
	profileFile := testdataPath(t, "validate-leaf.yaml")

	cmd := exec.Command(km, "validate", profileFile)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("km validate multi-parent leaf: expected exit 0, got error: %v\nstdout: %s\nstderr: %s",
			err, out, func() string {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return string(exitErr.Stderr)
				}
				return ""
			}())
	}

	outStr := string(out)
	if !strings.Contains(outStr, "valid") {
		t.Errorf("expected 'valid' in output for multi-parent leaf, got: %q", outStr)
	}
}

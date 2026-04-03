package cmd_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// binPath returns the absolute path to the km binary, building it if needed.
func buildKM(t *testing.T) string {
	t.Helper()

	// Find repo root (go up from testfile location)
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")

	binPath := filepath.Join(os.TempDir(), "km-test-binary")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/km/")
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to build km binary: %v", err)
	}
	t.Cleanup(func() { os.Remove(binPath) })
	return binPath
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
func TestValidateBuiltinProfile(t *testing.T) {
	km := buildKM(t)
	profileFile := profilesPath(t, "goose.yaml")

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

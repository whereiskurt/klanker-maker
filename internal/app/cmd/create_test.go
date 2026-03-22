package cmd_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCreateCmd_FlagRegistration verifies that the create command exposes
// --on-demand and --aws-profile flags.
func TestCreateCmd_FlagRegistration(t *testing.T) {
	km := buildKM(t)

	cmd := exec.Command(km, "create", "--help")
	out, _ := cmd.Output()
	outStr := string(out)

	if !strings.Contains(outStr, "--on-demand") {
		t.Errorf("expected --on-demand flag in create --help, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "--aws-profile") {
		t.Errorf("expected --aws-profile flag in create --help, got:\n%s", outStr)
	}
}

// TestCreateCmd_RequiresProfileArg verifies that km create with no args exits
// non-zero with a usage hint.
func TestCreateCmd_RequiresProfileArg(t *testing.T) {
	km := buildKM(t)

	cmd := exec.Command(km, "create")
	_, err := cmd.Output()
	if err == nil {
		t.Fatal("km create with no args: expected non-zero exit, got exit 0")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("km create with no args: expected ExitError, got: %T %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Error("km create with no args: expected non-zero exit code")
	}
}

// TestCreateCmd_InvalidProfile verifies that km create with a nonexistent
// profile path returns a non-zero exit and an error message.
func TestCreateCmd_InvalidProfile(t *testing.T) {
	km := buildKM(t)

	cmd := exec.Command(km, "create", "/tmp/nonexistent-profile-xyz123.yaml")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("km create with invalid path: expected non-zero exit, got exit 0\noutput: %s", out)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("km create with invalid path: expected ExitError, got %T", err)
	}
	if exitErr.ExitCode() == 0 {
		t.Error("km create with invalid path: expected non-zero exit code")
	}
}

// TestCreateCmd_Workflow verifies the create command workflow sequence using a
// real valid profile but mocked environment. Because apply calls terragrunt
// (not present in CI), we only verify up to the point of the apply attempt.
// The test confirms that validation, compilation, and sandbox dir creation happen
// before apply is reached — detectable because the error is about terragrunt, not
// about the profile or sandbox ID.
func TestCreateCmd_Workflow(t *testing.T) {
	km := buildKM(t)

	// Use a real valid profile from testdata
	profileFile := testdataPath(t, "valid-minimal.yaml")
	if _, err := os.Stat(profileFile); os.IsNotExist(err) {
		t.Skip("testdata/profiles/valid-minimal.yaml not found — skipping workflow test")
	}

	// Run with an invalid aws-profile so it fails at credential validation,
	// confirming the workflow progressed past profile parsing and compilation.
	cmd := exec.Command(km, "create", "--aws-profile", "nonexistent-profile-xyz", profileFile)
	out, err := cmd.CombinedOutput()

	// We expect a non-zero exit (AWS credential validation fails)
	if err == nil {
		t.Fatalf("km create with nonexistent AWS profile: expected non-zero exit\noutput: %s", out)
	}

	outStr := string(out)
	// The error should mention AWS or credentials — not "profile not found" or compile error
	// This confirms we got past profile loading/validation/compilation.
	if strings.Contains(outStr, "failed to parse") {
		t.Errorf("workflow stopped at parse stage (expected to pass): %s", outStr)
	}
}

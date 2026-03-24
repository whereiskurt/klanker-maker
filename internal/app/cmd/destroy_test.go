package cmd_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRunDestroy_GitHubTokenCleanup verifies that destroy.go contains github-token cleanup wiring.
// Source-level verification: confirms call sites exist and follow the non-fatal pattern.
func TestRunDestroy_GitHubTokenCleanup(t *testing.T) {
	src, err := os.ReadFile("destroy.go")
	if err != nil {
		t.Fatalf("read destroy.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"github-token dir reference", "github-token"},
		{"Step 7c label", "Step 7c"},
		{"SSM parameter path", "/sandbox/%s/github-token"},
		{"DeleteParameter call", "DeleteParameter"},
		{"ParameterNotFound swallow", "ParameterNotFound"},
		{"non-fatal cleanup pattern", "non-fatal"},
		{"GitHub token resources cleaned up print", "GitHub token resources cleaned up"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("destroy.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestRunDestroy_MLflow verifies that destroy.go contains FinalizeMLflowRun wiring.
func TestRunDestroy_MLflow(t *testing.T) {
	src, err := os.ReadFile("destroy.go")
	if err != nil {
		t.Fatalf("read destroy.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"FinalizeMLflowRun call", "FinalizeMLflowRun"},
		{"MLflowMetrics struct literal", "awspkg.MLflowMetrics{"},
		{"non-fatal pattern", "non-fatal"},
		{"ExitStatus field", "ExitStatus:"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("destroy.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestDestroyCmd_RequiresSandboxIDArg verifies that km destroy with no args
// exits non-zero.
func TestDestroyCmd_RequiresSandboxIDArg(t *testing.T) {
	km := buildKM(t)

	cmd := exec.Command(km, "destroy")
	_, err := cmd.Output()
	if err == nil {
		t.Fatal("km destroy with no args: expected non-zero exit, got exit 0")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("km destroy with no args: expected exit error, got %T %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Error("km destroy with no args: expected non-zero exit code")
	}
}

// TestDestroyCmd_InvalidSandboxID verifies that sandbox IDs not matching
// sb-[a-f0-9]{8} are rejected before any AWS calls are made.
func TestDestroyCmd_InvalidSandboxID(t *testing.T) {
	km := buildKM(t)

	invalidIDs := []string{
		"sandbox-123",      // wrong prefix
		"sb-ABCD1234",      // uppercase hex not allowed
		"sb-12345678x",     // extra character
		"sb-1234567",       // too short
		"sb-123456789",     // too long
		"not-a-sandbox-id", // completely wrong
	}

	for _, id := range invalidIDs {
		id := id
		t.Run(id, func(t *testing.T) {
			cmd := exec.Command(km, "destroy", "--yes", id)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("km destroy %q: expected non-zero exit, got exit 0\noutput: %s", id, out)
			}
			outStr := string(out)
			// Error should mention the format problem
			lower := strings.ToLower(outStr)
			if !strings.Contains(lower, "sandbox id") &&
				!strings.Contains(lower, "invalid") &&
				!strings.Contains(lower, "format") {
				t.Errorf("km destroy %q: expected error about invalid sandbox ID format, got: %s", id, outStr)
			}
		})
	}
}

// TestDestroyCmd_ValidIDFormatAccepted verifies that a well-formed sandbox ID
// passes format validation and proceeds to the AWS credential check.
func TestDestroyCmd_ValidIDFormatAccepted(t *testing.T) {
	km := buildKM(t)

	// A correctly formatted sandbox ID that won't exist in AWS
	validID := "sb-a1b2c3d4"

	cmd := exec.Command(km, "destroy", "--yes", "--aws-profile", "nonexistent-profile-xyz", validID)
	out, err := cmd.CombinedOutput()

	// We expect a non-zero exit (AWS config or credential failure) but NOT an
	// "invalid sandbox ID" error — confirming format validation passed.
	if err == nil {
		t.Fatalf("km destroy valid ID with fake profile: expected non-zero exit (AWS error)\noutput: %s", out)
	}

	outStr := string(out)
	// Should NOT contain "invalid sandbox id" — that would mean format check failed wrongly
	if strings.Contains(strings.ToLower(outStr), "invalid sandbox id") {
		t.Errorf("km destroy valid ID %q incorrectly rejected as invalid format: %s", validID, outStr)
	}
}

// TestDestroyCmd_FlagRegistration verifies the destroy command has --aws-profile
// and --force flags.
func TestDestroyCmd_FlagRegistration(t *testing.T) {
	km := buildKM(t)

	cmd := exec.Command(km, "destroy", "--help")
	out, _ := cmd.Output()
	outStr := string(out)

	if !strings.Contains(outStr, "--aws-profile") {
		t.Errorf("expected --aws-profile flag in destroy --help, got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "--yes") {
		t.Errorf("expected --yes flag in destroy --help, got:\n%s", outStr)
	}
}

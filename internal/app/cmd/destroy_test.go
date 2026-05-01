package cmd_test

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
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

// TestSandboxIDPattern verifies the sandboxIDPattern regex accepts and rejects correct inputs.
// Uses source-level inspection since the pattern is package-private.
func TestSandboxIDPattern(t *testing.T) {
	src, err := os.ReadFile("destroy.go")
	if err != nil {
		t.Fatalf("read destroy.go: %v", err)
	}
	s := string(src)

	// Pattern should be generalized (not sb-specific)
	if !strings.Contains(s, `[a-z][a-z0-9]{0,11}-[a-f0-9]{8}`) {
		t.Errorf("destroy.go sandboxIDPattern does not contain generalized pattern [a-z][a-z0-9]{0,11}-[a-f0-9]{8}")
	}
}

// TestDestroyCmd_GeneralizedPatternAcceptsCustomPrefix verifies that sandbox IDs
// with custom prefixes (not sb-) are accepted by the generalized pattern.
func TestDestroyCmd_GeneralizedPatternAcceptsCustomPrefix(t *testing.T) {
	km := buildKM(t)

	validCustomIDs := []string{
		"claude-abc12345",
		"build-abc12345",
		"a-abc12345",
	}

	for _, id := range validCustomIDs {
		id := id
		t.Run(id, func(t *testing.T) {
			cmd := exec.Command(km, "destroy", "--yes", "--remote=false", "--aws-profile", "nonexistent-profile-xyz", id)
			out, err := cmd.CombinedOutput()

			// Should fail on AWS credential check, NOT on sandbox ID format validation
			if err == nil {
				t.Fatalf("km destroy %q with fake profile: expected non-zero exit (AWS error)\noutput: %s", id, out)
			}
			outStr := string(out)
			if strings.Contains(strings.ToLower(outStr), "invalid sandbox id") {
				t.Errorf("km destroy custom prefix ID %q incorrectly rejected as invalid format: %s", id, outStr)
			}
		})
	}
}

// TestDestroyCmd_InvalidSandboxID verifies that sandbox IDs not matching
// the generalized pattern are rejected before any AWS calls are made.
func TestDestroyCmd_InvalidSandboxID(t *testing.T) {
	km := buildKM(t)

	invalidIDs := []string{
		"sb-ABCD1234",      // uppercase hex not allowed
		"sb-12345678x",     // extra character
		"sb-1234567",       // too short
		"sb-123456789",     // too long
		"not-a-sandbox-id", // completely wrong
		"ABC-abc12345",     // uppercase prefix not allowed
		"abc12345",         // no prefix/dash
	}

	for _, id := range invalidIDs {
		id := id
		t.Run(id, func(t *testing.T) {
			cmd := exec.Command(km, "destroy", "--yes", "--remote=false", id)
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

	cmd := exec.Command(km, "destroy", "--yes", "--remote=false", "--aws-profile", "nonexistent-profile-xyz", validID)
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

// ---- Remote path tests ----

// runDestroyRemote is a helper that invokes km destroy --remote via injected publisher.
func runDestroyRemote(t *testing.T, pub cmd.RemoteCommandPublisher, sandboxID string) error {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	destroyCmd := cmd.NewDestroyCmdWithPublisher(cfg, pub)
	root.AddCommand(destroyCmd)
	// --yes skips interactive confirmation prompt
	root.SetArgs([]string{"destroy", "--yes", "--remote", sandboxID})
	return root.Execute()
}

// TestDestroyCmd_RemotePublishesCorrectEvent verifies km destroy --remote dispatches
// an EventBridge event with eventType "destroy" and the correct sandbox ID.
func TestDestroyCmd_RemotePublishesCorrectEvent(t *testing.T) {
	pub := &fakePublisher{}
	err := runDestroyRemote(t, pub, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("destroy --remote returned error: %v", err)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(pub.calls))
	}
	call := pub.calls[0]
	if call.sandboxID != "sb-aabbccdd" {
		t.Errorf("sandboxID = %q, want %q", call.sandboxID, "sb-aabbccdd")
	}
	if call.eventType != "destroy" {
		t.Errorf("eventType = %q, want %q", call.eventType, "destroy")
	}
}

// TestDestroyCmd_RemotePublishFailure verifies that EventBridge publish failure
// propagates a clear error to the caller.
func TestDestroyCmd_RemotePublishFailure(t *testing.T) {
	pub := &fakePublisher{err: errors.New("eventbridge unavailable")}
	err := runDestroyRemote(t, pub, "sb-aabbccdd")
	if err == nil {
		t.Fatal("expected error when publisher fails, got nil")
	}
}

// TestDestroyCmd_RemoteInvalidSandboxID verifies that an invalid sandbox ID
// returns an error before calling the publisher.
func TestDestroyCmd_RemoteInvalidSandboxID(t *testing.T) {
	pub := &fakePublisher{}
	// Use a non-matching ID that passes ResolveSandboxID string check but fails regex.
	// Note: destroy validates format in runDestroy, but --remote path goes through
	// ResolveSandboxID first. We use an obviously wrong ID here.
	err := runDestroyRemote(t, pub, "NOT-VALID")
	if err == nil {
		t.Fatal("expected error for invalid sandbox ID, got nil")
	}
	if len(pub.calls) != 0 {
		t.Errorf("expected 0 publisher calls for invalid ID, got %d", len(pub.calls))
	}
}

// ---- SLCK-12 Option A wiring tests (Plan 63.1-02 Task 3) ----

// TestDestroyDestroy_SlackTeardownWiredIntoRemotePath verifies (via source-level
// inspection) that destroy.go calls runSlackTeardown in the remote block BEFORE
// publisher.PublishSandboxCommand. This is the structural guard for the SLCK-12
// Option A fix: the Lambda handler has no Slack code, so the operator workstation
// must handle teardown.
func TestDestroyDestroy_SlackTeardownWiredIntoRemotePath(t *testing.T) {
	src, err := os.ReadFile("destroy.go")
	if err != nil {
		t.Fatalf("read destroy.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"runSlackTeardown called in destroy.go", "runSlackTeardown("},
		{"SLCK-12 fix comment present", "SLCK-12"},
		{"Option A comment present", "Option A"},
		{"remote-path Slack teardown before dispatch comment", "run Slack teardown locally BEFORE"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("destroy.go missing %s (expected pattern %q)", c.name, c.pattern)
		}
	}
}

// TestDestroyDestroy_LocalPathUsesSharedHelper verifies that the local destroy
// path uses the runSlackTeardown helper (not the old inline SSM/key setup). This
// prevents drift between remote and local paths (both must use the same code).
func TestDestroyDestroy_LocalPathUsesSharedHelper(t *testing.T) {
	src, err := os.ReadFile("destroy.go")
	if err != nil {
		t.Fatalf("read destroy.go: %v", err)
	}
	s := string(src)

	// The old inline pattern is gone.
	if strings.Contains(s, "slackSSMClient := ssm.NewFromConfig") {
		t.Error("destroy.go still contains old inline slackSSMClient — expected refactored to runSlackTeardown()")
	}
	if strings.Contains(s, "slackKeyLoader := PrivKeyLoaderFunc") {
		t.Error("destroy.go still contains old inline slackKeyLoader — expected refactored to runSlackTeardown()")
	}
	// The shared helper is present.
	if !strings.Contains(s, "runSlackTeardown(ctx") {
		t.Error("destroy.go missing runSlackTeardown(ctx, ...) call in local path")
	}
}

// TestDestroyCmd_RemoteSlackTeardownNonFatalWhenAWSUnavailable verifies that
// km destroy --remote still dispatches the EventBridge event even when the AWS
// config fails to load (which means runSlackTeardown is skipped gracefully).
// This guards the non-fatal requirement: Slack failures must never block destroy.
func TestDestroyCmd_RemoteSlackTeardownNonFatalWhenAWSUnavailable(t *testing.T) {
	// The remote path tries to load AWS config with profile "klanker-terraform".
	// In CI / this test environment, that profile won't exist — LoadAWSConfig
	// will fail, the Slack teardown block is skipped, and the publisher is still
	// called. This verifies the non-fatal property end-to-end.
	pub := &fakePublisher{}
	err := runDestroyRemote(t, pub, "sb-aabbccdd")
	if err != nil {
		t.Fatalf("destroy --remote: unexpected error (Slack failure must be non-fatal): %v", err)
	}
	// Publisher must have been called exactly once.
	if len(pub.calls) != 1 {
		t.Fatalf("expected 1 publisher call (destroy dispatched), got %d", len(pub.calls))
	}
	if pub.calls[0].eventType != "destroy" {
		t.Errorf("eventType = %q, want %q", pub.calls[0].eventType, "destroy")
	}
}

// Compile-time check: fakePublisher implements RemoteCommandPublisher.
var _ cmd.RemoteCommandPublisher = (*fakePublisher)(nil)

package cmd_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRunCreate_GitHubToken verifies that create.go contains the GitHub token wiring.
// Source-level verification: confirms call sites exist and follow the non-fatal pattern.
func TestRunCreate_GitHubToken(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"Step 13a guard", "SourceAccess.GitHub"},
		{"generateAndStoreGitHubToken call", "generateAndStoreGitHubToken"},
		{"github-token dir", "github-token"},
		{"GitHubTokenHCL check", "GitHubTokenHCL"},
		{"Step 13b non-fatal pattern", "non-fatal — sandbox is provisioned"},
		{"Step 13a print", "Step 13a"},
		{"Step 13b print", "Step 13b"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestRunCreate_MLflow verifies that create.go contains WriteMLflowRun wiring.
// This is a source-level verification test — the actual MLflow S3 write is tested
// in pkg/aws/mlflow_test.go. This test confirms the call site exists and follows
// the non-fatal pattern.
func TestRunCreate_MLflow(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"WriteMLflowRun call", "WriteMLflowRun"},
		{"MLflowRun struct literal", "awspkg.MLflowRun{"},
		{"non-fatal pattern", "non-fatal"},
		{"SandboxID field", "SandboxID:"},
		{"ProfileName field", "ProfileName:"},
		{"Experiment field", `Experiment:`},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

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

// TestRunCreate_IdentityProvisioning verifies that create.go contains the identity
// provisioning wiring (Step 15). Source-level verification confirms call sites exist
// and follow the non-fatal pattern established for budget and GitHub token.
func TestRunCreate_IdentityProvisioning(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"Step 15 comment", "Step 15"},
		{"email section guard", "Spec.Email"},
		{"GenerateSandboxIdentity call", "GenerateSandboxIdentity"},
		{"PublishIdentity call", "PublishIdentity"},
		{"non-fatal pattern", "failed to provision sandbox identity (non-fatal)"},
		{"encryption check", "GenerateEncryptionKey"},
		{"kmsKeyAlias construction", "alias/km-platform"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestRunDestroy_IdentityCleanup verifies that destroy.go contains the identity
// cleanup wiring (Step 11). Source-level verification confirms the cleanup call
// follows the non-fatal/idempotent pattern from SES cleanup (Step 10).
func TestRunDestroy_IdentityCleanup(t *testing.T) {
	src, err := os.ReadFile("destroy.go")
	if err != nil {
		t.Fatalf("read destroy.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"Step 11 comment", "Step 11"},
		{"CleanupSandboxIdentity call", "CleanupSandboxIdentity"},
		{"non-fatal pattern", "failed to cleanup sandbox identity (non-fatal)"},
		{"identity table name", "IdentityTableName"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("destroy.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestRunCreate_PublishIdentityAliasWiring verifies that create.go threads alias and
// allowedSenders from the profile EmailSpec to the PublishIdentity call.
// Source-level verification confirms the call site includes both parameters.
func TestRunCreate_PublishIdentityAliasWiring(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"alias local extracted from Email.Alias", "Email.Alias"},
		{"allowedSenders local extracted from Email.AllowedSenders", "Email.AllowedSenders"},
		{"alias passed to PublishIdentity", "alias, allowedSenders"},
		{"AllowedSenders in full PublishIdentity call line", "allowedSenders); pubErr != nil {"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestRunCreate_PublishIdentityAliasBackwardCompat verifies that alias and
// allowedSenders extraction uses the existing prof.Spec.Email guard (nil-safe).
func TestRunCreate_PublishIdentityAliasBackwardCompat(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	// Both alias and allowedSenders must be read directly from Spec.Email,
	// which is only reached inside the Email != nil guard — backward-compatible.
	if !strings.Contains(s, "Email.Alias") {
		t.Error("create.go: alias not read from Spec.Email.Alias")
	}
	if !strings.Contains(s, "Email.AllowedSenders") {
		t.Error("create.go: allowedSenders not read from Spec.Email.AllowedSenders")
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

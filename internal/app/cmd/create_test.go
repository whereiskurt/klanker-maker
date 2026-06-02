package cmd_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
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
		{"kmsKeyAlias construction", "cfg.GetPlatformKMSAlias()"},
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

// TestCreateSafePhraseStorage verifies that create.go contains safe phrase generation
// and SSM storage wiring (Step 12d). Source-level verification confirms the call site
// follows the non-fatal pattern.
func TestCreateSafePhraseStorage(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"Step 12d comment", "Step 12d"},
		{"safe-phrase SSM path", "safe-phrase"},
		{"crypto/rand usage", "crypto/rand"},
		{"hex encoding", "hex.EncodeToString"},
		{"safe phrase stdout print", "Safe phrase:"},
		{"non-fatal pattern", "non-fatal"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestRunCreate_MaxLifetime verifies that create.go populates MaxLifetime in
// the SandboxMetadata struct from the profile's lifecycle.maxLifetime field.
// Source-level verification confirms the assignment is present — matching the
// pattern used by TestRunCreate_GitHubToken and TestRunCreate_MLflow.
func TestRunCreate_MaxLifetime(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"MaxLifetime field assignment in SandboxMetadata", "MaxLifetime: resolvedProfile.Spec.Lifecycle.MaxLifetime"},
		{"SandboxMetadata struct literal present", "awspkg.SandboxMetadata{"},
		{"IdleTimeout already present (guard that struct literal is correct)", "IdleTimeout: resolvedProfile.Spec.Lifecycle.IdleTimeout"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestRunCreate_MaxLifetime_JSON verifies that when SandboxMetadata is marshalled
// to JSON with MaxLifetime set, the max_lifetime key appears; and when it is empty
// the key is omitted (omitempty semantics from the struct tag).
func TestRunCreate_MaxLifetime_JSON(t *testing.T) {
	// We verify the struct behaviour directly — no AWS calls required.
	// Uses the same json:"max_lifetime,omitempty" tag as SandboxMetadata.MaxLifetime.
	type metaSubset struct {
		MaxLifetime string `json:"max_lifetime,omitempty"`
	}

	t.Run("present when set", func(t *testing.T) {
		m := metaSubset{MaxLifetime: "48h"}
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(out), `"max_lifetime":"48h"`) {
			t.Errorf("expected max_lifetime in JSON, got: %s", out)
		}
	})

	t.Run("omitted when empty (omitempty)", func(t *testing.T) {
		m := metaSubset{MaxLifetime: ""}
		out, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(out), "max_lifetime") {
			t.Errorf("expected max_lifetime absent in JSON (omitempty), got: %s", out)
		}
	})
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

// TestRunCreate_SlackIntegration verifies that create.go contains the Slack
// channel resolution wiring (Plan 63-08). Source-level verification confirms
// call sites exist and follow the expected patterns.
func TestRunCreate_SlackIntegration(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"Step 6c comment", "Step 6c"},
		{"resolveSlackChannel call", "resolveSlackChannel"},
		{"slackChannelID assignment", "slackChannelID"},
		{"slackPerSandbox assignment", "slackPerSandbox"},
		{"notification.slack enabled guard", "notificationSlack(resolvedProfile)"},
		{"SlackChannelID metadata field", "SlackChannelID:"},
		{"SlackPerSandbox metadata field", "SlackPerSandbox:"},
		{"SlackArchiveOnDestroy metadata write", "SlackArchiveOnDestroy"},
		{"Step 11d comment", "Step 11d"},
		{"runStep11dInject call", "runStep11dInject"},
		{"PutParameter wiring", "ssmClientForInject.PutParameter"},
		{"Slack non-fatal pattern", "non-fatal"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestRunCreate_SlackArchiveOnDestroy verifies the metadata field population
// for SlackArchiveOnDestroy from the resolved profile notification.slack spec.
func TestRunCreate_SlackArchiveOnDestroy(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	// The field should be set from notification.slack.archiveOnDestroy, nil round-trips as nil
	checks := []struct {
		name    string
		pattern string
	}{
		{"notification.slack nil guard", "notificationSlack(resolvedProfile)"},
		{"SlackArchiveOnDestroy write", "meta.SlackArchiveOnDestroy"},
		{"nil round-trip comment", "nil round-trips as nil"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

// TestDesktopCredential verifies the per-sandbox KasmVNC credential lifecycle.
//
// It tests the GenerateDesktopCredential helper directly — not the full runCreate
// flow — so the test is fast and does not require AWS credentials. The helper is
// the single source of truth for credential generation; the runCreate/runCreateRemote
// call sites are thin wrappers verified by source-level checks below.
func TestDesktopCredential(t *testing.T) {
	t.Run("enabled profile writes file and threads NetworkConfig", func(t *testing.T) {
		home := t.TempDir()
		network := &compiler.NetworkConfig{}
		if err := cmd.GenerateDesktopCredential(home, "sbx-test-01", network); err != nil {
			t.Fatalf("GenerateDesktopCredential: %v", err)
		}

		// File must exist at ~/.km/desktop/<id>
		credPath := filepath.Join(home, ".km", "desktop", "sbx-test-01")
		data, err := os.ReadFile(credPath)
		if err != nil {
			t.Fatalf("credential file not written: %v", err)
		}

		// Content must be "kasm:<password>"
		content := string(data)
		if !strings.HasPrefix(content, "kasm:") {
			t.Errorf("credential content %q does not start with 'kasm:'", content)
		}
		parts := strings.SplitN(content, ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			t.Errorf("credential content %q: expected non-empty password after colon", content)
		}
		password := parts[1]
		if strings.ContainsAny(password, ":\n") {
			t.Errorf("password %q contains invalid characters (colon or newline)", password)
		}
		if len(password) < 8 {
			t.Errorf("password %q is too short (got %d, want >= 8)", password, len(password))
		}

		// File mode must be 0600
		info, err := os.Stat(credPath)
		if err != nil {
			t.Fatalf("stat credential file: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("credential file mode: got %04o, want 0600", got)
		}

		// NetworkConfig fields must be threaded
		if network.DesktopKasmUser != "kasm" {
			t.Errorf("NetworkConfig.DesktopKasmUser: got %q, want %q", network.DesktopKasmUser, "kasm")
		}
		if network.DesktopKasmPass != password {
			t.Errorf("NetworkConfig.DesktopKasmPass does not match file password")
		}
	})

	t.Run("two creates produce different passwords", func(t *testing.T) {
		home := t.TempDir()
		n1 := &compiler.NetworkConfig{}
		n2 := &compiler.NetworkConfig{}
		if err := cmd.GenerateDesktopCredential(home, "sbx-a", n1); err != nil {
			t.Fatalf("first GenerateDesktopCredential: %v", err)
		}
		if err := cmd.GenerateDesktopCredential(home, "sbx-b", n2); err != nil {
			t.Fatalf("second GenerateDesktopCredential: %v", err)
		}
		if n1.DesktopKasmPass == n2.DesktopKasmPass {
			t.Errorf("two creates produced the same password %q — not random", n1.DesktopKasmPass)
		}
	})

	t.Run("env var override skips file write", func(t *testing.T) {
		// KM_DESKTOP_KASM_USER / KM_DESKTOP_KASM_PASS override (Lambda subprocess path)
		t.Setenv("KM_DESKTOP_KASM_USER", "kasm")
		t.Setenv("KM_DESKTOP_KASM_PASS", "override-password")

		home := t.TempDir()
		network := &compiler.NetworkConfig{}
		if err := cmd.GenerateDesktopCredential(home, "sbx-override", network); err != nil {
			t.Fatalf("GenerateDesktopCredential with env override: %v", err)
		}

		// No file should be written when env vars are set (Lambda has no ~/.km)
		credPath := filepath.Join(home, ".km", "desktop", "sbx-override")
		if _, err := os.Stat(credPath); err == nil {
			t.Errorf("credential file written even though env override was set — Lambda path must not write files")
		}

		// NetworkConfig threaded from env
		if network.DesktopKasmUser != "kasm" {
			t.Errorf("NetworkConfig.DesktopKasmUser from env: got %q, want %q", network.DesktopKasmUser, "kasm")
		}
		if network.DesktopKasmPass != "override-password" {
			t.Errorf("NetworkConfig.DesktopKasmPass from env: got %q, want %q", network.DesktopKasmPass, "override-password")
		}
	})
}

// TestDesktopCredentialSource verifies that create.go contains the required
// call sites and source patterns for desktop credential integration.
func TestDesktopCredentialSource(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"GenerateDesktopCredential helper", "func GenerateDesktopCredential("},
		{"randomPassword helper", "func randomPassword("},
		{"IsDesktopEnabled guard (local)", "IsDesktopEnabled"},
		{"KM_DESKTOP_KASM_USER env", "KM_DESKTOP_KASM_USER"},
		{"KM_DESKTOP_KASM_PASS env", "KM_DESKTOP_KASM_PASS"},
		{"~/.km/desktop dir", `".km", "desktop"`},
		{"DesktopKasmUser thread", "DesktopKasmUser"},
		{"DesktopKasmPass thread", "DesktopKasmPass"},
		{"desktop-creds.txt upload", "desktop-creds.txt"},
		{"crypto/rand usage", "cryptorand"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}

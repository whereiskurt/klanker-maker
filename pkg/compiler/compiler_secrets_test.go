package compiler_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// repoRoot returns the absolute path to the repo root by walking up from this test file.
// Uses the runtime caller to locate the file and navigates to the repo root.
func repoRootForSecretsTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file is .../klankrmkr/pkg/compiler/compiler_secrets_test.go
	// Navigate up 3 dirs: compiler -> pkg -> klankrmkr
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestSandboxTemplateUsesEC2SpotV120 asserts that the sandbox terragrunt.hcl template
// references ec2spot/v1.2.0 (not v1.1.0). This locks down WARNING 3 / phase 89 module bump.
// The template is at infra/templates/sandbox/terragrunt.hcl and is copied verbatim
// by terragrunt.CreateSandboxDir — so checking the file directly is authoritative.
func TestSandboxTemplateUsesEC2SpotV120(t *testing.T) {
	root := repoRootForSecretsTest(t)
	templatePath := filepath.Join(root, "infra", "templates", "sandbox", "terragrunt.hcl")
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read sandbox template: %v", err)
	}
	content := string(data)

	// The template uses a Terraform local for the module name:
	// source = "${local.repo_root}/infra/modules/${local.svc_config.locals.substrate_module}/v1.2.0"
	// So check for the version literal, not the full "ec2spot/v1.2.0" string.
	if strings.Contains(content, "/v1.1.0") {
		t.Errorf("sandbox template still references v1.1.0; expected v1.2.0 after Phase 89 bump\n%s", templatePath)
	}
	if !strings.Contains(content, "/v1.2.0") {
		t.Errorf("sandbox template does not reference /v1.2.0; expected bump from v1.1.0\n%s", templatePath)
	}
}

// TestCompileEC2ServiceHCLHasArtifactsBucket asserts that the compiled service.hcl
// for an EC2 sandbox contains 'artifacts_bucket' in its module_inputs block.
// This is WARNING 3: without this, ec2spot v1.2.0's S3 IAM policy is silently skipped
// → boot-time 403 on secrets.enc.yaml fetch.
func TestCompileEC2ServiceHCLHasArtifactsBucket(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := compiler.GenerateSandboxID("")
	artifacts, err := compiler.Compile(p, id, false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if !strings.Contains(artifacts.ServiceHCL, "artifacts_bucket") {
		t.Errorf("compiled service.hcl missing 'artifacts_bucket' in module_inputs — WARNING 3: ec2spot v1.2.0 S3 IAM policy will not be created\nServiceHCL:\n%s", artifacts.ServiceHCL)
	}
}

// TestSopsBundlePresentPopulatedFromProfile asserts that when a profile has
// Spec.Secrets.SopsFile set, the compiled userdata contains the SOPS section 5.5 markers.
// This verifies the SopsBundlePresent field is correctly propagated through the compile path.
func TestSopsBundlePresentPopulatedFromProfile(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.Secrets = &profile.SecretsSpec{
		SopsFile: "./secrets/test.enc.yaml",
	}
	id := "sb-sops01"
	artifacts, err := compiler.Compile(p, id, false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile() with SopsFile set error = %v", err)
	}

	required := []string{
		"SOPS secret injection",
		"sops decrypt --output-type dotenv",
		"secrets.enc.yaml",
		"/etc/sandbox-secrets.env",
		"/etc/profile.d/zz-sandbox-secrets.sh",
	}
	for _, want := range required {
		if !strings.Contains(artifacts.UserData, want) {
			t.Errorf("compiled userdata missing %q when SopsFile is set", want)
		}
	}
}

// TestSopsBundleAbsentWhenProfileHasNoSecrets asserts that when a profile has
// no Spec.Secrets, the compiled userdata does NOT contain the SOPS section 5.5 block.
// Backwards compat: pre-Phase-89 profiles must produce identical output.
func TestSopsBundleAbsentWhenProfileHasNoSecrets(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	// Ensure Spec.Secrets is nil (default state)
	p.Spec.Secrets = nil
	id := "sb-nosops1"
	artifacts, err := compiler.Compile(p, id, false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile() without SopsFile error = %v", err)
	}

	for _, banned := range []string{
		"SOPS secret injection",
		"sops decrypt",
		"sandbox-secrets",
	} {
		if strings.Contains(artifacts.UserData, banned) {
			t.Errorf("compiled userdata contains SOPS marker %q when no SopsFile set (backwards compat broken)", banned)
		}
	}
}

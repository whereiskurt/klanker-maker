package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// The single shared-literal sandbox-template version check that used to live
// here was superseded in Phase 125 Plan 02 by the per-substrate assertion in
// pkg/terragrunt/substrate_version_pin_test.go, which the per-substrate
// version map broke (there is no longer one shared literal to assert on).

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

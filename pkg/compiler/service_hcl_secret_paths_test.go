package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// TestServiceHCL_SecretPathsEmitted asserts resolved paths reach module_inputs.
func TestServiceHCL_SecretPathsEmitted(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "km")

	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.IAM.AllowedSecretPaths = []string{
		"{{prefix}}/wiz/wiz-api-client-id",
		"{{prefix}}/wiz/wiz-api-client-secret",
	}

	got, err := compiler.Compile(p, "sb-wiz03", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	want := `secret_paths = ["/km/wiz/wiz-api-client-id", "/km/wiz/wiz-api-client-secret"]`
	if !strings.Contains(got.ServiceHCL, want) {
		t.Errorf("service.hcl missing %q\n--- got ---\n%s", want, got.ServiceHCL)
	}
}

// TestServiceHCL_SecretPathsAbsentWhenUnset is the byte-identity guard. A
// profile that declares no secret paths must render NO secret_paths line, or
// every frozen golden fixture churns.
func TestServiceHCL_SecretPathsAbsentWhenUnset(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "km")

	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.IAM.AllowedSecretPaths = nil

	got, err := compiler.Compile(p, "sb-wiz04", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if strings.Contains(got.ServiceHCL, "secret_paths") {
		t.Errorf("service.hcl contains secret_paths for a profile that declares none; "+
			"emission must be conditional\n--- got ---\n%s", got.ServiceHCL)
	}
}

package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// TestCompile_SecretPathsFailLoud proves a {{prefix}} path with no
// KM_RESOURCE_PREFIX aborts the compile instead of defaulting to "km".
func TestCompile_SecretPathsFailLoud(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "")

	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.IAM.AllowedSecretPaths = []string{"{{prefix}}/wiz/wiz-api-client-id"}

	_, err := compiler.Compile(p, "sb-wiz01", false, testNetwork(), nil)
	if err == nil {
		t.Fatal("Compile() succeeded with an unset KM_RESOURCE_PREFIX; " +
			"it must fail rather than render IAM for the wrong install")
	}
	if !strings.Contains(err.Error(), "KM_RESOURCE_PREFIX") {
		t.Errorf("error should name the env var, got: %v", err)
	}
}

// TestCompile_SecretPathsResolve proves the token expands against the env var.
func TestCompile_SecretPathsResolve(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "km2")

	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.IAM.AllowedSecretPaths = []string{"{{prefix}}/wiz/wiz-api-client-id"}

	got, err := compiler.Compile(p, "sb-wiz02", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := "/km2/wiz/wiz-api-client-id"
	found := false
	for _, sp := range got.SecretPaths {
		if sp == want {
			found = true
		}
	}
	if !found {
		t.Errorf("SecretPaths = %q, want to contain %q", got.SecretPaths, want)
	}
}

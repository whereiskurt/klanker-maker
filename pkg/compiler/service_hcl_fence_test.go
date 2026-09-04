package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// renderServiceHCLForTest compiles ec2-basic.yaml with a secrets block whose
// fenceIMDS is fence (nil leaves the field unset) and returns the service.hcl.
// The bundle is always present, so a dormant assertion cannot pass merely
// because there was no secrets block at all.
func renderServiceHCLForTest(t *testing.T, fence *bool) string {
	t.Helper()
	t.Setenv("KM_RESOURCE_PREFIX", "km")

	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.Secrets = &profile.SecretsSpec{SopsFile: "./secrets/x.enc.yaml", FenceIMDS: fence}

	got, err := compiler.Compile(p, "sb-fence01", false, testNetwork(), nil)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return got.ServiceHCL
}

// A profile that does not mention fenceIMDS must emit NO fence_imds line, so
// every pre-Wave-2 sandbox renders byte-identical Terraform.
func TestServiceHCL_NoFenceInputWhenUnset(t *testing.T) {
	if got := renderServiceHCLForTest(t, nil); strings.Contains(got, "fence_imds") {
		t.Errorf("service.hcl carries fence_imds for a profile that never set it\n--- got ---\n%s", got)
	}
}

func TestServiceHCL_FenceInputWhenTrue(t *testing.T) {
	on := true
	if got := renderServiceHCLForTest(t, &on); !strings.Contains(got, "fence_imds = true") {
		t.Errorf("service.hcl missing `fence_imds = true`\n--- got ---\n%s", got)
	}
}

// Explicit false is dormant too: the module default is false, so emitting the
// input would be noise and would break byte-identity for a profile that only
// wrote down its intent.
func TestServiceHCL_NoFenceInputWhenExplicitFalse(t *testing.T) {
	off := false
	if got := renderServiceHCLForTest(t, &off); strings.Contains(got, "fence_imds") {
		t.Errorf("explicit false must render no fence_imds input\n--- got ---\n%s", got)
	}
}

// The predicate carries the bundle requirement itself, so the IAM path and the
// userdata path cannot disagree about whether a box is fenced. A fence without a
// bundle has no broker to mint credentials from, so it would strand every helper
// rather than protect anything.
func TestIsFenceIMDSEnabled(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name string
		in   *profile.SecretsSpec
		want bool
	}{
		{"nil spec", nil, false},
		{"no field", &profile.SecretsSpec{SopsFile: "x.enc.yaml"}, false},
		{"explicit false", &profile.SecretsSpec{SopsFile: "x.enc.yaml", FenceIMDS: &off}, false},
		{"explicit true with a bundle", &profile.SecretsSpec{SopsFile: "x.enc.yaml", FenceIMDS: &on}, true},
		{"true but no bundle", &profile.SecretsSpec{FenceIMDS: &on}, false},
	}
	for _, c := range cases {
		if got := profile.IsFenceIMDSEnabled(c.in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

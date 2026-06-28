package compiler

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestEC2ServiceHCL_AZPreferenceAbsent verifies that a profile WITHOUT azPreference
// compiles to byte-identical service.hcl output compared to the same profile compiled
// with azPreference explicitly nil/empty.
//
// This is the key byte-identity guard for Phase 124 (Pitfall 4 in 124-RESEARCH):
// azPreference affects only the ordering of network.AvailabilityZones before Compile,
// it must NOT inject any new HCL token into the template output.
func TestEC2ServiceHCL_AZPreferenceAbsent(t *testing.T) {
	// Baseline profile: no azPreference field.
	p := baseEC2Profile()
	net := baseEC2Network()

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	baseline, err := generateEC2ServiceHCL(p, "test-sb-baseline", true, nil, iamPolicy, "", net, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL (baseline): %v", err)
	}

	// Same profile with azPreference explicitly set to nil (should be identical output).
	pWithNil := baseEC2Profile()
	pWithNil.Spec.Runtime.AZPreference = nil
	withNil, err := generateEC2ServiceHCL(pWithNil, "test-sb-baseline", true, nil, iamPolicy, "", net, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL (nil azPreference): %v", err)
	}

	if baseline != withNil {
		t.Error("service.hcl output changed when AZPreference is nil — azPreference must NOT inject new HCL tokens")
	}

	// Same profile with azPreference set to an empty slice.
	pWithEmpty := baseEC2Profile()
	pWithEmpty.Spec.Runtime.AZPreference = []string{}
	withEmpty, err := generateEC2ServiceHCL(pWithEmpty, "test-sb-baseline", true, nil, iamPolicy, "", net, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL (empty azPreference): %v", err)
	}

	if baseline != withEmpty {
		t.Error("service.hcl output changed when AZPreference is empty — azPreference must NOT inject new HCL tokens")
	}
}

// TestEC2ServiceHCL_AZPreferenceDoesNotAppearInHCL verifies that the literal token
// "azPreference" never appears in the compiled service.hcl output — it is a
// profile field that only affects pre-compile AZ ordering, not a Terraform variable.
func TestEC2ServiceHCL_AZPreferenceDoesNotAppearInHCL(t *testing.T) {
	p := baseEC2Profile()
	p.Spec.Runtime.AZPreference = []string{"us-east-1c", "us-east-1b"}
	net := baseEC2Network()

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	// Note: we pass the network as-is (not reordered) — pre-compile AZ reordering
	// happens in create.go, not in the compiler. This test just confirms the field
	// does not leak into the HCL template output.
	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL: %v", err)
	}

	// The literal token "azPreference" must never appear in HCL output.
	if contains(out, "azPreference") {
		t.Errorf("azPreference token found in service.hcl output — it must not be emitted as HCL:\n%s", out)
	}
	if contains(out, "az_preference") {
		t.Errorf("az_preference token found in service.hcl output — it must not be emitted as HCL:\n%s", out)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i+len(substr) <= len(s); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// Ensure AZPreference field exists in RuntimeSpec to keep the test compilable.
var _ = profile.RuntimeSpec{}.AZPreference

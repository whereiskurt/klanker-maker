package compiler

import (
	"strings"
	"testing"
)

// TestEC2ServiceHCL_LaunchAccountAbsent verifies that a NetworkConfig with an
// empty LaunchAccount produces a service.hcl containing none of the three new
// launch-account local names — the dormancy guard for Phase 126, REQ-126-LAUNCH.
func TestEC2ServiceHCL_LaunchAccountAbsent(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	// LaunchAccount/LauncherRoleARN/LauncherExternalID left at zero value.

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net, nil, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL: %v", err)
	}

	for _, tok := range []string{"launch_account", "launcher_role_arn", "launcher_external_id"} {
		if strings.Contains(out, tok) {
			t.Errorf("service.hcl contains %q with no LaunchAccount set — dormancy guard violated:\n%s", tok, out)
		}
	}
}

// TestEC2ServiceHCL_LaunchAccountSet verifies that a NetworkConfig with LaunchAccount,
// LauncherRoleARN, and LauncherExternalID set produces a service.hcl whose locals block
// carries all three, each as a quoted string with the exact value passed in.
func TestEC2ServiceHCL_LaunchAccountSet(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	net.LaunchAccount = "mgmt-gpu"
	net.LauncherRoleARN = "arn:aws:iam::123456789012:role/km-gpu-launcher"
	net.LauncherExternalID = "km-launch-mgmt-gpu-Ab12"

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net, nil, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL: %v", err)
	}

	wantLines := []string{
		`launch_account       = "mgmt-gpu"`,
		`launcher_role_arn    = "arn:aws:iam::123456789012:role/km-gpu-launcher"`,
		`launcher_external_id = "km-launch-mgmt-gpu-Ab12"`,
	}
	for _, line := range wantLines {
		if !strings.Contains(out, line) {
			t.Errorf("service.hcl missing expected line %q\nfull output:\n%s", line, out)
		}
	}
}

// TestEC2ServiceHCL_LaunchAccountSurvivesSpecialChars verifies the emitted values
// survive an ARN containing colons and slashes and an external id containing mixed
// case and hyphens without HCL-escaping damage.
func TestEC2ServiceHCL_LaunchAccountSurvivesSpecialChars(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	net.LaunchAccount = "prod-borrow"
	net.LauncherRoleARN = "arn:aws:iam::999988887777:role/prod-borrow-gpu-launcher"
	net.LauncherExternalID = "kM-Launch-Prod-Borrow-9f3E-external-id"

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net, nil, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL: %v", err)
	}

	if !strings.Contains(out, `launcher_role_arn    = "arn:aws:iam::999988887777:role/prod-borrow-gpu-launcher"`) {
		t.Errorf("launcher_role_arn with colons/slashes not preserved intact:\n%s", out)
	}
	if !strings.Contains(out, `launcher_external_id = "kM-Launch-Prod-Borrow-9f3E-external-id"`) {
		t.Errorf("launcher_external_id with mixed case/hyphens not preserved intact:\n%s", out)
	}
}

// TestEC2ServiceHCL_LaunchAccountDormantByteIdentical verifies that the dormant
// compile output (LaunchAccount left at zero value) is byte-identical to the same
// compile performed with the new fields explicitly zeroed out — i.e. the new fields
// inject no HCL tokens at all when unset.
func TestEC2ServiceHCL_LaunchAccountDormantByteIdentical(t *testing.T) {
	p := baseEC2Profile()

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	netA := baseEC2Network()
	outA, err := generateEC2ServiceHCL(p, "test-sb-baseline", true, nil, iamPolicy, "", netA, nil, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL (baseline): %v", err)
	}

	netB := baseEC2Network()
	netB.LaunchAccount = ""
	netB.LauncherRoleARN = ""
	netB.LauncherExternalID = ""
	outB, err := generateEC2ServiceHCL(p, "test-sb-baseline", true, nil, iamPolicy, "", netB, nil, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL (explicit zero values): %v", err)
	}

	if outA != outB {
		t.Error("service.hcl output changed when LaunchAccount fields are explicitly zeroed — dormant render must be byte-identical")
	}
}

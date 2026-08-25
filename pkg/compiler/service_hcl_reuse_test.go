package compiler

import (
	"strings"
	"testing"
)

// TestEC2ServiceHCL_ExistingSGAndProfileAbsent is the dormancy guard: a
// same-account sandbox must emit neither reuse input, so the ec2spot module keeps
// creating its own per-sandbox security group and instance profile exactly as it
// did before Phase 126.
func TestEC2ServiceHCL_ExistingSGAndProfileAbsent(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	// ExistingSecurityGroupID / ExistingInstanceProfile left at zero value.

	iamPolicy := &IAMSessionPolicy{MaxSessionDuration: 3600, AllowedRegions: []string{"us-east-1"}}
	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL: %v", err)
	}
	for _, tok := range []string{"existing_security_group_id", "existing_instance_profile_name"} {
		if strings.Contains(out, tok) {
			t.Errorf("service.hcl contains %q with no launch account set — dormancy guard violated:\n%s", tok, out)
		}
	}
}

// TestEC2ServiceHCL_ExistingSGAndProfileSet verifies a cross-account sandbox emits
// both reuse inputs. Without them the ec2spot module creates a per-sandbox security
// group and IAM role, which the launcher role has no permission to do — the apply
// fails with UnauthorizedOperation on ec2:CreateSecurityGroup and AccessDenied on
// iam:CreateRole (observed live, sb-ebdca692).
func TestEC2ServiceHCL_ExistingSGAndProfileSet(t *testing.T) {
	p := baseEC2Profile()
	net := baseEC2Network()
	net.LaunchAccount = "gpuman"
	net.LauncherRoleARN = "arn:aws:iam::481723467561:role/km-gpu-launcher"
	net.LauncherExternalID = "secret"
	net.ExistingSecurityGroupID = "sg-0c4d21c992351eb71"
	net.ExistingInstanceProfile = "km-gpu-box"

	iamPolicy := &IAMSessionPolicy{MaxSessionDuration: 3600, AllowedRegions: []string{"us-east-1"}}
	out, err := generateEC2ServiceHCL(p, "test-sb", true, nil, iamPolicy, "", net, nil)
	if err != nil {
		t.Fatalf("generateEC2ServiceHCL: %v", err)
	}
	for _, want := range []string{
		`existing_security_group_id     = "sg-0c4d21c992351eb71"`,
		`existing_instance_profile_name = "km-gpu-box"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("service.hcl missing %q\nGot:\n%s", want, out)
		}
	}
}

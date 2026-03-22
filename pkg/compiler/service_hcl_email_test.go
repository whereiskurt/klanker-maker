package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// TestECSSESIAMPermission verifies that ses:SendEmail IAM permission is included
// in the ECS service.hcl with a ses:FromAddress condition scoped to sandbox email.
func TestECSSESIAMPermission(t *testing.T) {
	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "sb-test1234", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "ses:SendEmail") {
		t.Error("expected ses:SendEmail in ECS service.hcl IAM policy")
	}
	// Verify the ses:FromAddress condition is scoped to the sandbox email address.
	if !strings.Contains(out, "sb-test1234@sandboxes.klankermaker.ai") {
		t.Error("expected sandbox email address (ses:FromAddress condition) in ECS service.hcl")
	}
	if !strings.Contains(out, "ses:FromAddress") {
		t.Error("expected ses:FromAddress condition variable in ECS service.hcl IAM policy")
	}
}

// TestECSS3InboxReadPermission verifies that s3:ListObjectsV2 and s3:GetObject
// IAM permissions scoped to mail/{sandbox-id}/* are included in ECS service.hcl.
func TestECSS3InboxReadPermission(t *testing.T) {
	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "sb-test1234", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "s3:ListObjectsV2") {
		t.Error("expected s3:ListObjectsV2 in ECS service.hcl IAM policy")
	}
	if !strings.Contains(out, "s3:GetObject") {
		t.Error("expected s3:GetObject in ECS service.hcl IAM policy")
	}
	// The IAM resource should reference the mail/{sandbox-id}/* prefix.
	if !strings.Contains(out, "mail/sb-test1234/") {
		t.Error("expected mail/{sandbox-id}/* prefix in ECS service.hcl S3 IAM resource")
	}
}

// TestECSKMEmailAddressEnvVar verifies that KM_EMAIL_ADDRESS env var is included
// in the main container's environment with the sandbox email address value.
func TestECSKMEmailAddressEnvVar(t *testing.T) {
	p := baseECSProfile()
	out, err := generateECSServiceHCL(p, "sb-test1234", false, nil, baseECSNetwork())
	if err != nil {
		t.Fatalf("generateECSServiceHCL failed: %v", err)
	}
	if !strings.Contains(out, "KM_EMAIL_ADDRESS") {
		t.Error("expected KM_EMAIL_ADDRESS env var in ECS main container environment")
	}
	if !strings.Contains(out, "sb-test1234@sandboxes.klankermaker.ai") {
		t.Error("expected sandbox email as KM_EMAIL_ADDRESS value in ECS service.hcl")
	}
}

// TestEC2UserDataKMEmailAddress verifies that KM_EMAIL_ADDRESS is exported in EC2 user-data.
func TestEC2UserDataKMEmailAddress(t *testing.T) {
	p := &profile.SandboxProfile{
		Metadata: profile.Metadata{Name: "test-ec2-email"},
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate:    "ec2",
				Region:       "us-east-1",
				InstanceType: "t3.medium",
			},
		},
	}
	out, err := generateUserData(p, "sb-test1234", nil, "km-sandbox-artifacts-ea554771", false)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if !strings.Contains(out, "KM_EMAIL_ADDRESS") {
		t.Error("expected KM_EMAIL_ADDRESS export in EC2 user-data")
	}
	if !strings.Contains(out, "sb-test1234@sandboxes.klankermaker.ai") {
		t.Error("expected sandbox email as KM_EMAIL_ADDRESS value in EC2 user-data")
	}
}

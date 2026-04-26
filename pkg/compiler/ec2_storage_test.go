package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// minimalEC2StorageProfile returns a minimal EC2 profile for storage validation tests.
func minimalEC2StorageProfile() *profile.SandboxProfile {
	return &profile.SandboxProfile{
		Metadata: profile.Metadata{Name: "storage-test-profile"},
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate:    "ec2",
				Spot:         false,
				InstanceType: "t3.medium",
				Region:       "us-east-1",
			},
		},
	}
}

// minimalEC2StorageNetwork returns a minimal NetworkConfig for EC2 storage validation tests.
func minimalEC2StorageNetwork() *NetworkConfig {
	return &NetworkConfig{
		VPCID:             "vpc-test",
		PublicSubnets:     []string{"subnet-a"},
		AvailabilityZones: []string{"us-east-1a"},
		RegionLabel:       "use1",
	}
}

// minimalIAMPolicy returns a minimal IAMSessionPolicy for tests.
func minimalIAMPolicy() *IAMSessionPolicy {
	return &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}
}

// TestHibernationSpotConflict verifies that hibernation=true + spot=true returns a clear error.
func TestHibernationSpotConflict(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.Hibernation = true
	p.Spec.Runtime.Spot = true

	_, err := generateEC2ServiceHCL(p, "test-sb", true, nil, nil, "", minimalEC2StorageNetwork())
	if err == nil {
		t.Fatal("expected error for hibernation=true + spot=true, got nil")
	}
	if !strings.Contains(err.Error(), "hibernation") {
		t.Errorf("expected error to mention 'hibernation', got: %v", err)
	}
	if !strings.Contains(err.Error(), "on-demand") {
		t.Errorf("expected error to mention 'on-demand', got: %v", err)
	}
}

// TestHibernationECSConflict verifies that hibernation=true on ECS substrate returns a clear error.
func TestHibernationECSConflict(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.Substrate = "ecs"
	p.Spec.Runtime.Hibernation = true

	_, err := generateEC2ServiceHCL(p, "test-sb", false, nil, nil, "", minimalEC2StorageNetwork())
	if err == nil {
		t.Fatal("expected error for hibernation=true on ECS substrate, got nil")
	}
	if !strings.Contains(err.Error(), "hibernation") {
		t.Errorf("expected error to mention 'hibernation', got: %v", err)
	}
}

// TestAdditionalVolumeECSConflict verifies that additionalVolume on ECS substrate returns a clear error.
func TestAdditionalVolumeECSConflict(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.Substrate = "ecs"
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size:       100,
		MountPoint: "/data",
	}

	_, err := generateEC2ServiceHCL(p, "test-sb", false, nil, nil, "", minimalEC2StorageNetwork())
	if err == nil {
		t.Fatal("expected error for additionalVolume on ECS substrate, got nil")
	}
	if !strings.Contains(err.Error(), "additionalVolume") {
		t.Errorf("expected error to mention 'additionalVolume', got: %v", err)
	}
}

// TestHibernationOnDemandValid verifies that hibernation=true + spot=false passes validation.
func TestHibernationOnDemandValid(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.Hibernation = true
	p.Spec.Runtime.Spot = false

	_, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Errorf("expected no error for hibernation=true + spot=false (on-demand), got: %v", err)
	}
}

// TestAdditionalVolumeEC2Valid verifies that additionalVolume on EC2 substrate passes validation.
func TestAdditionalVolumeEC2Valid(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size:       100,
		MountPoint: "/data",
		Encrypted:  true,
	}

	_, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Errorf("expected no error for additionalVolume on EC2 substrate, got: %v", err)
	}
}

// ============================================================
// Phase 33 Plan 02: HCL template output tests (TDD RED)
// ============================================================

// TestRootVolumeSizeInHCL verifies that rootVolumeSize=50 generates HCL with root_volume_size_gb = 50.
func TestRootVolumeSizeInHCL(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.RootVolumeSize = 50

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "root_volume_size_gb    = 50"
	if !strings.Contains(hcl, want) {
		t.Errorf("HCL output missing %q\ngot:\n%s", want, hcl)
	}
}

// TestRootVolumeSizeZeroInHCL verifies that rootVolumeSize=0 generates HCL with root_volume_size_gb = 0.
func TestRootVolumeSizeZeroInHCL(t *testing.T) {
	p := minimalEC2StorageProfile()
	// RootVolumeSize defaults to 0

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "root_volume_size_gb    = 0"
	if !strings.Contains(hcl, want) {
		t.Errorf("HCL output missing %q\ngot:\n%s", want, hcl)
	}
}

// TestHibernationEnabledInHCL verifies that hibernation=true generates HCL with hibernation_enabled = true.
func TestHibernationEnabledInHCL(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.Hibernation = true

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "hibernation_enabled    = true"
	if !strings.Contains(hcl, want) {
		t.Errorf("HCL output missing %q\ngot:\n%s", want, hcl)
	}
}

// TestAMISlugExplicitInHCL verifies that ami="ubuntu-24.04" generates HCL with ami_slug = "ubuntu-24.04".
func TestAMISlugExplicitInHCL(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.AMI = "ubuntu-24.04"

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `ami_slug               = "ubuntu-24.04"`
	if !strings.Contains(hcl, want) {
		t.Errorf("HCL output missing %q\ngot:\n%s", want, hcl)
	}
}

// TestAMISlugDefaultInHCL verifies that empty ami field generates HCL with ami_slug = "amazon-linux-2023".
func TestAMISlugDefaultInHCL(t *testing.T) {
	p := minimalEC2StorageProfile()
	// AMI field is empty — should default to amazon-linux-2023

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `ami_slug               = "amazon-linux-2023"`
	if !strings.Contains(hcl, want) {
		t.Errorf("HCL output missing %q\ngot:\n%s", want, hcl)
	}
}

// ============================================================
// Phase 33 Plan 03: Additional EBS volume HCL tests (TDD)
// ============================================================

// TestAdditionalVolumeInHCL verifies that additionalVolume={size:100, mountPoint:"/data", encrypted:true}
// generates HCL containing additional_volume_size_gb = 100 and additional_volume_encrypted = true.
func TestAdditionalVolumeInHCL(t *testing.T) {
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.AdditionalVolume = &profile.AdditionalVolumeSpec{
		Size:       100,
		MountPoint: "/data",
		Encrypted:  true,
	}

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantSize := "additional_volume_size_gb    = 100"
	if !strings.Contains(hcl, wantSize) {
		t.Errorf("HCL output missing %q\ngot:\n%s", wantSize, hcl)
	}
	wantEncrypted := "additional_volume_encrypted  = true"
	if !strings.Contains(hcl, wantEncrypted) {
		t.Errorf("HCL output missing %q\ngot:\n%s", wantEncrypted, hcl)
	}
}

// TestAdditionalVolumeAbsentInHCL verifies that no additionalVolume generates
// HCL containing additional_volume_size_gb = 0.
func TestAdditionalVolumeAbsentInHCL(t *testing.T) {
	p := minimalEC2StorageProfile()
	// AdditionalVolume is nil — no additional volume

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "additional_volume_size_gb    = 0"
	if !strings.Contains(hcl, want) {
		t.Errorf("HCL output missing %q\ngot:\n%s", want, hcl)
	}
}

// ============================================================
// Phase 33.1 Plan 01: isRawAMIID helper + Wave 0 HCL scaffold
// ============================================================

// TestIsRawAMIID verifies the isRawAMIID() helper function.
func TestIsRawAMIID(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"8-char valid", "ami-0abc1234", true},
		{"17-char canonical", "ami-0abcdef1234567890", true},
		{"slug ubuntu-24.04", "ubuntu-24.04", false},
		{"slug amazon-linux-2023", "amazon-linux-2023", false},
		{"empty string", "", false},
		{"uppercase hex", "ami-GGGGGGGG", false},
		{"too short 7 hex", "ami-123", false},
		{"uppercase prefix", "AMI-0abc12345", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRawAMIID(tc.input)
			if got != tc.want {
				t.Errorf("isRawAMIID(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestAMIRawIDInHCL verifies that a raw AMI ID emits ami_id and ami_slug = "" in HCL.
// Wave 0: red until Plan 02 wires HCL template to emit ami_id.
func TestAMIRawIDInHCL(t *testing.T) {
	// Wave 0: red until Plan 02 wires HCL template to emit ami_id.
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.AMI = "ami-0abcdef1234567890"

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantAMIID := `ami_id                 = "ami-0abcdef1234567890"`
	if !strings.Contains(hcl, wantAMIID) {
		t.Errorf("HCL output missing %q\ngot:\n%s", wantAMIID, hcl)
	}
	wantSlugEmpty := `ami_slug               = ""`
	if !strings.Contains(hcl, wantSlugEmpty) {
		t.Errorf("HCL output missing %q\ngot:\n%s", wantSlugEmpty, hcl)
	}
}

// TestAMISlugInHCLEmitsEmptyAMIID verifies that a slug path emits ami_id = "" alongside ami_slug.
// Wave 0: red until Plan 02 wires HCL template to emit ami_id.
func TestAMISlugInHCLEmitsEmptyAMIID(t *testing.T) {
	// Wave 0: red until Plan 02 wires HCL template to emit ami_id.
	p := minimalEC2StorageProfile()
	p.Spec.Runtime.AMI = "ubuntu-24.04"

	hcl, err := generateEC2ServiceHCL(p, "test-sb", false, nil, minimalIAMPolicy(), "", minimalEC2StorageNetwork())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantSlug := `ami_slug               = "ubuntu-24.04"`
	if !strings.Contains(hcl, wantSlug) {
		t.Errorf("HCL output missing %q\ngot:\n%s", wantSlug, hcl)
	}
	wantAMIIDEmpty := `ami_id                 = ""`
	if !strings.Contains(hcl, wantAMIIDEmpty) {
		t.Errorf("HCL output missing %q\ngot:\n%s", wantAMIIDEmpty, hcl)
	}
}

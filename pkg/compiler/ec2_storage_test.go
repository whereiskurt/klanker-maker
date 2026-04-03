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

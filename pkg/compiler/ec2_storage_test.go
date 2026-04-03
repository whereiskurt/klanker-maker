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

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	_, err := generateEC2ServiceHCL(p, "test-sb", false, nil, iamPolicy, "", minimalEC2StorageNetwork())
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

	iamPolicy := &IAMSessionPolicy{
		MaxSessionDuration: 3600,
		AllowedRegions:     []string{"us-east-1"},
	}

	_, err := generateEC2ServiceHCL(p, "test-sb", false, nil, iamPolicy, "", minimalEC2StorageNetwork())
	if err != nil {
		t.Errorf("expected no error for additionalVolume on EC2 substrate, got: %v", err)
	}
}

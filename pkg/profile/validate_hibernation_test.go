package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// hibernationProfile builds the smallest SandboxProfile that exercises the
// hibernation rootVolumeSize check: enough Runtime fields populated for the
// rule to fire, nothing else.
func hibernationProfile(instanceType string, rootVolumeSizeGB int, hibernation bool) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha1",
		Kind:       "SandboxProfile",
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				Substrate:      "ec2",
				InstanceType:   instanceType,
				RootVolumeSize: rootVolumeSizeGB,
				Hibernation:    hibernation,
			},
		},
	}
}

func hasHibernationRootVolumeError(errs []profile.ValidationError) bool {
	for _, e := range errs {
		if e.IsWarning {
			continue
		}
		if strings.Contains(e.Message, "hibernation") && strings.Contains(e.Message, "rootVolumeSize") {
			return true
		}
	}
	return false
}

// TestValidateSemantic_HibernationRootVolume_TooSmall reproduces the user-visible
// failure: t3.2xlarge (32 GiB RAM) with rootVolumeSize=15 plus hibernation=true
// is rejected by EC2 at RunInstances time. Catch it at km validate / km create.
func TestValidateSemantic_HibernationRootVolume_TooSmall(t *testing.T) {
	p := hibernationProfile("t3.2xlarge", 15, true)
	errs := profile.ValidateSemantic(p)
	if !hasHibernationRootVolumeError(errs) {
		t.Fatalf("expected hibernation rootVolumeSize error for t3.2xlarge+15GB+hibernation, got: %v", errs)
	}
}

// TestValidateSemantic_HibernationRootVolume_EqualToRAM_StillFails verifies that
// rootVolumeSize == RAM is rejected (AWS requires strictly greater — the volume
// must hold the RAM dump plus the OS).
func TestValidateSemantic_HibernationRootVolume_EqualToRAM_StillFails(t *testing.T) {
	p := hibernationProfile("t3.2xlarge", 32, true)
	errs := profile.ValidateSemantic(p)
	if !hasHibernationRootVolumeError(errs) {
		t.Fatalf("expected error for rootVolumeSize==RAM, got: %v", errs)
	}
}

// TestValidateSemantic_HibernationRootVolume_Sufficient verifies the happy path:
// rootVolumeSize comfortably above RAM produces no error.
func TestValidateSemantic_HibernationRootVolume_Sufficient(t *testing.T) {
	p := hibernationProfile("t3.2xlarge", 50, true)
	errs := profile.ValidateSemantic(p)
	if hasHibernationRootVolumeError(errs) {
		t.Errorf("expected no hibernation error for rootVolumeSize=50 on t3.2xlarge, got: %v", errs)
	}
}

// TestValidateSemantic_HibernationRootVolume_SmallInstance verifies the
// previous-working config still validates: t3.large (8 GiB) with rootVolumeSize=15.
func TestValidateSemantic_HibernationRootVolume_SmallInstance(t *testing.T) {
	p := hibernationProfile("t3.large", 15, true)
	errs := profile.ValidateSemantic(p)
	if hasHibernationRootVolumeError(errs) {
		t.Errorf("expected no hibernation error for t3.large+15GB, got: %v", errs)
	}
}

// TestValidateSemantic_HibernationRootVolume_NoHibernation verifies that the
// check is gated on hibernation=true. Same too-small volume must pass when
// hibernation is off.
func TestValidateSemantic_HibernationRootVolume_NoHibernation(t *testing.T) {
	p := hibernationProfile("t3.2xlarge", 15, false)
	errs := profile.ValidateSemantic(p)
	if hasHibernationRootVolumeError(errs) {
		t.Errorf("expected no hibernation error when hibernation=false, got: %v", errs)
	}
}

// TestValidateSemantic_HibernationRootVolume_UnknownInstanceType verifies that
// instance types absent from the RAM table do not produce false positives —
// the rule fails open so a new/unknown type is not blocked.
func TestValidateSemantic_HibernationRootVolume_UnknownInstanceType(t *testing.T) {
	p := hibernationProfile("zz9.plural-z-alpha", 15, true)
	errs := profile.ValidateSemantic(p)
	if hasHibernationRootVolumeError(errs) {
		t.Errorf("expected no error for unknown instance type, got: %v", errs)
	}
}

// TestValidateSemantic_HibernationRootVolume_DefaultVolumeSize verifies that
// rootVolumeSize=0 (use AMI default) skips the check. We can't compare against
// an unknown AMI default at semantic-validation time.
func TestValidateSemantic_HibernationRootVolume_DefaultVolumeSize(t *testing.T) {
	p := hibernationProfile("t3.2xlarge", 0, true)
	errs := profile.ValidateSemantic(p)
	if hasHibernationRootVolumeError(errs) {
		t.Errorf("expected no error when rootVolumeSize=0 (AMI default), got: %v", errs)
	}
}

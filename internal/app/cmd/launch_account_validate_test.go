package cmd

// Phase 126 Plan 01 Task 3: ValidateLaunchAccountLink unit tests (Tests 4-7).
// White-box (package cmd) so *config.Config and *profile.SandboxProfile can be
// constructed directly without spinning up a full km binary — mirrors the
// bootstrap_84_3_test.go convention for exercising cmd-package logic.

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// profileWithLaunchAccount builds a minimal *profile.SandboxProfile with the
// given launchAccount + instanceType, sufficient for ValidateLaunchAccountLink's
// two reads (Spec.Runtime.LaunchAccount, Spec.Runtime.InstanceType).
func profileWithLaunchAccount(launchAccount, instanceType string) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		Spec: profile.Spec{
			Runtime: profile.RuntimeSpec{
				LaunchAccount: launchAccount,
				InstanceType:  instanceType,
			},
		},
	}
}

// TestValidateLaunchAccountLink_UnknownLink (Test 4): a profile naming a link
// absent from the config returns a non-warning error naming the unknown link
// and the km account register remediation.
func TestValidateLaunchAccountLink_UnknownLink(t *testing.T) {
	cfg := &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"other-link": {AccountID: "999988887777"},
		},
	}
	p := profileWithLaunchAccount("mgmt-gpu", "g6e.12xlarge")

	errs := ValidateLaunchAccountLink(p, cfg)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %+v", len(errs), errs)
	}
	if errs[0].IsWarning {
		t.Error("expected a non-warning error for an unknown link, got IsWarning=true")
	}
	if errs[0].Path != "spec.runtime.launchAccount" {
		t.Errorf("Path: got %q, want %q", errs[0].Path, "spec.runtime.launchAccount")
	}
	msg := errs[0].Message
	if !strings.Contains(msg, "mgmt-gpu") {
		t.Errorf("Message does not name the unknown link %q: %s", "mgmt-gpu", msg)
	}
	if !strings.Contains(msg, "km account register") {
		t.Errorf("Message does not name the km account register remediation: %s", msg)
	}
}

// TestValidateLaunchAccountLink_KnownLink (Test 5): the same profile against a
// config that HAS the link returns no errors.
func TestValidateLaunchAccountLink_KnownLink(t *testing.T) {
	cfg := &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"mgmt-gpu": {AccountID: "111122223333"},
		},
	}
	p := profileWithLaunchAccount("mgmt-gpu", "g6e.12xlarge")

	errs := ValidateLaunchAccountLink(p, cfg)
	if len(errs) != 0 {
		t.Errorf("expected no errors for a known link with no instance-type allowlist, got %d: %+v", len(errs), errs)
	}
}

// TestValidateLaunchAccountLink_OffAllowlistInstanceType (Test 6): a profile
// whose instanceType is absent from the link's instance_types allowlist returns
// a WARNING (not an error).
func TestValidateLaunchAccountLink_OffAllowlistInstanceType(t *testing.T) {
	cfg := &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"mgmt-gpu": {
				AccountID:     "111122223333",
				InstanceTypes: []string{"g6e.12xlarge", "g6e.48xlarge"},
			},
		},
	}
	p := profileWithLaunchAccount("mgmt-gpu", "t3.medium")

	errs := ValidateLaunchAccountLink(p, cfg)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %+v", len(errs), errs)
	}
	if !errs[0].IsWarning {
		t.Error("expected a WARNING for an off-allowlist instance type, got IsWarning=false (hard error)")
	}
	if !strings.Contains(errs[0].Message, "t3.medium") {
		t.Errorf("Message does not name the requested instance type: %s", errs[0].Message)
	}
	if !strings.Contains(errs[0].Message, "g6e.12xlarge") {
		t.Errorf("Message does not name the allowlist: %s", errs[0].Message)
	}
}

// TestValidateLaunchAccountLink_Dormant (Test 7): a profile with an empty
// LaunchAccount returns nil regardless of config contents — no config lookup
// is performed at all.
func TestValidateLaunchAccountLink_Dormant(t *testing.T) {
	cfg := &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"mgmt-gpu": {AccountID: "111122223333"},
		},
	}
	p := profileWithLaunchAccount("", "t3.medium")

	errs := ValidateLaunchAccountLink(p, cfg)
	if errs != nil {
		t.Errorf("expected nil for an empty launchAccount, got %+v", errs)
	}

	// Also verify with a nil config — dormancy must not even dereference cfg.
	errs = ValidateLaunchAccountLink(p, nil)
	if errs != nil {
		t.Errorf("expected nil for an empty launchAccount even with a nil *config.Config, got %+v", errs)
	}
}

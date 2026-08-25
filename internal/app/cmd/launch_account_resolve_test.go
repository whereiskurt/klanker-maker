package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// countingSSMStore is an SSMParamStore stub that records every Get() call, so
// the dormancy test (Test 1) can assert a zero call count.
type countingSSMStore struct {
	vals    map[string]string
	getErr  error
	calls   int
	lastKey string
}

func (s *countingSSMStore) Get(_ context.Context, name string, _ bool) (string, error) {
	s.calls++
	s.lastKey = name
	if s.getErr != nil {
		return "", s.getErr
	}
	return s.vals[name], nil
}

func testLaunchAccountConfig() *config.Config {
	return &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"mgmt-gpu": {
				AccountID:         "222233334444",
				LauncherRoleARN:   "arn:aws:iam::222233334444:role/km-gpu-launcher",
				Region:            "us-west-2",
				SubnetIDs:         []string{"subnet-a", "subnet-b"},
				AvailabilityZones: []string{"us-west-2a", "us-west-2b"},
				SecurityGroupID:   "sg-0123456789",
				ResultsBucket:     "km-results-222233334444",
				ExternalIDSSM:     "/km/launch-accounts/mgmt-gpu/external-id",
			},
		},
	}
}

// Test 1: a profile with no launch account and no CLI override resolves to a nil
// target and a nil error, performing no config lookup and no parameter read.
func TestResolveLaunchTarget_Dormant(t *testing.T) {
	p := &profile.SandboxProfile{}
	ssmStore := &countingSSMStore{}

	target, err := ResolveLaunchTarget(context.Background(), testLaunchAccountConfig(), p, nil, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != nil {
		t.Fatalf("expected nil target, got %+v", target)
	}
	if ssmStore.calls != 0 {
		t.Errorf("expected zero SSM Get calls (dormancy proof), got %d", ssmStore.calls)
	}
}

// Test 2: a CLI override selects the named link even when the profile names a
// different one.
func TestResolveLaunchTarget_CLIOverrideWins(t *testing.T) {
	cfg := testLaunchAccountConfig()
	cfg.LaunchAccounts["other-link"] = config.LaunchAccountConfig{
		AccountID:         "999988887777",
		LauncherRoleARN:   "arn:aws:iam::999988887777:role/km-gpu-launcher",
		Region:            "us-east-2",
		SubnetIDs:         []string{"subnet-c"},
		AvailabilityZones: []string{"us-east-2a"},
		ExternalIDSSM:     "/km/launch-accounts/other-link/external-id",
	}
	p := &profile.SandboxProfile{}
	p.Spec.Runtime.LaunchAccount = "mgmt-gpu"

	ssmStore := &countingSSMStore{vals: map[string]string{
		"/km/launch-accounts/other-link/external-id": "ext-other",
	}}

	target, err := ResolveLaunchTarget(context.Background(), cfg, p, strPtr("other-link"), ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target == nil {
		t.Fatal("expected a resolved target")
	}
	if target.LinkName != "other-link" {
		t.Errorf("LinkName: got %q, want other-link", target.LinkName)
	}
}

// Test 3: an explicitly empty CLI override forces the home account even when the
// profile names a link.
func TestResolveLaunchTarget_CLIOverrideForcesHome(t *testing.T) {
	p := &profile.SandboxProfile{}
	p.Spec.Runtime.LaunchAccount = "mgmt-gpu"
	ssmStore := &countingSSMStore{}

	target, err := ResolveLaunchTarget(context.Background(), testLaunchAccountConfig(), p, strPtr(""), ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != nil {
		t.Fatalf("expected nil target (forced home), got %+v", target)
	}
	if ssmStore.calls != 0 {
		t.Errorf("expected zero SSM Get calls, got %d", ssmStore.calls)
	}
}

// Test 4: an unknown link name returns an error naming the link and the register
// command.
func TestResolveLaunchTarget_UnknownLink(t *testing.T) {
	p := &profile.SandboxProfile{}
	p.Spec.Runtime.LaunchAccount = "does-not-exist"
	ssmStore := &countingSSMStore{}

	target, err := ResolveLaunchTarget(context.Background(), testLaunchAccountConfig(), p, nil, ssmStore)
	if err == nil {
		t.Fatal("expected an error for an unknown link")
	}
	if target != nil {
		t.Fatalf("expected nil target on error, got %+v", target)
	}
	if !containsAll(err.Error(), "does-not-exist", "km account register") {
		t.Errorf("error message missing link name or register command: %v", err)
	}
}

// Test 5: a resolved target carries the link's account id, launcher ARN, region,
// subnet list, availability-zone list, security group and results bucket, plus
// the external id read from the parameter store with decryption requested.
func TestResolveLaunchTarget_PopulatesTarget(t *testing.T) {
	p := &profile.SandboxProfile{}
	p.Spec.Runtime.LaunchAccount = "mgmt-gpu"

	ssmStore := &countingSSMStore{vals: map[string]string{
		"/km/launch-accounts/mgmt-gpu/external-id": "secret-ext-id",
	}}

	target, err := ResolveLaunchTarget(context.Background(), testLaunchAccountConfig(), p, nil, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target == nil {
		t.Fatal("expected a resolved target")
	}
	if target.AccountID != "222233334444" {
		t.Errorf("AccountID: got %q", target.AccountID)
	}
	if target.LauncherRoleARN != "arn:aws:iam::222233334444:role/km-gpu-launcher" {
		t.Errorf("LauncherRoleARN: got %q", target.LauncherRoleARN)
	}
	if target.Region != "us-west-2" {
		t.Errorf("Region: got %q", target.Region)
	}
	if len(target.SubnetIDs) != 2 || target.SubnetIDs[0] != "subnet-a" {
		t.Errorf("SubnetIDs: got %v", target.SubnetIDs)
	}
	if len(target.AvailabilityZones) != 2 || target.AvailabilityZones[0] != "us-west-2a" {
		t.Errorf("AvailabilityZones: got %v", target.AvailabilityZones)
	}
	if target.SecurityGroupID != "sg-0123456789" {
		t.Errorf("SecurityGroupID: got %q", target.SecurityGroupID)
	}
	if target.ResultsBucket != "km-results-222233334444" {
		t.Errorf("ResultsBucket: got %q", target.ResultsBucket)
	}
	if target.ExternalID != "secret-ext-id" {
		t.Errorf("ExternalID: got %q", target.ExternalID)
	}
	if ssmStore.calls != 1 {
		t.Errorf("expected exactly one SSM Get call, got %d", ssmStore.calls)
	}
}

// Test 6: a parameter-store read failure is a hard error — the target is not
// returned with an empty external id.
func TestResolveLaunchTarget_ParamStoreReadFailureIsFatal(t *testing.T) {
	p := &profile.SandboxProfile{}
	p.Spec.Runtime.LaunchAccount = "mgmt-gpu"
	ssmStore := &countingSSMStore{getErr: errors.New("ssm: throttled")}

	target, err := ResolveLaunchTarget(context.Background(), testLaunchAccountConfig(), p, nil, ssmStore)
	if err == nil {
		t.Fatal("expected an error on parameter-store read failure")
	}
	if target != nil {
		t.Fatalf("expected nil target on error — must never return a target with an empty external id, got %+v", target)
	}
}

// Test 6b: an empty (missing) external id at the recorded path is also fatal.
func TestResolveLaunchTarget_EmptyExternalIDIsFatal(t *testing.T) {
	p := &profile.SandboxProfile{}
	p.Spec.Runtime.LaunchAccount = "mgmt-gpu"
	ssmStore := &countingSSMStore{} // no value at the path — Get returns ""

	target, err := ResolveLaunchTarget(context.Background(), testLaunchAccountConfig(), p, nil, ssmStore)
	if err == nil {
		t.Fatal("expected an error for an empty external id")
	}
	if target != nil {
		t.Fatalf("expected nil target on error, got %+v", target)
	}
}

// Test 7: a link whose subnet list and availability-zone list have different
// lengths is rejected, because downstream code pairs them by index.
func TestResolveLaunchTarget_SubnetAZLengthMismatch(t *testing.T) {
	cfg := &config.Config{
		LaunchAccounts: map[string]config.LaunchAccountConfig{
			"broken-link": {
				AccountID:         "111122223333",
				LauncherRoleARN:   "arn:aws:iam::111122223333:role/km-gpu-launcher",
				Region:            "us-west-2",
				SubnetIDs:         []string{"subnet-a", "subnet-b"},
				AvailabilityZones: []string{"us-west-2a"},
				ExternalIDSSM:     "/km/launch-accounts/broken-link/external-id",
			},
		},
	}
	p := &profile.SandboxProfile{}
	p.Spec.Runtime.LaunchAccount = "broken-link"
	ssmStore := &countingSSMStore{}

	target, err := ResolveLaunchTarget(context.Background(), cfg, p, nil, ssmStore)
	if err == nil {
		t.Fatal("expected an error for mismatched subnet/AZ list lengths")
	}
	if target != nil {
		t.Fatalf("expected nil target on error, got %+v", target)
	}
	if ssmStore.calls != 0 {
		t.Errorf("expected the length check to fail before any SSM read, got %d calls", ssmStore.calls)
	}
}

// Test 8: checkLaunchAccountEFSGuard rejects a profile requesting the shared
// filesystem against a link with no filesystem id, naming the enrollment flag.
func TestCheckLaunchAccountEFSGuard(t *testing.T) {
	if err := checkLaunchAccountEFSGuard(false, ""); err != nil {
		t.Errorf("profile not requesting EFS should never error: %v", err)
	}
	if err := checkLaunchAccountEFSGuard(true, "fs-0123456789"); err != nil {
		t.Errorf("link with an EFS id should never error: %v", err)
	}
	err := checkLaunchAccountEFSGuard(true, "")
	if err == nil {
		t.Fatal("expected an error when the profile wants EFS but the link has none")
	}
	if !containsAll(err.Error(), "--provision-efs") {
		t.Errorf("error message missing the enrollment flag: %v", err)
	}
}

// containsAll reports whether s contains every one of subs.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

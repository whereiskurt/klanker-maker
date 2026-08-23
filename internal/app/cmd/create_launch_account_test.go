package cmd

// Phase 126 Plan 06 Task 2/3: tests for the pure launch-account helpers wired
// into the local and remote create paths and the capacity gate. Mirrors the
// create_private_subnet_test.go / create_docker_test.go convention: runCreate
// and runCreateRemote are large integration functions that fail-fast against
// LoadAWSConfig under this package's TestMain seam, so the placement logic is
// factored into small pure functions (applyLaunchAccountNetwork,
// resolveLaunchRegion, launchAccountCapacityNamespace,
// resolveLaunchNATServedAZs, buildCapacityAWSConfig) and tested directly, with
// source-text structural checks covering the wiring that connects them.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

func testTarget() *LaunchTarget {
	return &LaunchTarget{
		LinkName:          "mgmt-gpu",
		AccountID:         "222233334444",
		LauncherRoleARN:   "arn:aws:iam::222233334444:role/km-gpu-launcher",
		ExternalID:        "secret-ext-id",
		Region:            "us-west-2",
		SubnetIDs:         []string{"subnet-a", "subnet-b"},
		AvailabilityZones: []string{"us-west-2a", "us-west-2b"},
		SecurityGroupID:   "sg-0123456789",
		ResultsBucket:     "km-results-222233334444",
		EFSID:             "fs-0123456789",
	}
}

// Test 1 (dormancy): with no launch target, none of the launch-account fields
// on a fresh NetworkConfig are ever populated — applyLaunchAccountNetwork is
// simply never called (the create.go call site is gated on launchTarget != nil,
// verified structurally below), so the zero-value NetworkConfig proves the
// three fields stay empty.
func TestApplyLaunchAccountNetwork_DormantFieldsStayEmpty(t *testing.T) {
	network := &compiler.NetworkConfig{PublicSubnets: []string{"subnet-home"}, AvailabilityZones: []string{"us-east-1a"}}
	if network.LaunchAccount != "" || network.LauncherRoleARN != "" || network.LauncherExternalID != "" {
		t.Fatalf("expected zero-value launch-account fields, got %+v", network)
	}
}

// Test 2: applyLaunchAccountNetwork sources subnets (both PublicSubnets and
// SandboxSubnets — a linked account's subnets are always public), availability
// zones, and the three service.hcl launch-account locals from the target.
func TestApplyLaunchAccountNetwork_PopulatesFromTarget(t *testing.T) {
	target := testTarget()
	network := &compiler.NetworkConfig{}
	applyLaunchAccountNetwork(network, target)

	if len(network.PublicSubnets) != 2 || network.PublicSubnets[0] != "subnet-a" {
		t.Errorf("PublicSubnets: got %v", network.PublicSubnets)
	}
	if len(network.SandboxSubnets) != 2 || network.SandboxSubnets[0] != "subnet-a" {
		t.Errorf("SandboxSubnets: got %v", network.SandboxSubnets)
	}
	if len(network.AvailabilityZones) != 2 || network.AvailabilityZones[0] != "us-west-2a" {
		t.Errorf("AvailabilityZones: got %v", network.AvailabilityZones)
	}
	if network.LaunchAccount != "mgmt-gpu" {
		t.Errorf("LaunchAccount: got %q", network.LaunchAccount)
	}
	if network.LauncherRoleARN != target.LauncherRoleARN {
		t.Errorf("LauncherRoleARN: got %q", network.LauncherRoleARN)
	}
	if network.LauncherExternalID != "secret-ext-id" {
		t.Errorf("LauncherExternalID: got %q", network.LauncherExternalID)
	}
}

// Test 3: the capacity store namespace is "" for a home launch and the link's
// account id for a linked one — locking in the pre-126 bare-key behavior for
// home (pkg/capacity.DynamoCapacityStore's doc comment: namespacing home would
// orphan every accumulated sticky-AZ record).
func TestLaunchAccountCapacityNamespace(t *testing.T) {
	if got := launchAccountCapacityNamespace(nil); got != "" {
		t.Errorf("home (nil target): got %q, want empty", got)
	}
	if got := launchAccountCapacityNamespace(testTarget()); got != "222233334444" {
		t.Errorf("linked target: got %q, want account id", got)
	}
}

// Test 4: the NAT-served-zone filter passed to RankAZs is nil for a linked
// launch regardless of the profile's privateSubnet bool or the home network's
// NAT gateways — a filter derived from a NAT-less network would drop every AZ.
func TestResolveLaunchNATServedAZs(t *testing.T) {
	azs := []string{"us-east-1a", "us-east-1b"}
	natIDs := []string{"nat-1", ""}

	if got := resolveLaunchNATServedAZs(testTarget(), true, azs, natIDs); got != nil {
		t.Errorf("linked launch: expected nil NAT filter regardless of wantsPrivate, got %v", got)
	}
	if got := resolveLaunchNATServedAZs(testTarget(), false, azs, natIDs); got != nil {
		t.Errorf("linked launch: expected nil NAT filter, got %v", got)
	}

	// Home launch (target == nil): unchanged Phase 125 behavior.
	if got := resolveLaunchNATServedAZs(nil, false, azs, natIDs); got != nil {
		t.Errorf("home, public: expected nil NAT filter, got %v", got)
	}
	got := resolveLaunchNATServedAZs(nil, true, azs, natIDs)
	if len(got) != 1 || got[0] != "us-east-1a" {
		t.Errorf("home, private: expected [us-east-1a] (only the NAT-served AZ), got %v", got)
	}
}

// Test 5 (T-126-29, the single most dangerous silent-wrong-answer in this
// phase): a failure to build the assumed-role config aborts with an error
// naming the launcher role — it never proceeds with the home account's config.
func TestBuildCapacityAWSConfig_HomeUnchanged(t *testing.T) {
	base := awssdk.Config{Region: "us-east-1"}
	cfg, err := buildCapacityAWSConfig(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("unexpected error for a home launch: %v", err)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("expected base config to pass through unchanged, got region %q", cfg.Region)
	}
}

func TestBuildCapacityAWSConfig_AssumeFailureIsFatalNoFallback(t *testing.T) {
	orig := awspkg.AssumeRoleConfig
	defer func() { awspkg.AssumeRoleConfig = orig }()
	awspkg.AssumeRoleConfig = func(_ context.Context, _ awssdk.Config, _, _, _ string) (awssdk.Config, error) {
		return awssdk.Config{}, errors.New("sts: access denied")
	}

	base := awssdk.Config{Region: "us-east-1"}
	target := testTarget()
	cfg, err := buildCapacityAWSConfig(context.Background(), base, target)
	if err == nil {
		t.Fatal("expected an error when the assume-role call fails")
	}
	if !strings.Contains(err.Error(), target.LauncherRoleARN) {
		t.Errorf("error must name the launcher role ARN: %v", err)
	}
	// The returned config must never be the (or resemble the) base/home config —
	// a caller that ignored the error and used cfg anyway must not silently see
	// the home account's region.
	if cfg.Region == base.Region {
		t.Errorf("returned config must not fall back to base/home region on failure, got %q", cfg.Region)
	}
}

func TestBuildCapacityAWSConfig_AssumeSuccessUsesAssumedConfig(t *testing.T) {
	orig := awspkg.AssumeRoleConfig
	defer func() { awspkg.AssumeRoleConfig = orig }()
	var gotRoleARN, gotExternalID, gotRegion string
	awspkg.AssumeRoleConfig = func(_ context.Context, base awssdk.Config, roleARN, externalID, region string) (awssdk.Config, error) {
		gotRoleARN, gotExternalID, gotRegion = roleARN, externalID, region
		assumed := base.Copy()
		assumed.Region = region
		return assumed, nil
	}

	base := awssdk.Config{Region: "us-east-1"}
	target := testTarget()
	cfg, err := buildCapacityAWSConfig(context.Background(), base, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Region != target.Region {
		t.Errorf("expected assumed config region %q, got %q", target.Region, cfg.Region)
	}
	if gotRoleARN != target.LauncherRoleARN || gotExternalID != target.ExternalID || gotRegion != target.Region {
		t.Errorf("AssumeRoleConfig called with wrong args: role=%q ext=%q region=%q", gotRoleARN, gotExternalID, gotRegion)
	}
}

// Test 7: resolveLaunchRegion carries the account's region and warns (without
// erroring) on a conflicting profile region; a home launch (nil target) is
// unaffected.
func TestResolveLaunchRegion(t *testing.T) {
	target := testTarget()

	region, warning := resolveLaunchRegion("", nil)
	if region != "" || warning != "" {
		t.Errorf("home, no region set: got region=%q warning=%q", region, warning)
	}

	region, warning = resolveLaunchRegion("us-east-1", nil)
	if region != "us-east-1" || warning != "" {
		t.Errorf("home: got region=%q warning=%q, want profile region unchanged and no warning", region, warning)
	}

	region, warning = resolveLaunchRegion("", target)
	if region != target.Region || warning != "" {
		t.Errorf("linked, no profile region: got region=%q warning=%q", region, warning)
	}

	region, warning = resolveLaunchRegion(target.Region, target)
	if region != target.Region || warning != "" {
		t.Errorf("linked, matching profile region: got region=%q warning=%q, want no warning", region, warning)
	}

	region, warning = resolveLaunchRegion("us-east-1", target)
	if region != target.Region {
		t.Errorf("linked, conflicting profile region: got region=%q, want the link's region %q", region, target.Region)
	}
	if warning == "" || !strings.Contains(warning, "us-east-1") || !strings.Contains(warning, target.Region) {
		t.Errorf("expected a warning naming both regions, got %q", warning)
	}
}

// Test 6: the sandbox metadata write records the resolved link's name on
// meta.LaunchAccount, gated on launchTarget != nil, in both create paths.
// Source-text structural check (mirrors create_private_subnet_test.go's
// convention) — runCreate/runCreateRemote are integration functions that
// cannot run end-to-end under this package's AWS-fail-fast TestMain seam.
func TestSandboxMetadata_LaunchAccountFieldWired(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	if got := strings.Count(s, "meta.LaunchAccount = launchTarget.LinkName"); got != 1 {
		t.Errorf("expected exactly one local-path meta.LaunchAccount assignment, found %d", got)
	}
	if got := strings.Count(s, "startingMeta.LaunchAccount = launchTarget.LinkName"); got != 1 {
		t.Errorf("expected exactly one remote-path startingMeta.LaunchAccount assignment, found %d", got)
	}
}

// Both the local and remote network-resolution sites in create.go reference
// the resolved launch target — the specific way an unwired remote path would
// silently launch into the home account with a link-shaped profile (T-126-31).
func TestCreateGo_BothNetworkResolutionSitesReferenceLaunchTarget(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	if got := strings.Count(string(src), "applyLaunchAccountNetwork(network, launchTarget)"); got != 2 {
		t.Errorf("expected exactly two applyLaunchAccountNetwork call sites (local + remote), found %d", got)
	}
}

// Both NewDynamoCapacityStore call sites in create.go (local AZ-ranking block)
// must route through the single namespaced constructor call — never a second,
// diverging call site.
func TestCreateGo_SingleNewDynamoCapacityStoreCallSite(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	if got := strings.Count(string(src), "NewDynamoCapacityStore("); got != 1 {
		t.Errorf("expected exactly one NewDynamoCapacityStore( call site in create.go, found %d", got)
	}
}

// The assume-role failure branch in the AZ-ranking block must return before
// constructing any client from the home config — there must be no continuing
// branch that falls back to awsCfg after buildCapacityAWSConfig fails.
func TestCreateGo_AssumeFailureAbortsBeforeHomeFallback(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)
	idx := strings.Index(s, "capacityCfg, cfgErr := buildCapacityAWSConfig(ctx, awsCfg, launchTarget)")
	if idx == -1 {
		t.Fatal("buildCapacityAWSConfig call site not found in create.go")
	}
	// The very next non-blank statement after the call must be the fatal return.
	tail := s[idx:]
	returnIdx := strings.Index(tail, "return cfgErr")
	ec2Idx := strings.Index(tail, "ec2svc.NewFromConfig(capacityCfg")
	if returnIdx == -1 || ec2Idx == -1 {
		t.Fatal("expected both the fatal return and the ec2Offerings client construction after buildCapacityAWSConfig")
	}
	if returnIdx >= ec2Idx {
		t.Error("the fatal return on assume failure must precede client construction — found ec2 client built first")
	}
}

// `km create --help` lists --launch-account.
func TestCreateCmd_LaunchAccountFlagRegistered(t *testing.T) {
	cmd := NewCreateCmd(nil)
	flag := cmd.Flags().Lookup("launch-account")
	if flag == nil {
		t.Fatal("--launch-account flag not registered on km create")
	}
	if flag.DefValue != "" {
		t.Errorf("expected default empty string, got %q", flag.DefValue)
	}
}

// `km capacity --help` lists --launch-account.
func TestCapacityCmd_LaunchAccountFlagRegistered(t *testing.T) {
	cmd := newCapacityCmd(nil)
	flag := cmd.Flags().Lookup("launch-account")
	if flag == nil {
		t.Fatal("--launch-account flag not registered on km capacity")
	}
	if flag.DefValue != "" {
		t.Errorf("expected default empty string, got %q", flag.DefValue)
	}
}

// The capacity report header always names the account being reported on
// (T-126-34) — "home" for a nil target, "{link} ({accountID})" for a linked
// one — so a reader can never confuse a linked report for a home one.
func TestLaunchAccountReportLabel(t *testing.T) {
	if got := launchAccountReportLabel(nil); got != "home" {
		t.Errorf("home: got %q, want \"home\"", got)
	}
	target := testTarget()
	want := "mgmt-gpu (222233334444)"
	if got := launchAccountReportLabel(target); got != want {
		t.Errorf("linked: got %q, want %q", got, want)
	}
}

// capacity.go must route through the single namespaced NewDynamoCapacityStore
// constructor call — never a second, diverging call site.
func TestCapacityGo_SingleNewDynamoCapacityStoreCallSite(t *testing.T) {
	src, err := os.ReadFile("capacity.go")
	if err != nil {
		t.Fatalf("read capacity.go: %v", err)
	}
	if got := strings.Count(string(src), "NewDynamoCapacityStore("); got != 1 {
		t.Errorf("expected exactly one NewDynamoCapacityStore( call site in capacity.go, found %d", got)
	}
}

// Test 6 (the single most dangerous silent-wrong-answer in this phase,
// mirrored on the capacity command): the assume-role failure branch in
// runCapacity must return before constructing any client — no report is ever
// produced with the home account's numbers under the link's name.
func TestCapacityGo_AssumeFailureAbortsBeforeAnyClient(t *testing.T) {
	src, err := os.ReadFile("capacity.go")
	if err != nil {
		t.Fatalf("read capacity.go: %v", err)
	}
	s := string(src)
	idx := strings.Index(s, "capacityCfg, cfgErr := buildCapacityAWSConfig(ctx, awsCfg, launchTarget)")
	if idx == -1 {
		t.Fatal("buildCapacityAWSConfig call site not found in capacity.go")
	}
	tail := s[idx:]
	returnIdx := strings.Index(tail, "return cfgErr")
	ec2Idx := strings.Index(tail, "ec2.NewFromConfig(capacityCfg")
	if returnIdx == -1 || ec2Idx == -1 {
		t.Fatal("expected both the fatal return and the ec2Client construction after buildCapacityAWSConfig")
	}
	if returnIdx >= ec2Idx {
		t.Error("the fatal return on assume failure must precede client construction — found ec2 client built first")
	}
}

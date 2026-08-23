package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// homeDestroyExpectedHCL is the pre-Phase-126 literal `minimalHCL` output,
// embedded verbatim so Test 3's home half is a byte-equality assertion, not a
// structural one — see the plan's acceptance criteria.
const homeDestroyExpectedHCL = `# Minimal service.hcl for state resolution during destroy
locals {
  sandbox_id       = "km-abc12345"
  region_label     = "use1"
  region_full      = ""
  substrate_module = "ec2spot"
  module_inputs    = {}
}
`

// Test 1 (+ Pattern 5 fix chain): discovery does not fail when the home-account
// tag scan returns not-found for a sandbox row carrying a launch account — the
// flow degrades to a location carrying only the sandbox id and proceeds.
func TestDestroyDiscoveryOutcome_LinkedNotFoundIsNonFatal(t *testing.T) {
	target := &LaunchTarget{LinkName: "mgmt-gpu"}
	loc, err := destroyDiscoveryOutcome("km-abc12345", nil, awspkg.ErrSandboxNotFound, target)
	if err != nil {
		t.Fatalf("expected no error for a linked sandbox's not-found tag scan, got: %v", err)
	}
	if loc == nil {
		t.Fatal("expected a degraded, non-nil location")
	}
	if loc.SandboxID != "km-abc12345" {
		t.Fatalf("expected SandboxID to be preserved, got %q", loc.SandboxID)
	}
	if loc.ResourceCount != 0 {
		t.Fatalf("expected ResourceCount to stay at its zero value, got %d", loc.ResourceCount)
	}
}

// Test 2: the home-account not-found error is still a hard failure with the
// exact pre-Phase-126 message — no behavior change for a plain sandbox.
func TestDestroyDiscoveryOutcome_HomeNotFoundStillFatal(t *testing.T) {
	_, err := destroyDiscoveryOutcome("km-abc12345", nil, awspkg.ErrSandboxNotFound, nil)
	if err == nil {
		t.Fatal("expected a hard failure for a home-account not-found result")
	}
	want := "sandbox km-abc12345 not found: no AWS resources tagged with km:sandbox-id=km-abc12345"
	if err.Error() != want {
		t.Fatalf("error message changed:\n got:  %q\n want: %q", err.Error(), want)
	}
}

// A non-not-found discovery error is fatal regardless of whether the sandbox is
// linked — a transient/auth failure querying the home account's tag API is not
// the same signal as "this resource lives in another account."
func TestDestroyDiscoveryOutcome_OtherErrorStillFatalEvenWhenLinked(t *testing.T) {
	target := &LaunchTarget{LinkName: "mgmt-gpu"}
	otherErr := errors.New("throttled")
	_, err := destroyDiscoveryOutcome("km-abc12345", nil, otherErr, target)
	if err == nil {
		t.Fatal("expected an error to propagate")
	}
	if !strings.Contains(err.Error(), "throttled") {
		t.Fatalf("expected the underlying error to be wrapped, got: %v", err)
	}
}

// A successful discovery result passes through unchanged regardless of target.
func TestDestroyDiscoveryOutcome_SuccessPassesThrough(t *testing.T) {
	loc := &awspkg.SandboxLocation{SandboxID: "km-abc12345", ResourceCount: 4}
	got, err := destroyDiscoveryOutcome("km-abc12345", loc, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != loc {
		t.Fatal("expected the original location to pass through unchanged")
	}
}

// Test 6: the printed resource-count line degrades gracefully for a linked
// sandbox instead of printing a misleading zero as if the sandbox were empty.
func TestDestroyStartMessage_LinkedDegradesGracefully(t *testing.T) {
	target := &LaunchTarget{LinkName: "mgmt-gpu"}
	loc := &awspkg.SandboxLocation{SandboxID: "km-abc12345"} // ResourceCount 0 (unknown, not "empty")
	msg := destroyStartMessage("km-abc12345", loc, target)
	if strings.Contains(msg, "(0 resources)") {
		t.Fatalf("message must not claim 0 resources for a linked sandbox, got: %q", msg)
	}
	if !strings.Contains(msg, "mgmt-gpu") {
		t.Fatalf("expected the message to name the linked account, got: %q", msg)
	}
}

// Home-account message is unchanged: still prints the tag-scan resource count.
func TestDestroyStartMessage_HomeUnchanged(t *testing.T) {
	loc := &awspkg.SandboxLocation{SandboxID: "km-abc12345", ResourceCount: 7}
	msg := destroyStartMessage("km-abc12345", loc, nil)
	want := "Destroying sandbox km-abc12345 (7 resources)...\n"
	if msg != want {
		t.Fatalf("message changed:\n got:  %q\n want: %q", msg, want)
	}
}

// Test 4: the region label used to recreate the sandbox directory for a linked
// sandbox comes from the link record, not from the home-account tag inspection.
func TestResolveDestroyRegionLabel_LinkedUsesLinkRegion(t *testing.T) {
	target := &LaunchTarget{LinkName: "mgmt-gpu", Region: "us-west-2"}
	// A nil location proves the tag inspection is never consulted for this branch.
	got := resolveDestroyRegionLabel(nil, target)
	if got != "usw2" {
		t.Fatalf("expected the link's region to resolve to the usw2 label, got %q", got)
	}
}

// Home-account region resolution is unchanged: falls back to "use1" when tags
// yield nothing.
func TestResolveDestroyRegionLabel_HomeFallsBackToUse1(t *testing.T) {
	got := resolveDestroyRegionLabel(&awspkg.SandboxLocation{}, nil)
	if got != "use1" {
		t.Fatalf("expected use1 fallback, got %q", got)
	}
}

// Test 3 (home half): the synthesized configuration for a home sandbox is
// byte-equal to the pre-Phase-126 literal — the dormancy guard.
func TestSynthesizeDestroyServiceHCL_HomeByteIdentical(t *testing.T) {
	got := synthesizeDestroyServiceHCL("km-abc12345", "use1", nil)
	if got != homeDestroyExpectedHCL {
		t.Fatalf("home-account synthesized service.hcl changed:\n got:\n%s\nwant:\n%s", got, homeDestroyExpectedHCL)
	}
}

// Test 3 (linked half): the synthesized configuration for a linked sandbox
// contains the launch account, launcher role ARN, and external id locals.
func TestSynthesizeDestroyServiceHCL_LinkedCarriesLaunchAccountLocals(t *testing.T) {
	target := &LaunchTarget{
		LinkName:        "mgmt-gpu",
		LauncherRoleARN: "arn:aws:iam::222233334444:role/km-gpu-launcher",
		ExternalID:      "super-secret-external-id",
	}
	got := synthesizeDestroyServiceHCL("km-abc12345", "usw2", target)
	for _, want := range []string{
		`launch_account       = "mgmt-gpu"`,
		`launcher_role_arn    = "arn:aws:iam::222233334444:role/km-gpu-launcher"`,
		`launcher_external_id = "super-secret-external-id"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected synthesized service.hcl to contain %q, got:\n%s", want, got)
		}
	}
	// The home-account literal must NOT appear verbatim inside the linked output —
	// proves the locals were actually inserted rather than the home path silently
	// running for a linked sandbox.
	if strings.Contains(got, homeDestroyExpectedHCL) {
		t.Fatal("linked synthesized service.hcl unexpectedly matches the home-account literal")
	}
}

// Test 5: a sandbox row that cannot be read at all (meta == nil, mirroring the
// upstream metadata-read failure) still falls back to today's behavior — dormant,
// zero config lookup, zero SSM read — rather than failing the destroy early.
func TestDestroyLaunchContext_NilMetaIsDormant(t *testing.T) {
	ssmStore := &countingSSMStore{}
	target, err := destroyLaunchContext(context.Background(), testLaunchAccountConfig(), nil, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != nil {
		t.Fatalf("expected nil target for a nil meta, got %+v", target)
	}
	if ssmStore.calls != 0 {
		t.Fatalf("expected zero SSM reads, got %d", ssmStore.calls)
	}
}

// A home-account sandbox row (empty LaunchAccount) is equally dormant.
func TestDestroyLaunchContext_EmptyLaunchAccountIsDormant(t *testing.T) {
	ssmStore := &countingSSMStore{}
	meta := &awspkg.SandboxMetadata{LaunchAccount: ""}
	target, err := destroyLaunchContext(context.Background(), testLaunchAccountConfig(), meta, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != nil {
		t.Fatalf("expected nil target, got %+v", target)
	}
	if ssmStore.calls != 0 {
		t.Fatalf("expected zero SSM reads, got %d", ssmStore.calls)
	}
}

// A linked sandbox row resolves through to ResolveLaunchTarget's link lookup,
// external-id read, and populated LaunchTarget fields.
func TestDestroyLaunchContext_LinkedResolvesTarget(t *testing.T) {
	ssmStore := &countingSSMStore{vals: map[string]string{
		"/km/launch-accounts/mgmt-gpu/external-id": "super-secret-external-id",
	}}
	meta := &awspkg.SandboxMetadata{LaunchAccount: "mgmt-gpu"}
	target, err := destroyLaunchContext(context.Background(), testLaunchAccountConfig(), meta, ssmStore)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target == nil {
		t.Fatal("expected a resolved target")
	}
	if target.LinkName != "mgmt-gpu" {
		t.Fatalf("expected LinkName mgmt-gpu, got %q", target.LinkName)
	}
	if target.LauncherRoleARN != "arn:aws:iam::222233334444:role/km-gpu-launcher" {
		t.Fatalf("unexpected LauncherRoleARN: %q", target.LauncherRoleARN)
	}
	if target.ExternalID != "super-secret-external-id" {
		t.Fatalf("expected the external id to be read from the parameter store, got %q", target.ExternalID)
	}
	if ssmStore.calls != 1 {
		t.Fatalf("expected exactly one SSM read, got %d", ssmStore.calls)
	}
}

// An unknown link name on the sandbox row is a hard error naming the fix.
func TestDestroyLaunchContext_UnknownLinkIsFatal(t *testing.T) {
	ssmStore := &countingSSMStore{}
	meta := &awspkg.SandboxMetadata{LaunchAccount: "no-such-link"}
	_, err := destroyLaunchContext(context.Background(), testLaunchAccountConfig(), meta, ssmStore)
	if err == nil {
		t.Fatal("expected an error for an unregistered link name")
	}
	if !strings.Contains(err.Error(), "no-such-link") {
		t.Fatalf("expected the error to name the unknown link, got: %v", err)
	}
}

// Sanity: testLaunchAccountConfig (defined in launch_account_resolve_test.go,
// same package) is the fixture this file's tests share with plan 06's tests —
// asserting its shape here catches accidental drift early.
func TestDestroyLaunchContext_SharedFixtureShape(t *testing.T) {
	cfg := testLaunchAccountConfig()
	if _, ok := cfg.GetLaunchAccount("mgmt-gpu"); !ok {
		t.Fatal("expected the shared fixture to register the mgmt-gpu link")
	}
}

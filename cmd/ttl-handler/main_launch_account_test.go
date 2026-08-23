package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// This file covers Phase 126 (REQ-126-TEARDOWN) plan 08's Task 2: the expiry
// Lambda's link-aware provider, link region, and prefix-correct module label.
//
// All six behaviors are exercised against renderDestroyMainTF and
// buildDestroyTerraformInputs directly — both are pure functions that take no
// *TTLHandler and perform no AWS/SSM call (the plan's own acceptance
// criterion). No test in this file constructs a *TTLHandler or calls
// terraformDestroy/HandleTTLEvent, so the existing "teardown-function seam"
// precaution documented in main_test.go (a nil TeardownFunc reaching the real
// destroy path hits live instance metadata and hangs) does not apply here —
// consistent with the pre-existing suite, which has never called
// terraformDestroy directly either (it execs a bundled terraform binary and
// writes/removes a /tmp scratch dir, and is not unit-testable without one).

// homeMainTFExpected is the pre-Phase-126 literal terraformDestroy rendered
// for a home-account sandbox, embedded verbatim so Test 1 is a byte-equality
// assertion against the exact historical output, not a structural one.
const homeMainTFExpected = `
terraform {
  required_version = ">= 1.6.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
  backend "s3" {
    bucket         = "km-tfstate"
    key            = "tf-km/use1/sandboxes/km-abc12345/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "tf-km-locks-use1"
  }
}

provider "aws" {
  region = "us-east-1"
}

module "sandbox" {
  source       = "./module"
  km_label     = "km"
  region_label = "use1"
  region_full  = "us-east-1"
  sandbox_id   = "km-abc12345"
  vpc_id       = "destroy-placeholder"
  public_subnets     = []
  availability_zones = []
  ec2spots           = []
}
`

func homeDestroyInputs() destroyTerraformInputs {
	return destroyTerraformInputs{
		StateBucket: "km-tfstate",
		StateKey:    "tf-km/use1/sandboxes/km-abc12345/terraform.tfstate",
		LockTable:   "tf-km-locks-use1",
		Region:      "us-east-1",
		RegionLabel: "use1",
		SandboxID:   "km-abc12345",
		ModuleLabel: "km",
	}
}

// Test 1: for a sandbox row with no launch account, the rendered configuration
// is byte-identical to today's output for the same inputs.
func TestRenderDestroyMainTF_HomeByteIdentical(t *testing.T) {
	got := renderDestroyMainTF(homeDestroyInputs())
	if got != homeMainTFExpected {
		t.Fatalf("rendered main.tf changed for a home-account sandbox:\ngot:\n%s\nwant:\n%s", got, homeMainTFExpected)
	}
}

// Test 2: for a sandbox row with a launch account, the rendered provider block
// contains an assume-role block with the link's launcher ARN and the external
// id read from the parameter store.
func TestRenderDestroyMainTF_LinkedCarriesAssumeRoleBlock(t *testing.T) {
	in := homeDestroyInputs()
	in.LauncherRoleARN = "arn:aws:iam::222233334444:role/km-gpu-launcher"
	in.ExternalID = "super-secret-external-id"

	got := renderDestroyMainTF(in)
	if !strings.Contains(got, `provider "aws" {`) {
		t.Fatalf("expected a provider block, got:\n%s", got)
	}
	for _, want := range []string{
		"assume_role {",
		`role_arn    = "arn:aws:iam::222233334444:role/km-gpu-launcher"`,
		`external_id = "super-secret-external-id"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected rendered main.tf to contain %q, got:\n%s", want, got)
		}
	}
	// The assume_role block must be nested INSIDE the provider block, not
	// floating separately — assert ordering by index.
	providerIdx := strings.Index(got, `provider "aws" {`)
	assumeIdx := strings.Index(got, "assume_role {")
	closeIdx := strings.Index(got, "\n}\n\nmodule \"sandbox\"")
	if !(providerIdx < assumeIdx && assumeIdx < closeIdx) {
		t.Fatalf("expected assume_role to sit inside the provider block, got:\n%s", got)
	}
}

// The home-account provider block has no assume_role at all.
func TestRenderDestroyMainTF_HomeHasNoAssumeRole(t *testing.T) {
	got := renderDestroyMainTF(homeDestroyInputs())
	if strings.Contains(got, "assume_role") {
		t.Fatalf("expected no assume_role block for a home-account sandbox, got:\n%s", got)
	}
}

// Test 6: the module label emitted into the rendered configuration equals the
// resource prefix rather than a hardcoded default.
func TestRenderDestroyMainTF_ModuleLabelIsResourcePrefix(t *testing.T) {
	in := homeDestroyInputs()
	in.ModuleLabel = "acme"
	got := renderDestroyMainTF(in)
	if !strings.Contains(got, `km_label     = "acme"`) {
		t.Fatalf("expected km_label to reflect the resource prefix, got:\n%s", got)
	}
	if strings.Contains(got, `km_label     = "km"`) {
		t.Fatalf("expected the hardcoded \"km\" literal to be gone, got:\n%s", got)
	}
}

// stubExternalIDReader is a ttlExternalIDReader stub that records the ssmPath
// it was called with and returns a canned value/error.
func stubExternalIDReader(value string, err error) (ttlExternalIDReader, *string) {
	var lastPath string
	return func(_ context.Context, ssmPath string) (string, error) {
		lastPath = ssmPath
		return value, err
	}, &lastPath
}

// Test 1 (buildDestroyTerraformInputs side): a home-account sandbox (empty
// launchAccountName) is dormant — the home region/label are used unchanged and
// the external-id reader is never called.
func TestBuildDestroyTerraformInputs_HomeDormant(t *testing.T) {
	reader, lastPath := stubExternalIDReader("should-not-be-used", nil)
	*lastPath = "" // sentinel

	in, err := buildDestroyTerraformInputs(context.Background(), "", nil,
		"us-east-1", "use1", "km", "km-tfstate", "tf-km", "km-abc12345", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in.Region != "us-east-1" || in.RegionLabel != "use1" {
		t.Fatalf("expected home region/label unchanged, got region=%q regionLabel=%q", in.Region, in.RegionLabel)
	}
	if in.LauncherRoleARN != "" || in.ExternalID != "" {
		t.Fatalf("expected no assume-role fields for a home sandbox, got %+v", in)
	}
	if *lastPath != "" {
		t.Fatalf("expected the external-id reader to never be called, got path %q", *lastPath)
	}
	if in.StateKey != "tf-km/use1/sandboxes/km-abc12345/terraform.tfstate" {
		t.Fatalf("unexpected state key: %q", in.StateKey)
	}
}

// Test 3: for a linked sandbox, the region and the state key's region label
// come from the link record, not from the handler's instance-wide fields.
func TestBuildDestroyTerraformInputs_LinkedUsesLinkRegion(t *testing.T) {
	links := map[string]launchAccountLink{
		"mgmt-gpu": {
			LauncherRoleARN: "arn:aws:iam::222233334444:role/km-gpu-launcher",
			ExternalIDSSM:   "/km/launch-accounts/mgmt-gpu/external-id",
			Region:          "us-west-2",
		},
	}
	reader, lastPath := stubExternalIDReader("super-secret-external-id", nil)

	in, err := buildDestroyTerraformInputs(context.Background(), "mgmt-gpu", links,
		"us-east-1" /* handler's instance-wide home region — must NOT be used */, "use1",
		"km", "km-tfstate", "tf-km", "km-abc12345", reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if in.Region != "us-west-2" {
		t.Fatalf("expected the linked region, got %q", in.Region)
	}
	if in.RegionLabel != "usw2" {
		t.Fatalf("expected the linked region label, got %q", in.RegionLabel)
	}
	if in.StateKey != "tf-km/usw2/sandboxes/km-abc12345/terraform.tfstate" {
		t.Fatalf("expected the state key's region label to come from the link, got %q", in.StateKey)
	}
	if in.LauncherRoleARN != "arn:aws:iam::222233334444:role/km-gpu-launcher" {
		t.Fatalf("unexpected LauncherRoleARN: %q", in.LauncherRoleARN)
	}
	if in.ExternalID != "super-secret-external-id" {
		t.Fatalf("expected the external id to be read from the parameter store, got %q", in.ExternalID)
	}
	if *lastPath != "/km/launch-accounts/mgmt-gpu/external-id" {
		t.Fatalf("expected the reader to be called with the link's SSM path, got %q", *lastPath)
	}
}

// Test 4: a parameter-store read failure for the external id aborts the
// teardown with a logged error rather than rendering a configuration with an
// empty external id that would fail confusingly at apply time.
func TestBuildDestroyTerraformInputs_ExternalIDReadFailureAborts(t *testing.T) {
	links := map[string]launchAccountLink{
		"mgmt-gpu": {
			LauncherRoleARN: "arn:aws:iam::222233334444:role/km-gpu-launcher",
			ExternalIDSSM:   "/km/launch-accounts/mgmt-gpu/external-id",
			Region:          "us-west-2",
		},
	}
	reader, _ := stubExternalIDReader("", errors.New("AccessDeniedException"))

	_, err := buildDestroyTerraformInputs(context.Background(), "mgmt-gpu", links,
		"us-east-1", "use1", "km", "km-tfstate", "tf-km", "km-abc12345", reader)
	if err == nil {
		t.Fatal("expected an error when the external-id read fails")
	}
	if !strings.Contains(err.Error(), "AccessDeniedException") {
		t.Fatalf("expected the underlying SSM error to be wrapped, got: %v", err)
	}
}

// Test 5: an unknown link name on the sandbox row aborts with a clear error
// naming the link and the environment variable that should have carried it.
func TestBuildDestroyTerraformInputs_UnknownLinkAborts(t *testing.T) {
	reader, lastPath := stubExternalIDReader("should-not-be-used", nil)

	_, err := buildDestroyTerraformInputs(context.Background(), "no-such-link", map[string]launchAccountLink{},
		"us-east-1", "use1", "km", "km-tfstate", "tf-km", "km-abc12345", reader)
	if err == nil {
		t.Fatal("expected an error for an unregistered link name")
	}
	if !strings.Contains(err.Error(), "no-such-link") {
		t.Fatalf("expected the error to name the unknown link, got: %v", err)
	}
	if !strings.Contains(err.Error(), "KM_LAUNCH_ACCOUNTS") {
		t.Fatalf("expected the error to name KM_LAUNCH_ACCOUNTS, got: %v", err)
	}
	if *lastPath != "" {
		t.Fatalf("expected the external-id reader to never be called for an unknown link, got path %q", *lastPath)
	}
}

// A nil links map (KM_LAUNCH_ACCOUNTS unset — the dormant cold-start default)
// behaves exactly like an empty one: any non-empty launchAccountName is an
// unknown-link error, never a nil-map panic.
func TestBuildDestroyTerraformInputs_NilLinksMapIsSafe(t *testing.T) {
	reader, _ := stubExternalIDReader("unused", nil)
	_, err := buildDestroyTerraformInputs(context.Background(), "mgmt-gpu", nil,
		"us-east-1", "use1", "km", "km-tfstate", "tf-km", "km-abc12345", reader)
	if err == nil {
		t.Fatal("expected an error for a link name against a nil links map")
	}
}

// parseLaunchAccountsEnv: absent env var is dormant (nil map, no error path to
// observe since it returns silently).
func TestParseLaunchAccountsEnv_AbsentIsDormant(t *testing.T) {
	t.Setenv("KM_LAUNCH_ACCOUNTS", "")
	got := parseLaunchAccountsEnv()
	if got != nil {
		t.Fatalf("expected nil for an absent KM_LAUNCH_ACCOUNTS, got %+v", got)
	}
}

// parseLaunchAccountsEnv: valid JSON round-trips into the link map.
func TestParseLaunchAccountsEnv_ValidJSON(t *testing.T) {
	t.Setenv("KM_LAUNCH_ACCOUNTS", `{"mgmt-gpu":{"launcher_role_arn":"arn:aws:iam::222233334444:role/km-gpu-launcher","external_id_ssm":"/km/launch-accounts/mgmt-gpu/external-id","region":"us-west-2"}}`)
	got := parseLaunchAccountsEnv()
	link, ok := got["mgmt-gpu"]
	if !ok {
		t.Fatalf("expected the mgmt-gpu link to be present, got %+v", got)
	}
	if link.Region != "us-west-2" || link.LauncherRoleARN != "arn:aws:iam::222233334444:role/km-gpu-launcher" {
		t.Fatalf("unexpected link contents: %+v", link)
	}
}

// parseLaunchAccountsEnv: malformed JSON degrades to dormant (nil), never a panic.
func TestParseLaunchAccountsEnv_InvalidJSONIsDormant(t *testing.T) {
	t.Setenv("KM_LAUNCH_ACCOUNTS", `{not valid json`)
	got := parseLaunchAccountsEnv()
	if got != nil {
		t.Fatalf("expected nil for invalid JSON, got %+v", got)
	}
}

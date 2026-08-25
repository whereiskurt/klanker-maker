package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	profilepkg "github.com/whereiskurt/klanker-maker/pkg/profile"
)

// destroyLaunchContext resolves the launch-account link for a sandbox being torn
// down, using its DynamoDB row's LaunchAccount field. Returns (nil, nil) — zero
// config lookup, zero SSM read — when the row is unavailable (meta == nil, a
// metadata-read failure upstream) or names no launch account (a home-account
// sandbox): both are Phase 125's unmodified destroy path (must_haves.truths #5,
// Test 5).
//
// Reuses ResolveLaunchTarget (plan 06) with a synthetic empty profile and the
// row's launch account name passed as an explicit override — the exact shape
// `km capacity --launch-account` already uses (capacity.go's
// ResolveLaunchTarget(ctx, cfg, &profile.SandboxProfile{}, &launchAccountFlag,
// ssmStore) call) — rather than a second hand-rolled parameter-store read
// (126-RESEARCH.md Pattern 5's read_first pointer).
func destroyLaunchContext(ctx context.Context, cfg *config.Config, meta *awspkg.SandboxMetadata, ssmStore SSMParamStore) (*LaunchTarget, error) {
	if meta == nil || meta.LaunchAccount == "" {
		return nil, nil
	}
	name := meta.LaunchAccount
	return ResolveLaunchTarget(ctx, cfg, &profilepkg.SandboxProfile{}, &name, ssmStore)
}

// destroyDiscoveryOutcome classifies the result of the home-account tag-based
// discovery call (FindSandboxByID) for a sandbox that may live in a linked
// account. The home-account tagging API can never see a resource that lives in
// a different AWS account (126-RESEARCH.md Pattern 5 / Pitfall 3) — a not-found
// result there is EXPECTED, not fatal, exactly when target is non-nil.
//
// target == nil (home-account sandbox): behavior is byte-identical to Phase
// 125 — a not-found is still a hard failure with the original message (Test 2),
// and any other discovery error is still wrapped and returned (unchanged).
//
// target != nil (linked sandbox) and the lookup returned not-found: returns a
// location carrying only the sandbox id (ResourceCount 0) so the flow proceeds
// to terragrunt destroy, which crosses the account boundary via the generated
// provider's assume_role block (Pattern 1) — the printed resource count
// degrades gracefully rather than reporting a misleading zero as if the
// sandbox were empty (destroyStartMessage handles that formatting).
//
// target != nil and the lookup returned a DIFFERENT error (not ErrSandboxNotFound):
// still fatal — a transient/auth failure querying the home account's tag API is
// not the same as "this resource lives in another account."
func destroyDiscoveryOutcome(sandboxID string, location *awspkg.SandboxLocation, findErr error, target *LaunchTarget) (*awspkg.SandboxLocation, error) {
	if findErr == nil {
		return location, nil
	}
	if errors.Is(findErr, awspkg.ErrSandboxNotFound) {
		if target != nil {
			return &awspkg.SandboxLocation{SandboxID: sandboxID}, nil
		}
		return nil, fmt.Errorf("sandbox %s not found: no AWS resources tagged with km:sandbox-id=%s", sandboxID, sandboxID)
	}
	return nil, fmt.Errorf("failed to discover sandbox %s: %w", sandboxID, findErr)
}

// destroyStartMessage formats the "Destroying sandbox ..." line printed right
// after discovery. A linked sandbox's resource count is not visible from the
// home-account tag scan (Pattern 5) — printing location.ResourceCount there
// would misleadingly claim the sandbox has zero resources, so the message
// names the linked account instead of a count.
func destroyStartMessage(sandboxID string, location *awspkg.SandboxLocation, target *LaunchTarget) string {
	if target != nil {
		return fmt.Sprintf("Destroying sandbox %s (linked account %s — resource count not visible from the home-account tag scan)...\n", sandboxID, target.LinkName)
	}
	return fmt.Sprintf("Destroying sandbox %s (%d resources)...\n", sandboxID, location.ResourceCount)
}

// resolveDestroyRegionLabel picks the region label used to recreate a missing
// local sandbox directory during cold-clone destroy. A linked sandbox's region
// comes from the resolved link record — the home-account tag inspection found
// nothing to determine it from in that case (Test 4). A home-account sandbox is
// unchanged: derived from the discovered tags, falling back to "use1".
func resolveDestroyRegionLabel(location *awspkg.SandboxLocation, target *LaunchTarget) string {
	if target != nil {
		return compiler.RegionLabel(target.Region)
	}
	label := determineRegionFromTags(location)
	if label == "" {
		label = "use1" // fallback to default
	}
	return label
}

// synthesizeDestroyServiceHCL builds the minimal service.hcl locals block written
// into a freshly-recreated sandbox directory during cold-clone destroy (no local
// Terragrunt state existed on this machine). The three launch-account locals
// (mirroring pkg/compiler/service_hcl.go's ec2ServiceHCLTemplate, read at
// terragrunt generate-time by infra/templates/sandbox/terragrunt.hcl's
// conditional provider override — Pattern 1) are emitted only when target is
// non-nil, so a home sandbox's synthesized file is byte-identical to Phase 125's
// (Test 3's home half; the acceptance criteria requires this asserted against an
// embedded literal).
func synthesizeDestroyServiceHCL(sandboxID, regionLabel string, target *LaunchTarget) string {
	var b strings.Builder
	b.WriteString("# Minimal service.hcl for state resolution during destroy\n")
	b.WriteString("locals {\n")
	fmt.Fprintf(&b, "  sandbox_id       = %q\n", sandboxID)
	fmt.Fprintf(&b, "  region_label     = %q\n", regionLabel)
	b.WriteString("  region_full      = \"\"\n")
	b.WriteString("  substrate_module = \"ec2spot\"\n")
	if target != nil {
		fmt.Fprintf(&b, "  launch_account       = %q\n", target.LinkName)
		fmt.Fprintf(&b, "  launcher_role_arn    = %q\n", target.LauncherRoleARN)
		fmt.Fprintf(&b, "  launcher_external_id = %q\n", target.ExternalID)
	}
	b.WriteString("  module_inputs    = {}\n")
	b.WriteString("}\n")
	return b.String()
}

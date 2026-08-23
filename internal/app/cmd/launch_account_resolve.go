package cmd

import (
	"context"
	"fmt"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// LaunchTarget carries everything the create flow and the capacity command need
// to launch into, or report on, a linked account — resolved once by
// ResolveLaunchTarget and threaded through both create paths (Phase 126,
// REQ-126-LAUNCH / REQ-126-CAPACITY).
type LaunchTarget struct {
	// LinkName is the launch_accounts.<name> key this target was resolved from.
	LinkName string
	// AccountID is the linked AWS account id (informational — the actual crossing
	// happens via LauncherRoleARN).
	AccountID string
	// LauncherRoleARN is the role in the linked account the generated terragrunt
	// provider assumes into (plan 02's conditional provider override) and the role
	// the capacity gate assumes for its EC2/Service-Quotas clients (plan 03's
	// AssumeRoleConfig).
	LauncherRoleARN string
	// ExternalID is the resolved secret read from the home account's parameter
	// store at the link's ExternalIDSSM path. Never logged, never included in an
	// error message (T-126-30).
	ExternalID string
	// Region is the linked account's region. It carries its own region — a
	// conflicting profile region is overridden with a warning, not an error (see
	// this plan's "Decisions recorded" section).
	Region string
	// SubnetIDs and AvailabilityZones are index-parallel: SubnetIDs[i] lives in
	// AvailabilityZones[i]. ResolveLaunchTarget rejects a link whose lists
	// disagree in length, because downstream code (the AZ sweep's subnet reorder)
	// pairs them by index.
	SubnetIDs         []string
	AvailabilityZones []string
	// VPCID is the VPC that owns SubnetIDs. It is NOT part of the link record
	// (launch_accounts config carries subnet ids only) — it is resolved
	// separately, post-construction, via a DescribeSubnets call against the
	// launcher-role-assumed account (see create.go's hydrateLaunchAccountVPCID).
	// Empty until that call runs. Required: infra/modules/ec2spot/v1.3.0
	// self-provisions a brand-new VPC whenever its vpc_id input is empty, and
	// would then try to place the ENI into a subnet from a DIFFERENT vpc (the
	// link's real one) — AWS rejects that at apply time.
	VPCID string
	// SecurityGroupID is the linked account's pre-provisioned security group id.
	SecurityGroupID string
	// ResultsBucket is the linked account's results bucket (B→A read path).
	ResultsBucket string
	// EFSID is the linked account's shared filesystem id, or empty when the link
	// was enrolled without --provision-efs.
	EFSID string
}

// ResolveLaunchTarget resolves the effective launch-account link for a create (or
// a `km capacity` report) using profile-primary, CLI-override precedence, looks it
// up in the loaded config, and reads its external id from the home account's
// parameter store.
//
// cliOverride is a pointer so an explicitly empty override ("--launch-account=")
// can be distinguished from an absent flag: nil defers entirely to the profile's
// spec.runtime.launchAccount; a non-nil empty string forces the home account even
// when the profile names a link; a non-nil non-empty string names the link
// regardless of what the profile says.
//
// Dormant path: when the effective name is empty, this performs NO config lookup,
// NO parameter-store read, and NO logging — it returns (nil, nil) immediately. A
// profile naming no launch account (and no CLI override) is byte-identical to
// Phase 125.
//
// Deviation from the plan's literal 4-argument signature: an SSMParamStore
// parameter was added so the external-id read is testable via a stub, mirroring
// pkg/aws.AssumeRoleConfig's NewAssumeRoleSTSClient seam (126-03) — the plan's
// literal (ctx, cfg, p, cliOverride) signature carries no AWS session and cannot
// actually perform a parameter-store read. See the plan-06 SUMMARY.
func ResolveLaunchTarget(ctx context.Context, cfg *config.Config, p *profile.SandboxProfile, cliOverride *string, ssmStore SSMParamStore) (*LaunchTarget, error) {
	name := p.Spec.Runtime.LaunchAccount
	if cliOverride != nil {
		name = *cliOverride
	}
	if name == "" {
		return nil, nil
	}

	link, ok := cfg.GetLaunchAccount(name)
	if !ok {
		return nil, fmt.Errorf(
			"launchAccount %q is not a registered link in km-config.yaml launch_accounts — register it with `km account register %s`",
			name, name,
		)
	}

	if link.LauncherRoleARN == "" {
		return nil, fmt.Errorf("launch_accounts.%s has no launcher_role_arn — the link record is incomplete; re-run `km account register`", name)
	}
	if link.Region == "" {
		return nil, fmt.Errorf("launch_accounts.%s has no region — the link record is incomplete; re-run `km account register`", name)
	}
	if len(link.SubnetIDs) == 0 {
		return nil, fmt.Errorf("launch_accounts.%s has no subnet_ids — the link record is incomplete; re-run `km account register`", name)
	}
	// Downstream code (the AZ sweep's subnet reorder in the AZ-ranking block) pairs
	// SubnetIDs[i] with AvailabilityZones[i] by index — a length mismatch would
	// silently mis-pair a subnet with the wrong zone.
	if len(link.SubnetIDs) != len(link.AvailabilityZones) {
		return nil, fmt.Errorf(
			"launch_accounts.%s: subnet_ids (%d) and availability_zones (%d) have different lengths — the link record is corrupt, re-run `km account add`",
			name, len(link.SubnetIDs), len(link.AvailabilityZones),
		)
	}

	// Fatal on any read failure — a target must never be returned with an empty
	// external id (T-126-01: the secret never lives in km-config.yaml itself).
	externalID, err := ssmStore.Get(ctx, link.ExternalIDSSM, true)
	if err != nil {
		return nil, fmt.Errorf("read external id for launch_accounts.%s from %s: %w", name, link.ExternalIDSSM, err)
	}
	if externalID == "" {
		return nil, fmt.Errorf("external id for launch_accounts.%s at %s is empty or missing — re-run `km account register`", name, link.ExternalIDSSM)
	}

	return &LaunchTarget{
		LinkName:          name,
		AccountID:         link.AccountID,
		LauncherRoleARN:   link.LauncherRoleARN,
		ExternalID:        externalID,
		Region:            link.Region,
		SubnetIDs:         link.SubnetIDs,
		AvailabilityZones: link.AvailabilityZones,
		SecurityGroupID:   link.SecurityGroupID,
		ResultsBucket:     link.ResultsBucket,
		EFSID:             link.EFSID,
	}, nil
}

// checkLaunchAccountEFSGuard fails a linked create fast when the profile requests
// the shared EFS mount but the resolved link has no filesystem id. Shaped exactly
// like checkPrivateSubnetGuard: pure, no AWS session, names the exact fix.
func checkLaunchAccountEFSGuard(wantsEFS bool, linkEFSID string) error {
	if !wantsEFS {
		return nil
	}
	if linkEFSID != "" {
		return nil
	}
	return fmt.Errorf("profile requests spec.runtime.mountEFS but this launch account has no EFS filesystem — " +
		"re-run `km account add --provision-efs` for this link")
}

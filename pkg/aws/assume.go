package aws

import (
	"context"
	"fmt"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// assumeRoleSessionName identifies every session this helper mints, so a
// CloudTrail event in the linked account is always attributable to km
// regardless of which local operator or Lambda role triggered it.
const assumeRoleSessionName = "km-cross-account-capacity"

// assumeRoleSessionDuration is set explicitly (rather than left at the SDK's
// 15-minute default) so the credential lifetime is documented in one place
// and does not silently drift if the SDK's own default ever changes.
const assumeRoleSessionDuration = 15 * time.Minute

// AssumeRoleSTSAPI is the minimal STS interface required to assume a role.
// Narrowed to the single method this package needs, following the existing
// pkg/aws narrow-interface convention (see rotation.go, identity.go).
// Implemented by *sts.Client; test binaries inject a stub instead.
type AssumeRoleSTSAPI interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// NewAssumeRoleSTSClient constructs the STS client AssumeRoleConfig assumes
// through. Exported as a package-level var (not a call inlined into
// AssumeRoleConfig, and not a func declaration) so external test packages
// can substitute a stub AssumeRoleSTSAPI without a live AWS session — the
// same seam convention as LoadAWSConfig in client.go.
var NewAssumeRoleSTSClient = func(cfg awssdk.Config) AssumeRoleSTSAPI {
	return sts.NewFromConfig(cfg)
}

// AssumeRoleConfig returns an aws.Config whose credentials come from
// assuming roleARN via STS, region-pinned to region.
//
// FAIL-CLOSED BY DESIGN: unlike the pre-existing AssumeRole pattern in
// internal/app/cmd/uninit.go (which, on failure, logs a warning and falls
// back to the caller's own credentials — a defensible choice for its
// best-effort SCP-cleanup use case), this helper never falls back. A caller
// that needs the linked account's identity — a GPU quota check, a capacity
// sweep, anything gated on account B's limits — MUST treat a credential
// resolution failure here as fatal. A silent fallback would check the home
// account's GPU quota while the operator believes it checked the linked
// account's, reporting a capacity verdict for the wrong account entirely.
//
// The returned config's Credentials provider is wrapped in the SDK's
// credentials cache (aws.NewCredentialsCache), so repeated use auto-refreshes
// rather than holding a one-shot credential set — stscreds.AssumeRoleProvider
// does not self-cache on its own.
//
// This is a package-level var (not a func declaration), matching the
// LoadAWSConfig/LoadAWSConfigInRegion seam convention in client.go, so test
// binaries can override it without touching production call sites.
var AssumeRoleConfig = func(ctx context.Context, base awssdk.Config, roleARN, externalID, region string) (awssdk.Config, error) {
	if roleARN == "" {
		return awssdk.Config{}, fmt.Errorf("AssumeRoleConfig: roleARN is empty — a regionless/roleless config would silently resolve against the caller's own account")
	}
	if region == "" {
		return awssdk.Config{}, fmt.Errorf("AssumeRoleConfig: region is empty — a regionless config would silently resolve against the caller's own account")
	}

	stsClient := NewAssumeRoleSTSClient(base)

	provider := stscreds.NewAssumeRoleProvider(stsClient, roleARN, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = assumeRoleSessionName
		o.Duration = assumeRoleSessionDuration
		if externalID != "" {
			o.ExternalID = awssdk.String(externalID)
		}
	})

	cfg := base.Copy()
	cfg.Credentials = awssdk.NewCredentialsCache(provider)
	cfg.Region = region
	return cfg, nil
}

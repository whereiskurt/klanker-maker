package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
)

// BootstrapApplyTimeout bounds defaultApplyTerragrunt (Reconfigure + Apply).
// Matches the 10-minute bound used for the foundation ses-shared-rule-set
// regional module in init.go's defaultModuleTimeout (Plan 84.1-02 Task 2).
//
// Plan-checker rev 1 H6: without this bound, km bootstrap --shared-ses can
// hang indefinitely on a wedged terragrunt — the same indefinite-hang surface
// GAP-4 / GAP-5 closed in km init.
//
// Exported as a package-level var (not a const) so tests can lower the bound
// for fast-running fake-terragrunt scenarios.
var BootstrapApplyTimeout = 10 * time.Minute

// =============================================================================
// Phase 84: km bootstrap --shared-ses
// =============================================================================

// SESIdentityLister abstracts the two SES read operations needed for shared-SES
// auto-detection. The real *realSESLister satisfies this interface in production;
// tests inject a mock.
type SESIdentityLister interface {
	// ListReceiptRuleSets returns the list of SES classic v1 receipt rule sets.
	ListReceiptRuleSets(ctx context.Context, in *ses.ListReceiptRuleSetsInput, optFns ...func(*ses.Options)) (*ses.ListReceiptRuleSetsOutput, error)
	// ListEmailIdentities returns the list of SES v2 email identities.
	ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error)
}

// realSESLister adapts the production SES classic v1 and SES v2 clients to
// satisfy SESIdentityLister.
type realSESLister struct {
	sesClient   *ses.Client
	sesv2Client *sesv2.Client
}

func (r *realSESLister) ListReceiptRuleSets(ctx context.Context, in *ses.ListReceiptRuleSetsInput, optFns ...func(*ses.Options)) (*ses.ListReceiptRuleSetsOutput, error) {
	return r.sesClient.ListReceiptRuleSets(ctx, in, optFns...)
}

func (r *realSESLister) ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error) {
	return r.sesv2Client.ListEmailIdentities(ctx, in, optFns...)
}

// DetectSharedSESState checks whether the shared SES receipt rule set and the
// target email domain identity already exist.
// Exported for use in tests (cmd_test package).
//
// Returns:
//   - registerSharedRuleSet: true when the rule set does NOT exist yet (i.e. Terraform should create it)
//   - registerDomainIdentity: true when the domain identity does NOT exist yet (i.e. Terraform should create it)
func DetectSharedSESState(ctx context.Context, lister SESIdentityLister, ruleSetName, emailDomain string) (registerSharedRuleSet, registerDomainIdentity bool, err error) {
	return detectSharedSESState(ctx, lister, nil, ruleSetName, emailDomain)
}

// DetectSharedSESStateWithStateReader is the Phase 84.1 variant of
// DetectSharedSESState that also consults the foundation tfstate via a
// FoundationStateReader. When stateReader.StateOwns reports that a shared
// resource is already in foundation state, the corresponding register_* flag
// stays TRUE — keeping foundation in charge of the resource (the new "manage"
// semantic; GAP-2 closure).
//
// Pass nil for stateReader to fall back to the legacy AWS-reality check
// (used by defaultSESPreflight in init.go — the documented "skip state check"
// mode for read-only existence checks).
//
// Exported for use in tests (cmd_test package).
func DetectSharedSESStateWithStateReader(ctx context.Context, lister SESIdentityLister, stateReader FoundationStateReader, ruleSetName, emailDomain string) (registerSharedRuleSet, registerDomainIdentity bool, err error) {
	return detectSharedSESState(ctx, lister, stateReader, ruleSetName, emailDomain)
}

// FoundationStateReader returns true iff the named resource address is present
// in the foundation module's terraform state.
//
// Phase 84.1: implementations may read the state file from S3 (production)
// or from an in-memory map (tests). A nil-safe contract is enforced at the
// caller: detectSharedSESState skips the state check when the reader is nil.
//
// Resource addresses use Terraform's address syntax — for count=1 resources,
// always include the [0] suffix (e.g. "aws_ses_domain_identity.sandbox[0]").
type FoundationStateReader interface {
	// StateOwns reports whether resourceAddr is in foundation tfstate.
	// Returns (false, nil) when the state file does not exist (fresh account).
	// Returns (false, err) only on unexpected I/O errors — the caller treats
	// errors as "not owned" to avoid blocking init on transient S3 issues.
	StateOwns(ctx context.Context, resourceAddr string) (bool, error)
}

// detectSharedSESState is the unexported implementation called by DetectSharedSESState and runBootstrapSharedSES.
//
// Phase 84.1 Task 1: signature extended with FoundationStateReader. When
// stateReader is non-nil and reports state ownership for a shared resource,
// the corresponding register_* flag stays TRUE — the new "manage this
// resource" semantic (GAP-2). When stateReader is nil OR reports no ownership,
// fall back to the legacy AWS-reality check.
//
// The new semantic: register_* flags mean "this module manages the resource",
// NOT "create only on first apply". Once foundation owns the resource in
// state, the flag stays true; flipping to false intentionally orphans the
// resource (which prevent_destroy then blocks at the terraform layer).
//
// TODO(84.1-04 Task 1 GREEN): wire the stateReader.StateOwns calls.
// RED commit currently only changes the signature so tests compile.
func detectSharedSESState(ctx context.Context, lister SESIdentityLister, stateReader FoundationStateReader, ruleSetName, emailDomain string) (registerSharedRuleSet, registerDomainIdentity bool, err error) {
	// Default: create both (safe idempotent starting point).
	registerSharedRuleSet = true
	registerDomainIdentity = true

	rsOut, err := lister.ListReceiptRuleSets(ctx, &ses.ListReceiptRuleSetsInput{})
	if err != nil {
		return registerSharedRuleSet, registerDomainIdentity, fmt.Errorf("ListReceiptRuleSets: %w", err)
	}
	for _, rs := range rsOut.RuleSets {
		if aws.ToString(rs.Name) == ruleSetName {
			registerSharedRuleSet = false
			break
		}
	}

	idOut, err := lister.ListEmailIdentities(ctx, &sesv2.ListEmailIdentitiesInput{})
	if err != nil {
		return registerSharedRuleSet, registerDomainIdentity, fmt.Errorf("ListEmailIdentities: %w", err)
	}
	for _, id := range idOut.EmailIdentities {
		if id.IdentityType == sesv2types.IdentityTypeDomain && aws.ToString(id.IdentityName) == emailDomain {
			registerDomainIdentity = false
			break
		}
	}

	return registerSharedRuleSet, registerDomainIdentity, nil
}

// RunBootstrapSharedSES is the exported test seam for runBootstrapSharedSES.
// Tests in the cmd_test package call this to exercise the env-var export +
// shared-SES detection without going through the cobra command (which has no
// hook for injecting an SESIdentityLister mock).
//
// Production code uses the unexported runBootstrapSharedSES via the cobra
// command's RunE — this wrapper is intentionally a one-line forwarder.
func RunBootstrapSharedSES(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer, listerOverride SESIdentityLister) error {
	return runBootstrapSharedSES(ctx, cfg, dryRun, w, listerOverride)
}

// runBootstrapSharedSES implements the `km bootstrap --shared-ses` workflow.
// It auto-detects whether the shared SES rule set and domain identity exist,
// sets the corresponding Terragrunt env vars, and applies
// infra/live/use1/ses-shared-rule-set/ via ApplyTerragruntFunc (or plans it
// when dryRun is true).
func runBootstrapSharedSES(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer, listerOverride SESIdentityLister) error {
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	// Ensure all config env vars are exported so Terragrunt site.hcl picks them up.
	ExportTerragruntEnvVars(loadedCfg)

	// Build the full email domain: {email_subdomain}.{domain}
	emailSubdomain := loadedCfg.EmailSubdomain
	if emailSubdomain == "" {
		emailSubdomain = "sandboxes"
	}
	emailDomain := fmt.Sprintf("%s.%s", emailSubdomain, loadedCfg.Domain)

	// Build the SES client pair (or use the override in tests).
	var lister SESIdentityLister
	if listerOverride != nil {
		lister = listerOverride
	} else {
		region := loadedCfg.PrimaryRegion
		if region == "" {
			region = "us-east-1"
		}
		awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		lister = &realSESLister{
			sesClient:   ses.NewFromConfig(awsCfg),
			sesv2Client: sesv2.NewFromConfig(awsCfg),
		}
	}

	// Phase 84.1 Task 1: pass nil for the FoundationStateReader in the RED commit.
	// GREEN commit will construct an s3FoundationStateReader and wire it in here
	// so re-running km bootstrap against an already-bootstrapped account is a true
	// no-op (foundation state ownership keeps register_* flags TRUE).
	registerRS, registerID, err := detectSharedSESState(ctx, lister, nil, "sandbox-email-shared", emailDomain)
	if err != nil {
		return fmt.Errorf("SES auto-detect: %w", err)
	}

	// Log step-level summaries (OPER-01 pattern).
	if registerRS {
		fmt.Fprintln(w, "Shared SES rule set:      creating")
	} else {
		fmt.Fprintln(w, "Shared SES rule set:      reusing existing")
	}
	if registerID {
		fmt.Fprintln(w, "Shared SES domain identity: creating")
	} else {
		fmt.Fprintln(w, "Shared SES domain identity: reusing existing")
	}

	// Export the two Phase-84-specific vars.
	os.Setenv("KM_REGISTER_SHARED_RULESET", strconv.FormatBool(registerRS))
	os.Setenv("KM_REGISTER_DOMAIN_IDENTITY", strconv.FormatBool(registerID))

	sesDir := filepath.Join(findRepoRoot(), "infra", "live", "use1", "ses-shared-rule-set")

	if dryRun {
		fmt.Fprintf(w, "Dry run — would run: terragrunt plan %s\n", sesDir)
		fmt.Fprintf(w, "  KM_REGISTER_SHARED_RULESET=%s\n", os.Getenv("KM_REGISTER_SHARED_RULESET"))
		fmt.Fprintf(w, "  KM_REGISTER_DOMAIN_IDENTITY=%s\n", os.Getenv("KM_REGISTER_DOMAIN_IDENTITY"))
		return nil
	}

	fmt.Fprintln(w, "Applying ses-shared-rule-set...")
	if err := ApplyTerragruntFunc(ctx, sesDir); err != nil {
		return fmt.Errorf("ses-shared-rule-set apply: %w", err)
	}
	fmt.Fprintln(w, "ses-shared-rule-set applied.")
	return nil
}

// SCPStatement represents a single statement in an SCP policy document.
// Exported so tests can inspect individual statement fields without AWS access.
type SCPStatement struct {
	Sid       string      `json:"Sid"`
	Effect    string      `json:"Effect"`
	Action    []string    `json:"Action,omitempty"`
	NotAction []string    `json:"NotAction,omitempty"`
	Resource  string      `json:"Resource"`
	Condition interface{} `json:"Condition,omitempty"`
}

// SCPPolicyDoc is the top-level SCP policy document structure.
// Exported so tests can inspect the full policy without AWS access.
type SCPPolicyDoc struct {
	Version   string         `json:"Version"`
	Statement []SCPStatement `json:"Statement"`
}

// BuildSCPPolicy returns the SCP policy document for the application account
// given the resolved trusted-principal sets. Pure (no AWS calls); tested directly.
func BuildSCPPolicy(trustedBase, trustedInstance, trustedIAM, trustedSSM []string, region string) SCPPolicyDoc {
	arnNotLike := func(arns []string) interface{} {
		return map[string]interface{}{
			"ArnNotLike": map[string]interface{}{
				"aws:PrincipalARN": arns,
			},
		}
	}

	return SCPPolicyDoc{
		Version: "2012-10-17",
		Statement: []SCPStatement{
			{
				Sid:    "DenyInfraAndStorage",
				Effect: "Deny",
				Action: []string{
					"ec2:CreateSecurityGroup", "ec2:DeleteSecurityGroup",
					"ec2:AuthorizeSecurityGroup*", "ec2:RevokeSecurityGroup*",
					"ec2:ModifySecurityGroupRules",
					"ec2:CreateVpc", "ec2:CreateSubnet", "ec2:CreateRouteTable",
					"ec2:CreateRoute", "ec2:*InternetGateway", "ec2:CreateNatGateway",
					"ec2:*VpcPeeringConnection", "ec2:CreateTransitGateway*",
					"ec2:CreateSnapshot", "ec2:CopySnapshot", "ec2:DeleteSnapshot",
					// AMI / EBS snapshot lifecycle (Phase 56): trusted-base principals (operator,
					// km-provisioner, km-lifecycle) may bake, copy, deregister, and clean up.
					// Describe* ops are read-only and intentionally NOT denied here — they remain
					// implicitly allowed for inspection. NOTE: SCP exemption alone does not grant
					// permission — operator IAM allow policy must affirmatively include these ops.
					// See WriteOperatorIAMGuidance() output for operator-side requirements.
					"ec2:CreateImage", "ec2:CopyImage", "ec2:ExportImage", "ec2:DeregisterImage",
					"ec2:CreateTags",
				},
				Resource:  "*",
				Condition: arnNotLike(trustedBase),
			},
			{
				Sid:    "DenyInstanceMutation",
				Effect: "Deny",
				Action: []string{
					"ec2:RunInstances", "ec2:ModifyInstanceAttribute",
					"ec2:ModifyInstanceMetadataOptions",
				},
				Resource:  "*",
				Condition: arnNotLike(trustedInstance),
			},
			{
				Sid:    "DenyIAMEscalation",
				Effect: "Deny",
				Action: []string{
					"iam:CreateRole", "iam:AttachRolePolicy", "iam:DetachRolePolicy",
					"iam:PassRole", "iam:AssumeRole",
				},
				Resource:  "*",
				Condition: arnNotLike(trustedIAM),
			},
			{
				Sid:      "DenySSMPivot",
				Effect:   "Deny",
				Action:   []string{"ssm:SendCommand", "ssm:StartSession"},
				Resource: "*",
				Condition: arnNotLike(trustedSSM),
			},
			{
				Sid:    "DenyOrgDiscovery",
				Effect: "Deny",
				Action: []string{"organizations:List*", "organizations:Describe*"},
				Resource: "*",
			},
			{
				Sid:    "DenyOutsideRegion",
				Effect: "Deny",
				NotAction: []string{
					"iam:*", "sts:*", "organizations:*", "support:*", "health:*",
					"trustedadvisor:*", "cloudfront:*", "waf:*", "shield:*",
					"route53:*", "route53domains:*", "budgets:*", "ce:*", "cur:*",
					"globalaccelerator:*", "networkmanager:*", "pricing:*", "bedrock:*",
					"s3:GetAccountPublicAccessBlock", "s3:ListAllMyBuckets",
					"s3:PutAccountPublicAccessBlock",
				},
				Resource: "*",
				Condition: map[string]interface{}{
					"StringNotEquals": map[string]interface{}{
						"aws:RequestedRegion": []string{region},
					},
					"ArnNotLike": map[string]interface{}{
						"aws:PrincipalArn": trustedBase,
					},
				},
			},
		},
	}
}

// WriteOperatorIAMGuidance writes the Phase 56 AMI-lifecycle positive-allow
// requirements block to w. Documents read-only and mutating ops the operator
// role must have in its IAM allow policy (independent of the SCP exemption).
// Exported so tests can verify the guidance text without invoking runShowSCP.
func WriteOperatorIAMGuidance(w io.Writer) {
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w, "# Operator IAM Positive-Allow Requirements (Phase 56 AMI Lifecycle)")
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# The SCP above (DenyInfraAndStorage) un-blocks the following AMI-lifecycle")
	fmt.Fprintln(w, "# operations for trusted-base principals via ArnNotLike exemption:")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "#   ec2:CreateImage, ec2:CopyImage, ec2:ExportImage,")
	fmt.Fprintln(w, "#   ec2:DeregisterImage, ec2:DeleteSnapshot, ec2:CreateTags")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# IMPORTANT: Un-blocking via SCP is NOT the same as granting permission.")
	fmt.Fprintln(w, "# The operator's SSO permission set (or the klanker-terraform role's inline")
	fmt.Fprintln(w, "# policy) must AFFIRMATIVELY ALLOW these actions in addition to the SCP")
	fmt.Fprintln(w, "# exemption.")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# Required AMI-lifecycle permissions for the operator role:")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "#   Mutating (also exempted in SCP above):")
	fmt.Fprintln(w, "#     - ec2:CreateImage")
	fmt.Fprintln(w, "#     - ec2:CopyImage")
	fmt.Fprintln(w, "#     - ec2:DeregisterImage")
	fmt.Fprintln(w, "#     - ec2:DeleteSnapshot")
	fmt.Fprintln(w, "#     - ec2:CreateTags")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "#   Read-only (NOT in SCP — must be in IAM allow policy):")
	fmt.Fprintln(w, "#     - ec2:DescribeImages       (km ami list, km doctor stale-AMI check)")
	fmt.Fprintln(w, "#     - ec2:DescribeSnapshots    (km ami list --wide for snapshot count)")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# Example IAM policy statement to add to the operator's SSO permission set")
	fmt.Fprintln(w, "# or the klanker-terraform role inline policy:")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# {")
	fmt.Fprintln(w, "#   \"Version\": \"2012-10-17\",")
	fmt.Fprintln(w, "#   \"Statement\": [")
	fmt.Fprintln(w, "#     {")
	fmt.Fprintln(w, "#       \"Sid\": \"KMAMILifecycle\",")
	fmt.Fprintln(w, "#       \"Effect\": \"Allow\",")
	fmt.Fprintln(w, "#       \"Action\": [")
	fmt.Fprintln(w, "#         \"ec2:CreateImage\",")
	fmt.Fprintln(w, "#         \"ec2:CopyImage\",")
	fmt.Fprintln(w, "#         \"ec2:DeregisterImage\",")
	fmt.Fprintln(w, "#         \"ec2:DeleteSnapshot\",")
	fmt.Fprintln(w, "#         \"ec2:CreateTags\",")
	fmt.Fprintln(w, "#         \"ec2:DescribeImages\",")
	fmt.Fprintln(w, "#         \"ec2:DescribeSnapshots\"")
	fmt.Fprintln(w, "#       ],")
	fmt.Fprintln(w, "#       \"Resource\": \"*\"")
	fmt.Fprintln(w, "#     }")
	fmt.Fprintln(w, "#   ]")
	fmt.Fprintln(w, "# }")
	fmt.Fprintln(w, "#")
	fmt.Fprintln(w, "# Without these, `km ami list`, `km ami delete`, `km ami copy`, and")
	fmt.Fprintln(w, "# `km doctor` stale-AMI checks will fail with UnauthorizedOperation.")
	fmt.Fprintln(w, "# ============================================================")
}

// KMSEnsureAPI covers the KMS operations needed to create a key and alias.
// Allows test injection without real AWS calls.
type KMSEnsureAPI interface {
	DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	CreateKey(ctx context.Context, params *kms.CreateKeyInput, optFns ...func(*kms.Options)) (*kms.CreateKeyOutput, error)
	CreateAlias(ctx context.Context, params *kms.CreateAliasInput, optFns ...func(*kms.Options)) (*kms.CreateAliasOutput, error)
}

// TerragruntApplyFunc is the function signature for applying a Terragrunt unit.
// It is exported so external test packages can inject a fake without executing terragrunt.
type TerragruntApplyFunc func(ctx context.Context, dir string) error

// ApplyTerragruntFunc is the package-level apply function used by runBootstrap.
// Tests replace this variable to capture apply calls without executing terragrunt.
var ApplyTerragruntFunc TerragruntApplyFunc = defaultApplyTerragrunt

// defaultApplyTerragrunt runs `terragrunt apply -auto-approve` on the given directory
// using the management-account AWS profile. Calls Reconfigure first to initialize the
// S3 backend on first apply of a new module (e.g. the Phase 84 ses-shared-rule-set
// module on an in-place upgrade) — terragrunt's auto-init does not fire when the
// backend config is new to this working tree.
//
// Phase 84.1-02 Task 3 (plan-checker rev 1 H6): Reconfigure + Apply are
// wrapped in a single BootstrapApplyTimeout (default 10min) — the same upper
// bound RunInitWithRunner uses for the regional ses-shared-rule-set module.
// Without this bound, a wedged terragrunt blocks km bootstrap forever,
// mirroring the original 84-10-UAT.md GAP-4/5 km init regression.
func defaultApplyTerragrunt(ctx context.Context, dir string) error {
	awsProfile := "klanker-terraform"
	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner(awsProfile, repoRoot)

	boundCtx, cancel := context.WithTimeout(ctx, BootstrapApplyTimeout)
	defer cancel()

	if err := runner.Reconfigure(boundCtx, dir); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("terragrunt init -reconfigure %s wedged after %s — see OPERATOR-GUIDE.md § Phase 84.1 state-digest recovery: %w", dir, BootstrapApplyTimeout, err)
		}
		return fmt.Errorf("terragrunt init -reconfigure: %w", err)
	}
	if err := runner.Apply(boundCtx, dir); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("terragrunt apply %s wedged after %s — kill orphan terragrunt PID (see heartbeat above) and consult OPERATOR-GUIDE.md § Phase 84.1 state-digest recovery: %w", dir, BootstrapApplyTimeout, err)
		}
		return err
	}
	return nil
}

// NewBootstrapCmd creates the "km bootstrap" command using os.Stdout for output.
func NewBootstrapCmd(cfg *config.Config) *cobra.Command {
	return NewBootstrapCmdWithWriter(cfg, os.Stdout)
}

// NewBootstrapCmdWithWriter creates the "km bootstrap" command writing output to w.
// Pass nil to use os.Stdout. Used in tests for output capture.
//
// bootstrap validates that km-config.yaml exists and is loadable, then
// (with --dry-run, the default) prints what infrastructure would be created.
// With --dry-run=false, it deploys the SCP containment policy to the management account.
func NewBootstrapCmdWithWriter(cfg *config.Config, w io.Writer) *cobra.Command {
	if w == nil {
		w = os.Stdout
	}

	var dryRun bool
	var showPrereqs bool
	var showSCP bool
	var sharedSES bool

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Validate config and show what infrastructure bootstrap would create",
		Long:  helpText("bootstrap"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if showSCP {
				return runShowSCP(cmd.Context(), cfg, w)
			}
			if showPrereqs {
				return runShowPrereqs(cmd.Context(), cfg, w)
			}
			if sharedSES {
				return runBootstrapSharedSES(cmd.Context(), cfg, dryRun, w, nil)
			}
			return runBootstrap(cmd.Context(), cfg, dryRun, w)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", true,
		"Print what would be created without making any changes (default: true)")
	cmd.Flags().BoolVar(&showPrereqs, "show-prereqs", false,
		"Print the IAM role and trust policy that must be created in the management account before bootstrap can deploy the SCP")
	cmd.Flags().BoolVar(&showSCP, "scp", false,
		"Print the km-sandbox-containment SCP policy JSON and the km-org-admin role/trust policy")
	cmd.Flags().BoolVar(&sharedSES, "shared-ses", false,
		"Provision the account-shared SES rule set + domain identity (Phase 84); run before km init on a fresh account")

	return cmd
}

// findKMConfigPath locates km-config.yaml by checking (in order):
//  1. The current working directory
//  2. The repo root (as determined by findRepoRoot)
func findKMConfigPath() string {
	cwd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(cwd, "km-config.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(findRepoRoot(), "km-config.yaml")
}

// runShowPrereqs prints the IAM role and trust policy needed in the organization account.
func runShowPrereqs(ctx context.Context, cfg *config.Config, w io.Writer) error {
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	if loadedCfg.OrganizationAccountID == "" {
		fmt.Fprintln(w, "accounts.organization not set — SCP deployment disabled.")
		fmt.Fprintln(w, "Set accounts.organization in km-config.yaml to enable org-level sandbox containment via Service Control Policies.")
		return nil
	}

	// Determine the caller identity for the trust policy
	callerAccount := loadedCfg.ApplicationAccountID
	if callerAccount == "" {
		callerAccount = loadedCfg.TerraformAccountID
	}
	if callerAccount == "" {
		callerAccount = "<APPLICATION_ACCOUNT_ID>"
	}

	orgAccount := loadedCfg.OrganizationAccountID
	roleName := "km-org-admin"

	fmt.Fprintln(w, "# Prerequisites for km bootstrap")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "The SCP deployment assumes a role `%s` in the organization account (%s).\n", roleName, orgAccount)
	fmt.Fprintf(w, "This role must be created manually before running `km bootstrap --dry-run=false`.\n")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Option 1: AWS CLI")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Run these commands while authenticated to the organization account (%s):\n", orgAccount)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```bash")
	fmt.Fprintln(w, "# 1. Create the role with a trust policy allowing the application account to assume it")
	fmt.Fprintf(w, `aws iam create-role --role-name %s --assume-role-policy-document '{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::%s:root"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "ArnLike": {
          "aws:PrincipalArn": [
            "arn:aws:iam::%s:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
            "arn:aws:iam::%s:role/km-provisioner-*"
          ]
        }
      }
    }
  ]
}'
`, roleName, callerAccount, callerAccount, callerAccount)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# 2. Attach the Organizations policy permissions (least-privilege, three statements)")
	fmt.Fprintln(w, "#    NOTE: Replace <ORG_ID> below with your Organization ID (e.g., o-abc123xyz)")
	fmt.Fprintln(w, "#    Find it with: aws organizations describe-organization --query 'Organization.Id' --output text")
	fmt.Fprintf(w, `aws iam put-role-policy --role-name %s --policy-name km-org-admin-policy --policy-document '{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SCPMutate",
      "Effect": "Allow",
      "Action": [
        "organizations:UpdatePolicy",
        "organizations:DeletePolicy",
        "organizations:DescribePolicy",
        "organizations:ListTargetsForPolicy",
        "organizations:TagResource",
        "organizations:UntagResource",
        "organizations:ListTagsForResource"
      ],
      "Resource": "arn:aws:organizations::%s:policy/*/service_control_policy/*"
    },
    {
      "Sid": "SCPAttachDetach",
      "Effect": "Allow",
      "Action": [
        "organizations:AttachPolicy",
        "organizations:DetachPolicy"
      ],
      "Resource": [
        "arn:aws:organizations::%s:policy/*/service_control_policy/*",
        "arn:aws:organizations::%s:account/*/%s"
      ]
    },
    {
      "Sid": "SCPCreateListDescribe",
      "Effect": "Allow",
      "Action": [
        "organizations:CreatePolicy",
        "organizations:ListPolicies",
        "organizations:ListPoliciesForTarget",
        "organizations:DescribeOrganization"
      ],
      "Resource": "*"
    }
  ]
}'
`, roleName, orgAccount, orgAccount, orgAccount, callerAccount)
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Option 2: CloudFormation")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Deploy this stack in the organization account (%s):\n", orgAccount)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```yaml")
	fmt.Fprintln(w, "AWSTemplateFormatVersion: '2010-09-09'")
	fmt.Fprintf(w, "Description: Cross-account role for Klanker Maker SCP management\n")
	fmt.Fprintln(w, "Resources:")
	fmt.Fprintln(w, "  KMOrgAdminRole:")
	fmt.Fprintln(w, "    Type: AWS::IAM::Role")
	fmt.Fprintln(w, "    Properties:")
	fmt.Fprintf(w, "      RoleName: %s\n", roleName)
	fmt.Fprintln(w, "      AssumeRolePolicyDocument:")
	fmt.Fprintln(w, "        Version: '2012-10-17'")
	fmt.Fprintln(w, "        Statement:")
	fmt.Fprintln(w, "          - Effect: Allow")
	fmt.Fprintln(w, "            Principal:")
	fmt.Fprintf(w, "              AWS: 'arn:aws:iam::%s:root'\n", callerAccount)
	fmt.Fprintln(w, "            Action: 'sts:AssumeRole'")
	fmt.Fprintln(w, "            Condition:")
	fmt.Fprintln(w, "              ArnLike:")
	fmt.Fprintln(w, "                aws:PrincipalArn:")
	fmt.Fprintf(w, "                  - 'arn:aws:iam::%s:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*'\n", callerAccount)
	fmt.Fprintf(w, "                  - 'arn:aws:iam::%s:role/km-provisioner-*'\n", callerAccount)
	fmt.Fprintln(w, "      Policies:")
	fmt.Fprintln(w, "        - PolicyName: km-org-admin-policy")
	fmt.Fprintln(w, "          PolicyDocument:")
	fmt.Fprintln(w, "            Version: '2012-10-17'")
	fmt.Fprintln(w, "            Statement:")
	fmt.Fprintln(w, "              # NOTE: Replace <ORG_ID> with your Organization ID (e.g., o-abc123xyz)")
	fmt.Fprintln(w, "              - Sid: SCPMutate")
	fmt.Fprintln(w, "                Effect: Allow")
	fmt.Fprintln(w, "                Action:")
	fmt.Fprintln(w, "                  - organizations:UpdatePolicy")
	fmt.Fprintln(w, "                  - organizations:DeletePolicy")
	fmt.Fprintln(w, "                  - organizations:DescribePolicy")
	fmt.Fprintln(w, "                  - organizations:ListTargetsForPolicy")
	fmt.Fprintln(w, "                  - organizations:TagResource")
	fmt.Fprintln(w, "                  - organizations:UntagResource")
	fmt.Fprintln(w, "                  - organizations:ListTagsForResource")
	fmt.Fprintln(w, "                Resource:")
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:policy/*/service_control_policy/*'\n", orgAccount)
	fmt.Fprintln(w, "              - Sid: SCPAttachDetach")
	fmt.Fprintln(w, "                Effect: Allow")
	fmt.Fprintln(w, "                Action:")
	fmt.Fprintln(w, "                  - organizations:AttachPolicy")
	fmt.Fprintln(w, "                  - organizations:DetachPolicy")
	fmt.Fprintln(w, "                Resource:")
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:policy/*/service_control_policy/*'\n", orgAccount)
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:account/*/%s'\n", orgAccount, callerAccount)
	fmt.Fprintln(w, "              - Sid: SCPCreateListDescribe")
	fmt.Fprintln(w, "                Effect: Allow")
	fmt.Fprintln(w, "                Action:")
	fmt.Fprintln(w, "                  - organizations:CreatePolicy")
	fmt.Fprintln(w, "                  - organizations:ListPolicies")
	fmt.Fprintln(w, "                  - organizations:ListPoliciesForTarget")
	fmt.Fprintln(w, "                  - organizations:DescribeOrganization")
	fmt.Fprintln(w, "                Resource: '*'")
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Step 0: Enable SCPs in your Organization")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "SCPs must be enabled before bootstrap can create policies. Check and enable from the organization account:")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```bash")
	fmt.Fprintln(w, "# Check if SCPs are enabled")
	fmt.Fprintln(w, "aws organizations list-roots --query 'Roots[0].PolicyTypes'")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# If SERVICE_CONTROL_POLICY is not listed, enable it:")
	fmt.Fprintln(w, "aws organizations enable-policy-type \\")
	fmt.Fprintln(w, "  --root-id $(aws organizations list-roots --query 'Roots[0].Id' --output text) \\")
	fmt.Fprintln(w, "  --policy-type SERVICE_CONTROL_POLICY")
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## What this role does")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- **Role ARN:** arn:aws:iam::%s:role/%s\n", orgAccount, roleName)
	fmt.Fprintf(w, "- **Trusted by:** Application account %s (SSO and provisioner roles only)\n", callerAccount)
	fmt.Fprintln(w, "- **Permissions:** Organizations SCP CRUD — create, attach, update, and delete Service Control Policies")
	fmt.Fprintln(w, "- **Used by:** `km bootstrap --dry-run=false` to deploy the km-sandbox-containment SCP")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "After creating this role, run: km bootstrap --dry-run=false\n")

	return nil
}

// runShowSCP prints the km-sandbox-containment SCP policy JSON and the km-org-admin
// role/trust policy, with real account IDs from km-config.yaml substituted in.
func runShowSCP(ctx context.Context, cfg *config.Config, w io.Writer) error {
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	appAccount := loadedCfg.ApplicationAccountID
	if appAccount == "" {
		return fmt.Errorf("no application account configured\nRun 'km configure' and set accounts.application first")
	}
	orgAccount := loadedCfg.OrganizationAccountID
	if orgAccount == "" {
		fmt.Fprintln(w, "SCP enforcement disabled — no organization account configured.")
		fmt.Fprintln(w, "Set accounts.organization in km-config.yaml to enable SCP deployment.")
		return nil
	}

	region := loadedCfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}

	// Determine caller account for trust policy (same logic as runShowPrereqs).
	callerAccount := appAccount
	if callerAccount == "" {
		callerAccount = loadedCfg.TerraformAccountID
	}

	// --- Trusted role ARN sets (mirrors infra/modules/scp/v1.0.0/main.tf locals) ---
	trustedBase := []string{
		fmt.Sprintf("arn:aws:iam::%s:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*", appAccount),
		fmt.Sprintf("arn:aws:iam::%s:role/km-provisioner-*", appAccount),
		fmt.Sprintf("arn:aws:iam::%s:role/km-lifecycle-*", appAccount),
		fmt.Sprintf("arn:aws:iam::%s:role/km-ecs-spot-handler", appAccount),
		fmt.Sprintf("arn:aws:iam::%s:role/km-ttl-handler", appAccount),
		fmt.Sprintf("arn:aws:iam::%s:role/km-create-handler", appAccount),
	}
	// trustedInstance is the same as trustedBase here because km-ecs-spot-handler
	// is already in the base set (added via terragrunt inputs). In the Terraform module
	// it's concat'd separately, but the result is equivalent.
	trustedInstance := append([]string{}, trustedBase...)
	trustedIAM := append(append([]string{}, trustedBase...), fmt.Sprintf("arn:aws:iam::%s:role/km-budget-enforcer-*", appAccount))
	trustedSSM := []string{
		fmt.Sprintf("arn:aws:iam::%s:role/km-ec2spot-ssm-*", appAccount),
		fmt.Sprintf("arn:aws:iam::%s:role/km-github-token-refresher-*", appAccount),
		fmt.Sprintf("arn:aws:iam::%s:role/km-ttl-handler", appAccount),
		"arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
	}

	// Build SCP policy document (mirrors the Terraform data.aws_iam_policy_document).
	policy := BuildSCPPolicy(trustedBase, trustedInstance, trustedIAM, trustedSSM, region)

	policyJSON, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal SCP policy: %w", err)
	}

	// --- Print SCP policy ---
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w, "# km-sandbox-containment SCP Policy")
	fmt.Fprintln(w, "#")
	fmt.Fprintf(w, "# Target: Application account %s\n", appAccount)
	fmt.Fprintf(w, "# Region lock: %s\n", region)
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w)
	fmt.Fprintln(w, string(policyJSON))
	fmt.Fprintln(w)

	// Operator IAM positive-allow guidance for Phase 56 AMI lifecycle.
	WriteOperatorIAMGuidance(w)
	fmt.Fprintln(w)

	// --- Print km-org-admin role/trust policy ---
	roleName := "km-org-admin"

	trustPolicy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Effect": "Allow",
				"Principal": map[string]interface{}{
					"AWS": fmt.Sprintf("arn:aws:iam::%s:root", callerAccount),
				},
				"Action": "sts:AssumeRole",
				"Condition": map[string]interface{}{
					"ArnLike": map[string]interface{}{
						"aws:PrincipalArn": []string{
							fmt.Sprintf("arn:aws:iam::%s:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*", callerAccount),
							fmt.Sprintf("arn:aws:iam::%s:role/km-provisioner-*", callerAccount),
						},
					},
				},
			},
		},
	}
	trustJSON, _ := json.MarshalIndent(trustPolicy, "", "  ")

	rolePolicy := map[string]interface{}{
		"Version": "2012-10-17",
		"Statement": []map[string]interface{}{
			{
				"Sid":    "SCPMutate",
				"Effect": "Allow",
				"Action": []string{
					"organizations:UpdatePolicy", "organizations:DeletePolicy",
					"organizations:DescribePolicy", "organizations:ListTargetsForPolicy",
					"organizations:TagResource", "organizations:UntagResource",
					"organizations:ListTagsForResource",
				},
				"Resource": fmt.Sprintf("arn:aws:organizations::%s:policy/*/service_control_policy/*", orgAccount),
			},
			{
				"Sid":    "SCPAttachDetach",
				"Effect": "Allow",
				"Action": []string{"organizations:AttachPolicy", "organizations:DetachPolicy"},
				"Resource": []string{
					fmt.Sprintf("arn:aws:organizations::%s:policy/*/service_control_policy/*", orgAccount),
					fmt.Sprintf("arn:aws:organizations::%s:account/*/%s", orgAccount, appAccount),
				},
			},
			{
				"Sid":      "SCPCreateListDescribe",
				"Effect":   "Allow",
				"Action":   []string{"organizations:CreatePolicy", "organizations:ListPolicies", "organizations:ListPoliciesForTarget", "organizations:DescribeOrganization"},
				"Resource": "*",
			},
		},
	}
	rolePolicyJSON, _ := json.MarshalIndent(rolePolicy, "", "  ")

	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintf(w, "# km-org-admin Role — Organization account %s\n", orgAccount)
	fmt.Fprintln(w, "#")
	fmt.Fprintf(w, "# Assumed by: Application account %s (SSO + provisioner roles)\n", callerAccount)
	fmt.Fprintln(w, "# Used by:    km bootstrap --dry-run=false")
	fmt.Fprintln(w, "# ============================================================")
	fmt.Fprintln(w)

	fmt.Fprintf(w, "## Trust Policy (AssumeRolePolicyDocument) for role %s\n\n", roleName)
	fmt.Fprintln(w, string(trustJSON))
	fmt.Fprintln(w)

	fmt.Fprintf(w, "## Inline Policy (km-org-admin-policy) for role %s\n\n", roleName)
	fmt.Fprintln(w, string(rolePolicyJSON))
	fmt.Fprintln(w)

	fmt.Fprintln(w, "# AWS CLI commands to create this role:")
	fmt.Fprintf(w, "#   aws iam create-role --role-name %s --assume-role-policy-document '<trust-policy-json>'\n", roleName)
	fmt.Fprintf(w, "#   aws iam put-role-policy --role-name %s --policy-name km-org-admin-policy --policy-document '<inline-policy-json>'\n", roleName)

	return nil
}

// loadBootstrapConfig loads config from the injected cfg or from disk.
func loadBootstrapConfig(cfg *config.Config) (*config.Config, error) {
	if cfg != nil && (cfg.OrganizationAccountID != "" || cfg.DNSParentAccountID != "" || cfg.ApplicationAccountID != "" || cfg.Domain != "") {
		return cfg, nil
	}

	kmConfigPath := findKMConfigPath()
	if _, err := os.Stat(kmConfigPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("km-config.yaml not found at %s\nRun 'km configure' first", kmConfigPath)
	}

	loadedCfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("invalid km-config.yaml: %w", err)
	}
	return loadedCfg, nil
}

// runBootstrap implements bootstrap validation, dry-run output, and SCP deployment.
func runBootstrap(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}

	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "Domain:  %s\n", loadedCfg.Domain)
	fmt.Fprintf(w, "Region:  %s\n", loadedCfg.PrimaryRegion)
	orgDisplay := loadedCfg.OrganizationAccountID
	if orgDisplay == "" {
		orgDisplay = "(not set)"
	}
	dnsParentDisplay := loadedCfg.DNSParentAccountID
	if dnsParentDisplay == "" {
		dnsParentDisplay = "(not set)"
	}
	fmt.Fprintf(w, "Organization account: %s\n", orgDisplay)
	fmt.Fprintf(w, "DNS parent account: %s\n", dnsParentDisplay)
	fmt.Fprintf(w, "Application account: %s\n", loadedCfg.ApplicationAccountID)
	fmt.Fprintln(w)

	if dryRun {
		fmt.Fprintln(w, "Dry run — the following infrastructure would be created:")
		fmt.Fprintln(w)

		prefix := loadedCfg.GetResourcePrefix()
		regionLabel := compiler.RegionLabel(loadedCfg.PrimaryRegion)

		stateBucket := ""
		if cfg != nil {
			stateBucket = cfg.StateBucket
		}
		if stateBucket == "" {
			stateBucket = prefix + "-state-<hash>"
		}

		budgetTable := loadedCfg.BudgetTableName
		if budgetTable == "" {
			budgetTable = prefix + "-budgets"
		}

		fmt.Fprintf(w, "  S3 bucket:         %s\n", stateBucket)
		fmt.Fprintf(w, "    Purpose:         Sandbox metadata storage (km list/status)\n")
		fmt.Fprintf(w, "    Encryption:      AES256 (S3-managed)\n")
		fmt.Fprintf(w, "    Versioning:      enabled\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  S3 bucket:         tf-%s-state-%s  [created by Terragrunt --backend-bootstrap on first apply]\n", prefix, regionLabel)
		fmt.Fprintf(w, "    Purpose:         Terraform remote state\n")
		fmt.Fprintf(w, "    Encryption:      enabled (S3 default)\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  DynamoDB table:    tf-%s-locks-%s  [created by Terragrunt on first apply]\n", prefix, regionLabel)
		fmt.Fprintf(w, "    Purpose:         Terraform state locking\n")
		fmt.Fprintf(w, "    Billing:         PAY_PER_REQUEST\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  DynamoDB table:    %s\n", budgetTable)
		fmt.Fprintf(w, "    Purpose:         Sandbox budget enforcement tracking\n")
		fmt.Fprintf(w, "    Billing:         PAY_PER_REQUEST\n")
		fmt.Fprintln(w)

		// SCP policy section
		if loadedCfg.OrganizationAccountID != "" {
			fmt.Fprintf(w, "  SCP Policy:        km-sandbox-containment\n")
			fmt.Fprintf(w, "    Target:          Application account (%s)\n", loadedCfg.ApplicationAccountID)
			fmt.Fprintf(w, "    Threat coverage: SG mutation, network escape, instance mutation,\n")
			fmt.Fprintf(w, "                     IAM escalation, storage exfiltration, SSM pivot,\n")
			fmt.Fprintf(w, "                     Organizations discovery, region lock\n")
			fmt.Fprintf(w, "    Trusted roles:   AWSReservedSSO_*_*, km-provisioner-*, km-lifecycle-*,\n")
			fmt.Fprintf(w, "                     km-ecs-spot-handler, km-ttl-handler\n")
			fmt.Fprintf(w, "    Deploy via:      km bootstrap (organization account credentials required)\n")
		} else {
			fmt.Fprintf(w, "  SCP Policy:        [SKIPPED — accounts.organization not set]\n")
			fmt.Fprintf(w, "    Run 'km configure' and set accounts.organization to enable SCP deployment.\n")
		}
		fmt.Fprintln(w)
		platformAlias := loadedCfg.GetPlatformKMSAlias()
		fmt.Fprintf(w, "  KMS key:           %s\n", strings.TrimPrefix(platformAlias, "alias/"))
		fmt.Fprintf(w, "    Purpose:         SSM SecureString encryption for sandbox identity keys and secrets\n")
		fmt.Fprintf(w, "    Alias:           %s\n", platformAlias)
		fmt.Fprintln(w)

		if loadedCfg.ArtifactsBucket != "" {
			fmt.Fprintf(w, "  S3 bucket:         %s\n", loadedCfg.ArtifactsBucket)
			fmt.Fprintf(w, "    Purpose:         Lambda zips, sidecar binaries, sandbox artifacts\n")
			fmt.Fprintf(w, "    Versioning:      enabled\n")
		} else {
			fmt.Fprintf(w, "  S3 artifacts:      [SKIPPED — no artifacts_bucket configured]\n")
			fmt.Fprintf(w, "    Run 'km configure' and set artifacts_bucket to enable.\n")
		}
		fmt.Fprintln(w)

		fmt.Fprintln(w, "Run 'km bootstrap --dry-run=false' to provision.")
		return nil
	}

	// Non-dry-run: deploy SCP sandbox-containment policy.
	// DNS parent env var is always exported (independent of org).
	os.Setenv("KM_ACCOUNTS_DNS_PARENT", loadedCfg.DNSParentAccountID)
	if loadedCfg.OrganizationAccountID != "" {
		// Export config values as env vars for Terragrunt's site.hcl get_env() calls.
		os.Setenv("KM_ACCOUNTS_ORGANIZATION", loadedCfg.OrganizationAccountID)
		os.Setenv("KM_ACCOUNTS_APPLICATION", loadedCfg.ApplicationAccountID)
		if loadedCfg.Domain != "" {
			os.Setenv("KM_DOMAIN", loadedCfg.Domain)
		}
		if loadedCfg.PrimaryRegion != "" {
			os.Setenv("KM_REGION", loadedCfg.PrimaryRegion)
		}

		scpDir := filepath.Join(findRepoRoot(), "infra", "live", "management", "scp")
		fmt.Fprintln(w, "Deploying SCP sandbox-containment policy...")
		if err := ApplyTerragruntFunc(ctx, scpDir); err != nil {
			return fmt.Errorf("scp bootstrap: %w", err)
		}
		fmt.Fprintln(w, "SCP sandbox-containment policy deployed to application account.")
	} else {
		fmt.Fprintln(w, "Skipping SCP deployment — no organization account configured.")
	}

	// Create the platform KMS key (alias/{prefix}-platform) for SSM SecureString encryption.
	fmt.Fprintln(w)
	if err := ensureKMSPlatformKey(ctx, loadedCfg, w); err != nil {
		return fmt.Errorf("kms bootstrap: %w", err)
	}

	// Create S3 artifacts bucket if configured.
	if loadedCfg.ArtifactsBucket != "" {
		fmt.Fprintln(w)
		if err := ensureArtifactsBucket(ctx, loadedCfg, w); err != nil {
			return fmt.Errorf("artifacts bucket bootstrap: %w", err)
		}
	}

	return nil
}

// ensureKMSPlatformKey creates the platform KMS key and alias if they don't exist.
// The alias is alias/{prefix}-platform where prefix comes from cfg.GetResourcePrefix()
// (default "km"). Pass a non-nil kmsClient to override the default real AWS client (used in tests).
func ensureKMSPlatformKey(ctx context.Context, cfg *config.Config, w io.Writer, kmsClient ...KMSEnsureAPI) error {
	var client KMSEnsureAPI
	if len(kmsClient) > 0 && kmsClient[0] != nil {
		client = kmsClient[0]
	} else {
		region := cfg.PrimaryRegion
		if region == "" {
			region = "us-east-1"
		}

		awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		client = kms.NewFromConfig(awsCfg)
	}

	aliasName := cfg.GetPlatformKMSAlias()

	// Check if alias already exists.
	_, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{
		KeyId: aws.String(aliasName),
	})
	if err == nil {
		fmt.Fprintf(w, "KMS key %s already exists.\n", aliasName)
		return nil
	}

	// Create the key.
	fmt.Fprintf(w, "Creating KMS key %s...\n", aliasName)
	createOut, err := client.CreateKey(ctx, &kms.CreateKeyInput{
		Description: aws.String("Klanker Maker platform key — SSM SecureString encryption for sandbox secrets and identity keys"),
		KeyUsage:    kmstypes.KeyUsageTypeEncryptDecrypt,
		Tags: []kmstypes.Tag{
			{TagKey: aws.String("km:component"), TagValue: aws.String("platform")},
			{TagKey: aws.String("km:managed"), TagValue: aws.String("true")},
		},
	})
	if err != nil {
		return fmt.Errorf("create KMS key: %w", err)
	}

	// Create the alias.
	_, err = client.CreateAlias(ctx, &kms.CreateAliasInput{
		AliasName:   aws.String(aliasName),
		TargetKeyId: createOut.KeyMetadata.KeyId,
	})
	if err != nil {
		return fmt.Errorf("create KMS alias: %w", err)
	}

	fmt.Fprintf(w, "KMS key created: %s → %s\n", aliasName, aws.ToString(createOut.KeyMetadata.KeyId))
	return nil
}

// ensureArtifactsBucket creates the S3 artifacts bucket with versioning if it doesn't exist.
func ensureArtifactsBucket(ctx context.Context, cfg *config.Config, w io.Writer) error {
	region := cfg.PrimaryRegion
	if region == "" {
		region = "us-east-1"
	}

	awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", region)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg)

	bucketName := cfg.ArtifactsBucket

	// Check if bucket already exists.
	_, err = client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err == nil {
		fmt.Fprintf(w, "S3 bucket %s already exists.\n", bucketName)
		return nil
	}

	// Create the bucket.
	fmt.Fprintf(w, "Creating S3 bucket %s...\n", bucketName)
	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	}
	// us-east-1 must not specify LocationConstraint
	if region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}
	if _, err := client.CreateBucket(ctx, createInput); err != nil {
		return fmt.Errorf("create S3 bucket: %w", err)
	}

	// Enable versioning.
	_, err = client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	})
	if err != nil {
		return fmt.Errorf("enable bucket versioning: %w", err)
	}

	fmt.Fprintf(w, "S3 bucket %s created with versioning enabled.\n", bucketName)
	return nil
}

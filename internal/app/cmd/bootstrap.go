package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

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
// using the management-account AWS profile.
func defaultApplyTerragrunt(ctx context.Context, dir string) error {
	awsProfile := "klanker-terraform"
	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	return runner.Apply(ctx, dir)
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
			return runBootstrap(cmd.Context(), cfg, dryRun, w)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", true,
		"Print what would be created without making any changes (default: true)")
	cmd.Flags().BoolVar(&showPrereqs, "show-prereqs", false,
		"Print the IAM role and trust policy that must be created in the management account before bootstrap can deploy the SCP")
	cmd.Flags().BoolVar(&showSCP, "scp", false,
		"Print the km-sandbox-containment SCP policy JSON and the km-org-admin role/trust policy")

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

// runShowPrereqs prints the IAM role and trust policy needed in the management account.
func runShowPrereqs(ctx context.Context, cfg *config.Config, w io.Writer) error {
	loadedCfg, err := loadBootstrapConfig(cfg)
	if err != nil {
		return err
	}

	if loadedCfg.ManagementAccountID == "" {
		return fmt.Errorf("no management account configured\nRun 'km configure' and set accounts.management first")
	}

	// Determine the caller identity for the trust policy
	callerAccount := loadedCfg.ApplicationAccountID
	if callerAccount == "" {
		callerAccount = loadedCfg.TerraformAccountID
	}
	if callerAccount == "" {
		callerAccount = "<APPLICATION_ACCOUNT_ID>"
	}

	mgmtAccount := loadedCfg.ManagementAccountID
	roleName := "km-org-admin"

	fmt.Fprintln(w, "# Prerequisites for km bootstrap")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "The SCP deployment assumes a role `%s` in the management account (%s).\n", roleName, mgmtAccount)
	fmt.Fprintf(w, "This role must be created manually before running `km bootstrap --dry-run=false`.\n")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Option 1: AWS CLI")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Run these commands while authenticated to the management account (%s):\n", mgmtAccount)
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
`, roleName, mgmtAccount, mgmtAccount, mgmtAccount, callerAccount)
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Option 2: CloudFormation")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Deploy this stack in the management account (%s):\n", mgmtAccount)
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
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:policy/*/service_control_policy/*'\n", mgmtAccount)
	fmt.Fprintln(w, "              - Sid: SCPAttachDetach")
	fmt.Fprintln(w, "                Effect: Allow")
	fmt.Fprintln(w, "                Action:")
	fmt.Fprintln(w, "                  - organizations:AttachPolicy")
	fmt.Fprintln(w, "                  - organizations:DetachPolicy")
	fmt.Fprintln(w, "                Resource:")
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:policy/*/service_control_policy/*'\n", mgmtAccount)
	fmt.Fprintf(w, "                  - 'arn:aws:organizations::%s:account/*/%s'\n", mgmtAccount, callerAccount)
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
	fmt.Fprintln(w, "SCPs must be enabled before bootstrap can create policies. Check and enable from the management account:")
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
	fmt.Fprintf(w, "- **Role ARN:** arn:aws:iam::%s:role/%s\n", mgmtAccount, roleName)
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
	mgmtAccount := loadedCfg.ManagementAccountID
	if mgmtAccount == "" {
		return fmt.Errorf("no management account configured\nRun 'km configure' and set accounts.management first")
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
		"arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
	}

	// Build SCP policy document (mirrors the Terraform data.aws_iam_policy_document).
	type condition struct {
		Test     string   `json:"test"`
		Variable string   `json:"variable"`
		Values   []string `json:"values"`
	}
	type statement struct {
		Sid        string      `json:"Sid"`
		Effect     string      `json:"Effect"`
		Action     []string    `json:"Action,omitempty"`
		NotAction  []string    `json:"NotAction,omitempty"`
		Resource   string      `json:"Resource"`
		Condition  interface{} `json:"Condition,omitempty"`
	}
	type policyDoc struct {
		Version   string      `json:"Version"`
		Statement []statement `json:"Statement"`
	}

	arnNotLike := func(arns []string) interface{} {
		return map[string]interface{}{
			"ArnNotLike": map[string]interface{}{
				"aws:PrincipalARN": arns,
			},
		}
	}

	policy := policyDoc{
		Version: "2012-10-17",
		Statement: []statement{
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
					"ec2:CreateSnapshot", "ec2:CopySnapshot",
					"ec2:CreateImage", "ec2:CopyImage", "ec2:ExportImage",
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
				Sid:    "DenySSMPivot",
				Effect: "Deny",
				Action: []string{"ssm:SendCommand", "ssm:StartSession"},
				Resource:  "*",
				Condition: arnNotLike(trustedSSM),
			},
			{
				Sid:       "DenyOrgDiscovery",
				Effect:    "Deny",
				Action:    []string{"organizations:List*", "organizations:Describe*"},
				Resource:  "*",
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
				"Resource": fmt.Sprintf("arn:aws:organizations::%s:policy/*/service_control_policy/*", mgmtAccount),
			},
			{
				"Sid":    "SCPAttachDetach",
				"Effect": "Allow",
				"Action": []string{"organizations:AttachPolicy", "organizations:DetachPolicy"},
				"Resource": []string{
					fmt.Sprintf("arn:aws:organizations::%s:policy/*/service_control_policy/*", mgmtAccount),
					fmt.Sprintf("arn:aws:organizations::%s:account/*/%s", mgmtAccount, appAccount),
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
	fmt.Fprintf(w, "# km-org-admin Role — Management account %s\n", mgmtAccount)
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
	if cfg != nil && (cfg.ManagementAccountID != "" || cfg.ApplicationAccountID != "" || cfg.Domain != "") {
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
	fmt.Fprintf(w, "Management account: %s\n", loadedCfg.ManagementAccountID)
	fmt.Fprintf(w, "Application account: %s\n", loadedCfg.ApplicationAccountID)
	fmt.Fprintln(w)

	if dryRun {
		fmt.Fprintln(w, "Dry run — the following infrastructure would be created:")
		fmt.Fprintln(w)

		stateBucket := ""
		if cfg != nil {
			stateBucket = cfg.StateBucket
		}
		if stateBucket == "" {
			stateBucket = "km-terraform-state-<hash>"
		}

		budgetTable := loadedCfg.BudgetTableName
		if budgetTable == "" {
			budgetTable = "km-budgets"
		}

		fmt.Fprintf(w, "  S3 bucket:         %s\n", stateBucket)
		fmt.Fprintf(w, "    Purpose:         Terraform state and sandbox metadata\n")
		fmt.Fprintf(w, "    Encryption:      aws:kms (KMS key below)\n")
		fmt.Fprintf(w, "    Versioning:      enabled\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  DynamoDB table:    km-terraform-lock\n")
		fmt.Fprintf(w, "    Purpose:         Terraform state locking\n")
		fmt.Fprintf(w, "    Billing:         PAY_PER_REQUEST\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  KMS key:           km-terraform-state\n")
		fmt.Fprintf(w, "    Purpose:         S3 state bucket encryption\n")
		fmt.Fprintf(w, "    Deletion window: 30 days\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  DynamoDB table:    %s\n", budgetTable)
		fmt.Fprintf(w, "    Purpose:         Sandbox budget enforcement tracking\n")
		fmt.Fprintf(w, "    Billing:         PAY_PER_REQUEST\n")
		fmt.Fprintln(w)

		// SCP policy section
		if loadedCfg.ManagementAccountID != "" {
			fmt.Fprintf(w, "  SCP Policy:        km-sandbox-containment\n")
			fmt.Fprintf(w, "    Target:          Application account (%s)\n", loadedCfg.ApplicationAccountID)
			fmt.Fprintf(w, "    Threat coverage: SG mutation, network escape, instance mutation,\n")
			fmt.Fprintf(w, "                     IAM escalation, storage exfiltration, SSM pivot,\n")
			fmt.Fprintf(w, "                     Organizations discovery, region lock\n")
			fmt.Fprintf(w, "    Trusted roles:   AWSReservedSSO_*_*, km-provisioner-*, km-lifecycle-*,\n")
			fmt.Fprintf(w, "                     km-ecs-spot-handler, km-ttl-handler\n")
			fmt.Fprintf(w, "    Deploy via:      km bootstrap (management account credentials required)\n")
		} else {
			fmt.Fprintf(w, "  SCP Policy:        [SKIPPED — no management account configured]\n")
			fmt.Fprintf(w, "    Run 'km configure' and set accounts.management to enable SCP deployment.\n")
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  KMS key:           km-platform\n")
		fmt.Fprintf(w, "    Purpose:         SSM SecureString encryption for sandbox identity keys and secrets\n")
		fmt.Fprintf(w, "    Alias:           alias/km-platform\n")
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
	if loadedCfg.ManagementAccountID != "" {
		// Export config values as env vars for Terragrunt's site.hcl get_env() calls.
		os.Setenv("KM_ACCOUNTS_MANAGEMENT", loadedCfg.ManagementAccountID)
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
		fmt.Fprintln(w, "Skipping SCP deployment — no management account configured.")
	}

	// Create KMS key with alias/km-platform for SSM SecureString encryption.
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

// ensureKMSPlatformKey creates the km-platform KMS key and alias if they don't exist.
// Pass a non-nil kmsClient to override the default real AWS client (used in tests).
func ensureKMSPlatformKey(ctx context.Context, cfg *config.Config, w io.Writer, kmsClient ...KMSEnsureAPI) error {
	var client KMSEnsureAPI
	if len(kmsClient) > 0 && kmsClient[0] != nil {
		client = kmsClient[0]
	} else {
		region := cfg.PrimaryRegion
		if region == "" {
			region = "us-east-1"
		}

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(region),
			awsconfig.WithSharedConfigProfile("klanker-terraform"),
		)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		client = kms.NewFromConfig(awsCfg)
	}

	aliasName := "alias/km-platform"

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

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithSharedConfigProfile("klanker-terraform"),
	)
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

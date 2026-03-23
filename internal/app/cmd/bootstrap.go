package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
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

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Validate config and show what infrastructure bootstrap would create",
		Long:  helpText("bootstrap"),
		RunE: func(cmd *cobra.Command, args []string) error {
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

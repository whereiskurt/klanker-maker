package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// EC2StartAPI is the minimal EC2 interface required for sandbox auto-resume.
// Implemented by *ec2.Client.
type EC2StartAPI interface {
	StartInstances(ctx context.Context, input *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// IAMAttachAPI is the minimal IAM interface required for restoring Bedrock access.
// Implemented by *iam.Client.
type IAMAttachAPI interface {
	AttachRolePolicy(ctx context.Context, input *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error)
	ListAttachedRolePolicies(ctx context.Context, input *iam.ListAttachedRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error)
}

// SandboxMetaFetcher fetches sandbox metadata (substrate, region, etc.) for a given sandbox ID.
type SandboxMetaFetcher interface {
	FetchSandboxMeta(ctx context.Context, sandboxID string) (*kmaws.SandboxMetadata, error)
}

// bedrockPolicyARN is the AWS-managed policy that grants Bedrock model access.
const bedrockPolicyARN = "arn:aws:iam::aws:policy/AmazonBedrockFullAccess"

// sandboxRoleName derives the IAM role name for a given sandbox ID.
// Convention: km-sandbox-{sandboxID}-role (matches compiler output in Phase 02).
func sandboxRoleName(sandboxID string) string {
	return fmt.Sprintf("km-sandbox-%s-role", sandboxID)
}

// NewBudgetCmd creates the "km budget" command group.
// Usage: km budget add <sandbox-id> [--compute <amount>] [--ai <amount>]
func NewBudgetCmd(cfg *config.Config) *cobra.Command {
	return NewBudgetCmdWithDeps(cfg, nil, nil, nil, nil)
}

// NewBudgetCmdWithDeps builds the budget command with injected dependencies.
// Pass nil for any client to use the real AWS-backed client (requires credentials).
// Used in tests to inject fakes.
func NewBudgetCmdWithDeps(cfg *config.Config, budgetClient kmaws.BudgetAPI, ec2Client EC2StartAPI, iamClient IAMAttachAPI, metaFetcher SandboxMetaFetcher) *cobra.Command {
	budget := &cobra.Command{
		Use:          "budget",
		Short:        "Manage budget limits for a sandbox",
		Long:         helpText("budget"),
		SilenceUsage: true,
	}

	budget.AddCommand(newBudgetAddCmd(cfg, budgetClient, ec2Client, iamClient, metaFetcher))

	return budget
}

// newBudgetAddCmd creates the "km budget add" subcommand.
func newBudgetAddCmd(cfg *config.Config, budgetClient kmaws.BudgetAPI, ec2Client EC2StartAPI, iamClient IAMAttachAPI, metaFetcher SandboxMetaFetcher) *cobra.Command {
	var computeTopUp float64
	var aiTopUp float64

	add := &cobra.Command{
		Use:          "add <sandbox-id | #number>",
		Short:        "Add budget (top-up) to a sandbox and auto-resume if suspended",
		Long:         helpText("budget_add"),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			return runBudgetAdd(cmd, cfg, budgetClient, ec2Client, iamClient, metaFetcher, sandboxID, computeTopUp, aiTopUp)
		},
	}

	add.Flags().Float64Var(&computeTopUp, "compute", 0, "Amount in USD to add to the compute budget limit")
	add.Flags().Float64Var(&aiTopUp, "ai", 0, "Amount in USD to add to the AI budget limit")

	return add
}

// runBudgetAdd implements the km budget add logic.
// Steps:
//  1. Read current budget limits from DynamoDB
//  2. Calculate new limits (additive top-up)
//  3. Write new limits to DynamoDB
//  4. Auto-resume suspended sandbox (EC2 start or ECS re-provision, IAM restore)
//  5. Print summary
func runBudgetAdd(cmd *cobra.Command, cfg *config.Config, budgetClient kmaws.BudgetAPI, ec2Client EC2StartAPI, iamClient IAMAttachAPI, metaFetcher SandboxMetaFetcher, sandboxID string, computeTopUp, aiTopUp float64) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Initialize real clients if not injected (production path)
	awsProfile := cfg.AWSProfile
	if awsProfile == "" {
		awsProfile = "klanker-terraform"
	}
	if budgetClient == nil {
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		budgetClient = dynamodb.NewFromConfig(awsCfg)
		ec2Client = ec2.NewFromConfig(awsCfg)
		iamClient = iam.NewFromConfig(awsCfg)
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		metaFetcher = &realMetaFetcher{awsCfg: awsCfg, bucket: cfg.StateBucket, tableName: tableName}
	}

	tableName := cfg.BudgetTableName
	if tableName == "" {
		tableName = "km-budgets"
	}

	// Step 1: Read current limits from DynamoDB
	current, err := kmaws.GetBudget(ctx, budgetClient, tableName, sandboxID)
	if err != nil {
		return fmt.Errorf("read current budget for sandbox %s: %w", sandboxID, err)
	}

	// Step 2: Calculate new limits (additive)
	newComputeLimit := current.ComputeLimit + computeTopUp
	newAILimit := current.AILimit + aiTopUp
	warningThreshold := current.WarningThreshold
	if warningThreshold == 0 {
		warningThreshold = 0.80 // default 80%
	}

	// Step 3: Write new limits to DynamoDB
	if err := kmaws.SetBudgetLimits(ctx, budgetClient, tableName, sandboxID, newComputeLimit, newAILimit, warningThreshold); err != nil {
		return fmt.Errorf("update budget limits for sandbox %s: %w", sandboxID, err)
	}

	// Step 4: Auto-resume logic — check sandbox substrate and state
	resumed := false
	if metaFetcher != nil {
		meta, metaErr := metaFetcher.FetchSandboxMeta(ctx, sandboxID)
		if metaErr == nil && meta != nil {
			substrate := strings.ToLower(meta.Substrate)

			if strings.HasPrefix(substrate, "ec2") && ec2Client != nil {
				// Check EC2 instance state via describe (filter by sandbox tag)
				started, startErr := resumeEC2Sandbox(ctx, ec2Client, budgetClient, tableName, sandboxID)
				if startErr != nil {
					// Non-fatal: log but continue
					fmt.Fprintf(cmd.OutOrStdout(), "Warning: could not resume EC2 sandbox: %v\n", startErr)
				} else if started {
					resumed = true
				}
			}

			if substrate == "ecs" {
				// ECS Fargate tasks are ephemeral — they cannot be "started" like EC2.
				// Re-provision from the stored profile YAML in S3 using the same sandbox ID.
				artifactBucket := cfg.ArtifactsBucket
				if artifactBucket == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Warning: artifact bucket not configured — cannot re-provision ECS sandbox\n")
				} else {
					if err := reprovisionECSSandbox(ctx, cfg, sandboxID, artifactBucket, awsProfile); err != nil {
						fmt.Fprintf(cmd.OutOrStdout(), "Warning: could not re-provision ECS sandbox: %v\n", err)
					} else {
						resumed = true
					}
				}
			}
		}
	}

	// Step 5: Restore Bedrock IAM policy if detached
	if iamClient != nil {
		roleName := sandboxRoleName(sandboxID)
		restored, iamErr := restoreBedrockPolicy(ctx, iamClient, roleName)
		if iamErr != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Warning: could not check/restore Bedrock IAM policy: %v\n", iamErr)
		} else if restored {
			resumed = true
		}
	}

	// Step 6: Print summary
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Budget updated: compute $%.4f/$%.4f, AI $%.4f/$%.4f\n",
		current.ComputeSpent, newComputeLimit,
		current.AISpent, newAILimit,
	)
	if resumed {
		fmt.Fprintf(out, "Sandbox %s resumed.\n", sandboxID)
	}

	return nil
}

// resumeEC2Sandbox checks if EC2 instances for this sandbox are stopped and starts them.
// Returns true if any instance was started, false if all were already running.
// budgetClient and budgetTable are used to close the open pause interval after start succeeds.
func resumeEC2Sandbox(ctx context.Context, ec2Client EC2StartAPI, budgetClient kmaws.BudgetAPI, budgetTable, sandboxID string) (bool, error) {
	// Describe instances by sandbox-id tag
	out, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{
				Name:   awssdk.String("tag:km:sandbox-id"),
				Values: []string{sandboxID},
			},
		},
	})
	if err != nil {
		return false, fmt.Errorf("describe instances: %w", err)
	}

	var stoppedIDs []string
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			if instance.State != nil && instance.State.Name == ec2types.InstanceStateNameStopped {
				if instance.InstanceId != nil {
					stoppedIDs = append(stoppedIDs, *instance.InstanceId)
				}
			}
		}
	}

	if len(stoppedIDs) == 0 {
		return false, nil
	}

	_, err = ec2Client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: stoppedIDs,
	})
	if err != nil {
		return false, fmt.Errorf("start instances %v: %w", stoppedIDs, err)
	}

	// Close the open pause interval in the budget table so paused time stops accruing.
	// Non-fatal: a DynamoDB error only logs a warning and lifecycle continues.
	if budgetClient != nil && budgetTable != "" {
		if err := kmaws.RecordResumeClose(ctx, budgetClient, budgetTable, sandboxID, time.Now().UTC()); err != nil {
			log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("failed to record resume close in budget table (non-fatal)")
		}
	}

	return true, nil
}

// restoreBedrockPolicy checks if the Bedrock policy is attached to the sandbox role
// and attaches it if missing. Returns true if the policy was re-attached.
func restoreBedrockPolicy(ctx context.Context, iamClient IAMAttachAPI, roleName string) (bool, error) {
	out, err := iamClient.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: awssdk.String(roleName),
	})
	if err != nil {
		// Role may not exist (ECS or different substrate) — treat as non-fatal
		return false, nil
	}

	for _, policy := range out.AttachedPolicies {
		if policy.PolicyArn != nil && *policy.PolicyArn == bedrockPolicyARN {
			return false, nil // already attached
		}
	}

	// Bedrock policy is missing — attach it
	_, err = iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  awssdk.String(roleName),
		PolicyArn: awssdk.String(bedrockPolicyARN),
	})
	if err != nil {
		return false, fmt.Errorf("attach Bedrock policy to role %s: %w", roleName, err)
	}

	return true, nil
}

// realMetaFetcher is the real AWS-backed SandboxMetaFetcher.
// Reads from DynamoDB first; falls back to S3 on ResourceNotFoundException.
type realMetaFetcher struct {
	awsCfg    awssdk.Config
	bucket    string
	tableName string
}

func (r *realMetaFetcher) FetchSandboxMeta(ctx context.Context, sandboxID string) (*kmaws.SandboxMetadata, error) {
	dynamoClient := dynamodb.NewFromConfig(r.awsCfg)
	meta, err := kmaws.ReadSandboxMetadataDynamo(ctx, dynamoClient, r.tableName, sandboxID)
	if err != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &rnf) && r.bucket != "" {
			// Table doesn't exist — fall back to S3.
			s3Client := s3.NewFromConfig(r.awsCfg)
			return kmaws.ReadSandboxMetadata(ctx, s3Client, r.bucket, sandboxID)
		}
		return nil, err
	}
	return meta, nil
}

// reprovisionECSSandbox downloads the stored profile from S3 and runs terragrunt apply
// to restart the Fargate task with the same sandbox ID and container definitions.
//
// ECS Fargate tasks are ephemeral — budget enforcement stops the task (StopTask).
// The ECS service desired_count is set to 0 by the budget enforcer to prevent auto-relaunch.
// Re-provisioning calls terragrunt apply which reconciles desired_count back to 1.
//
// Critical: the existing sandboxID is reused (never a new one) so Terraform state maps
// correctly to the existing ECS cluster and task definition.
func reprovisionECSSandbox(ctx context.Context, cfg *config.Config, sandboxID, artifactBucket, awsProfile string) error {
	// Step 1: Load AWS config
	awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	// Step 2: Download stored profile YAML from S3
	s3Client := s3.NewFromConfig(awsCfg)
	resp, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: awssdk.String(artifactBucket),
		Key:    awssdk.String("artifacts/" + sandboxID + "/.km-profile.yaml"),
	})
	if err != nil {
		return fmt.Errorf("download profile for sandbox %s: %w", sandboxID, err)
	}
	defer resp.Body.Close()
	profileYAML, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read profile YAML: %w", err)
	}

	// Step 3: Parse and resolve profile
	parsed, err := profile.Parse(profileYAML)
	if err != nil {
		return fmt.Errorf("parse stored profile: %w", err)
	}
	resolvedProfile := parsed
	if parsed.Extends != "" {
		searchPaths := cfg.ProfileSearchPaths
		resolvedProfile, err = profile.Resolve(parsed.Extends, searchPaths)
		if err != nil {
			return fmt.Errorf("resolve profile extends: %w", err)
		}
	}

	// Step 4: Load network config (reuse init.go helper)
	repoRoot := findRepoRoot()
	region := resolvedProfile.Spec.Runtime.Region
	regionLabel := compiler.RegionLabel(region)
	networkOutputs, err := LoadNetworkOutputs(repoRoot, regionLabel)
	if err != nil {
		return fmt.Errorf("load network config: %w", err)
	}
	domain := cfg.Domain
	if domain == "" {
		domain = "klankermaker.ai"
	}
	network := &compiler.NetworkConfig{
		VPCID:             networkOutputs.VPCID,
		PublicSubnets:     networkOutputs.PublicSubnets,
		AvailabilityZones: networkOutputs.AvailabilityZones,
		RegionLabel:       regionLabel,
		EmailDomain:       "sandboxes." + domain,
	}

	// Step 5: Compile with existing sandboxID — never generate a new one.
	// Reusing the existing ID ensures Terraform state maps to the existing ECS cluster.
	artifacts, err := compiler.Compile(resolvedProfile, sandboxID, false, network)
	if err != nil {
		return fmt.Errorf("compile profile for re-provisioning: %w", err)
	}

	// Step 6: Write artifacts and run terragrunt apply
	sandboxDir, err := terragrunt.CreateSandboxDir(repoRoot, regionLabel, sandboxID)
	if err != nil {
		return fmt.Errorf("create sandbox dir: %w", err)
	}
	if err := terragrunt.PopulateSandboxDir(sandboxDir, artifacts.ServiceHCL, artifacts.UserData); err != nil {
		return fmt.Errorf("populate sandbox dir: %w", err)
	}
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	return runner.Apply(ctx, sandboxDir)
}

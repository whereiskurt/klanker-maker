package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	dynamodbpkg "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	iampkg "github.com/aws/aws-sdk-go-v2/service/iam"
	kmspkg "github.com/aws/aws-sdk-go-v2/service/kms"
	lambdapkg "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/lifecycle"
	"github.com/whereiskurt/klankrmkr/pkg/localnumber"
	profilepkg "github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// sandboxIDPattern matches valid sandbox IDs: {prefix}-{8hex}
var sandboxIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`)

// NewDestroyCmd creates the "km destroy" subcommand.
// Usage: km destroy <sandbox-id> [--aws-profile <name>] [--force]
//
// Command flow:
//  1. Validate sandbox ID format (must match sb-[a-f0-9]{8})
//  2. Load and validate AWS credentials
//  3. Discover sandbox via tag-based lookup (fail if not found)
//  4. Check substrate — if EC2 spot, explicitly terminate instance before destroy
//  5. Run terragrunt destroy (streams output in real time)
//  6. On success: clean up local sandbox directory
func NewDestroyCmd(cfg *config.Config) *cobra.Command {
	return NewDestroyCmdWithPublisher(cfg, nil)
}

// NewDestroyCmdWithPublisher builds the destroy command with an optional injected
// RemoteCommandPublisher. Pass nil to use the real AWS-backed publisher.
// Used in tests to inject a mock publisher for --remote path testing.
func NewDestroyCmdWithPublisher(cfg *config.Config, pub RemoteCommandPublisher) *cobra.Command {
	var awsProfile string
	var yes bool
	var verbose bool
	var remote bool

	cmd := &cobra.Command{
		Use:     "destroy <sandbox-id | #number>",
		Aliases: []string{"kill"},
		Short:   "Destroy a provisioned sandbox",
		Long:  helpText("destroy"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			if !yes {
				fmt.Printf("Destroy sandbox %s? This cannot be undone. [y/N] ", sandboxID)
				var answer string
				fmt.Scanln(&answer)
				if answer != "y" && answer != "Y" && answer != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}
			// Docker substrates always destroy locally — remote Lambda can't reach local containers.
			// SLCK-12 fix (Plan 63.1-02 Option A): run Slack teardown locally BEFORE dispatching
			// the remote destroy event, because the Lambda handler has no Slack code. The operator
			// workstation already holds the signing key and IAM perms, matching the local-path trust
			// model. Failures are non-fatal — Slack issues never block destroy.
			if remote {
				remoteAWSCfg, remoteAWSErr := awspkg.LoadAWSConfig(ctx, "klanker-terraform")
				if remoteAWSErr == nil {
					tableName := cfg.SandboxTableName
					if tableName == "" {
						tableName = "km-sandboxes"
					}
					dynClient := dynamodbpkg.NewFromConfig(remoteAWSCfg)
					if meta, metaErr := awspkg.ReadSandboxMetadataDynamo(ctx, dynClient, tableName, sandboxID); metaErr == nil {
						if meta.Substrate == "docker" {
							remote = false
							fmt.Printf("  [info] Docker substrate — destroying locally\n")
						} else {
							// Still remote: run Slack teardown locally before dispatch.
							runSlackTeardown(ctx, remoteAWSCfg, meta)
						}
					}
				}
			}
			if remote {
				publisher := pub
				if publisher == nil {
					publisher = newRealRemotePublisher(cfg)
				}
				return publisher.PublishSandboxCommand(ctx, sandboxID, "destroy")
			}
			if awsProfile == "" {
				awsProfile = "klanker-terraform"
			}
			return runDestroy(cfg, sandboxID, awsProfile, yes, verbose)
		},
	}

	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-terraform",
		"AWS CLI profile to use")
	cmd.Flags().BoolVar(&yes, "yes", false,
		"Skip confirmation prompt")
	cmd.Flags().BoolVar(&verbose, "verbose", false,
		"Show full terragrunt/terraform output")
	cmd.Flags().BoolVar(&remote, "remote", true,
		"Trigger destroy via Lambda (EventBridge) instead of local terragrunt (default: true, forced off for docker substrate)")

	return cmd
}

func runDestroy(cfg *config.Config, sandboxID, awsProfile string, force bool, verbose bool) error {
	ctx := context.Background()

	// Suppress structured JSON log output when not verbose.
	if !verbose {
		origLogger := log.Logger
		log.Logger = zerolog.New(io.Discard)
		defer func() { log.Logger = origLogger }()
	}

	// Step 1: Validate sandbox ID format
	if !sandboxIDPattern.MatchString(sandboxID) {
		return fmt.Errorf("invalid sandbox ID %q: must match format {prefix}-[a-f0-9]{8}", sandboxID)
	}

	// Step 2: Load AWS config and validate credentials
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed — check that profile %q is configured: %w", awsProfile, err)
	}

	// Step 2b: Check lock guard — block destroy if sandbox is locked.
	// Uses cfg.StateBucket; fail-open if bucket not configured or metadata missing.
	if err := CheckSandboxLock(ctx, cfg, sandboxID); err != nil {
		return err
	}

	// Step 2b2: Best-effort eBPF cleanup — no-op on non-Linux or proxy-mode sandboxes.
	// On Linux: removes pinned BPF programs and maps from bpffs if IsPinned is true.
	// For remote destroy (operator laptop): this is a no-op; bpffs is cleaned up
	// automatically when the EC2 instance is terminated (bpffs is an in-memory filesystem).
	cleanupEBPF(sandboxID, log.Logger)

	// Step 2c: Check metadata for docker substrate — route before tag-based lookup.
	// Docker sandboxes have no AWS-tagged EC2/ECS resources, so tag lookup would fail.
	// Primary: DynamoDB; fallback: S3 on ResourceNotFoundException.
	{
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		dynamoClientEarly := dynamodbpkg.NewFromConfig(awsCfg)
		meta, metaErr := awspkg.ReadSandboxMetadataDynamo(ctx, dynamoClientEarly, tableName, sandboxID)
		if metaErr != nil {
			var rnf *dynamodbtypes.ResourceNotFoundException
			if errors.As(metaErr, &rnf) {
				stateBucket := cfg.StateBucket
				if stateBucket == "" {
					stateBucket = os.Getenv("KM_STATE_BUCKET")
				}
				if stateBucket != "" {
					s3ClientEarly := s3.NewFromConfig(awsCfg)
					if m, s3Err := awspkg.ReadSandboxMetadata(ctx, s3ClientEarly, stateBucket, sandboxID); s3Err == nil {
						meta = m
						metaErr = nil
					}
				}
			}
		}
		if metaErr == nil && meta != nil && meta.Substrate == "docker" {
			return runDestroyDocker(ctx, cfg, awsCfg, sandboxID, verbose)
		}
		// If metadata not found or substrate is not docker, proceed with normal Terragrunt path.
	}

	// Step 3: Discover sandbox via tag-based lookup
	tagClient := resourcegroupstaggingapi.NewFromConfig(awsCfg)
	location, err := awspkg.FindSandboxByID(ctx, tagClient, sandboxID)
	if err != nil {
		if errors.Is(err, awspkg.ErrSandboxNotFound) {
			return fmt.Errorf("sandbox %s not found: no AWS resources tagged with km:sandbox-id=%s", sandboxID, sandboxID)
		}
		return fmt.Errorf("failed to discover sandbox %s: %w", sandboxID, err)
	}

	fmt.Printf("Destroying sandbox %s (%d resources)...\n", sandboxID, location.ResourceCount)

	// Step 4: Locate sandbox directory by scanning region directories
	repoRoot := findRepoRoot()
	sandboxDir, regionLabel := findSandboxDir(repoRoot, sandboxID)

	if sandboxDir == "" {
		// Not found locally — determine region from AWS resource tags and recreate
		regionLabel = determineRegionFromTags(location)
		if regionLabel == "" {
			regionLabel = "use1" // fallback to default
		}
		log.Debug().Str("regionLabel", regionLabel).Msg("sandbox directory not found locally — recreating from template")
		var createErr error
		sandboxDir, createErr = terragrunt.CreateSandboxDir(repoRoot, regionLabel, sandboxID)
		if createErr != nil {
			return fmt.Errorf("failed to recreate sandbox directory for destroy: %w", createErr)
		}
		// Write minimal service.hcl with all fields needed by terragrunt.hcl for destroy.
		// substrate_module and module_inputs are required by the sandbox terragrunt.hcl template.
		minimalHCL := fmt.Sprintf(`# Minimal service.hcl for state resolution during destroy
locals {
  sandbox_id       = %q
  region_label     = %q
  region_full      = ""
  substrate_module = "ec2spot"
  module_inputs    = {}
}
`, sandboxID, regionLabel)
		if populateErr := terragrunt.PopulateSandboxDir(sandboxDir, minimalHCL, ""); populateErr != nil {
			_ = terragrunt.CleanupSandboxDir(sandboxDir)
			return fmt.Errorf("failed to populate sandbox directory for destroy: %w", populateErr)
		}
	}
	_ = regionLabel // used above for directory creation

	// Step 5: For EC2 substrate, explicitly terminate spot instance before destroy.
	// Critical: aws_spot_instance_request destroy cancels the spot REQUEST but leaves
	// the actual EC2 instance running. Explicit termination is required (Pitfall 1).
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = verbose
	outputs, outputErr := runner.Output(ctx, sandboxDir)
	if outputErr == nil {
		// Try to get spot instance ID — if present, this is an EC2 spot sandbox
		instanceID, spotErr := awspkg.GetSpotInstanceID(outputs)
		if spotErr == nil && instanceID != "" {
			log.Debug().Str("instanceID", instanceID).Msg("terminating EC2 spot instance before destroy")
			fmt.Printf("Terminating EC2 spot instance %s...\n", instanceID)
			ec2Client := ec2.NewFromConfig(awsCfg)
			if err := awspkg.TerminateSpotInstance(ctx, ec2Client, instanceID); err != nil {
				// Log warning but don't fail — the destroy may still succeed
				log.Warn().Err(err).Str("instanceID", instanceID).
					Msg("failed to terminate spot instance; proceeding with destroy anyway")
			}
		}
		// If spot_instance_id not found: ECS or on-demand EC2 — skip termination
	} else {
		log.Debug().Err(outputErr).Msg("could not get terragrunt output before destroy — skipping spot termination")
	}

	// Step 6: Cancel EventBridge schedules (TTL, budget) so they don't fire after resources are gone.
	// Idempotent — DeleteTTLSchedule ignores ResourceNotFoundException.
	// Done before terragrunt destroy so schedules are cancelled even if destroy partially fails.
	schedulerClient := scheduler.NewFromConfig(awsCfg)
	if err := awspkg.DeleteTTLSchedule(ctx, schedulerClient, sandboxID); err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("failed to delete TTL schedule (non-fatal)")
	}
	// Clean up budget schedule (km-budget-{sandbox-id}).
	budgetScheduleName := "km-budget-" + sandboxID
	if _, err := schedulerClient.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: aws.String(budgetScheduleName),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") && !strings.Contains(err.Error(), "not found") {
			log.Warn().Err(err).Str("schedule", budgetScheduleName).Msg("failed to delete budget schedule (non-fatal)")
		}
	}

	// Step 7: Attempt to load sandbox profile from S3 for artifact upload.
	// Profile is stored during km create at artifacts/{sandbox-id}/.km-profile.yaml.
	// If unavailable (missing or S3 unreachable), artifact upload is skipped with a warning.
	artifactBucket := cfg.ArtifactsBucket
	if artifactBucket == "" {
		artifactBucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	s3Client := s3.NewFromConfig(awsCfg)
	var sandboxProfile *profilepkg.SandboxProfile
	profileBytes, profileLoadErr := downloadProfileFromS3(ctx, s3Client, artifactBucket, sandboxID)
	if profileLoadErr != nil {
		log.Warn().Err(profileLoadErr).Msg("could not load sandbox profile from S3; skipping artifact upload in destroy path")
	} else {
		sandboxProfile, _ = profilepkg.Parse(profileBytes)
	}

	// Step 7b: Destroy the budget-enforcer BEFORE the main sandbox.
	// The budget-enforcer Lambda depends on sandbox resources (IAM role, instance ID),
	// so it must be destroyed first to avoid dangling references.
	// Non-fatal: proceed with main sandbox destroy even if enforcer destroy fails.
	budgetEnforcerDir := filepath.Join(sandboxDir, "budget-enforcer")
	if _, statErr := os.Stat(budgetEnforcerDir); statErr == nil {
		fmt.Printf("Destroying budget enforcer for sandbox %s...\n", sandboxID)
		if beErr := runner.Destroy(ctx, budgetEnforcerDir); beErr != nil {
			log.Warn().Err(beErr).Str("sandbox_id", sandboxID).
				Msg("budget-enforcer destroy failed (non-fatal — proceeding with main sandbox destroy)")
		} else {
			log.Info().Str("sandbox_id", sandboxID).Msg("budget enforcer destroyed")
		}
	}

	// Step 7c: Destroy github-token resources BEFORE the main sandbox.
	// Non-fatal: proceed with main sandbox destroy even if github-token cleanup fails.
	// Cleanup is idempotent — ParameterNotFound is swallowed.
	githubTokenDir := filepath.Join(sandboxDir, "github-token")
	ssmClient := ssm.NewFromConfig(awsCfg)
	if _, statErr := os.Stat(githubTokenDir); statErr == nil {
		fmt.Printf("Destroying GitHub token resources for sandbox %s...\n", sandboxID)
		if ghErr := runner.Destroy(ctx, githubTokenDir); ghErr != nil {
			log.Warn().Err(ghErr).Str("sandbox_id", sandboxID).
				Msg("github-token terragrunt destroy failed — falling back to SDK cleanup")
			cleanupGitHubTokenResources(ctx, awsCfg, sandboxID)
		} else {
			log.Info().Str("sandbox_id", sandboxID).Msg("github-token refresher Lambda destroyed")
		}
	} else {
		// No local Terragrunt dir — clean up via SDK (remote destroy, TTL handler, etc).
		fmt.Printf("Cleaning up GitHub token resources for sandbox %s (SDK fallback)...\n", sandboxID)
		cleanupGitHubTokenResources(ctx, awsCfg, sandboxID)
	}
	// Always attempt SSM cleanup (parameter may exist even if github-token dir is gone).
	githubTokenParam := fmt.Sprintf("/sandbox/%s/github-token", sandboxID)
	if _, delErr := ssmClient.DeleteParameter(ctx, &ssm.DeleteParameterInput{
		Name: aws.String(githubTokenParam),
	}); delErr != nil {
		// Swallow ParameterNotFound — idempotent for retries.
		var notFound *ssmtypes.ParameterNotFound
		if !errors.As(delErr, &notFound) {
			log.Warn().Err(delErr).Str("sandbox_id", sandboxID).
				Msg("failed to delete GitHub token SSM parameter (non-fatal)")
		}
	} else {
		log.Info().Str("sandbox_id", sandboxID).Str("param", githubTokenParam).
			Msg("GitHub token SSM parameter deleted")
	}
	fmt.Printf("GitHub token resources cleaned up for %s\n", sandboxID)

	// Step 8: Run terragrunt destroy (streams output in real time)
	// If destroy fails, check if it was a state lock error and offer to retry.
	destroyFunc := func(dCtx context.Context, sid string) error {
		// Capture stderr to detect lock errors while still streaming to terminal.
		var stderrBuf strings.Builder
		err := runner.DestroyWithStderr(dCtx, sandboxDir, &stderrBuf)
		if err != nil && strings.Contains(stderrBuf.String(), "Error acquiring the state lock") {
			shouldClear := force
			if !shouldClear {
				fmt.Printf("\nState lock detected. This is usually a stale lock from a failed Lambda or previous operation.\n")
				fmt.Printf("Clear lock and retry destroy? [y/N] ")
				var answer string
				fmt.Scanln(&answer)
				shouldClear = answer == "y" || answer == "Y" || answer == "yes"
			}
			if shouldClear {
				fmt.Printf("Retrying destroy with -lock=false...\n")
				return runner.DestroyForceUnlock(dCtx, sandboxDir)
			}
		}
		return err
	}
	uploadFunc := func(uCtx context.Context, sid string) error {
		if sandboxProfile == nil || sandboxProfile.Spec.Artifacts == nil || len(sandboxProfile.Spec.Artifacts.Paths) == 0 {
			return nil
		}
		_, _, err := awspkg.UploadArtifacts(uCtx, s3Client, artifactBucket, sid,
			sandboxProfile.Spec.Artifacts.Paths, sandboxProfile.Spec.Artifacts.MaxSizeMB)
		return err
	}
	// Step 8 (continued): build SES client for notification callback.
	// Derive email domain from config; default to "klankermaker.ai" when not set.
	destroyBaseDomain := cfg.Domain
	if destroyBaseDomain == "" {
		destroyBaseDomain = "klankermaker.ai"
	}
	emailDomain := "sandboxes." + destroyBaseDomain
	sesClient := sesv2.NewFromConfig(awsCfg)

	callbacks := lifecycle.TeardownCallbacks{
		Destroy:         destroyFunc,
		UploadArtifacts: uploadFunc,
		OnNotify: func(nCtx context.Context, sid string, event string) error {
			operatorEmail := cfg.OperatorEmail
			if operatorEmail == "" {
				operatorEmail = os.Getenv("KM_OPERATOR_EMAIL")
			}
			if operatorEmail == "" {
				return nil
			}
			return awspkg.SendLifecycleNotification(nCtx, sesClient, operatorEmail, sid, event, emailDomain)
		},
	}
	if err := lifecycle.ExecuteTeardown(ctx, "destroy", sandboxID, callbacks); err != nil {
		return fmt.Errorf("terragrunt destroy failed for sandbox %s: %w", sandboxID, err)
	}

	// Step 8a: Finalize MLflow run record (OBSV-09).
	// Non-fatal: destroy has already succeeded.
	if mlflowErr := awspkg.FinalizeMLflowRun(ctx, s3Client, artifactBucket, sandboxID, "klankrmkr",
		awspkg.MLflowMetrics{
			ExitStatus: 0,
		}); mlflowErr != nil {
		log.Warn().Err(mlflowErr).Str("sandbox_id", sandboxID).
			Msg("failed to finalize MLflow run record (non-fatal)")
	} else {
		log.Info().Str("sandbox_id", sandboxID).Msg("MLflow run finalized")
	}

	// Step 9: Clean up local sandbox directory
	if err := terragrunt.CleanupSandboxDir(sandboxDir); err != nil {
		log.Warn().Err(err).Str("sandboxDir", sandboxDir).Msg("failed to clean up local sandbox directory")
	}

	// Step 10: Clean up SES email identity (idempotent — swallows NotFoundException).
	if err := awspkg.CleanupSandboxEmail(ctx, sesClient, sandboxID, emailDomain); err != nil {
		log.Warn().Err(err).Msg("failed to cleanup sandbox email (non-fatal)")
	}

	// Step 11: Clean up sandbox identity (SSM signing key + DynamoDB identity row).
	// Non-fatal: idempotent — swallows ParameterNotFound for SSM and DeleteItem is a no-op for missing rows.
	{
		identitySSMClient := ssm.NewFromConfig(awsCfg)
		identityDynClient := dynamodbpkg.NewFromConfig(awsCfg)
		identityTableName := cfg.IdentityTableName
		if identityTableName == "" {
			identityTableName = "km-identities"
		}
		if identErr := awspkg.CleanupSandboxIdentity(ctx, identitySSMClient, identityDynClient, identityTableName, sandboxID); identErr != nil {
			log.Warn().Err(identErr).Str("sandbox_id", sandboxID).
				Msg("failed to cleanup sandbox identity (non-fatal)")
		} else {
			log.Info().Str("sandbox_id", sandboxID).Msg("sandbox identity cleaned up")
		}
	}

	// Step 12: Delete sandbox metadata from DynamoDB (with S3 fallback) so km list no longer shows it.
	{
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		dynamoClientDel := dynamodbpkg.NewFromConfig(awsCfg)
		// Read existing metadata to check for alias AND for Slack teardown — non-fatal if read fails.
		if existingMeta, readErr := awspkg.ReadSandboxMetadataDynamo(ctx, dynamoClientDel, tableName, sandboxID); readErr == nil {
			if existingMeta.Alias != "" {
				fmt.Printf("Alias freed: %s\n", existingMeta.Alias)
			}
			// Phase 67-07: drain Slack-inbound queue and thread rows BEFORE the
			// Phase 63 final-post + archive so the final "destroyed" message lands
			// while the channel still exists.
			// Non-fatal: each step is best-effort; destroy has already succeeded.
			if existingMeta.SlackInboundQueueURL != "" {
				sqsRegionForDrain := existingMeta.Region
				if sqsRegionForDrain == "" {
					sqsRegionForDrain = awsCfg.Region
				}
				sqsClientForDrain, sqsClientErr := awspkg.NewSQSClient(ctx, sqsRegionForDrain)
				if sqsClientErr != nil {
					log.Warn().Err(sqsClientErr).Str("sandbox_id", sandboxID).
						Msg("drain: failed to init SQS client (non-fatal)")
				} else {
					ssmClientForDrain := ssm.NewFromConfig(awsCfg)
					drainDeps := destroyInboundDeps{
						SandboxID:             sandboxID,
						QueueURL:              existingMeta.SlackInboundQueueURL,
						ChannelID:             existingMeta.SlackChannelID,
						SQS:                   sqsClientForDrain,
						DDB:                   dynamoClientDel,
						SlackThreadsTableName: cfg.GetSlackThreadsTableName(),
						StopPoller:            makeStopPoller(ssmClientForDrain),
						WaitForAgentRunIdle:   makeWaitForAgentRunIdle(ssmClientForDrain),
					}
					drainSlackInbound(ctx, drainDeps)
				}
			}
			// Phase 63 — per-sandbox Slack archive flow (local destroy path).
			// runSlackTeardown is the shared helper; the remote path calls it before dispatch.
			// Non-fatal: destroy has already succeeded; Slack failures are WARN-logged.
			runSlackTeardown(ctx, awsCfg, existingMeta)
		} else {
			// S3 fallback for alias read.
			stateBucketForAlias := cfg.StateBucket
			if stateBucketForAlias == "" {
				stateBucketForAlias = os.Getenv("KM_STATE_BUCKET")
			}
			if stateBucketForAlias != "" {
				if existingMeta, s3ReadErr := awspkg.ReadSandboxMetadata(ctx, s3Client, stateBucketForAlias, sandboxID); s3ReadErr == nil {
					if existingMeta.Alias != "" {
						fmt.Printf("Alias freed: %s\n", existingMeta.Alias)
					}
				}
			}
		}
		// Delete from DynamoDB first, then S3 (both non-fatal).
		if delErr := awspkg.DeleteSandboxMetadataDynamo(ctx, dynamoClientDel, tableName, sandboxID); delErr != nil {
			var rnf *dynamodbtypes.ResourceNotFoundException
			if !errors.As(delErr, &rnf) {
				log.Warn().Err(delErr).Str("sandbox_id", sandboxID).
					Msg("failed to delete sandbox metadata from DynamoDB (non-fatal)")
			}
		} else {
			log.Info().Str("sandbox_id", sandboxID).Msg("sandbox metadata deleted from DynamoDB")
		}
		// Also delete from S3 (idempotent — metadata may exist in both during migration).
		stateBucketForDel := cfg.StateBucket
		if stateBucketForDel == "" {
			stateBucketForDel = os.Getenv("KM_STATE_BUCKET")
		}
		if stateBucketForDel != "" {
			if delErr := awspkg.DeleteSandboxMetadata(ctx, s3Client, stateBucketForDel, sandboxID); delErr != nil {
				log.Debug().Err(delErr).Str("sandbox_id", sandboxID).
					Msg("S3 metadata delete (non-fatal, may not exist)")
			}
		}
	}

	// Step 13: Export CloudWatch logs to S3 then delete the log group.
	// Export is fire-and-forget (async in AWS) and non-fatal — deletion proceeds regardless.
	cwClient := cloudwatchlogs.NewFromConfig(awsCfg)
	if artifactBucket != "" {
		if exportErr := awspkg.ExportSandboxLogs(ctx, cwClient, sandboxID, artifactBucket); exportErr != nil {
			log.Warn().Err(exportErr).Str("sandbox_id", sandboxID).
				Msg("failed to export sandbox logs to S3 (non-fatal)")
		} else {
			log.Info().Str("sandbox_id", sandboxID).Str("bucket", artifactBucket).
				Msg("sandbox logs export task initiated")
		}
	}
	if cwErr := awspkg.DeleteSandboxLogGroup(ctx, cwClient, sandboxID); cwErr != nil {
		log.Warn().Err(cwErr).Str("sandbox_id", sandboxID).
			Msg("failed to delete sandbox log group (non-fatal)")
	} else {
		log.Info().Str("sandbox_id", sandboxID).Msg("sandbox log group deleted")
	}

	if lnState, lnErr := localnumber.Load(); lnErr == nil {
		localnumber.Remove(lnState, sandboxID)
		_ = localnumber.Save(lnState)
	}

	fmt.Printf("Sandbox %s destroyed successfully.\n", sandboxID)

	return nil
}

// runDestroyDocker handles the full docker destroy workflow.
// It tears down a local Docker Compose sandbox without any Terragrunt involvement.
// Steps: check lock, docker compose down -v, delete IAM roles, delete S3 metadata,
// delete SSM GitHub token parameter, remove local directory.
// Each step is independent and logs warnings instead of failing hard (idempotent cleanup).
func runDestroyDocker(ctx context.Context, cfg *config.Config, awsCfg aws.Config, sandboxID string, verbose bool) error {
	// Verify this Docker sandbox is running on the local host.
	// Docker containers are local — you can see other operators' Docker sandboxes in km list
	// (shared DynamoDB) but can only destroy ones running on your machine.
	homeDir, _ := os.UserHomeDir()
	composeFile := filepath.Join(homeDir, ".km", "sandboxes", sandboxID, "docker-compose.yml")
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		return fmt.Errorf("docker sandbox %s is not running on this host (no %s found).\n"+
			"  This sandbox may be running on another machine. Use km list to check.", sandboxID, composeFile)
	}

	fmt.Printf("Destroying docker sandbox %s...\n", sandboxID)

	region := awsCfg.Region
	if region == "" {
		region = "us-east-1"
	}

	// Step DD1: Run docker compose down -v (stops and removes containers, networks, volumes).
	composeErr := runDockerCompose(ctx, sandboxID, "down", "-v")
	if composeErr != nil {
		// Warn but continue — containers may already be gone.
		log.Warn().Err(composeErr).Str("sandbox_id", sandboxID).Msg("docker compose down failed (non-fatal)")
		fmt.Printf("  Warning: docker compose down: %v\n", composeErr)
	} else {
		fmt.Printf("  ✓ docker compose down -v completed\n")
	}

	// Step DD2: Delete IAM roles via SDK.
	iamClient := iampkg.NewFromConfig(awsCfg)
	sandboxRoleName := fmt.Sprintf("km-docker-%s-%s", sandboxID, region)
	sidecarRoleName := fmt.Sprintf("km-sidecar-%s-%s", sandboxID, region)

	for _, roleName := range []string{sandboxRoleName, sidecarRoleName} {
		// List and delete inline policies first (required before role deletion).
		listOut, listErr := iamClient.ListRolePolicies(ctx, &iampkg.ListRolePoliciesInput{
			RoleName: aws.String(roleName),
		})
		if listErr == nil {
			for _, policyName := range listOut.PolicyNames {
				_, delPolicyErr := iamClient.DeleteRolePolicy(ctx, &iampkg.DeleteRolePolicyInput{
					RoleName:   aws.String(roleName),
					PolicyName: aws.String(policyName),
				})
				if delPolicyErr != nil {
					log.Warn().Err(delPolicyErr).Str("role", roleName).Str("policy", policyName).
						Msg("failed to delete inline policy from role (non-fatal)")
				}
			}
		}
		// Delete the role (ignore NoSuchEntity).
		_, delRoleErr := iamClient.DeleteRole(ctx, &iampkg.DeleteRoleInput{
			RoleName: aws.String(roleName),
		})
		if delRoleErr != nil {
			// Swallow NoSuchEntityException — idempotent cleanup.
			errMsg := delRoleErr.Error()
			if strings.Contains(errMsg, "NoSuchEntity") || strings.Contains(errMsg, "not found") {
				log.Debug().Str("role", roleName).Msg("IAM role already deleted")
			} else {
				log.Warn().Err(delRoleErr).Str("role", roleName).Msg("failed to delete IAM role (non-fatal)")
			}
		} else {
			fmt.Printf("  ✓ IAM role deleted: %s\n", roleName)
		}
	}

	// Step DD3: Delete SSM GitHub token parameter.
	ssmClient := ssm.NewFromConfig(awsCfg)
	githubTokenParam := fmt.Sprintf("/sandbox/%s/github-token", sandboxID)
	if _, delErr := ssmClient.DeleteParameter(ctx, &ssm.DeleteParameterInput{
		Name: aws.String(githubTokenParam),
	}); delErr != nil {
		var notFound *ssmtypes.ParameterNotFound
		if !errors.As(delErr, &notFound) && !strings.Contains(delErr.Error(), "ParameterNotFound") {
			log.Warn().Err(delErr).Str("sandbox_id", sandboxID).
				Msg("failed to delete GitHub token SSM parameter (non-fatal)")
		}
	} else {
		log.Info().Str("param", githubTokenParam).Msg("GitHub token SSM parameter deleted")
	}

	// Step DD3b: Clean up github-token resources (KMS key, Lambda, EventBridge schedule,
	// IAM roles, CloudWatch log group). These are created by the github-token Terraform
	// module but the Docker destroy path doesn't run Terragrunt, so SDK cleanup is needed.
	cleanupGitHubTokenResources(ctx, awsCfg, sandboxID)

	// Step DD3c: Cancel EventBridge schedules (TTL, budget) so they don't fire after resources are gone.
	schedulerClient := scheduler.NewFromConfig(awsCfg)
	if err := awspkg.DeleteTTLSchedule(ctx, schedulerClient, sandboxID); err != nil {
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("failed to delete TTL schedule (non-fatal)")
	}
	budgetScheduleName := "km-budget-" + sandboxID
	if _, err := schedulerClient.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: aws.String(budgetScheduleName),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") && !strings.Contains(err.Error(), "not found") {
			log.Warn().Err(err).Str("schedule", budgetScheduleName).Msg("failed to delete budget schedule (non-fatal)")
		}
	}

	// Step DD3d: Clean up budget-enforcer resources (Lambda, IAM roles, log group).
	cleanupBudgetEnforcerResources(ctx, awsCfg, sandboxID)

	// Step DD3e: Clean up SES email identity.
	destroyBaseDomain := cfg.Domain
	if destroyBaseDomain == "" {
		destroyBaseDomain = "klankermaker.ai"
	}
	emailDomain := "sandboxes." + destroyBaseDomain
	sesClient := sesv2.NewFromConfig(awsCfg)
	if err := awspkg.CleanupSandboxEmail(ctx, sesClient, sandboxID, emailDomain); err != nil {
		log.Warn().Err(err).Msg("failed to cleanup sandbox email (non-fatal)")
	}

	// Step DD3f: Clean up sandbox identity (SSM signing key + DynamoDB identity row).
	{
		identitySSMClient := ssm.NewFromConfig(awsCfg)
		identityDynClient := dynamodbpkg.NewFromConfig(awsCfg)
		identityTableName := cfg.IdentityTableName
		if identityTableName == "" {
			identityTableName = "km-identities"
		}
		if identErr := awspkg.CleanupSandboxIdentity(ctx, identitySSMClient, identityDynClient, identityTableName, sandboxID); identErr != nil {
			log.Warn().Err(identErr).Str("sandbox_id", sandboxID).
				Msg("failed to cleanup sandbox identity (non-fatal)")
		}
	}

	// Step DD4: Delete sandbox metadata from DynamoDB (and S3 fallback).
	{
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		dynamoClientDD := dynamodbpkg.NewFromConfig(awsCfg)
		if delErr := awspkg.DeleteSandboxMetadataDynamo(ctx, dynamoClientDD, tableName, sandboxID); delErr != nil {
			var rnf *dynamodbtypes.ResourceNotFoundException
			if !errors.As(delErr, &rnf) {
				log.Warn().Err(delErr).Str("sandbox_id", sandboxID).
					Msg("failed to delete sandbox metadata from DynamoDB (non-fatal)")
			}
		} else {
			fmt.Printf("  ✓ DynamoDB metadata deleted\n")
		}
		// Also clean up any S3 metadata (idempotent).
		stateBucket := cfg.StateBucket
		if stateBucket == "" {
			stateBucket = os.Getenv("KM_STATE_BUCKET")
		}
		if stateBucket != "" {
			s3Client := s3.NewFromConfig(awsCfg)
			if delErr := awspkg.DeleteSandboxMetadata(ctx, s3Client, stateBucket, sandboxID); delErr != nil {
				log.Warn().Err(delErr).Str("sandbox_id", sandboxID).
					Msg("failed to delete sandbox metadata from S3 (non-fatal)")
			}
		}
	}

	// Step DD5: Remove local sandbox directory.
	homeDir, _ = os.UserHomeDir()
	sandboxLocalDir := filepath.Join(homeDir, ".km", "sandboxes", sandboxID)
	if err := os.RemoveAll(sandboxLocalDir); err != nil {
		log.Warn().Err(err).Str("dir", sandboxLocalDir).Msg("failed to remove local sandbox directory (non-fatal)")
	} else {
		fmt.Printf("  ✓ Local directory removed: %s\n", sandboxLocalDir)
	}

	if lnState, lnErr := localnumber.Load(); lnErr == nil {
		localnumber.Remove(lnState, sandboxID)
		_ = localnumber.Save(lnState)
	}

	fmt.Printf("Sandbox %s destroyed successfully.\n", sandboxID)
	return nil
}

// cleanupGitHubTokenResources removes all resources created by the github-token
// Terraform module using SDK calls. This is the fallback for when Terragrunt
// destroy isn't available (Docker sandboxes, remote destroy, TTL handler).
// Each step is idempotent and non-fatal.
func cleanupGitHubTokenResources(ctx context.Context, awsCfg aws.Config, sandboxID string) {
	iamClient := iampkg.NewFromConfig(awsCfg)
	kmsClient := kmspkg.NewFromConfig(awsCfg)
	lambdaClient := lambdapkg.NewFromConfig(awsCfg)
	cwClient := cloudwatchlogs.NewFromConfig(awsCfg)
	schedClient := scheduler.NewFromConfig(awsCfg)

	// 1. Delete EventBridge schedule.
	scheduleName := "km-github-token-" + sandboxID
	if _, err := schedClient.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: aws.String(scheduleName),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") && !strings.Contains(err.Error(), "not found") {
			log.Warn().Err(err).Str("schedule", scheduleName).Msg("failed to delete github-token schedule (non-fatal)")
		}
	} else {
		fmt.Printf("  ✓ EventBridge schedule deleted: %s\n", scheduleName)
	}

	// 2. Delete Lambda function.
	lambdaName := "km-github-token-refresher-" + sandboxID
	if _, err := lambdaClient.DeleteFunction(ctx, &lambdapkg.DeleteFunctionInput{
		FunctionName: aws.String(lambdaName),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") && !strings.Contains(err.Error(), "not found") {
			log.Warn().Err(err).Str("function", lambdaName).Msg("failed to delete github-token Lambda (non-fatal)")
		}
	} else {
		fmt.Printf("  ✓ Lambda deleted: %s\n", lambdaName)
	}

	// 3. Delete CloudWatch log group.
	logGroupName := "/aws/lambda/km-github-token-refresher-" + sandboxID
	if _, err := cwClient.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
		LogGroupName: aws.String(logGroupName),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") && !strings.Contains(err.Error(), "not found") {
			log.Warn().Err(err).Str("log_group", logGroupName).Msg("failed to delete github-token log group (non-fatal)")
		}
	}

	// 4. Delete IAM roles (refresher + scheduler).
	for _, roleName := range []string{
		"km-github-token-refresher-" + sandboxID,
		"km-github-token-scheduler-" + sandboxID,
	} {
		// Delete inline policies.
		listOut, _ := iamClient.ListRolePolicies(ctx, &iampkg.ListRolePoliciesInput{
			RoleName: aws.String(roleName),
		})
		if listOut != nil {
			for _, policyName := range listOut.PolicyNames {
				iamClient.DeleteRolePolicy(ctx, &iampkg.DeleteRolePolicyInput{
					RoleName:   aws.String(roleName),
					PolicyName: aws.String(policyName),
				})
			}
		}
		if _, err := iamClient.DeleteRole(ctx, &iampkg.DeleteRoleInput{
			RoleName: aws.String(roleName),
		}); err != nil {
			if !strings.Contains(err.Error(), "NoSuchEntity") && !strings.Contains(err.Error(), "not found") {
				log.Warn().Err(err).Str("role", roleName).Msg("failed to delete github-token IAM role (non-fatal)")
			}
		} else {
			fmt.Printf("  ✓ IAM role deleted: %s\n", roleName)
		}
	}

	// 5. Schedule KMS key deletion and remove alias.
	kmsAlias := "alias/km-github-token-" + sandboxID
	descOut, err := kmsClient.DescribeKey(ctx, &kmspkg.DescribeKeyInput{
		KeyId: aws.String(kmsAlias),
	})
	if err == nil && descOut.KeyMetadata != nil {
		keyID := aws.ToString(descOut.KeyMetadata.KeyId)
		if _, schedErr := kmsClient.ScheduleKeyDeletion(ctx, &kmspkg.ScheduleKeyDeletionInput{
			KeyId:               aws.String(keyID),
			PendingWindowInDays: aws.Int32(7),
		}); schedErr != nil {
			if !strings.Contains(schedErr.Error(), "pending deletion") {
				log.Warn().Err(schedErr).Str("key", kmsAlias).Msg("failed to schedule KMS key deletion (non-fatal)")
			}
		} else {
			fmt.Printf("  ✓ KMS key scheduled for deletion: %s\n", kmsAlias)
		}
		kmsClient.DeleteAlias(ctx, &kmspkg.DeleteAliasInput{
			AliasName: aws.String(kmsAlias),
		})
	}
}

// cleanupBudgetEnforcerResources removes budget-enforcer resources for a sandbox
// via SDK calls: Lambda function, IAM roles, CloudWatch log group.
// The EventBridge schedule is handled separately (caller deletes it directly).
// All errors are non-fatal (logged as warnings) — idempotent cleanup.
func cleanupBudgetEnforcerResources(ctx context.Context, awsCfg aws.Config, sandboxID string) {
	// Delete budget-enforcer Lambda.
	lambdaClient := lambdapkg.NewFromConfig(awsCfg)
	fnName := "km-budget-enforcer-" + sandboxID
	if _, err := lambdaClient.DeleteFunction(ctx, &lambdapkg.DeleteFunctionInput{
		FunctionName: aws.String(fnName),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") && !strings.Contains(err.Error(), "not found") {
			log.Warn().Err(err).Str("function", fnName).Msg("failed to delete budget-enforcer Lambda (non-fatal)")
		}
	} else {
		fmt.Printf("  ✓ Budget-enforcer Lambda deleted: %s\n", fnName)
	}

	// Delete budget-enforcer IAM roles.
	iamClient := iampkg.NewFromConfig(awsCfg)
	for _, roleName := range []string{
		"km-budget-enforcer-" + sandboxID,
		"km-budget-scheduler-" + sandboxID,
	} {
		listOut, _ := iamClient.ListRolePolicies(ctx, &iampkg.ListRolePoliciesInput{
			RoleName: aws.String(roleName),
		})
		if listOut != nil {
			for _, policyName := range listOut.PolicyNames {
				iamClient.DeleteRolePolicy(ctx, &iampkg.DeleteRolePolicyInput{
					RoleName:   aws.String(roleName),
					PolicyName: aws.String(policyName),
				})
			}
		}
		if _, err := iamClient.DeleteRole(ctx, &iampkg.DeleteRoleInput{
			RoleName: aws.String(roleName),
		}); err != nil {
			if !strings.Contains(err.Error(), "NoSuchEntity") && !strings.Contains(err.Error(), "not found") {
				log.Warn().Err(err).Str("role", roleName).Msg("failed to delete budget-enforcer IAM role (non-fatal)")
			}
		}
	}

	// Delete budget-enforcer CloudWatch log group.
	cwClient := cloudwatchlogs.NewFromConfig(awsCfg)
	logGroup := "/aws/lambda/km-budget-enforcer-" + sandboxID
	if _, err := cwClient.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
		LogGroupName: aws.String(logGroup),
	}); err != nil {
		if !strings.Contains(err.Error(), "ResourceNotFoundException") && !strings.Contains(err.Error(), "not found") {
			log.Warn().Err(err).Str("log_group", logGroup).Msg("failed to delete budget-enforcer log group (non-fatal)")
		}
	}
}

// downloadProfileFromS3 retrieves the sandbox profile YAML stored at
// artifacts/{sandboxID}/.km-profile.yaml in the given S3 bucket.
func downloadProfileFromS3(ctx context.Context, client *s3.Client, bucket, sandboxID string) ([]byte, error) {
	key := "artifacts/" + sandboxID + "/.km-profile.yaml"
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get profile from S3 s3://%s/%s: %w", bucket, key, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// findSandboxDir scans region directories for the sandbox ID.
// Returns (sandboxDir, regionLabel) or ("", "") if not found.
func findSandboxDir(repoRoot, sandboxID string) (string, string) {
	liveDir := filepath.Join(repoRoot, "infra", "live")
	entries, err := os.ReadDir(liveDir)
	if err != nil {
		return "", ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(liveDir, entry.Name(), "sandboxes", sandboxID)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, entry.Name()
		}
	}
	return "", ""
}

// determineRegionFromTags extracts the region label from sandbox discovery results.
func determineRegionFromTags(location *awspkg.SandboxLocation) string {
	if location == nil || len(location.ResourceARNs) == 0 {
		return ""
	}
	// Parse region from first ARN: arn:aws:<service>:<region>:<account>:...
	arn := location.ResourceARNs[0]
	parts := splitARN(arn)
	if len(parts) >= 4 {
		return compiler.RegionLabel(parts[3])
	}
	return ""
}

func splitARN(arn string) []string {
	// Simple ARN split: arn:partition:service:region:account:resource
	result := []string{}
	current := ""
	for _, c := range arn {
		if c == ':' {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	result = append(result, current)
	return result
}

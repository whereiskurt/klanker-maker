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
	dynamodbpkg "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
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
		Use:   "destroy <sandbox-id | #number>",
		Short: "Destroy a provisioned sandbox",
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
	cmd.Flags().BoolVar(&remote, "remote", false,
		"Trigger destroy via Lambda (EventBridge) instead of local terragrunt")

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

	// Step 6: Cancel the EventBridge TTL schedule so it does not fire after resources are gone.
	// Idempotent — DeleteTTLSchedule ignores ResourceNotFoundException.
	// Done before terragrunt destroy so the schedule is cancelled even if destroy partially fails.
	schedulerClient := scheduler.NewFromConfig(awsCfg)
	if err := awspkg.DeleteTTLSchedule(ctx, schedulerClient, sandboxID); err != nil {
		// Log warning but do not fail destroy — the schedule will fire but find no resources.
		log.Warn().Err(err).Str("sandbox_id", sandboxID).Msg("failed to delete TTL schedule (non-fatal)")
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
				Msg("github-token destroy failed (non-fatal — proceeding with main sandbox destroy)")
		} else {
			log.Info().Str("sandbox_id", sandboxID).Msg("github-token refresher Lambda destroyed")
		}
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

	// Step 12: Delete sandbox metadata.json from S3 so km list no longer shows it.
	stateBucket := cfg.StateBucket
	if stateBucket == "" {
		stateBucket = os.Getenv("KM_STATE_BUCKET")
	}
	if stateBucket != "" {
		if delErr := awspkg.DeleteSandboxMetadata(ctx, s3Client, stateBucket, sandboxID); delErr != nil {
			log.Warn().Err(delErr).Str("sandbox_id", sandboxID).
				Msg("failed to delete sandbox metadata from S3 (non-fatal)")
		} else {
			log.Info().Str("sandbox_id", sandboxID).Msg("sandbox metadata deleted from S3")
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

	fmt.Printf("Sandbox %s destroyed successfully.\n", sandboxID)

	return nil
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

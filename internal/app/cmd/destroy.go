package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/lifecycle"
	profilepkg "github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// sandboxIDPattern matches valid sandbox IDs: sb-[a-f0-9]{8}
var sandboxIDPattern = regexp.MustCompile(`^sb-[a-f0-9]{8}$`)

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
	var awsProfile string
	var force bool

	cmd := &cobra.Command{
		Use:   "destroy <sandbox-id>",
		Short: "Destroy a provisioned sandbox",
		Long: `Destroy discovers the sandbox by its tag in AWS and tears it down.

For EC2 spot sandboxes, the instance is explicitly terminated before Terraform
destroy runs. This is critical: 'aws_spot_instance_request' destroy cancels the
spot request but leaves the instance running — explicit termination is required.

After a successful destroy, the local sandbox directory is removed.

Exit code 0 — sandbox destroyed successfully
Exit code 1 — sandbox not found, destruction failed, or cleanup failed`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" {
				awsProfile = "klanker-terraform"
			}
			return runDestroy(cfg, args[0], awsProfile, force)
		},
	}

	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-terraform",
		"AWS CLI profile to use")
	cmd.Flags().BoolVar(&force, "force", false,
		"Skip confirmation prompt (for future use; currently always proceeds)")

	return cmd
}

// runDestroy executes the full destroy workflow.
func runDestroy(cfg *config.Config, sandboxID, awsProfile string, force bool) error {
	ctx := context.Background()

	// Step 1: Validate sandbox ID format
	if !sandboxIDPattern.MatchString(sandboxID) {
		return fmt.Errorf("invalid sandbox ID %q: must match format sb-[a-f0-9]{8}", sandboxID)
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
		// Write minimal service.hcl with sandbox_id + region for state key resolution
		minimalHCL := fmt.Sprintf("# Minimal service.hcl for state resolution during destroy\n"+
			"locals {\n  sandbox_id = %q\n  region_label = %q\n  region_full = \"\"\n}\n", sandboxID, regionLabel)
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
	artifactBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if artifactBucket == "" {
		artifactBucket = "km-sandbox-artifacts-ea554771"
	}
	s3Client := s3.NewFromConfig(awsCfg)
	var sandboxProfile *profilepkg.SandboxProfile
	profileBytes, profileLoadErr := downloadProfileFromS3(ctx, s3Client, artifactBucket, sandboxID)
	if profileLoadErr != nil {
		log.Warn().Err(profileLoadErr).Msg("could not load sandbox profile from S3; skipping artifact upload in destroy path")
	} else {
		sandboxProfile, _ = profilepkg.Parse(profileBytes)
	}

	// Step 8: Run terragrunt destroy (streams output in real time)
	destroyFunc := func(dCtx context.Context, sid string) error {
		return runner.Destroy(dCtx, sandboxDir)
	}
	uploadFunc := func(uCtx context.Context, sid string) error {
		if sandboxProfile == nil || sandboxProfile.Spec.Artifacts == nil || len(sandboxProfile.Spec.Artifacts.Paths) == 0 {
			return nil
		}
		_, _, err := awspkg.UploadArtifacts(uCtx, s3Client, artifactBucket, sid,
			sandboxProfile.Spec.Artifacts.Paths, sandboxProfile.Spec.Artifacts.MaxSizeMB)
		return err
	}
	callbacks := lifecycle.TeardownCallbacks{
		Destroy:         destroyFunc,
		UploadArtifacts: uploadFunc,
	}
	if err := lifecycle.ExecuteTeardown(ctx, "destroy", sandboxID, callbacks); err != nil {
		return fmt.Errorf("terragrunt destroy failed for sandbox %s: %w", sandboxID, err)
	}

	// Step 9: Clean up local sandbox directory
	if err := terragrunt.CleanupSandboxDir(sandboxDir); err != nil {
		log.Warn().Err(err).Str("sandboxDir", sandboxDir).Msg("failed to clean up local sandbox directory")
	}

	// Step 10: Clean up SES email identity (idempotent — swallows NotFoundException).
	const emailDomain = "sandboxes.klankermaker.ai"
	sesClient := sesv2.NewFromConfig(awsCfg)
	if err := awspkg.CleanupSandboxEmail(ctx, sesClient, sandboxID, emailDomain); err != nil {
		log.Warn().Err(err).Msg("failed to cleanup sandbox email (non-fatal)")
	}

	fmt.Printf("Sandbox %s destroyed successfully.\n", sandboxID)

	// Step 11: Send lifecycle notification if operator email is configured.
	if operatorEmail := os.Getenv("KM_OPERATOR_EMAIL"); operatorEmail != "" {
		if err := awspkg.SendLifecycleNotification(ctx, sesClient, operatorEmail, sandboxID, "destroyed", emailDomain); err != nil {
			log.Warn().Err(err).Msg("failed to send destroyed lifecycle notification (non-fatal)")
		}
	}

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

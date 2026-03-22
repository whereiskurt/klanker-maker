package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
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

	// Step 4: Locate (or reconstruct) sandbox directory
	repoRoot := findRepoRoot()
	sandboxDir := filepath.Join(repoRoot, "infra", "live", "sandboxes", sandboxID)

	dirExists := false
	if _, err := os.Stat(sandboxDir); err == nil {
		dirExists = true
	}

	if !dirExists {
		// Sandbox directory doesn't exist locally — re-create from template.
		// We need the terragrunt.hcl for state key resolution during destroy.
		log.Debug().Str("sandboxDir", sandboxDir).Msg("sandbox directory not found locally — recreating from template")
		sandboxDir, err = terragrunt.CreateSandboxDir(repoRoot, sandboxID)
		if err != nil {
			return fmt.Errorf("failed to recreate sandbox directory for destroy: %w", err)
		}
		// Write minimal service.hcl with only the sandbox_id so Terragrunt
		// can resolve the state key without the full profile artifacts.
		minimalHCL := fmt.Sprintf("# Minimal service.hcl for state resolution during destroy\n"+
			"locals {\n  sandbox_id = %q\n}\n", sandboxID)
		if err := terragrunt.PopulateSandboxDir(sandboxDir, minimalHCL, ""); err != nil {
			_ = terragrunt.CleanupSandboxDir(sandboxDir)
			return fmt.Errorf("failed to populate sandbox directory for destroy: %w", err)
		}
	}

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

	// Step 6: Run terragrunt destroy (streams output in real time)
	if err := runner.Destroy(ctx, sandboxDir); err != nil {
		return fmt.Errorf("terragrunt destroy failed for sandbox %s: %w", sandboxID, err)
	}

	// Step 7: Clean up local sandbox directory
	if err := terragrunt.CleanupSandboxDir(sandboxDir); err != nil {
		log.Warn().Err(err).Str("sandboxDir", sandboxDir).Msg("failed to clean up local sandbox directory")
	}

	fmt.Printf("Sandbox %s destroyed successfully.\n", sandboxID)
	return nil
}

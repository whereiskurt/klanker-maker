package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// UninitRunner is a narrow interface for the Destroy operation, allowing test injection.
type UninitRunner interface {
	Destroy(ctx context.Context, dir string) error
}

// NewUninitCmd creates the "km uninit" subcommand.
// Usage: km uninit [--region <region>] [--aws-profile <name>] [--force]
//
// Command flow:
//  1. Validate AWS credentials
//  2. Check for active sandboxes in the region (requires StateBucket; error if not set unless --force)
//  3. If active sandboxes exist and --force is not set: return error
//  4. Destroy all regional modules in reverse dependency order
func NewUninitCmd(cfg *config.Config) *cobra.Command {
	var awsProfile string
	var region string
	var force bool
	var yes bool
	var verbose bool

	cmd := &cobra.Command{
		Use:   "uninit",
		Short: "Tear down all shared regional infrastructure for a region",
		Long:  helpText("uninit"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				fmt.Printf("Destroy ALL shared infrastructure in %s? This cannot be undone. [y/N] ", region)
				var answer string
				fmt.Scanln(&answer)
				if answer != "y" && answer != "Y" && answer != "yes" {
					fmt.Println("Aborted.")
					return nil
				}
			}
			if awsProfile == "" {
				awsProfile = "klanker-application"
			}
			return runUninit(cfg, awsProfile, region, force, verbose)
		},
	}

	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-application",
		"AWS CLI profile to use for teardown")
	cmd.Flags().StringVar(&region, "region", "us-east-1",
		"AWS region to uninitialize (e.g. us-east-1, ca-central-1)")
	cmd.Flags().BoolVar(&force, "force", false,
		"Destroy even if active sandboxes exist in the region")
	cmd.Flags().BoolVar(&yes, "yes", false,
		"Skip confirmation prompt")
	cmd.Flags().BoolVar(&verbose, "verbose", false,
		"Show full terragrunt/terraform output")

	return cmd
}

// runUninit is the top-level uninit logic (uses real AWS clients).
func runUninit(cfg *config.Config, awsProfile, region string, force bool, verbose bool) error {
	ctx := context.Background()

	// Validate AWS credentials
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}

	// Export config values as env vars for Terragrunt's site.hcl get_env() calls.
	if cfg.ArtifactsBucket != "" && os.Getenv("KM_ARTIFACTS_BUCKET") == "" {
		os.Setenv("KM_ARTIFACTS_BUCKET", cfg.ArtifactsBucket)
	}
	if cfg.OrganizationAccountID != "" && os.Getenv("KM_ACCOUNTS_ORGANIZATION") == "" {
		os.Setenv("KM_ACCOUNTS_ORGANIZATION", cfg.OrganizationAccountID)
	}
	if cfg.DNSParentAccountID != "" && os.Getenv("KM_ACCOUNTS_DNS_PARENT") == "" {
		os.Setenv("KM_ACCOUNTS_DNS_PARENT", cfg.DNSParentAccountID)
	}
	if cfg.ApplicationAccountID != "" && os.Getenv("KM_ACCOUNTS_APPLICATION") == "" {
		os.Setenv("KM_ACCOUNTS_APPLICATION", cfg.ApplicationAccountID)
	}
	if cfg.Domain != "" && os.Getenv("KM_DOMAIN") == "" {
		os.Setenv("KM_DOMAIN", cfg.Domain)
	}
	if cfg.PrimaryRegion != "" && os.Getenv("KM_REGION") == "" {
		os.Setenv("KM_REGION", cfg.PrimaryRegion)
	}
	if cfg.Route53ZoneID != "" && os.Getenv("KM_ROUTE53_ZONE_ID") == "" {
		os.Setenv("KM_ROUTE53_ZONE_ID", cfg.Route53ZoneID)
	}

	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = verbose

	var lister SandboxLister
	if cfg.StateBucket != "" {
		s3Client := s3.NewFromConfig(awsCfg)
		lister = &awsSandboxLister{
			s3Client: s3Client,
			bucket:   cfg.StateBucket,
		}
	}

	return RunUninitWithDeps(cfg, runner, lister, region, force)
}

// RunUninitWithDeps is the testable core of uninit with dependency injection.
// It accepts a UninitRunner and SandboxLister to allow unit testing without AWS.
//
// Exported for use in uninit_test.go.
func RunUninitWithDeps(cfg *config.Config, runner UninitRunner, lister SandboxLister, region string, force bool) error {
	ctx := context.Background()

	// Step 1: Verify we can check for active sandboxes.
	// If StateBucket is not configured, we can't verify — require --force.
	if cfg.StateBucket == "" && !force {
		return fmt.Errorf(
			"cannot verify active sandboxes — state_bucket not configured; use --force to proceed without the check",
		)
	}

	// Step 2: Check for active sandboxes in the target region.
	if lister != nil && !force {
		records, err := lister.ListSandboxes(ctx, false)
		if err != nil {
			return fmt.Errorf("failed to list sandboxes (use --force to skip this check): %w", err)
		}

		activeCount := 0
		for _, r := range records {
			if r.Region == region && r.Status == "running" {
				activeCount++
			}
		}

		if activeCount > 0 {
			return fmt.Errorf(
				"%d active sandbox(es) found in region %s — destroy them first or use --force to proceed anyway",
				activeCount, region,
			)
		}
	}

	// Step 3: Build module list in reverse dependency order.
	repoRoot := findRepoRoot()
	regionLabel := compiler.RegionLabel(region)
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)

	// Reverse dependency order: TTL handler first (depends on network), network last.
	type moduleEntry struct {
		dir  string
		name string
	}
	modules := []moduleEntry{
		{dir: filepath.Join(regionDir, "ttl-handler"), name: "TTL handler Lambda"},
		{dir: filepath.Join(regionDir, "s3-replication"), name: "S3 artifact replication"},
		{dir: filepath.Join(regionDir, "ses"), name: "SES email infrastructure"},
		{dir: filepath.Join(regionDir, "dynamodb-identities"), name: "DynamoDB identity table"},
		{dir: filepath.Join(regionDir, "dynamodb-budget"), name: "DynamoDB budget table"},
		{dir: filepath.Join(regionDir, "network"), name: "network (VPC/subnets/SGs)"},
	}

	// Step 4: Destroy each module. Skip missing directories; continue on error.
	destroyed := 0
	for _, mod := range modules {
		if _, err := os.Stat(mod.dir); os.IsNotExist(err) {
			fmt.Printf("  Skipping %s (directory not found)\n", mod.name)
			continue
		}

		fmt.Printf("  Destroying %s...", mod.name)
		if err := runner.Destroy(ctx, mod.dir); err != nil {
			fmt.Printf("\n  Warning: %s destroy failed (continuing): %v\n", mod.name, err)
			continue
		}
		fmt.Println(" done")
		destroyed++
	}

	fmt.Printf("\nUninit complete for %s (%s): %d module(s) destroyed\n", region, regionLabel, destroyed)
	return nil
}

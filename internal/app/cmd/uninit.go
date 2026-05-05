package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// ECRRepoDeleter abstracts ECR repository deletion. Returns nil when the
// repository doesn't exist (treated as already-deleted) so callers can
// loop idempotently across the well-known repo list.
type ECRRepoDeleter interface {
	DeleteRepository(ctx context.Context, region, name string) error
}

// awsCLIECRDeleter shells out to the AWS CLI to match init.go's existing
// pattern (init.go also shells out to `aws ecr describe-repositories /
// create-repository` rather than using the SDK), avoiding a new module
// dependency. RepositoryNotFoundException is treated as success.
type awsCLIECRDeleter struct {
	awsProfile string
}

func (d *awsCLIECRDeleter) DeleteRepository(ctx context.Context, region, name string) error {
	cmd := exec.CommandContext(ctx, "aws", "ecr", "delete-repository",
		"--repository-name", name,
		"--force",
		"--region", region,
		"--profile", d.awsProfile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// AWS CLI prints "RepositoryNotFoundException" in stderr/stdout when
		// the repo doesn't exist — treat as already-deleted.
		if strings.Contains(string(out), "RepositoryNotFoundException") {
			return nil
		}
		return fmt.Errorf("aws ecr delete-repository %s: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// ecrReposToDelete is the list of ECR repositories created by km init's
// container-substrate path. Names are NOT prefixed with resource_prefix
// (init.go hardcodes "km-sandbox" etc.), so a uninit on one resource_prefix
// would also affect another install in the same AWS account if any exists.
// Operators with multi-install setups should disable container_substrates_enabled
// or skip ECR cleanup.
var ecrReposToDelete = []string{
	"km-sandbox",
	"km-dns-proxy",
	"km-http-proxy",
	"km-audit-log",
	"km-tracing",
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
	// Use the canonical helper so KM_RESOURCE_PREFIX (and other Phase-66 vars)
	// are included — the previous hand-rolled copy missed those, which made
	// terragrunt resolve the backend bucket as tf-km-state-* instead of the
	// operator's tf-{prefix}-state-* and fail with HeadBucket 403.
	ExportConfigEnvVars(cfg)
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

	ecrDeleter := &awsCLIECRDeleter{awsProfile: awsProfile}

	return RunUninitWithDeps(cfg, runner, lister, ecrDeleter, region, force)
}

// RunUninitWithDeps is the testable core of uninit with dependency injection.
// It accepts a UninitRunner, SandboxLister, and ECRRepoDeleter to allow unit
// testing without AWS. Pass a nil ECRRepoDeleter to skip the ECR cleanup pass
// (e.g. for tests that only exercise terragrunt destroy ordering).
//
// Exported for use in uninit_test.go.
func RunUninitWithDeps(cfg *config.Config, runner UninitRunner, lister SandboxLister, ecrDeleter ECRRepoDeleter, region string, force bool) error {
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

	// Step 3: Destroy modules in REVERSE dependency order using the same
	// regionalModules() definition km init applies. Reversing keeps init/uninit
	// in lockstep — adding a new module to init automatically destroys it on
	// uninit too, no second list to drift.
	repoRoot := findRepoRoot()
	regionLabel := compiler.RegionLabel(region)
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)

	applyOrder := regionalModules(regionDir)
	// Reverse in place into a fresh slice so applyOrder isn't mutated.
	modules := make([]regionalModule, len(applyOrder))
	for i, m := range applyOrder {
		modules[len(applyOrder)-1-i] = m
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

	// Step 5: Delete ECR repositories. Optional (skipped in tests with nil deleter).
	// Repos are global to the AWS account (not resource_prefix-namespaced), so a
	// multi-install operator should be aware this cleanup is shared.
	ecrDeleted := 0
	if ecrDeleter != nil {
		fmt.Println()
		fmt.Println("Deleting ECR repositories...")
		for _, repo := range ecrReposToDelete {
			fmt.Printf("  Deleting %s...", repo)
			if err := ecrDeleter.DeleteRepository(ctx, region, repo); err != nil {
				fmt.Printf("\n  Warning: %s deletion failed (continuing): %v\n", repo, err)
				continue
			}
			fmt.Println(" done")
			ecrDeleted++
		}
	}

	fmt.Printf("\nUninit complete for %s (%s): %d module(s) destroyed", region, regionLabel, destroyed)
	if ecrDeleter != nil {
		fmt.Printf(", %d ECR repo(s) deleted", ecrDeleted)
	}
	fmt.Println()
	return nil
}

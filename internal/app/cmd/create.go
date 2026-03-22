package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// NewCreateCmd creates the "km create" subcommand.
// Usage: km create <profile.yaml> [--on-demand] [--aws-profile <name>]
//
// Command flow:
//  1. Parse and validate the profile (fail early on invalid input)
//  2. Validate AWS credentials (fail early before any provisioning)
//  3. Compile the profile into Terragrunt artifacts
//  4. Create and populate the sandbox directory
//  5. Run terragrunt apply (streams output in real time)
//  6. On failure: attempt sandbox dir cleanup
//
// Security notes:
//   - NETW-05 (IMDSv2): enforced at the Terraform module level via
//     http_tokens = "required" in the ec2spot module. No create command code needed.
//   - NETW-07 (SOPS): decryption happens at provision time via site.hcl's
//     run_cmd("sops", "--decrypt", ...) pattern. SSM parameter ARNs are written
//     into tfvars by the compiler; user-data decrypts at boot using the instance
//     IAM role. No SOPS handling needed in the create command.
func NewCreateCmd(cfg *config.Config) *cobra.Command {
	var onDemand bool
	var awsProfile string

	cmd := &cobra.Command{
		Use:   "create <profile.yaml>",
		Short: "Provision a new sandbox from a profile",
		Long: `Create validates, compiles, and provisions a new sandbox from the given profile.

The profile is validated before any AWS resources are created. AWS credentials
are verified before compilation or provisioning begins. Terragrunt output is
streamed to the terminal in real time.

If provisioning fails, the local sandbox directory is removed. AWS resources
that were partially created must be cleaned up manually with 'km destroy'.

Exit code 0 — sandbox created successfully
Exit code 1 — validation, compilation, or provisioning failed`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" {
				awsProfile = "klanker-terraform"
			}
			return runCreate(cfg, args[0], onDemand, awsProfile)
		},
	}

	cmd.Flags().BoolVar(&onDemand, "on-demand", false,
		"Override spot: true in the profile — use on-demand instances instead")
	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-terraform",
		"AWS CLI profile to use")

	return cmd
}

// runCreate executes the full create workflow.
func runCreate(cfg *config.Config, profilePath string, onDemand bool, awsProfile string) error {
	ctx := context.Background()

	// Step 1: Read profile file
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		return fmt.Errorf("cannot read profile %s: %w", profilePath, err)
	}

	// Step 2: Parse profile to check for extends field
	parsed, err := profile.Parse(raw)
	if err != nil {
		return fmt.Errorf("failed to parse profile %s: %w", profilePath, err)
	}

	// Step 3: Resolve inheritance chain if extends is present
	var resolvedProfile *profile.SandboxProfile
	if parsed.Extends != "" {
		log.Debug().Str("extends", parsed.Extends).Msg("resolving inheritance chain")
		fileDir := filepath.Dir(profilePath)
		searchPaths := append([]string{fileDir}, cfg.ProfileSearchPaths...)
		resolvedProfile, err = profile.Resolve(parsed.Extends, searchPaths)
		if err != nil {
			return fmt.Errorf("failed to resolve extends %q: %w", parsed.Extends, err)
		}
		// Schema-validate raw child bytes; semantic-validate merged profile
		schemaErrs := profile.ValidateSchema(raw)
		semanticErrs := profile.ValidateSemantic(resolvedProfile)
		allErrs := append(schemaErrs, semanticErrs...)
		if len(allErrs) > 0 {
			for _, e := range allErrs {
				fmt.Fprintf(os.Stderr, "ERROR: %s: %s\n", profilePath, e.Error())
			}
			return fmt.Errorf("profile %s failed validation", profilePath)
		}
	} else {
		// No extends — validate raw bytes and use parsed profile directly
		errs := profile.Validate(raw)
		if len(errs) > 0 {
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "ERROR: %s: %s\n", profilePath, e.Error())
			}
			return fmt.Errorf("profile %s failed validation", profilePath)
		}
		resolvedProfile = parsed
	}

	// Step 4: Generate sandbox ID
	sandboxID := compiler.GenerateSandboxID()
	substrate := resolvedProfile.Spec.Runtime.Substrate
	spot := resolvedProfile.Spec.Runtime.Spot && !onDemand
	fmt.Printf("Creating sandbox %s (substrate: %s, spot: %v)...\n", sandboxID, substrate, spot)

	// Step 5: Load and validate AWS credentials (fail before any provisioning)
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed — check that profile %q is configured: %w", awsProfile, err)
	}

	// Step 6: Load shared network config from km init outputs
	repoRoot := findRepoRoot()
	networkOutputs, err := LoadNetworkOutputs(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to load network config: %w\nRun 'km init' to provision the shared VPC first", err)
	}
	network := &compiler.NetworkConfig{
		VPCID:             networkOutputs.VPCID,
		PublicSubnets:     networkOutputs.PublicSubnets,
		AvailabilityZones: networkOutputs.AvailabilityZones,
	}

	// Step 7: Compile profile into Terragrunt artifacts
	artifacts, err := compiler.Compile(resolvedProfile, sandboxID, onDemand, network)
	if err != nil {
		return fmt.Errorf("failed to compile profile: %w", err)
	}

	// Step 8: Create sandbox directory
	sandboxDir, err := terragrunt.CreateSandboxDir(repoRoot, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to create sandbox directory: %w", err)
	}

	// Step 9: Populate sandbox directory with compiled artifacts
	if err := terragrunt.PopulateSandboxDir(sandboxDir, artifacts.ServiceHCL, artifacts.UserData); err != nil {
		_ = terragrunt.CleanupSandboxDir(sandboxDir)
		return fmt.Errorf("failed to populate sandbox directory: %w", err)
	}

	// Step 10: Run terragrunt apply (streams output in real time)
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	if err := runner.Apply(ctx, sandboxDir); err != nil {
		// Do NOT run destroy — resources may be partially created and require
		// manual cleanup. Only remove the local sandbox directory.
		fmt.Fprintf(os.Stderr, "ERROR: terragrunt apply failed: %v\n", err)
		if cleanErr := terragrunt.CleanupSandboxDir(sandboxDir); cleanErr != nil {
			log.Warn().Err(cleanErr).Msg("failed to clean up sandbox directory after apply failure")
		}
		return fmt.Errorf("provisioning failed for sandbox %s", sandboxID)
	}

	fmt.Printf("Sandbox %s created successfully.\n", sandboxID)
	return nil
}

// findRepoRoot locates the repository root by walking up from the executable
// or the current working directory looking for a CLAUDE.md anchor file.
// Falls back to the current working directory if not found.
func findRepoRoot() string {
	// Try runtime caller path first (works in tests)
	_, thisFile, _, ok := runtime.Caller(0)
	if ok {
		// Walk up from this source file's location
		dir := filepath.Dir(thisFile)
		for i := 0; i < 6; i++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// Fall back to cwd
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	// Walk up from cwd looking for go.mod
	dir := cwd
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd
}

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	dynamodbpkg "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
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

	// Step 6: Load shared network config for the profile's region
	repoRoot := findRepoRoot()
	region := resolvedProfile.Spec.Runtime.Region
	regionLabel := compiler.RegionLabel(region)
	networkOutputs, err := LoadNetworkOutputs(repoRoot, regionLabel)
	if err != nil {
		return fmt.Errorf("failed to load network config for %s: %w\nRun 'km init --region %s' first", region, err, region)
	}
	// Derive email domain early so the compiler can use it.
	// This must be computed before calling compiler.Compile.
	networkDomain := cfg.Domain
	if networkDomain == "" {
		networkDomain = "klankermaker.ai"
	}
	network := &compiler.NetworkConfig{
		VPCID:             networkOutputs.VPCID,
		PublicSubnets:     networkOutputs.PublicSubnets,
		AvailabilityZones: networkOutputs.AvailabilityZones,
		RegionLabel:       regionLabel,
		EmailDomain:       "sandboxes." + networkDomain,
	}

	// Step 7: Compile profile into Terragrunt artifacts
	artifacts, err := compiler.Compile(resolvedProfile, sandboxID, onDemand, network)
	if err != nil {
		return fmt.Errorf("failed to compile profile: %w", err)
	}

	// Step 8: Create sandbox directory
	sandboxDir, err := terragrunt.CreateSandboxDir(repoRoot, regionLabel, sandboxID)
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

	// Step 11: Write sandbox metadata to S3 so km list/status can read it without tag API calls.
	// Non-fatal: sandbox is provisioned even if metadata write fails.
	now := time.Now().UTC()
	var ttlExpiry *time.Time
	if resolvedProfile.Spec.Lifecycle.TTL != "" {
		if d, parseErr := time.ParseDuration(resolvedProfile.Spec.Lifecycle.TTL); parseErr == nil {
			t := now.Add(d)
			ttlExpiry = &t
		} else {
			log.Warn().Str("ttl", resolvedProfile.Spec.Lifecycle.TTL).Err(parseErr).
				Msg("failed to parse TTL duration — TTL schedule not created")
		}
	}

	// Determine artifact bucket for S3 operations.
	artifactBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
	if artifactBucket == "" {
		artifactBucket = "km-sandbox-artifacts-ea554771"
	}

	s3Client := s3.NewFromConfig(awsCfg)

	if cfg.StateBucket != "" {
		meta := awspkg.SandboxMetadata{
			SandboxID:   sandboxID,
			ProfileName: resolvedProfile.Metadata.Name,
			Substrate:   string(resolvedProfile.Spec.Runtime.Substrate),
			Region:      resolvedProfile.Spec.Runtime.Region,
			CreatedAt:   now,
			TTLExpiry:   ttlExpiry,
		}
		metaJSON, _ := json.Marshal(meta)
		_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(cfg.StateBucket),
			Key:         aws.String("tf-km/sandboxes/" + sandboxID + "/metadata.json"),
			Body:        bytes.NewReader(metaJSON),
			ContentType: aws.String("application/json"),
		})
		if putErr != nil {
			log.Warn().Err(putErr).Str("sandbox_id", sandboxID).
				Msg("failed to write sandbox metadata (non-fatal)")
		}
	} else {
		log.Debug().Msg("KM_STATE_BUCKET not set — skipping sandbox metadata write")
	}

	// Step 11b: Store profile YAML in S3 so km destroy can load it for artifact upload.
	// Non-fatal: artifact upload in destroy will be skipped with a warning if unavailable.
	profileYAML, _ := os.ReadFile(profilePath)
	if len(profileYAML) > 0 {
		_, profilePutErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(artifactBucket),
			Key:         aws.String("artifacts/" + sandboxID + "/.km-profile.yaml"),
			Body:        bytes.NewReader(profileYAML),
			ContentType: aws.String("application/x-yaml"),
		})
		if profilePutErr != nil {
			log.Warn().Err(profilePutErr).Str("sandbox_id", sandboxID).
				Msg("failed to store profile in S3 (non-fatal — artifact upload in destroy may be skipped)")
		} else {
			log.Debug().Str("sandbox_id", sandboxID).Msg("profile stored in S3 for destroy retrieval")
		}
	}

	// Step 12: Create EventBridge TTL schedule if TTL is configured and Lambda ARN is set.
	// Non-fatal: sandbox is provisioned; operator can re-schedule manually if this fails.
	if ttlExpiry != nil && cfg.TTLLambdaARN != "" {
		schedInput := compiler.BuildTTLScheduleInput(sandboxID, *ttlExpiry, cfg.TTLLambdaARN, cfg.SchedulerRoleARN)
		schedulerClient := scheduler.NewFromConfig(awsCfg)
		if err := awspkg.CreateTTLSchedule(ctx, schedulerClient, schedInput); err != nil {
			log.Error().Err(err).Str("sandbox_id", sandboxID).
				Msg("failed to create TTL schedule (non-fatal — sandbox is provisioned)")
		} else {
			log.Info().Str("sandbox_id", sandboxID).Time("ttl_expiry", *ttlExpiry).
				Msg("TTL schedule created")
		}
	}

	// Step 12b: Write initial budget limits to DynamoDB if profile has a budget section.
	// Non-fatal: sandbox is provisioned even if budget write fails.
	if resolvedProfile.Spec.Budget != nil {
		tableName := cfg.BudgetTableName
		if tableName == "" {
			tableName = "km-budgets"
		}
		budgetClient := dynamodbpkg.NewFromConfig(awsCfg)

		var computeLimit, aiLimit float64
		if resolvedProfile.Spec.Budget.Compute != nil {
			computeLimit = resolvedProfile.Spec.Budget.Compute.MaxSpendUSD
		}
		if resolvedProfile.Spec.Budget.AI != nil {
			aiLimit = resolvedProfile.Spec.Budget.AI.MaxSpendUSD
		}
		warningThreshold := resolvedProfile.Spec.Budget.WarningThreshold
		if warningThreshold == 0 {
			warningThreshold = 0.80 // default 80%
		}

		if budgetErr := awspkg.SetBudgetLimits(ctx, budgetClient, tableName, sandboxID, computeLimit, aiLimit, warningThreshold); budgetErr != nil {
			log.Warn().Err(budgetErr).Str("sandbox_id", sandboxID).
				Msg("failed to write budget limits (non-fatal)")
		} else {
			log.Info().
				Str("sandbox_id", sandboxID).
				Float64("compute_limit", computeLimit).
				Float64("ai_limit", aiLimit).
				Float64("warning_threshold", warningThreshold).
				Msg("Budget limits set")
			fmt.Printf("Budget limits set: compute $%.2f, AI $%.2f, warning at %.0f%%\n",
				computeLimit, aiLimit, warningThreshold*100)
		}
	}

	// Step 13: Provision SES email identity for the sandbox.
	// Non-fatal: sandbox is still usable without email.
	// Derive email domain from config; default to "klankermaker.ai" when not set.
	baseDomain := cfg.Domain
	if baseDomain == "" {
		baseDomain = "klankermaker.ai"
	}
	emailDomain := "sandboxes." + baseDomain
	sesClient := sesv2.NewFromConfig(awsCfg)
	emailAddr, emailErr := awspkg.ProvisionSandboxEmail(ctx, sesClient, sandboxID, emailDomain)
	if emailErr != nil {
		log.Warn().Err(emailErr).Msg("failed to provision sandbox email (non-fatal)")
	} else {
		log.Info().Str("email", emailAddr).Msg("sandbox email provisioned")
		fmt.Fprintf(os.Stdout, "Email: %s\n", emailAddr)
	}

	fmt.Printf("Sandbox %s created successfully.\n", sandboxID)

	// Step 14: Send lifecycle notification if operator email is configured.
	if operatorEmail := os.Getenv("KM_OPERATOR_EMAIL"); operatorEmail != "" {
		if err := awspkg.SendLifecycleNotification(ctx, sesClient, operatorEmail, sandboxID, "created", emailDomain); err != nil {
			log.Warn().Err(err).Msg("failed to send created lifecycle notification (non-fatal)")
		}
	}

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

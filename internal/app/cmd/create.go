package cmd

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	dynamodbpkg "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	iampkg "github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	githubpkg "github.com/whereiskurt/klankrmkr/pkg/github"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// ErrGitHubNotConfigured is returned by generateAndStoreGitHubToken when the
// GitHub App SSM parameters are not found. Callers convert this to a clean
// "skipped (not configured)" log message rather than showing a stack trace.
var ErrGitHubNotConfigured = errors.New("GitHub App not configured in SSM — run 'km configure github' first")

// SSMGetPutAPI is a narrow interface covering the SSM operations used by
// generateAndStoreGitHubToken. *ssm.Client satisfies this interface.
type SSMGetPutAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

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
	var noBedrock bool
	var awsProfile string
	var verbose bool
	var remote bool
	var local bool
	var sandboxIDOverride string
	var aliasOverride string
	var substrateOverride string
	var dockerShortcut bool

	cmd := &cobra.Command{
		Use:   "create <profile.yaml>",
		Short: "Provision a new sandbox from a profile",
		Long:  helpText("create"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" && os.Getenv("KM_REMOTE_CREATE") == "" {
				awsProfile = "klanker-terraform"
			}

			// --docker is a shortcut for --substrate=docker
			if dockerShortcut {
				substrateOverride = "docker"
			}

			// Auto-detect remote vs local based on substrate.
			// EC2/ECS default to --remote (no local terraform needed).
			// Docker defaults to --local (runs on operator's machine).
			// Explicit --remote or --local flags override the auto-detection.
			useRemote := remote
			if !remote && !local {
				// If running inside the create-handler Lambda, always use local
				// (the Lambda IS the remote — going remote again would recurse).
				if os.Getenv("KM_REMOTE_CREATE") != "" {
					useRemote = false
				} else {
					// Neither flag explicitly set — auto-detect from profile substrate
					sub := substrateOverride
					if sub == "" {
						data, readErr := os.ReadFile(args[0])
						if readErr == nil {
							p, parseErr := profile.Parse(data)
							if parseErr == nil {
								sub = string(p.Spec.Runtime.Substrate)
							}
						}
					}
					if sub == "" || sub == "ec2" || sub == "ecs" {
						useRemote = true // EC2/ECS default to remote
					}
				}
			}

			if useRemote {
				return runCreateRemote(cfg, args[0], onDemand, noBedrock, awsProfile, aliasOverride)
			}
			return runCreate(cfg, args[0], onDemand, noBedrock, awsProfile, verbose, sandboxIDOverride, aliasOverride, substrateOverride)
		},
	}

	cmd.Flags().BoolVar(&onDemand, "on-demand", false,
		"Override spot: true in the profile — use on-demand instances instead")
	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-terraform",
		"AWS CLI profile to use")
	cmd.Flags().BoolVar(&verbose, "verbose", false,
		"Show full terragrunt/terraform output")
	cmd.Flags().BoolVar(&remote, "remote", false,
		"Force remote create via Lambda (default for EC2/ECS substrates)")
	cmd.Flags().BoolVar(&local, "local", false,
		"Force local create with terragrunt (default for Docker substrate)")
	cmd.Flags().StringVar(&sandboxIDOverride, "sandbox-id", "",
		"Use a specific sandbox ID instead of generating one (used by create-handler Lambda)")
	cmd.Flags().MarkHidden("sandbox-id")
	cmd.Flags().StringVar(&aliasOverride, "alias", "",
		"Human-friendly alias for the sandbox (e.g. orc, wrkr). Overrides profile metadata.alias template.")
	cmd.Flags().StringVar(&substrateOverride, "substrate", "",
		"Override profile substrate (ec2, ecs, docker)")
	cmd.Flags().BoolVar(&noBedrock, "no-bedrock", false,
		"Disable Bedrock access — removes IAM permissions and Bedrock env vars")
	cmd.Flags().BoolVar(&dockerShortcut, "docker", false,
		"Shortcut for --substrate=docker")

	return cmd
}

// runCreate executes the full create workflow.
func runCreate(cfg *config.Config, profilePath string, onDemand bool, noBedrock bool, awsProfile string, verbose bool, sandboxIDOverride string, aliasOverride string, substrateOverride string) error {
	createStart := time.Now()
	ctx := context.Background()

	// Suppress structured JSON log output when not verbose — user sees fmt.Printf step summaries instead.
	if !verbose {
		origLogger := log.Logger
		log.Logger = zerolog.New(io.Discard)
		defer func() { log.Logger = origLogger }()
	}

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

	// Step 4: Generate sandbox ID (or use override from create-handler Lambda)
	sandboxID := sandboxIDOverride
	if sandboxID == "" {
		sandboxID = compiler.GenerateSandboxID(resolvedProfile.Metadata.Prefix)
	} else if !compiler.IsValidSandboxID(sandboxID) {
		return fmt.Errorf("invalid sandbox ID override %q: must match pattern [a-z][a-z0-9]{0,11}-[a-f0-9]{8}", sandboxID)
	}
	substrate := resolvedProfile.Spec.Runtime.Substrate
	if substrateOverride != "" {
		substrate = substrateOverride
		resolvedProfile.Spec.Runtime.Substrate = substrateOverride
	}
	spot := resolvedProfile.Spec.Runtime.Spot && !onDemand

	// substrateLabel differentiates ec2spot vs ec2demand for km list display.
	substrateLabel := string(substrate)
	if substrate == "ec2" {
		if spot {
			substrateLabel = "ec2spot"
		} else {
			substrateLabel = "ec2demand"
		}
	}

	// --no-bedrock: disable Bedrock access entirely
	if noBedrock {
		resolvedProfile.Spec.Execution.UseBedrock = false
		stripBedrockEnvVars(resolvedProfile)
	}

	printBanner("km create", sandboxID)
	fmt.Printf("\n  Substrate: %s, Spot: %v\n", substrate, spot)

	// Step 5: Load and validate AWS credentials (fail before any provisioning)
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed — check that profile %q is configured: %w", awsProfile, err)
	}

	// Step 5c: Enforce sandbox limit before any provisioning.
	if cfg.StateBucket != "" {
		s3Client := s3.NewFromConfig(awsCfg)
		activeCount, limitErr := checkSandboxLimit(ctx, s3Client, cfg.StateBucket, cfg.MaxSandboxes)
		if limitErr != nil {
			// Best-effort operator notification — don't block on SES failure.
			if cfg.OperatorEmail != "" {
				sesClient := sesv2.NewFromConfig(awsCfg)
				notifDomain := cfg.Domain
				if notifDomain == "" {
					notifDomain = "klankermaker.ai"
				}
				if notifErr := awspkg.SendLimitNotification(ctx, sesClient, cfg.OperatorEmail, sandboxID, notifDomain, activeCount, cfg.MaxSandboxes); notifErr != nil {
					log.Warn().Err(notifErr).Msg("failed to send sandbox limit notification (non-fatal)")
				}
			}
			fmt.Fprintf(os.Stderr, "\nERROR: %s\n", limitErr)
			return limitErr
		}
	}

	// Step 5b: Export config values as env vars for Terragrunt's site.hcl get_env() calls.
	// Export config values as env vars for Terragrunt's site.hcl get_env() calls.
	// Always set from config — config file values take precedence over pre-existing env.
	// This is critical for Lambda remote-create where the subprocess inherits minimal env.
	if cfg.ApplicationAccountID != "" {
		os.Setenv("KM_ACCOUNTS_APPLICATION", cfg.ApplicationAccountID)
	}
	if cfg.ManagementAccountID != "" {
		os.Setenv("KM_ACCOUNTS_MANAGEMENT", cfg.ManagementAccountID)
	}
	if cfg.Domain != "" {
		os.Setenv("KM_DOMAIN", cfg.Domain)
	}
	if cfg.PrimaryRegion != "" {
		os.Setenv("KM_REGION", cfg.PrimaryRegion)
	}
	if cfg.ArtifactsBucket != "" {
		os.Setenv("KM_ARTIFACTS_BUCKET", cfg.ArtifactsBucket)
	}
	if cfg.Route53ZoneID != "" {
		os.Setenv("KM_ROUTE53_ZONE_ID", cfg.Route53ZoneID)
	}
	if cfg.OperatorEmail != "" {
		os.Setenv("KM_OPERATOR_EMAIL", cfg.OperatorEmail)
		os.Setenv("KM_AWS_PROFILE", awsProfile)
	}

	// Step 6: Load shared network config for the profile's region.
	// For docker substrate, skip LoadNetworkOutputs — there are no Terragrunt network outputs.
	// Build a minimal NetworkConfig from km-config.yaml fields instead.
	repoRoot := findRepoRoot()
	region := resolvedProfile.Spec.Runtime.Region
	regionLabel := compiler.RegionLabel(region)
	networkDomain := cfg.Domain
	if networkDomain == "" {
		networkDomain = "klankermaker.ai"
	}
	artifactsBucket := cfg.ArtifactsBucket
	if artifactsBucket == "" {
		artifactsBucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	var network *compiler.NetworkConfig
	if substrate == "docker" {
		// Docker substrate: construct minimal NetworkConfig — no Terragrunt outputs needed.
		network = &compiler.NetworkConfig{
			EmailDomain:     "sandboxes." + networkDomain,
			ArtifactsBucket: artifactsBucket,
		}
	} else {
		networkOutputs, err := LoadNetworkOutputs(repoRoot, regionLabel)
		if err != nil {
			return fmt.Errorf("failed to load network config for %s: %w\nRun 'km init --region %s' first", region, err, region)
		}
		network = &compiler.NetworkConfig{
			VPCID:             networkOutputs.VPCID,
			PublicSubnets:     networkOutputs.PublicSubnets,
			AvailabilityZones: networkOutputs.AvailabilityZones,
			RegionLabel:       regionLabel,
			EmailDomain:       "sandboxes." + networkDomain,
			ArtifactsBucket:   artifactsBucket,
		}
	}

	// Step 6a-efs: Load EFS outputs for shared filesystem mount (Phase 43).
	// Only applies to non-docker substrates; docker does not support EFS mounts.
	if substrate != "docker" {
		efsID, err := LoadEFSOutputs(repoRoot, regionLabel)
		if err != nil {
			return fmt.Errorf("failed to load EFS outputs for %s: %w", regionLabel, err)
		}
		network.EFSFilesystemID = efsID

		// Validate: profile requests EFS mount but EFS not initialized.
		if resolvedProfile.Spec.Runtime.MountEFS && efsID == "" {
			return fmt.Errorf("profile requests mountEFS but EFS is not initialized for region %s — run 'km init --region %s' first", regionLabel, region)
		}
	}

	// Step 6b: Resolve spot rate for budget enforcement (BUDG-03).
	// When budget.compute is set, we need a non-zero spot rate so the Lambda enforcer
	// can calculate compute spend as spot_rate * elapsed_minutes / 60. Without this,
	// compute spend is always $0.00 and enforcement never triggers.
	if resolvedProfile.Spec.Budget != nil && resolvedProfile.Spec.Budget.Compute != nil {
		instanceType := resolvedProfile.Spec.Runtime.InstanceType
		if instanceType == "" {
			instanceType = "t3.medium" // conservative default
		}
		// The AWS Pricing API is only available in us-east-1 (global endpoint).
		pricingCfg := awsCfg.Copy()
		pricingCfg.Region = "us-east-1"
		pricingClient := pricing.NewFromConfig(pricingCfg)
		spotRate, pricingErr := awspkg.GetSpotRate(ctx, pricingClient, instanceType, region)
		if pricingErr != nil || spotRate == 0 {
			// Static fallback — non-fatal, GetSpotRate uses on-demand approximation
			// which may return 0 for spot. Static table provides reasonable estimates.
			spotRate = staticSpotRate(instanceType)
			log.Warn().
				Str("instanceType", instanceType).
				Float64("fallbackRate", spotRate).
				Msg("Pricing API unavailable or returned zero — using static spot rate fallback")
		}
		network.SpotRateUSD = spotRate
	}

	// Pass alias to compiler so userdata can set KM_SANDBOX_ALIAS and alias email delivery.
	network.Alias = aliasOverride

	// Step 7: Compile profile into Terragrunt/Docker artifacts.
	// For docker substrate, compile once and dispatch immediately — no AZ retry loop needed.
	{
		artifacts, err := compiler.Compile(resolvedProfile, sandboxID, onDemand, network)
		if err != nil {
			return fmt.Errorf("failed to compile profile: %w", err)
		}
		if substrate == "docker" {
			return runCreateDocker(ctx, cfg, awsCfg, resolvedProfile, sandboxID, artifacts, verbose, noBedrock, aliasOverride)
		}
	}

	// Step 7-10: Create sandbox dir, populate, and apply (non-docker path).
	// For spot instances, retry across available AZs on capacity failure.
	maxAttempts := len(network.AvailabilityZones)
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if onDemand {
		maxAttempts = 1 // on-demand doesn't need AZ rotation
	}

	var sandboxDir string
	var artifacts *compiler.CompiledArtifacts
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = verbose

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Rotate subnets and AZs so index 0 points to the next AZ
			network.PublicSubnets = append(network.PublicSubnets[1:], network.PublicSubnets[0])
			network.AvailabilityZones = append(network.AvailabilityZones[1:], network.AvailabilityZones[0])
			fmt.Fprintf(os.Stderr, "  Retrying in %s (%s)...\n", network.AvailabilityZones[0], network.PublicSubnets[0])
		}

		// Step 7: Compile profile into Terragrunt artifacts
		var compileErr error
		artifacts, compileErr = compiler.Compile(resolvedProfile, sandboxID, onDemand, network)
		if compileErr != nil {
			return fmt.Errorf("failed to compile profile: %w", compileErr)
		}

		// Step 8: Create sandbox directory
		var dirErr error
		sandboxDir, dirErr = terragrunt.CreateSandboxDir(repoRoot, regionLabel, sandboxID)
		if dirErr != nil {
			return fmt.Errorf("failed to create sandbox directory: %w", dirErr)
		}

		// Step 8.5: Upload full user-data to S3 if it exceeded the 16KB limit.
		// The bootstrap stub in artifacts.UserData downloads this at boot.
		if artifacts.FullUserData != "" {
			artifactBucketForUD := cfg.ArtifactsBucket
			if artifactBucketForUD == "" {
				artifactBucketForUD = os.Getenv("KM_ARTIFACTS_BUCKET")
			}
			if artifactBucketForUD != "" {
				s3ClientUD := s3.NewFromConfig(awsCfg)
				udKey := fmt.Sprintf("artifacts/%s/km-userdata.sh", sandboxID)
				if _, putErr := s3ClientUD.PutObject(ctx, &s3.PutObjectInput{
					Bucket:      aws.String(artifactBucketForUD),
					Key:         aws.String(udKey),
					Body:        bytes.NewReader([]byte(artifacts.FullUserData)),
					ContentType: aws.String("application/x-shellscript"),
				}); putErr != nil {
					return fmt.Errorf("upload full user-data to S3: %w", putErr)
				}
				fmt.Printf("  ✓ Bootstrap script uploaded to S3 (%d bytes)\n", len(artifacts.FullUserData))
			}
		}

		// Step 9: Populate sandbox directory with compiled artifacts
		if err := terragrunt.PopulateSandboxDir(sandboxDir, artifacts.ServiceHCL, artifacts.UserData); err != nil {
			_ = terragrunt.CleanupSandboxDir(sandboxDir)
			return fmt.Errorf("failed to populate sandbox directory: %w", err)
		}

		// Step 10: Run terragrunt apply
		if attempt == 0 {
			fmt.Printf("\nProvisioning infrastructure...")
		}

		// Spinner: print dots while apply runs in background
		spinDone := make(chan struct{})
		if !verbose {
			go func() {
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-spinDone:
						return
					case <-ticker.C:
						fmt.Print(".")
					}
				}
			}()
		}

		var applyStderr strings.Builder
		applyErr := runner.ApplyWithStderr(ctx, sandboxDir, &applyStderr)
		close(spinDone)

		if applyErr == nil {
			// Success
			fmt.Println(" done")
			fmt.Printf("  ✓ Infrastructure provisioned")
			if attempt > 0 {
				fmt.Printf(" (AZ: %s, attempt %d/%d)", network.AvailabilityZones[0], attempt+1, maxAttempts)
			}
			fmt.Println()
			break
		}

		// Apply failed — check if it's a spot capacity error we can retry
		stderrStr := applyStderr.String()
		isSpotCapacity := strings.Contains(stderrStr, "capacity-not-available") ||
			strings.Contains(stderrStr, "InsufficientInstanceCapacity")

		// Clean up the failed sandbox dir before retry or exit
		if cleanErr := terragrunt.CleanupSandboxDir(sandboxDir); cleanErr != nil {
			log.Warn().Err(cleanErr).Msg("failed to clean up sandbox directory after apply failure")
		}

		if isSpotCapacity && attempt < maxAttempts-1 {
			// Spot capacity failure with more AZs to try
			fmt.Fprintf(os.Stderr, "\n  ✗ Spot capacity unavailable in %s\n", network.AvailabilityZones[0])
			continue
		}

		// Final failure — no more retries
		fmt.Println() // newline after dots
		if isSpotCapacity {
			fmt.Fprintf(os.Stderr, "\n  ✗ Spot capacity unavailable in all %d AZs.\n", maxAttempts)
			fmt.Fprintf(os.Stderr, "  Use on-demand: km create --on-demand %s\n\n", profilePath)
		} else {
			fmt.Fprintf(os.Stderr, "ERROR: provisioning failed: %v\n", applyErr)
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
	artifactBucket := cfg.ArtifactsBucket
	if artifactBucket == "" {
		artifactBucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}

	s3Client := s3.NewFromConfig(awsCfg)

	// Step 11a: Record MLflow run for session tracking (OBSV-09).
	// Non-fatal: sandbox is provisioned even if MLflow write fails.
	mlflowRun := awspkg.MLflowRun{
		SandboxID:   sandboxID,
		ProfileName: resolvedProfile.Metadata.Name,
		Substrate:   substrateLabel,
		Region:      resolvedProfile.Spec.Runtime.Region,
		TTL:         resolvedProfile.Spec.Lifecycle.TTL,
		StartTime:   now,
		Experiment:  "klankrmkr",
	}
	if mlflowErr := awspkg.WriteMLflowRun(ctx, s3Client, artifactBucket, mlflowRun); mlflowErr != nil {
		log.Warn().Err(mlflowErr).Str("sandbox_id", sandboxID).
			Msg("failed to write MLflow run record (non-fatal)")
	} else {
		log.Info().Str("sandbox_id", sandboxID).Msg("MLflow run record written")
	}

	// Alias comes from --alias flag only (metadata.alias auto-generation removed).
	sandboxAlias := aliasOverride
	sandboxTableName := cfg.SandboxTableName
	if sandboxTableName == "" {
		sandboxTableName = "km-sandboxes"
	}
	dynamoClientCreate := dynamodbpkg.NewFromConfig(awsCfg)

	// Write sandbox metadata to DynamoDB. Non-fatal: sandbox is provisioned even if metadata write fails.
	{
		meta := awspkg.SandboxMetadata{
			SandboxID:   sandboxID,
			ProfileName: resolvedProfile.Metadata.Name,
			Substrate:   substrateLabel,
			Region:      resolvedProfile.Spec.Runtime.Region,
			CreatedAt:   now,
			TTLExpiry:   ttlExpiry,
			IdleTimeout: resolvedProfile.Spec.Lifecycle.IdleTimeout,
			MaxLifetime: resolvedProfile.Spec.Lifecycle.MaxLifetime,
			CreatedBy:   "cli",
			Alias:       sandboxAlias,
		}
		if writeErr := awspkg.WriteSandboxMetadataDynamo(ctx, dynamoClientCreate, sandboxTableName, &meta); writeErr != nil {
			log.Warn().Err(writeErr).Str("sandbox_id", sandboxID).
				Msg("failed to write sandbox metadata to DynamoDB (non-fatal)")
		} else {
			if sandboxAlias != "" {
				fmt.Printf("  ✓ Metadata stored (alias: %s)\n", sandboxAlias)
			} else {
				fmt.Printf("  ✓ Metadata stored\n")
			}
		}
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

	// Step 11c: Build and upload combined init script to S3.
	// This keeps user-data small (under 16KB) by offloading init to S3.
	if len(resolvedProfile.Spec.Execution.InitCommands) > 0 || len(resolvedProfile.Spec.Execution.InitScripts) > 0 {
		var initScript strings.Builder
		initScript.WriteString("#!/bin/bash\nset -e\n")
		initScript.WriteString("echo '[km-init] Starting profile init...'\n")

		// Inline commands
		for _, cmd := range resolvedProfile.Spec.Execution.InitCommands {
			initScript.WriteString(fmt.Sprintf("echo '[km-init] %s'\n", cmd))
			initScript.WriteString(cmd + "\n")
		}

		// Embedded init scripts (file contents inlined)
		profileDir := filepath.Dir(profilePath)
		for _, scriptFile := range resolvedProfile.Spec.Execution.InitScripts {
			scriptPath := filepath.Join(profileDir, scriptFile)
			if _, statErr := os.Stat(scriptPath); os.IsNotExist(statErr) {
				scriptPath = filepath.Join(repoRoot, scriptFile)
			}
			scriptData, readErr := os.ReadFile(scriptPath)
			if readErr != nil {
				log.Warn().Err(readErr).Str("script", scriptFile).
					Msg("failed to read init script (non-fatal)")
				continue
			}
			initScript.WriteString(fmt.Sprintf("\necho '[km-init] Running %s'\n", filepath.Base(scriptFile)))
			initScript.Write(scriptData)
			initScript.WriteString("\n")
		}

		initScript.WriteString("echo '[km-init] Profile init complete'\n")

		s3Key := fmt.Sprintf("artifacts/%s/km-init.sh", sandboxID)
		if _, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(artifactBucket),
			Key:         aws.String(s3Key),
			Body:        bytes.NewReader([]byte(initScript.String())),
			ContentType: aws.String("application/x-shellscript"),
		}); putErr != nil {
			log.Warn().Err(putErr).Msg("failed to upload init script to S3 (non-fatal)")
		} else {
			fmt.Printf("  ✓ Init script uploaded to S3 (%d bytes)\n", initScript.Len())
		}
	}

	// Step 12: Create EventBridge TTL schedule if TTL is configured.
	// Auto-discover Lambda ARN if not explicitly set.
	// Non-fatal: sandbox is provisioned; operator can re-schedule manually if this fails.
	ttlLambdaARN := cfg.TTLLambdaARN
	if ttlLambdaARN == "" && ttlExpiry != nil {
		// Auto-discover from the well-known Lambda function name.
		lambdaClient := lambda.NewFromConfig(awsCfg)
		fnOut, fnErr := lambdaClient.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: aws.String("km-ttl-handler"),
		})
		if fnErr == nil {
			ttlLambdaARN = aws.ToString(fnOut.Configuration.FunctionArn)
			log.Debug().Str("arn", ttlLambdaARN).Msg("auto-discovered TTL handler Lambda ARN")
		} else {
			log.Warn().Err(fnErr).Msg("TTL handler Lambda not found — TTL schedule will not be created")
		}
	}
	// Auto-discover scheduler role ARN if not explicitly set.
	schedulerRoleARN := cfg.SchedulerRoleARN
	if schedulerRoleARN == "" && ttlExpiry != nil {
		iamClient := iampkg.NewFromConfig(awsCfg)
		roleOut, roleErr := iamClient.GetRole(ctx, &iampkg.GetRoleInput{
			RoleName: aws.String("km-ttl-scheduler"),
		})
		if roleErr == nil {
			schedulerRoleARN = aws.ToString(roleOut.Role.Arn)
			log.Debug().Str("arn", schedulerRoleARN).Msg("auto-discovered TTL scheduler role ARN")
		} else {
			log.Warn().Err(roleErr).Msg("TTL scheduler role not found — run 'km init' to create it")
		}
	}
	if ttlExpiry != nil && ttlLambdaARN != "" && schedulerRoleARN != "" {
		schedInput := compiler.BuildTTLScheduleInput(sandboxID, *ttlExpiry, ttlLambdaARN, schedulerRoleARN)
		schedulerClient := scheduler.NewFromConfig(awsCfg)
		if err := awspkg.CreateTTLSchedule(ctx, schedulerClient, schedInput); err != nil {
			log.Error().Err(err).Str("sandbox_id", sandboxID).
				Msg("failed to create TTL schedule (non-fatal — sandbox is provisioned)")
		} else {
			fmt.Printf("  ✓ TTL schedule created (expires %s)\n", ttlExpiry.Local().Format("3:04 PM MST"))
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
			fmt.Printf("  ✓ Budget: compute $%.2f, AI $%.2f, warning at %.0f%%\n",
				computeLimit, aiLimit, warningThreshold*100)
		}
	}

	// Step 12c: Deploy per-sandbox budget-enforcer Lambda + EventBridge schedule.
	// Non-fatal: consistent with the "km create budget init is non-fatal" pattern
	// established in Phase 06-06. Sandbox is provisioned even if enforcer deploy fails.
	if artifacts.BudgetEnforcerHCL != "" {
		budgetEnforcerDir := filepath.Join(sandboxDir, "budget-enforcer")
		if mkErr := os.MkdirAll(budgetEnforcerDir, 0o755); mkErr != nil {
			log.Warn().Err(mkErr).Str("sandbox_id", sandboxID).
				Msg("failed to create budget-enforcer directory (non-fatal)")
		} else {
			hclPath := filepath.Join(budgetEnforcerDir, "terragrunt.hcl")
			if writeErr := os.WriteFile(hclPath, []byte(artifacts.BudgetEnforcerHCL), 0o644); writeErr != nil {
				log.Warn().Err(writeErr).Str("sandbox_id", sandboxID).
					Msg("failed to write budget-enforcer/terragrunt.hcl (non-fatal)")
			} else {
				if beErr := runner.Apply(ctx, budgetEnforcerDir); beErr != nil {
					log.Warn().Err(beErr).Str("sandbox_id", sandboxID).
						Msg("budget-enforcer apply failed (non-fatal — sandbox is provisioned)")
				} else {
					fmt.Printf("  ✓ Budget enforcer Lambda deployed\n")
					log.Info().Str("sandbox_id", sandboxID).Msg("budget enforcer Lambda deployed")
				}
			}
		}
	}

	// Step 12d: Generate and store safe phrase for email override authorization.
	// The safe phrase is a 32-char random hex string stored in SSM at /sandbox/{id}/safe-phrase.
	// It is shown once to the operator here — never stored in profile YAML.
	// Non-fatal: sandbox is provisioned even if safe phrase generation fails.
	{
		buf := make([]byte, 16)
		if _, randErr := cryptorand.Read(buf); randErr != nil {
			log.Warn().Err(randErr).Str("sandbox_id", sandboxID).
				Msg("Step 12d: failed to generate safe phrase random bytes (non-fatal)")
		} else {
			phrase := hex.EncodeToString(buf)
			phrasePath := "/sandbox/" + sandboxID + "/safe-phrase"
			phraseSMSClient := ssm.NewFromConfig(awsCfg)
			kmsKeyARNForPhrase := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
			if kmsKeyARNForPhrase == "" {
				kmsKeyARNForPhrase = "alias/km-platform"
			}
			_, phraseErr := phraseSMSClient.PutParameter(ctx, &ssm.PutParameterInput{
				Name:      aws.String(phrasePath),
				Value:     aws.String(phrase),
				Type:      ssmtypes.ParameterTypeSecureString,
				KeyId:     aws.String(kmsKeyARNForPhrase),
				Overwrite: aws.Bool(true),
			})
			if phraseErr != nil {
				log.Warn().Err(phraseErr).Str("sandbox_id", sandboxID).
					Msg("Step 12d: failed to store safe phrase in SSM (non-fatal)")
			} else {
				safeDomain := "sandboxes." + cfg.Domain
			if cfg.Domain == "" {
				safeDomain = "sandboxes.klankermaker.ai"
			}
			fmt.Printf("  ✓ Safe phrase: %s\n", phrase)
			fmt.Printf("    Email: %s@%s\n", sandboxID, safeDomain)
				log.Info().Str("sandbox_id", sandboxID).
					Msg("Step 12d: safe phrase stored in SSM")
			}
		}
	}

	// Step 13a: Generate GitHub App installation token and write to SSM.
	// Guarded by sourceAccess.github with non-empty allowedRepos — deny-by-default
	// ensures empty repos is treated the same as no github config.
	// Non-fatal: sandbox is provisioned even if GitHub token generation fails.
	if resolvedProfile.Spec.SourceAccess.GitHub != nil && len(resolvedProfile.Spec.SourceAccess.GitHub.AllowedRepos) > 0 {
		ssmClient := ssm.NewFromConfig(awsCfg)
		kmsKeyARN := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
		if kmsKeyARN == "" {
			kmsKeyARN = "alias/km-platform" // fallback — real key resolved by SSM
		}
		gh := resolvedProfile.Spec.SourceAccess.GitHub
		if tokenErr := generateAndStoreGitHubToken(ctx, ssmClient, sandboxID, kmsKeyARN, gh.AllowedRepos, nil); tokenErr != nil {
			if errors.Is(tokenErr, ErrGitHubNotConfigured) {
				fmt.Printf("  ⊘ GitHub token: skipped (not configured)\n")
			} else {
				log.Warn().Err(tokenErr).Str("sandbox_id", sandboxID).
					Msg("Step 13a: GitHub App token generation failed (non-fatal — sandbox is provisioned)")
			}
		} else {
			fmt.Printf("  ✓ GitHub token stored in SSM\n")
		}
	}

	// Step 13b: Deploy github-token/ Terragrunt directory.
	// Non-fatal: consistent with budget-enforcer pattern from Phase 06-06.
	// Sandbox is provisioned even if github-token Lambda deploy fails.
	if artifacts.GitHubTokenHCL != "" {
		githubTokenDir := filepath.Join(sandboxDir, "github-token")
		if mkErr := os.MkdirAll(githubTokenDir, 0o755); mkErr != nil {
			log.Warn().Err(mkErr).Str("sandbox_id", sandboxID).
				Msg("Step 13b: failed to create github-token directory (non-fatal)")
		} else {
			hclPath := filepath.Join(githubTokenDir, "terragrunt.hcl")
			if writeErr := os.WriteFile(hclPath, []byte(artifacts.GitHubTokenHCL), 0o644); writeErr != nil {
				log.Warn().Err(writeErr).Str("sandbox_id", sandboxID).
					Msg("Step 13b: failed to write github-token/terragrunt.hcl (non-fatal)")
			} else {
				if ghErr := runner.Apply(ctx, githubTokenDir); ghErr != nil {
					log.Warn().Err(ghErr).Str("sandbox_id", sandboxID).
						Msg("Step 13b: github-token apply failed (non-fatal — sandbox is provisioned)")
				} else {
					fmt.Printf("  ✓ GitHub token refresher Lambda deployed\n")
					log.Info().Str("sandbox_id", sandboxID).Msg("github-token refresher Lambda deployed")
				}
			}
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
		fmt.Printf("  ✓ Email: %s\n", emailAddr)
		log.Info().Str("email", emailAddr).Msg("sandbox email provisioned")
	}

	fmt.Println()
	fmt.Println(strings.Repeat("─", 50))
	elapsed := time.Since(createStart).Round(time.Second)
	fmt.Printf("Sandbox %s created successfully. (%s)\n", sandboxID, elapsed)
	if ttlExpiry != nil {
		fmt.Printf("  TTL: %s (expires %s)\n", resolvedProfile.Spec.Lifecycle.TTL, ttlExpiry.Local().Format("3:04:05 PM MST"))
	}

	// Step 14: Send lifecycle notification if operator email is configured.
	operatorEmail := cfg.OperatorEmail
	if operatorEmail == "" {
		operatorEmail = os.Getenv("KM_OPERATOR_EMAIL")
	}
	if operatorEmail != "" {
		profileName := ""
		ttl := ""
		if resolvedProfile.Metadata.Name != "" {
			profileName = resolvedProfile.Metadata.Name
		}
		if resolvedProfile.Spec.Lifecycle.TTL != "" {
			ttl = resolvedProfile.Spec.Lifecycle.TTL
		}
		if err := awspkg.SendCreateNotification(ctx, sesClient, operatorEmail, sandboxID, emailDomain, profileName, ttl); err != nil {
			log.Warn().Err(err).Msg("failed to send created notification (non-fatal)")
		}
	}

	// Step 15: Provision sandbox identity (Ed25519 signing key + DynamoDB public key).
	// Non-fatal: sandbox is provisioned even if identity setup fails.
	// Only runs when profile has an email section (email policy configured).
	if resolvedProfile.Spec.Email != nil {
		identitySMSClient := ssm.NewFromConfig(awsCfg)
		// KMS key alias: use platform KMS key ARN when set; fallback to alias/km-platform.
		// Same approach as Step 13a GitHub token provisioning.
		kmsKeyAlias := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
		if kmsKeyAlias == "" {
			kmsKeyAlias = "alias/km-platform" // fallback — real key resolved by SSM
		}

		pubKey, identErr := awspkg.GenerateSandboxIdentity(ctx, identitySMSClient, sandboxID, kmsKeyAlias)
		if identErr != nil {
			log.Warn().Err(identErr).Str("sandbox_id", sandboxID).
				Msg("failed to provision sandbox identity (non-fatal)")
		} else {
			// Conditionally generate X25519 encryption key if profile requires/allows encryption.
			var encPubKey *[32]byte
			enc := resolvedProfile.Spec.Email.Encryption
			if enc == "optional" || enc == "required" {
				encPubKey, identErr = awspkg.GenerateEncryptionKey(ctx, identitySMSClient, sandboxID, kmsKeyAlias)
				if identErr != nil {
					log.Warn().Err(identErr).Str("sandbox_id", sandboxID).
						Msg("failed to generate encryption key (non-fatal — signing key still published)")
				}
			}

			// Publish identity to DynamoDB.
			identityTableName := cfg.IdentityTableName
			if identityTableName == "" {
				identityTableName = "km-identities"
			}
			identityEmailAddr := fmt.Sprintf("%s@%s", sandboxID, emailDomain)
			dynamoIdentClient := dynamodbpkg.NewFromConfig(awsCfg)
			signing := resolvedProfile.Spec.Email.Signing
			verifyInbound := resolvedProfile.Spec.Email.VerifyInbound
			encryption := resolvedProfile.Spec.Email.Encryption
			alias := aliasOverride
			if alias == "" {
				alias = resolvedProfile.Spec.Email.Alias
			}
			allowedSenders := resolvedProfile.Spec.Email.AllowedSenders
			if pubErr := awspkg.PublishIdentity(ctx, dynamoIdentClient, identityTableName, sandboxID, identityEmailAddr, pubKey, encPubKey, signing, verifyInbound, encryption, alias, allowedSenders); pubErr != nil {
				log.Warn().Err(pubErr).Str("sandbox_id", sandboxID).
					Msg("failed to publish identity to DynamoDB (non-fatal)")
			} else {
				log.Info().Str("sandbox_id", sandboxID).Msg("sandbox identity provisioned and published")
				fmt.Printf("  ✓ Identity: Ed25519 key pair provisioned\n")
			}
		}
	}

	return nil
}

// runCreateDocker handles the full docker create workflow.
// It provisions a local sandbox via Docker Compose without any Terragrunt involvement.
// Steps: create sandbox dir, create IAM roles via SDK, assume roles for scoped creds,
// inject credentials into compose YAML, write docker-compose.yml, run docker compose up -d,
// write S3 metadata, write MLflow run record.
func runCreateDocker(ctx context.Context, cfg *config.Config, awsCfg aws.Config, resolvedProfile *profile.SandboxProfile, sandboxID string, artifacts *compiler.CompiledArtifacts, verbose bool, noBedrock bool, aliasOverride string) error {
	createStart := time.Now()
	fmt.Printf("\nProvisioning docker sandbox %s...\n", sandboxID)

	// Step D1: Create sandbox directory ~/.km/sandboxes/{sandboxID}/
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	sandboxLocalDir := filepath.Join(homeDir, ".km", "sandboxes", sandboxID)
	if err := os.MkdirAll(sandboxLocalDir, 0o700); err != nil {
		return fmt.Errorf("create sandbox local directory %s: %w", sandboxLocalDir, err)
	}
	fmt.Printf("  ✓ Sandbox directory created: %s\n", sandboxLocalDir)

	// Step D2: Get current AWS region and account ID for role naming.
	region := resolvedProfile.Spec.Runtime.Region
	if region == "" {
		region = awsCfg.Region
	}
	if region == "" {
		region = "us-east-1"
	}
	stsClient := sts.NewFromConfig(awsCfg)
	callerIDOut, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("get caller identity for docker role creation: %w", err)
	}
	accountID := aws.ToString(callerIDOut.Account)

	// Step D3: Create IAM roles via SDK (not Terraform).
	iamClient := iampkg.NewFromConfig(awsCfg)
	sandboxRoleName := fmt.Sprintf("km-docker-%s-%s", sandboxID, region)
	sidecarRoleName := fmt.Sprintf("km-sidecar-%s-%s", sandboxID, region)

	// Trust policy allows both ec2.amazonaws.com and the operator account for STS AssumeRole.
	sandboxTrustPolicy := fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {"Service": "ec2.amazonaws.com"},
      "Action": "sts:AssumeRole"
    },
    {
      "Effect": "Allow",
      "Principal": {"AWS": "arn:aws:iam::%s:root"},
      "Action": "sts:AssumeRole"
    }
  ]
}`, accountID)

	sidecarTrustPolicy := fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {"AWS": "arn:aws:iam::%s:root"},
      "Action": "sts:AssumeRole"
    }
  ]
}`, accountID)

	// Create sandbox role.
	sandboxRoleOut, sandboxRoleErr := iamClient.CreateRole(ctx, &iampkg.CreateRoleInput{
		RoleName:                 aws.String(sandboxRoleName),
		AssumeRolePolicyDocument: aws.String(sandboxTrustPolicy),
		Description:              aws.String(fmt.Sprintf("km docker sandbox role for %s", sandboxID)),
		Tags: []iamtypes.Tag{
			{Key: aws.String("km:sandbox-id"), Value: aws.String(sandboxID)},
			{Key: aws.String("km:substrate"), Value: aws.String("docker")},
		},
	})
	if sandboxRoleErr != nil {
		log.Warn().Err(sandboxRoleErr).Str("role", sandboxRoleName).Msg("failed to create sandbox IAM role (non-fatal)")
	} else {
		fmt.Printf("  ✓ IAM role created: %s\n", sandboxRoleName)
		// Attach inline policy scoping the sandbox role to its own resources.
		sandboxArtifactBucket := cfg.ArtifactsBucket
		if sandboxArtifactBucket == "" {
			sandboxArtifactBucket = os.Getenv("KM_ARTIFACTS_BUCKET")
		}
		var policyStatements []string
		if !noBedrock {
			policyStatements = append(policyStatements, `{
      "Effect": "Allow",
      "Action": ["bedrock:InvokeModel", "bedrock:InvokeModelWithResponseStream"],
      "Resource": "*"
    }`)
		}
		policyStatements = append(policyStatements, fmt.Sprintf(`{
      "Effect": "Allow",
      "Action": ["ssm:GetParameter", "ssm:GetParameters", "ssm:GetParametersByPath"],
      "Resource": "arn:aws:ssm:%s:%s:parameter/km/%s/*"
    }`, region, accountID, sandboxID))
		policyStatements = append(policyStatements, fmt.Sprintf(`{
      "Effect": "Allow",
      "Action": ["s3:PutObject", "s3:GetObject"],
      "Resource": "arn:aws:s3:::%s/sandboxes/%s/*"
    }`, sandboxArtifactBucket, sandboxID))
		sandboxPolicyDoc := fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    %s
  ]
}`, strings.Join(policyStatements, ",\n    "))
		_, putErr := iamClient.PutRolePolicy(ctx, &iampkg.PutRolePolicyInput{
			RoleName:       aws.String(sandboxRoleName),
			PolicyName:     aws.String("km-sandbox-inline"),
			PolicyDocument: aws.String(sandboxPolicyDoc),
		})
		if putErr != nil {
			log.Warn().Err(putErr).Str("role", sandboxRoleName).Msg("failed to attach inline policy to sandbox role (non-fatal)")
		}
	}

	// Create sidecar role.
	sidecarRoleOut, sidecarRoleErr := iamClient.CreateRole(ctx, &iampkg.CreateRoleInput{
		RoleName:                 aws.String(sidecarRoleName),
		AssumeRolePolicyDocument: aws.String(sidecarTrustPolicy),
		Description:              aws.String(fmt.Sprintf("km docker sidecar role for %s", sandboxID)),
		Tags: []iamtypes.Tag{
			{Key: aws.String("km:sandbox-id"), Value: aws.String(sandboxID)},
			{Key: aws.String("km:substrate"), Value: aws.String("docker")},
		},
	})
	if sidecarRoleErr != nil {
		log.Warn().Err(sidecarRoleErr).Str("role", sidecarRoleName).Msg("failed to create sidecar IAM role (non-fatal)")
	} else {
		fmt.Printf("  ✓ IAM role created: %s\n", sidecarRoleName)
		// Sidecar role inline policy: DynamoDB budget table + S3 audit/OTEL prefix.
		artifactBucket := cfg.ArtifactsBucket
		if artifactBucket == "" {
			artifactBucket = os.Getenv("KM_ARTIFACTS_BUCKET")
		}
		sidecarPolicyDoc := fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem"],
      "Resource": "arn:aws:dynamodb:%s:%s:table/km-budget-%s"
    },
    {
      "Effect": "Allow",
      "Action": ["s3:PutObject", "s3:GetObject"],
      "Resource": "arn:aws:s3:::%s/audit/%s/*"
    }
  ]
}`, region, accountID, sandboxID, artifactBucket, sandboxID)
		_, putErr := iamClient.PutRolePolicy(ctx, &iampkg.PutRolePolicyInput{
			RoleName:       aws.String(sidecarRoleName),
			PolicyName:     aws.String("km-sidecar-inline"),
			PolicyDocument: aws.String(sidecarPolicyDoc),
		})
		if putErr != nil {
			log.Warn().Err(putErr).Str("role", sidecarRoleName).Msg("failed to attach inline policy to sidecar role (non-fatal)")
		}
	}

	// Step D4: Wait for roles to propagate (IAM eventual consistency — Pitfall 4).
	// Poll GetRole until available, then wait additional 5s before AssumeRole.
	sandboxRoleARN := ""
	sidecarRoleARN := ""
	if sandboxRoleErr == nil && sandboxRoleOut.Role != nil {
		sandboxRoleARN = aws.ToString(sandboxRoleOut.Role.Arn)
		// Poll until role is reachable.
		for i := 0; i < 10; i++ {
			_, pollErr := iamClient.GetRole(ctx, &iampkg.GetRoleInput{RoleName: aws.String(sandboxRoleName)})
			if pollErr == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	if sidecarRoleErr == nil && sidecarRoleOut.Role != nil {
		sidecarRoleARN = aws.ToString(sidecarRoleOut.Role.Arn)
		// Poll until role is reachable.
		for i := 0; i < 10; i++ {
			_, pollErr := iamClient.GetRole(ctx, &iampkg.GetRoleInput{RoleName: aws.String(sidecarRoleName)})
			if pollErr == nil {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	// Step D6: Inject role ARNs into compose YAML.
	// Credentials are handled by cred-refresh sidecar (mounts host ~/.aws).
	composeYAML := artifacts.DockerComposeYAML
	composeYAML = strings.ReplaceAll(composeYAML, "PLACEHOLDER_SANDBOX_ROLE_ARN", sandboxRoleARN)
	composeYAML = strings.ReplaceAll(composeYAML, "PLACEHOLDER_SIDECAR_ROLE_ARN", sidecarRoleARN)

	// Step D6.5: Generate MITM CA cert for docker substrate.
	// The http-proxy sidecar reads KM_PROXY_CA_CERT (base64 PEM with cert+key).
	// The main container installs the cert portion into the OS trust store.
	caCertPEM, caKeyPEM, caErr := generateSelfSignedCA("km-proxy-ca")
	if caErr != nil {
		log.Warn().Err(caErr).Msg("failed to generate proxy CA cert (non-fatal, MITM inspection disabled)")
		composeYAML = strings.ReplaceAll(composeYAML, "PLACEHOLDER_PROXY_CA_B64", "")
	} else {
		// Proxy needs both cert+key concatenated, base64-encoded.
		combined := append(caCertPEM, caKeyPEM...)
		caB64 := base64.StdEncoding.EncodeToString(combined)
		composeYAML = strings.ReplaceAll(composeYAML, "PLACEHOLDER_PROXY_CA_B64", caB64)

		// Write cert to sandbox dir so main container can install it.
		certPath := filepath.Join(sandboxLocalDir, "km-proxy-ca.crt")
		if writeErr := os.WriteFile(certPath, caCertPEM, 0o644); writeErr != nil {
			log.Warn().Err(writeErr).Msg("failed to write CA cert file")
		}
		fmt.Printf("  ✓ MITM proxy CA cert generated\n")
	}

	// Step D7: Write docker-compose.yml to sandbox directory.
	composeFilePath := filepath.Join(sandboxLocalDir, "docker-compose.yml")
	if err := os.WriteFile(composeFilePath, []byte(composeYAML), 0o600); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}
	fmt.Printf("  ✓ docker-compose.yml written to %s\n", composeFilePath)

	// Step D7b: Write km-audit-init.sh — creates named pipe and shell audit hook.
	auditInitPath := filepath.Join(sandboxLocalDir, "km-audit-init.sh")
	auditInitScript := compiler.GenerateAuditInitScript(sandboxID)
	if err := os.WriteFile(auditInitPath, []byte(auditInitScript), 0o755); err != nil {
		return fmt.Errorf("write km-audit-init.sh: %w", err)
	}

	// Step D8: Write .km-ttl file with TTL expiry timestamp (ISO8601).
	now := time.Now().UTC()
	if resolvedProfile.Spec.Lifecycle.TTL != "" {
		if d, parseErr := time.ParseDuration(resolvedProfile.Spec.Lifecycle.TTL); parseErr == nil {
			ttlExpiry := now.Add(d)
			ttlPath := filepath.Join(sandboxLocalDir, ".km-ttl")
			if writeErr := os.WriteFile(ttlPath, []byte(ttlExpiry.Format(time.RFC3339)), 0o600); writeErr != nil {
				log.Warn().Err(writeErr).Str("sandbox_id", sandboxID).Msg("failed to write .km-ttl file (non-fatal)")
			}
		}
	}

	// Step D8.5: ECR docker login so compose can pull images.
	ecrRegistry := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", accountID, region)
	ecrLoginCmd := exec.CommandContext(ctx, "bash", "-c",
		fmt.Sprintf("aws ecr get-login-password --region %s --profile klanker-terraform | docker login --username AWS --password-stdin %s", region, ecrRegistry))
	if out, loginErr := ecrLoginCmd.CombinedOutput(); loginErr != nil {
		log.Warn().Err(loginErr).Str("output", string(out)).Msg("ECR docker login failed (non-fatal, images may fail to pull)")
	}

	// Step D9: Run `docker compose up -d`.
	// Use DockerComposeExecFunc (package-level var) so tests can override.
	if err := DockerComposeExecFunc(ctx, sandboxID, composeFilePath, verbose); err != nil {
		// Keep sandbox dir for debugging — user runs km destroy to clean up.
		fmt.Printf("  [warn] docker compose up failed — sandbox dir preserved at %s\n", sandboxLocalDir)
		return fmt.Errorf("docker compose up failed for sandbox %s: %w", sandboxID, err)
	}
	fmt.Printf("  ✓ docker compose up -d completed\n")

	// Step D10: Write S3 metadata so km list/status work unchanged.
	artifactBucket := cfg.ArtifactsBucket
	if artifactBucket == "" {
		artifactBucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	s3Client := s3.NewFromConfig(awsCfg)

	// Write MLflow run record (non-fatal).
	mlflowRun := awspkg.MLflowRun{
		SandboxID:   sandboxID,
		ProfileName: resolvedProfile.Metadata.Name,
		Substrate:   string(resolvedProfile.Spec.Runtime.Substrate),
		Region:      region,
		TTL:         resolvedProfile.Spec.Lifecycle.TTL,
		StartTime:   now,
		Experiment:  "klankrmkr",
	}
	if mlflowErr := awspkg.WriteMLflowRun(ctx, s3Client, artifactBucket, mlflowRun); mlflowErr != nil {
		log.Warn().Err(mlflowErr).Str("sandbox_id", sandboxID).Msg("failed to write MLflow run record (non-fatal)")
	}

	// Write sandbox metadata to DynamoDB (Docker substrate also uses DynamoDB — user explicitly required this).
	{
		var ttlExpiry *time.Time
		if resolvedProfile.Spec.Lifecycle.TTL != "" {
			if d, parseErr := time.ParseDuration(resolvedProfile.Spec.Lifecycle.TTL); parseErr == nil {
				t := now.Add(d)
				ttlExpiry = &t
			}
		}
		dockerTableName := cfg.SandboxTableName
		if dockerTableName == "" {
			dockerTableName = "km-sandboxes"
		}
		dockerDynamoClient := dynamodbpkg.NewFromConfig(awsCfg)

		sandboxAlias := aliasOverride

		meta := awspkg.SandboxMetadata{
			SandboxID:   sandboxID,
			ProfileName: resolvedProfile.Metadata.Name,
			Substrate:   "docker",
			Region:      region,
			CreatedAt:   now,
			TTLExpiry:   ttlExpiry,
			IdleTimeout: resolvedProfile.Spec.Lifecycle.IdleTimeout,
			MaxLifetime: resolvedProfile.Spec.Lifecycle.MaxLifetime,
			CreatedBy:   "cli",
			Alias:       sandboxAlias,
		}
		if writeErr := awspkg.WriteSandboxMetadataDynamo(ctx, dockerDynamoClient, dockerTableName, &meta); writeErr != nil {
			log.Warn().Err(writeErr).Str("sandbox_id", sandboxID).Msg("failed to write sandbox metadata to DynamoDB (non-fatal)")
		} else {
			if sandboxAlias != "" {
				fmt.Printf("  ✓ Metadata stored (alias: %s)\n", sandboxAlias)
			} else {
				fmt.Printf("  ✓ Metadata stored (substrate=docker)\n")
			}
		}
	}

	// Step D10.5: Provision sandbox identity (Ed25519 signing key + DynamoDB public key).
	// Non-fatal: sandbox is provisioned even if identity setup fails.
	// Only runs when profile has an email section (email policy configured).
	if resolvedProfile.Spec.Email != nil {
		identitySSMClient := ssm.NewFromConfig(awsCfg)
		kmsKeyAlias := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
		if kmsKeyAlias == "" {
			kmsKeyAlias = "alias/km-platform"
		}

		pubKey, identErr := awspkg.GenerateSandboxIdentity(ctx, identitySSMClient, sandboxID, kmsKeyAlias)
		if identErr != nil {
			log.Warn().Err(identErr).Str("sandbox_id", sandboxID).
				Msg("failed to provision sandbox identity (non-fatal)")
		} else {
			// Conditionally generate X25519 encryption key if profile requires/allows encryption.
			var encPubKey *[32]byte
			enc := resolvedProfile.Spec.Email.Encryption
			if enc == "optional" || enc == "required" {
				encPubKey, identErr = awspkg.GenerateEncryptionKey(ctx, identitySSMClient, sandboxID, kmsKeyAlias)
				if identErr != nil {
					log.Warn().Err(identErr).Str("sandbox_id", sandboxID).
						Msg("failed to generate encryption key (non-fatal — signing key still published)")
				}
			}

			// Publish identity to DynamoDB.
			// Derive email domain the same way as Step 13 in the main create path.
			dockerBaseDomain := cfg.Domain
			if dockerBaseDomain == "" {
				dockerBaseDomain = os.Getenv("KM_EMAIL_DOMAIN")
			}
			if dockerBaseDomain == "" {
				dockerBaseDomain = "klankermaker.ai"
			}
			emailDomain := "sandboxes." + dockerBaseDomain
			identityTableName := cfg.IdentityTableName
			if identityTableName == "" {
				identityTableName = "km-identities"
			}
			identityEmailAddr := fmt.Sprintf("%s@%s", sandboxID, emailDomain)
			dynamoIdentClient := dynamodbpkg.NewFromConfig(awsCfg)
			signing := resolvedProfile.Spec.Email.Signing
			verifyInbound := resolvedProfile.Spec.Email.VerifyInbound
			encryption := resolvedProfile.Spec.Email.Encryption
			alias := aliasOverride
			if alias == "" {
				alias = resolvedProfile.Spec.Email.Alias
			}
			allowedSenders := resolvedProfile.Spec.Email.AllowedSenders
			if pubErr := awspkg.PublishIdentity(ctx, dynamoIdentClient, identityTableName, sandboxID, identityEmailAddr, pubKey, encPubKey, signing, verifyInbound, encryption, alias, allowedSenders); pubErr != nil {
				log.Warn().Err(pubErr).Str("sandbox_id", sandboxID).
					Msg("failed to publish identity to DynamoDB (non-fatal)")
			} else {
				log.Info().Str("sandbox_id", sandboxID).Msg("sandbox identity provisioned and published")
				fmt.Printf("  ✓ Identity: Ed25519 key pair provisioned\n")
			}
		}
	}

	// Step D11: Print success banner.
	elapsed := time.Since(createStart).Round(time.Second)
	fmt.Println()
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("Sandbox %s created successfully (docker). (%s)\n", sandboxID, elapsed)
	if resolvedProfile.Spec.Lifecycle.TTL != "" {
		if d, err := time.ParseDuration(resolvedProfile.Spec.Lifecycle.TTL); err == nil {
			ttlExpiry := now.Add(d)
			fmt.Printf("  TTL: %s (expires %s)\n", resolvedProfile.Spec.Lifecycle.TTL, ttlExpiry.Local().Format("3:04:05 PM MST"))
		}
	}
	fmt.Printf("  Hint: km shell %s\n", sandboxID)

	return nil
}

// DockerComposeExecFunc is the package-level function for running docker compose up -d.
// Tests can replace this variable to avoid actually running docker.
var DockerComposeExecFunc = func(ctx context.Context, sandboxID, composeFilePath string, verbose bool) error {
	return runDockerComposeUp(ctx, sandboxID, composeFilePath, verbose)
}

// runDockerComposeUp runs `docker compose -f {path} -p km-{sandboxID} up -d`.
// generateSelfSignedCA creates an ephemeral CA certificate and private key for MITM proxy.
// Returns PEM-encoded cert and key as separate byte slices.
func generateSelfSignedCA(cn string) (certPEM []byte, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), cryptorand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %w", err)
	}

	serial, _ := cryptorand.Int(cryptorand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"klankermaker"}},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	certDER, err := x509.CreateCertificate(cryptorand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create CA cert: %w", err)
	}

	var certBuf, keyBuf bytes.Buffer
	pem.Encode(&certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	pem.Encode(&keyBuf, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certBuf.Bytes(), keyBuf.Bytes(), nil
}

func runDockerComposeUp(ctx context.Context, sandboxID, composeFilePath string, verbose bool) error {
	args := []string{"compose", "-f", composeFilePath, "-p", "km-" + sandboxID, "up", "-d"}
	dockerCmd := exec.CommandContext(ctx, "docker", args...)
	if verbose {
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
	} else {
		dockerCmd.Stdout = io.Discard
		dockerCmd.Stderr = io.Discard
	}
	return dockerCmd.Run()
}

// runCreateRemote compiles the profile locally, uploads artifacts to S3, and publishes
// a SandboxCreate event to EventBridge so the create-handler Lambda can run terragrunt
// in a compute environment that bundles the required binaries.
//
// This is the --remote dispatch path for km create. It performs Steps 1-7 of runCreate
// (parse, validate, generate ID, compile) but does NOT create a sandbox directory or
// run terragrunt locally. Instead it:
//  1. Uploads compiled artifacts to S3 under remote-create/{sandbox-id}/
//  2. Publishes a SandboxCreate EventBridge event with the artifact location
//
// The create-handler Lambda downloads the artifacts, runs km create as a subprocess,
// and sends notifications on success/failure.
func runCreateRemote(cfg *config.Config, profilePath string, onDemand bool, noBedrock bool, awsProfile string, aliasOverride string) error {
	ctx := context.Background()

	// Step 1: Read profile file
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		return fmt.Errorf("cannot read profile %s: %w", profilePath, err)
	}

	// Step 2: Parse profile
	parsed, err := profile.Parse(raw)
	if err != nil {
		return fmt.Errorf("failed to parse profile %s: %w", profilePath, err)
	}

	// Step 3: Resolve inheritance + validate
	var resolvedProfile *profile.SandboxProfile
	if parsed.Extends != "" {
		fileDir := filepath.Dir(profilePath)
		searchPaths := append([]string{fileDir}, cfg.ProfileSearchPaths...)
		resolvedProfile, err = profile.Resolve(parsed.Extends, searchPaths)
		if err != nil {
			return fmt.Errorf("failed to resolve extends %q: %w", parsed.Extends, err)
		}
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
	sandboxID := compiler.GenerateSandboxID(resolvedProfile.Metadata.Prefix)
	printBanner("km create --remote", sandboxID)

	// Step 5: Load AWS credentials
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed — check that profile %q is configured: %w", awsProfile, err)
	}

	// Step 5c: Enforce sandbox limit before dispatching remote create.
	if cfg.StateBucket != "" {
		s3Client := s3.NewFromConfig(awsCfg)
		activeCount, limitErr := checkSandboxLimit(ctx, s3Client, cfg.StateBucket, cfg.MaxSandboxes)
		if limitErr != nil {
			if cfg.OperatorEmail != "" {
				sesClient := sesv2.NewFromConfig(awsCfg)
				notifDomain := cfg.Domain
				if notifDomain == "" {
					notifDomain = "klankermaker.ai"
				}
				if notifErr := awspkg.SendLimitNotification(ctx, sesClient, cfg.OperatorEmail, sandboxID, notifDomain, activeCount, cfg.MaxSandboxes); notifErr != nil {
					log.Warn().Err(notifErr).Msg("failed to send sandbox limit notification (non-fatal)")
				}
			}
			fmt.Fprintf(os.Stderr, "\nERROR: %s\n", limitErr)
			return limitErr
		}
	}

	// Step 6: Load network config for compilation
	repoRoot := findRepoRoot()
	region := resolvedProfile.Spec.Runtime.Region
	regionLabel := compiler.RegionLabel(region)
	networkOutputs, err := LoadNetworkOutputs(repoRoot, regionLabel)
	if err != nil {
		return fmt.Errorf("failed to load network config for %s: %w\nRun 'km init --region %s' first", region, err, region)
	}
	networkDomain := cfg.Domain
	if networkDomain == "" {
		networkDomain = "klankermaker.ai"
	}
	remoteArtifactsBucket := cfg.ArtifactsBucket
	if remoteArtifactsBucket == "" {
		remoteArtifactsBucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	network := &compiler.NetworkConfig{
		VPCID:             networkOutputs.VPCID,
		PublicSubnets:     networkOutputs.PublicSubnets,
		AvailabilityZones: networkOutputs.AvailabilityZones,
		RegionLabel:       regionLabel,
		EmailDomain:       "sandboxes." + networkDomain,
		ArtifactsBucket:   remoteArtifactsBucket,
	}

	// --no-bedrock: disable Bedrock access entirely
	if noBedrock {
		resolvedProfile.Spec.Execution.UseBedrock = false
		stripBedrockEnvVars(resolvedProfile)
	}

	// Step 7: Compile profile into artifacts
	artifacts, err := compiler.Compile(resolvedProfile, sandboxID, onDemand, network)
	if err != nil {
		return fmt.Errorf("failed to compile profile: %w", err)
	}

	// Determine artifact bucket
	artifactBucket := cfg.ArtifactsBucket
	if artifactBucket == "" {
		artifactBucket = os.Getenv("KM_ARTIFACTS_BUCKET")
	}
	if artifactBucket == "" {
		return fmt.Errorf("artifact bucket not configured — set KM_ARTIFACTS_BUCKET or configure via km configure")
	}

	// Determine operator email
	remoteOperatorEmail := cfg.OperatorEmail
	if remoteOperatorEmail == "" {
		remoteOperatorEmail = os.Getenv("KM_OPERATOR_EMAIL")
	}

	// Step 8: Upload compiled artifacts to S3 under remote-create/{sandbox-id}/
	artifactPrefix := "remote-create/" + sandboxID
	s3Client := s3.NewFromConfig(awsCfg)

	type artifact struct {
		key     string
		content string
		mime    string
	}
	toUpload := []artifact{
		{key: artifactPrefix + "/service.hcl", content: artifacts.ServiceHCL, mime: "text/plain"},
		{key: artifactPrefix + "/user-data.sh", content: artifacts.UserData, mime: "text/plain"},
		{key: artifactPrefix + "/.km-profile.yaml", content: profileYAMLForUpload(resolvedProfile, raw, noBedrock), mime: "application/x-yaml"},
	}
	if artifacts.BudgetEnforcerHCL != "" {
		toUpload = append(toUpload, artifact{
			key:     artifactPrefix + "/budget-enforcer.hcl",
			content: artifacts.BudgetEnforcerHCL,
			mime:    "text/plain",
		})
	}
	if artifacts.GitHubTokenHCL != "" {
		toUpload = append(toUpload, artifact{
			key:     artifactPrefix + "/github-token.hcl",
			content: artifacts.GitHubTokenHCL,
			mime:    "text/plain",
		})
	}

	fmt.Printf("  Uploading artifacts to s3://%s/%s/\n", artifactBucket, artifactPrefix)
	for _, a := range toUpload {
		_, putErr := s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(artifactBucket),
			Key:         aws.String(a.key),
			Body:        strings.NewReader(a.content),
			ContentType: aws.String(a.mime),
		})
		if putErr != nil {
			return fmt.Errorf("upload artifact %s: %w", a.key, putErr)
		}
	}

	sandboxAlias := aliasOverride

	// Step 8b: Write "starting" metadata to DynamoDB so km list shows the sandbox immediately.
	remoteSubstrateLabel := string(resolvedProfile.Spec.Runtime.Substrate)
	if resolvedProfile.Spec.Runtime.Substrate == "ec2" {
		if resolvedProfile.Spec.Runtime.Spot && !onDemand {
			remoteSubstrateLabel = "ec2spot"
		} else {
			remoteSubstrateLabel = "ec2demand"
		}
	}
	tableName := cfg.SandboxTableName
	if tableName == "" {
		tableName = "km-sandboxes"
	}
	dynamoClient := dynamodbpkg.NewFromConfig(awsCfg)
	startingMeta := &awspkg.SandboxMetadata{
		SandboxID:   sandboxID,
		ProfileName: resolvedProfile.Metadata.Name,
		Substrate:   remoteSubstrateLabel,
		Region:      resolvedProfile.Spec.Runtime.Region,
		Status:      "starting",
		CreatedAt:   time.Now().UTC(),
		IdleTimeout: resolvedProfile.Spec.Lifecycle.IdleTimeout,
		MaxLifetime: resolvedProfile.Spec.Lifecycle.MaxLifetime,
		CreatedBy:   "remote",
		Alias:       sandboxAlias,
	}
	if writeErr := awspkg.WriteSandboxMetadataDynamo(ctx, dynamoClient, tableName, startingMeta); writeErr != nil {
		fmt.Fprintf(os.Stderr, "  [warn] failed to write provisioning metadata: %v\n", writeErr)
	} else {
		fmt.Printf("  ✓ Metadata stored (status: starting)\n")
	}

	// Step 9: Publish SandboxCreate event to EventBridge
	ebClient := eventbridge.NewFromConfig(awsCfg)
	detail := awspkg.SandboxCreateDetail{
		SandboxID:      sandboxID,
		ArtifactBucket: artifactBucket,
		ArtifactPrefix: artifactPrefix,
		OperatorEmail:  remoteOperatorEmail,
		OnDemand:       onDemand,
		Alias:          sandboxAlias,
	}
	if ebErr := awspkg.PutSandboxCreateEvent(ctx, ebClient, detail); ebErr != nil {
		return fmt.Errorf("publish SandboxCreate event: %w", ebErr)
	}

	fmt.Println()
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("Remote create dispatched: %s\n", sandboxID)
	fmt.Printf("  Artifacts: s3://%s/%s/\n", artifactBucket, artifactPrefix)
	fmt.Printf("  The create-handler Lambda will provision the sandbox and send a notification.\n")

	return nil
}

// generateAndStoreGitHubToken reads GitHub App credentials from SSM, generates an
// installation token, and writes it to SSM at /sandbox/{sandboxID}/github-token.
//
// Called from runCreate when profile.Spec.SourceAccess.GitHub is non-nil.
// Returns ErrGitHubNotConfigured when any SSM parameter is missing (ParameterNotFound).
// Returns a wrapped error for all other failures — the caller treats this as non-fatal.
func generateAndStoreGitHubToken(ctx context.Context, ssmClient SSMGetPutAPI, sandboxID, kmsKeyARN string, allowedRepos, permissions []string) error {
	withDecryption := true

	appClientIDOut, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/km/config/github/app-client-id"),
		WithDecryption: &withDecryption,
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return ErrGitHubNotConfigured
		}
		return fmt.Errorf("read app-client-id from SSM: %w", err)
	}

	privateKeyOut, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/km/config/github/private-key"),
		WithDecryption: &withDecryption,
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return ErrGitHubNotConfigured
		}
		return fmt.Errorf("read private-key from SSM: %w", err)
	}

	installIDOut, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String("/km/config/github/installation-id"),
		WithDecryption: &withDecryption,
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return ErrGitHubNotConfigured
		}
		return fmt.Errorf("read installation-id from SSM: %w", err)
	}

	appClientID := *appClientIDOut.Parameter.Value
	privateKeyPEM := []byte(*privateKeyOut.Parameter.Value)
	installationID := *installIDOut.Parameter.Value

	jwtToken, err := githubpkg.GenerateGitHubAppJWT(appClientID, privateKeyPEM)
	if err != nil {
		return fmt.Errorf("generate GitHub App JWT: %w", err)
	}

	perms := githubpkg.CompilePermissions(permissions)
	token, err := githubpkg.ExchangeForInstallationToken(ctx, jwtToken, installationID, allowedRepos, perms)
	if err != nil {
		return fmt.Errorf("exchange JWT for installation token: %w", err)
	}

	if err := githubpkg.WriteTokenToSSM(ctx, ssmClient, sandboxID, token, kmsKeyARN, false); err != nil {
		return fmt.Errorf("write token to SSM: %w", err)
	}

	return nil
}

// findRepoRoot locates the repository root by walking up from the executable
// or the current working directory looking for a CLAUDE.md anchor file.
// Falls back to the current working directory if not found.
func findRepoRoot() string {
	// Environment override for Lambda/container contexts where runtime.Caller
	// and CWD don't point to the repo root.
	if envRoot := os.Getenv("KM_REPO_ROOT"); envRoot != "" {
		return envRoot
	}

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

// checkSandboxLimit checks if the active sandbox count has reached the configured limit.
// Returns (activeCount, error). Error is non-nil when limit is reached.
// If maxSandboxes is 0, the check is skipped (unlimited).
// Active sandboxes are those whose Status != "destroyed".
// Reads from DynamoDB (with S3 fallback on ResourceNotFoundException).
func checkSandboxLimit(ctx context.Context, s3Client awspkg.S3ListAPI, bucket string, maxSandboxes int) (int, error) {
	if maxSandboxes <= 0 {
		return 0, nil
	}
	// Try DynamoDB if s3Client carries an awsCfg context — instead just try S3 here
	// since checkSandboxLimit is called before the DynamoDB client is wired.
	// The DynamoDB-powered path goes through newRealLister used by km list.
	// checkSandboxLimit is a non-critical best-effort check so S3 is fine here.
	records, err := awspkg.ListAllSandboxesByS3(ctx, s3Client, bucket)
	if err != nil {
		// Non-fatal: if we can't list, allow creation (don't block on list failure)
		log.Warn().Err(err).Msg("checkSandboxLimit: failed to list sandboxes — skipping limit check")
		return 0, nil
	}
	activeCount := 0
	for _, r := range records {
		if r.Status != "destroyed" {
			activeCount++
		}
	}
	if activeCount >= maxSandboxes {
		return activeCount, fmt.Errorf("sandbox limit reached (%d/%d) — increase max_sandboxes in km-config.yaml or destroy unused sandboxes", activeCount, maxSandboxes)
	}
	return activeCount, nil
}

// profileYAMLForUpload returns the profile YAML to upload for remote create.
// If the profile was modified (e.g. --no-bedrock), applies targeted text
// replacements to the original YAML rather than re-marshaling (which would
// emit zero-value fields that the schema rejects).
func profileYAMLForUpload(_ *profile.SandboxProfile, raw []byte, noBedrock bool) string {
	if !noBedrock {
		return string(raw)
	}
	s := string(raw)
	// Set useBedrock to false
	s = strings.Replace(s, "useBedrock: true", "useBedrock: false", 1)
	// Remove Bedrock-specific env vars from the YAML
	for _, line := range []string{
		"GOOSE_PROVIDER: aws_bedrock",
		"CLAUDE_CODE_USE_BEDROCK: \"1\"",
		"CLAUDE_CODE_USE_BEDROCK: 1",
	} {
		s = strings.Replace(s, line, "", 1)
	}
	return s
}

// stripBedrockEnvVars removes Bedrock-related environment variables from
// the profile's spec.execution.env map. Called when --no-bedrock is set.
func stripBedrockEnvVars(p *profile.SandboxProfile) {
	if p.Spec.Execution.Env == nil {
		return
	}
	bedrockKeys := []string{
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	}
	for _, k := range bedrockKeys {
		delete(p.Spec.Execution.Env, k)
	}
	// Strip GOOSE_PROVIDER if it's set to aws_bedrock
	if p.Spec.Execution.Env["GOOSE_PROVIDER"] == "aws_bedrock" {
		delete(p.Spec.Execution.Env, "GOOSE_PROVIDER")
	}
	// Strip GOOSE_MODEL if it references a bedrock model ID
	if v, ok := p.Spec.Execution.Env["GOOSE_MODEL"]; ok {
		if strings.Contains(v, "anthropic.claude") {
			delete(p.Spec.Execution.Env, "GOOSE_MODEL")
		}
	}
}

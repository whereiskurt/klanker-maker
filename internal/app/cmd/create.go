package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	dynamodbpkg "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	iampkg "github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
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
	var awsProfile string
	var verbose bool

	cmd := &cobra.Command{
		Use:   "create <profile.yaml>",
		Short: "Provision a new sandbox from a profile",
		Long:  helpText("create"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" {
				awsProfile = "klanker-terraform"
			}
			return runCreate(cfg, args[0], onDemand, awsProfile, verbose)
		},
	}

	cmd.Flags().BoolVar(&onDemand, "on-demand", false,
		"Override spot: true in the profile — use on-demand instances instead")
	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-terraform",
		"AWS CLI profile to use")
	cmd.Flags().BoolVar(&verbose, "verbose", false,
		"Show full terragrunt/terraform output")

	return cmd
}

// runCreate executes the full create workflow.
func runCreate(cfg *config.Config, profilePath string, onDemand bool, awsProfile string, verbose bool) error {
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

	// Step 5b: Export config values as env vars for Terragrunt's site.hcl get_env() calls.
	if cfg.ApplicationAccountID != "" && os.Getenv("KM_ACCOUNTS_APPLICATION") == "" {
		os.Setenv("KM_ACCOUNTS_APPLICATION", cfg.ApplicationAccountID)
	}
	if cfg.ManagementAccountID != "" && os.Getenv("KM_ACCOUNTS_MANAGEMENT") == "" {
		os.Setenv("KM_ACCOUNTS_MANAGEMENT", cfg.ManagementAccountID)
	}
	if cfg.Domain != "" && os.Getenv("KM_DOMAIN") == "" {
		os.Setenv("KM_DOMAIN", cfg.Domain)
	}
	if cfg.PrimaryRegion != "" && os.Getenv("KM_REGION") == "" {
		os.Setenv("KM_REGION", cfg.PrimaryRegion)
	}
	if cfg.ArtifactsBucket != "" && os.Getenv("KM_ARTIFACTS_BUCKET") == "" {
		os.Setenv("KM_ARTIFACTS_BUCKET", cfg.ArtifactsBucket)
	}
	if cfg.Route53ZoneID != "" && os.Getenv("KM_ROUTE53_ZONE_ID") == "" {
		os.Setenv("KM_ROUTE53_ZONE_ID", cfg.Route53ZoneID)
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

	// Step 10: Run terragrunt apply (streams output in real time when --verbose; quiet by default)
	fmt.Printf("  Provisioning infrastructure...")
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = verbose
	if err := runner.Apply(ctx, sandboxDir); err != nil {
		fmt.Println() // newline after progress indicator
		// Do NOT run destroy — resources may be partially created and require
		// manual cleanup. Only remove the local sandbox directory.
		fmt.Fprintf(os.Stderr, "ERROR: terragrunt apply failed: %v\n", err)
		if cleanErr := terragrunt.CleanupSandboxDir(sandboxDir); cleanErr != nil {
			log.Warn().Err(cleanErr).Msg("failed to clean up sandbox directory after apply failure")
		}
		return fmt.Errorf("provisioning failed for sandbox %s", sandboxID)
	}

	fmt.Println(" done")

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
		Substrate:   string(resolvedProfile.Spec.Runtime.Substrate),
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

	fmt.Printf("  Setting up TTL, budget, email, identity...")
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
				fmt.Printf("Step 12c: Deploying budget enforcer Lambda for %s...\n", sandboxID)
				if beErr := runner.Apply(ctx, budgetEnforcerDir); beErr != nil {
					log.Warn().Err(beErr).Str("sandbox_id", sandboxID).
						Msg("budget-enforcer apply failed (non-fatal — sandbox is provisioned)")
				} else {
					log.Info().Str("sandbox_id", sandboxID).Msg("budget enforcer Lambda deployed")
				}
			}
		}
	}

	// Step 13a: Generate GitHub App installation token and write to SSM.
	// Guarded by sourceAccess.github — only runs for GitHub-enabled profiles.
	// Non-fatal: sandbox is provisioned even if GitHub token generation fails.
	if resolvedProfile.Spec.SourceAccess.GitHub != nil {
		ssmClient := ssm.NewFromConfig(awsCfg)
		kmsKeyARN := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
		if kmsKeyARN == "" {
			kmsKeyARN = "alias/km-platform" // fallback — real key resolved by SSM
		}
		gh := resolvedProfile.Spec.SourceAccess.GitHub
		if tokenErr := generateAndStoreGitHubToken(ctx, ssmClient, sandboxID, kmsKeyARN, gh.AllowedRepos, gh.Permissions); tokenErr != nil {
			if errors.Is(tokenErr, ErrGitHubNotConfigured) {
				fmt.Printf("Step 13a: GitHub token skipped (not configured)\n")
			} else {
				log.Warn().Err(tokenErr).Str("sandbox_id", sandboxID).
					Msg("Step 13a: GitHub App token generation failed (non-fatal — sandbox is provisioned)")
			}
		} else {
			fmt.Printf("Step 13a: GitHub App installation token stored in SSM\n")
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
				fmt.Printf("Step 13b: Deploying GitHub token refresher Lambda for %s...\n", sandboxID)
				if ghErr := runner.Apply(ctx, githubTokenDir); ghErr != nil {
					log.Warn().Err(ghErr).Str("sandbox_id", sandboxID).
						Msg("Step 13b: github-token apply failed (non-fatal — sandbox is provisioned)")
				} else {
					log.Info().Str("sandbox_id", sandboxID).Msg("github-token refresher Lambda deployed")
					fmt.Printf("Step 13b: GitHub token refresher Lambda deployed for %s\n", sandboxID)
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
		log.Info().Str("email", emailAddr).Msg("sandbox email provisioned")
		fmt.Fprintf(os.Stdout, "Email: %s\n", emailAddr)
	}

	fmt.Println(" done")
	if ttlExpiry != nil {
		fmt.Printf("  TTL: %s (expires %s)\n", resolvedProfile.Spec.Lifecycle.TTL, ttlExpiry.Format("15:04:05"))
	}
	fmt.Printf("Sandbox %s created successfully.\n", sandboxID)

	// Step 14: Send lifecycle notification if operator email is configured.
	if operatorEmail := os.Getenv("KM_OPERATOR_EMAIL"); operatorEmail != "" {
		if err := awspkg.SendLifecycleNotification(ctx, sesClient, operatorEmail, sandboxID, "created", emailDomain); err != nil {
			log.Warn().Err(err).Msg("failed to send created lifecycle notification (non-fatal)")
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
			alias := resolvedProfile.Spec.Email.Alias
			allowedSenders := resolvedProfile.Spec.Email.AllowedSenders
			if pubErr := awspkg.PublishIdentity(ctx, dynamoIdentClient, identityTableName, sandboxID, identityEmailAddr, pubKey, encPubKey, signing, verifyInbound, encryption, alias, allowedSenders); pubErr != nil {
				log.Warn().Err(pubErr).Str("sandbox_id", sandboxID).
					Msg("failed to publish identity to DynamoDB (non-fatal)")
			} else {
				log.Info().Str("sandbox_id", sandboxID).Msg("sandbox identity provisioned and published")
				fmt.Printf("Step 15: Sandbox identity provisioned for %s\n", sandboxID)
			}
		}
	}

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

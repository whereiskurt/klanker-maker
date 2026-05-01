package cmd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
	"github.com/whereiskurt/klankrmkr/pkg/version"
	"gopkg.in/yaml.v3"
)

// InitRunner is the interface for applying Terragrunt modules.
// It is implemented by *terragrunt.Runner and by test mocks.
type InitRunner interface {
	Apply(ctx context.Context, dir string) error
	Output(ctx context.Context, dir string) (map[string]interface{}, error)
}

// NetworkOutputs holds the Terraform outputs from the shared network module.
type NetworkOutputs struct {
	VPCID             string   `json:"vpc_id"`
	PublicSubnets     []string `json:"public_subnets"`
	AvailabilityZones []string `json:"availability_zones"`
	SandboxMgmtSGID   string   `json:"sandbox_mgmt_sg_id"`
}

// regionalModule describes a single regional infrastructure module.
type regionalModule struct {
	name    string
	dir     string
	envReqs []string // environment variables required to apply this module
}

// RegionalModule is the exported view of a regional infrastructure module.
// Used by tests to inspect module order without importing internal fields.
type RegionalModule struct {
	Name string
	Dir  string
}

// RegionalModules returns the ordered list of regional infrastructure modules
// as exported RegionalModule values. Exported for testing only.
func RegionalModules(regionDir string) []RegionalModule {
	internal := regionalModules(regionDir)
	out := make([]RegionalModule, len(internal))
	for i, m := range internal {
		out[i] = RegionalModule{Name: m.name, Dir: m.dir}
	}
	return out
}

// regionalModules returns the ordered slice of regional infrastructure modules
// for the given region directory. Modules are returned in dependency order.
func regionalModules(regionDir string) []regionalModule {
	return []regionalModule{
		{
			name:    "network",
			dir:     filepath.Join(regionDir, "network"),
			envReqs: nil,
		},
		{
			// efs depends on network: its terragrunt.hcl reads network/outputs.json.
			// Must come after network so outputs.json is present before EFS apply.
			name:    "efs",
			dir:     filepath.Join(regionDir, "efs"),
			envReqs: nil,
		},
		{
			name:    "dynamodb-budget",
			dir:     filepath.Join(regionDir, "dynamodb-budget"),
			envReqs: nil,
		},
		{
			name:    "dynamodb-identities",
			dir:     filepath.Join(regionDir, "dynamodb-identities"),
			envReqs: nil,
		},
		{
			name:    "dynamodb-sandboxes",
			dir:     filepath.Join(regionDir, "dynamodb-sandboxes"),
			envReqs: nil,
		},
		{
			name:    "dynamodb-schedules",
			dir:     filepath.Join(regionDir, "dynamodb-schedules"),
			envReqs: nil,
		},
		{
			name:    "ssm-session-doc",
			dir:     filepath.Join(regionDir, "ssm-session-doc"),
			envReqs: nil,
		},
		{
			name:    "s3-replication",
			dir:     filepath.Join(regionDir, "s3-replication"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
		{
			// create-handler must apply before ttl-handler and email-handler
			// so its ARN is available via KM_CREATE_HANDLER_ARN.
			name:    "create-handler",
			dir:     filepath.Join(regionDir, "create-handler"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
		{
			name:    "ttl-handler",
			dir:     filepath.Join(regionDir, "ttl-handler"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
		{
			name:    "email-handler",
			dir:     filepath.Join(regionDir, "email-handler"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
		{
			// Phase 63: DynamoDB nonce table for Slack bridge replay protection.
			// Must apply before lambda-slack-bridge (dependency in terragrunt.hcl).
			name:    "dynamodb-slack-nonces",
			dir:     filepath.Join(regionDir, "dynamodb-slack-nonces"),
			envReqs: nil,
		},
		{
			// Phase 63: Slack-notify bridge Lambda with Function URL (auth=NONE;
			// Ed25519 + nonce provide application-layer auth). Depends on
			// dynamodb-identities, dynamodb-sandboxes, and dynamodb-slack-nonces.
			name:    "lambda-slack-bridge",
			dir:     filepath.Join(regionDir, "lambda-slack-bridge"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
		{
			// SES must apply LAST because it owns the consolidated S3 bucket policy.
			// The email-handler must apply before SES so its ARN is available for
			// the operator-inbound receipt rule.
			name:    "ses",
			dir:     filepath.Join(regionDir, "ses"),
			envReqs: []string{"KM_ROUTE53_ZONE_ID"},
		},
	}
}

func NewInitCmd(cfg *config.Config) *cobra.Command {
	var awsProfile string
	var region string
	var verbose bool
	var sidecarsOnly bool
	var lambdasOnly bool
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize all regional infrastructure (network, DynamoDB, SES, S3 replication, TTL handler)",
		Long:  helpText("init"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" {
				awsProfile = "klanker-application"
			}
			if sidecarsOnly || lambdasOnly {
				return runInitPartial(cfg, awsProfile, region, verbose, sidecarsOnly, lambdasOnly)
			}
			if dryRun {
				return runInitDryRun(cfg, region)
			}
			return runInit(cfg, awsProfile, region, verbose)
		},
	}

	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-application",
		"AWS CLI profile to use for provisioning")
	cmd.Flags().StringVar(&region, "region", "us-east-1",
		"AWS region to initialize (e.g. us-east-1, ca-central-1)")
	cmd.Flags().BoolVar(&verbose, "verbose", false,
		"Show full terragrunt/terraform output")
	cmd.Flags().BoolVar(&sidecarsOnly, "sidecars", false,
		"Only rebuild and upload sidecars + km binary + toolchain (skip Terraform)")
	cmd.Flags().BoolVar(&lambdasOnly, "lambdas", false,
		"Only rebuild and deploy Lambda functions (skip Terraform)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", true,
		"Show what would be initialized without making changes (use --dry-run=false to execute)")

	return cmd
}

// runInitDryRun prints what km init would do without making any changes.
func runInitDryRun(cfg *config.Config, region string) error {
	repoRoot := findRepoRoot()
	regionDir := filepath.Join(repoRoot, "infra", "live", region)

	printBanner("km init --dry-run", fmt.Sprintf("%s (%s)", region, compiler.RegionLabel(region)))

	fmt.Println()
	fmt.Println("The following steps would be performed:")
	fmt.Println()

	// DNS
	if cfg.Domain != "" {
		fmt.Printf("  1. DNS: ensure hosted zone and NS delegation for sandboxes.%s\n", cfg.Domain)
	} else {
		fmt.Printf("  1. DNS: [skip] no domain configured\n")
	}

	// Builds
	fmt.Printf("  2. Build Lambda zips\n")
	if cfg.ArtifactsBucket != "" {
		fmt.Printf("  3. Build and upload sidecars to s3://%s\n", cfg.ArtifactsBucket)
		fmt.Printf("  4. Build and push km-sandbox container image to ECR\n")
		fmt.Printf("  5. Build and push sidecar container images to ECR\n")
		fmt.Printf("  6. Upload create-handler toolchain to s3://%s\n", cfg.ArtifactsBucket)
		fmt.Printf("  7. Force create-handler Lambda cold start\n")
		fmt.Printf("  8. Ensure proxy CA certificate in s3://%s\n", cfg.ArtifactsBucket)
	} else {
		fmt.Printf("  3. [skip] artifacts_bucket not configured — sidecar/toolchain uploads skipped\n")
	}

	// SSM + identity
	if cfg.SafePhrase != "" {
		fmt.Printf("  9. Write safe phrase to SSM /km/config/remote-create/safe-phrase\n")
	}
	fmt.Printf(" 10. Ensure operator email identity (Ed25519 signing key)\n")

	// Terraform modules
	// Build a map of env vars that runInit would set from config before applying.
	configEnv := map[string]bool{
		"KM_ARTIFACTS_BUCKET": cfg.ArtifactsBucket != "",
		"KM_ROUTE53_ZONE_ID":  cfg.Route53ZoneID != "",
		"KM_DOMAIN":           cfg.Domain != "",
		"KM_REGION":           cfg.PrimaryRegion != "",
		"KM_OPERATOR_EMAIL":   cfg.OperatorEmail != "",
		"KM_SCHEDULER_ROLE_ARN": cfg.SchedulerRoleARN != "",
		"KM_ACCOUNTS_MANAGEMENT":  cfg.ManagementAccountID != "",
		"KM_ACCOUNTS_APPLICATION": cfg.ApplicationAccountID != "",
	}

	fmt.Printf(" 11. Apply regional infrastructure modules (in order):\n")
	modules := regionalModules(regionDir)
	for i, m := range modules {
		skip := ""
		for _, env := range m.envReqs {
			if os.Getenv(env) == "" && !configEnv[env] {
				skip = fmt.Sprintf(" [skip: %s not set]", env)
				break
			}
		}
		relDir := m.dir
		if rel, err := filepath.Rel(repoRoot, m.dir); err == nil {
			relDir = rel
		}
		fmt.Printf("     %2d. %-25s %s%s\n", i+1, m.name, relDir, skip)
	}

	fmt.Println()
	fmt.Println("Run 'km init --dry-run=false' to execute.")
	return nil
}

func runInit(cfg *config.Config, awsProfile, region string, verbose bool) error {
	ctx := context.Background()

	// Validate AWS credentials
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}

	// Export config values as env vars for Terragrunt's site.hcl get_env() calls
	// and for the envReqs checks in regionalModules.
	if cfg.ArtifactsBucket != "" && os.Getenv("KM_ARTIFACTS_BUCKET") == "" {
		os.Setenv("KM_ARTIFACTS_BUCKET", cfg.ArtifactsBucket)
	}
	if cfg.ManagementAccountID != "" && os.Getenv("KM_ACCOUNTS_MANAGEMENT") == "" {
		os.Setenv("KM_ACCOUNTS_MANAGEMENT", cfg.ManagementAccountID)
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
	if cfg.OperatorEmail != "" && os.Getenv("KM_OPERATOR_EMAIL") == "" {
		os.Setenv("KM_OPERATOR_EMAIL", cfg.OperatorEmail)
	}
	if cfg.SchedulerRoleARN != "" && os.Getenv("KM_SCHEDULER_ROLE_ARN") == "" {
		os.Setenv("KM_SCHEDULER_ROLE_ARN", cfg.SchedulerRoleARN)
	}

	repoRoot := findRepoRoot()

	printBanner("km init", fmt.Sprintf("%s (%s)", region, compiler.RegionLabel(region)))

	// Always ensure sandboxes.{domain} hosted zone AND NS delegation exist.
	// Even if the zone ID is known, delegation in the management account may be missing.
	if cfg.Domain != "" {
		fmt.Println()
		fmt.Printf("Ensuring DNS zone and NS delegation for sandboxes.%s...\n", cfg.Domain)
		zoneID, err := ensureSandboxHostedZone(ctx, cfg)
		if err != nil {
			fmt.Printf("  [warn] DNS zone setup failed: %v\n", err)
			fmt.Printf("  SES will be skipped. Set KM_ROUTE53_ZONE_ID manually to enable.\n")
		} else {
			fmt.Printf("  DNS zone ready: %s\n", zoneID)
			if os.Getenv("KM_ROUTE53_ZONE_ID") == "" {
				os.Setenv("KM_ROUTE53_ZONE_ID", zoneID)
				// Persist to km-config.yaml so future runs don't repeat this
				if persistErr := persistRoute53ZoneID(zoneID); persistErr != nil {
					fmt.Printf("  [warn] Could not save route53_zone_id to km-config.yaml: %v\n", persistErr)
				}
			}
		}
	}

	// Step 1: Build Lambda zips
	fmt.Println()
	fmt.Printf("Building Lambdas [%s]...\n", version.String())
	if err := buildLambdaZips(repoRoot); err != nil {
		fmt.Printf("  [warn] Lambda build failed: %v\n", err)
	}

	// Step 2: Build and upload sidecars
	fmt.Println()
	fmt.Printf("Building and uploading sidecars [%s]...\n", version.String())
	if cfg.ArtifactsBucket != "" {
		if err := buildAndUploadSidecars(repoRoot, cfg.ArtifactsBucket); err != nil {
			fmt.Printf("  [warn] Sidecar build/upload failed: %v\n", err)
		}
	} else {
		fmt.Printf("  [skip] artifacts_bucket not configured\n")
	}

	// Step 2a: Build and push km-sandbox container image to ECR
	fmt.Println()
	fmt.Printf("Building and pushing km-sandbox image [%s]...\n", version.String())
	if err := buildAndPushSandboxImage(repoRoot, cfg); err != nil {
		fmt.Printf("  [warn] Sandbox image build/push failed: %v\n", err)
	} else {
		fmt.Printf("  km-sandbox image pushed to ECR\n")
	}

	// Step 2a.1: Build and push sidecar container images to ECR
	fmt.Println()
	fmt.Printf("Building and pushing sidecar images [%s]...\n", version.String())
	if err := buildAndPushSidecarImages(repoRoot, cfg); err != nil {
		fmt.Printf("  [warn] Sidecar image build/push failed: %v\n", err)
	} else {
		fmt.Printf("  All sidecar images pushed to ECR\n")
	}

	// Step 2b: Upload create-handler toolchain (km + terraform + terragrunt + infra/) to S3
	fmt.Println()
	fmt.Println("Uploading create-handler toolchain...")
	if cfg.ArtifactsBucket != "" {
		if err := uploadCreateHandlerToolchain(repoRoot, cfg.ArtifactsBucket); err != nil {
			fmt.Printf("  [warn] Toolchain upload failed: %v\n", err)
		}
	} else {
		fmt.Printf("  [skip] artifacts_bucket not configured\n")
	}

	// Step 2c: Force Lambda cold start so it downloads the fresh toolchain.
	// The create-handler caches toolchain via sync.Once per container.
	if cfg.ArtifactsBucket != "" {
		fmt.Println()
		fmt.Println("Forcing create-handler Lambda cold start...")
		if err := forceLambdaColdStart(ctx, awsCfg); err != nil {
			fmt.Printf("  [warn] Lambda cold start trigger failed: %v\n", err)
		} else {
			fmt.Printf("  ✓ Lambda environment updated (next invocation downloads fresh toolchain)\n")
		}
	}

	// Step 3: Ensure proxy CA cert+key in S3
	fmt.Println()
	fmt.Println("Ensuring proxy CA certificate...")
	if cfg.ArtifactsBucket != "" {
		if err := ensureProxyCACert(repoRoot, cfg.ArtifactsBucket); err != nil {
			fmt.Printf("  [warn] Proxy CA setup failed: %v\n", err)
			fmt.Printf("  MITM budget enforcement will use goproxy's default CA.\n")
		}
	} else {
		fmt.Printf("  [skip] artifacts_bucket not configured\n")
	}

	// Write safe phrase to SSM if configured (idempotent — overwrites to stay in sync with config).
	if cfg.SafePhrase != "" {
		ssmClient := ssm.NewFromConfig(awsCfg)
		safePhraseKey := "/km/config/remote-create/safe-phrase"
		_, putErr := ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
			Name:      aws.String(safePhraseKey),
			Value:     aws.String(cfg.SafePhrase),
			Type:      ssmtypes.ParameterTypeSecureString,
			Overwrite: aws.Bool(true),
		})
		if putErr != nil {
			fmt.Printf("  ⚠ Failed to write safe phrase to SSM: %v\n", putErr)
		} else {
			fmt.Printf("  Safe phrase written to SSM: %s\n", safePhraseKey)
		}
	}

	// Step 3b: Provision operator identity (Ed25519 signing key + DynamoDB public key).
	// The operator inbox needs an identity so km email send --from operator sends signed emails.
	// Uses sandbox_id="operator" as the identity key. EnsureSandboxIdentity returns the
	// existing public key on re-runs (avoids drifting SSM private vs DynamoDB public).
	{
		fmt.Println()
		fmt.Println("Ensuring operator email identity...")
		ssmClient := ssm.NewFromConfig(awsCfg)
		kmsKeyAlias := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
		if kmsKeyAlias == "" {
			kmsKeyAlias = "alias/km-platform"
		}
		operatorID := "operator"
		pubKey, identErr := awspkg.EnsureSandboxIdentity(ctx, ssmClient, operatorID, kmsKeyAlias)
		if identErr != nil {
			fmt.Printf("  ⚠ Operator identity key generation failed: %v\n", identErr)
		} else {
			domain := cfg.Domain
			if domain == "" {
				domain = "klankermaker.ai"
			}
			identityTableName := cfg.IdentityTableName
			if identityTableName == "" {
				identityTableName = "km-identities"
			}
			operatorEmail := fmt.Sprintf("operator@sandboxes.%s", domain)
			dynamoClient := dynamodb.NewFromConfig(awsCfg)
			if pubErr := awspkg.PublishIdentity(ctx, dynamoClient, identityTableName, operatorID, operatorEmail, pubKey, nil, "required", "required", "off", "operator", []string{"*"}); pubErr != nil {
				fmt.Printf("  ⚠ Operator identity publish failed: %v\n", pubErr)
			} else {
				fmt.Printf("  ✓ Operator identity: Ed25519 key at /sandbox/operator/signing-key\n")
			}
		}
	}

	// Step 4: Apply regional infrastructure
	fmt.Println()
	fmt.Println("Applying infrastructure...")
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = verbose
	if err := RunInitWithRunner(runner, repoRoot, region); err != nil {
		return err
	}

	// Display email-to-create details if operator email and safe phrase are configured.
	if cfg.OperatorEmail != "" || cfg.SafePhrase != "" {
		domain := cfg.Domain
		if domain == "" {
			domain = "klankermaker.ai"
		}
		fmt.Println()
		fmt.Println("Email-to-create:")
		fmt.Printf("  Send to:     operator@sandboxes.%s\n", domain)
		if cfg.OperatorEmail != "" {
			fmt.Printf("  Operator:    %s\n", cfg.OperatorEmail)
		}
		if cfg.SafePhrase != "" {
			fmt.Printf("  KM-AUTH:     %s\n", cfg.SafePhrase)
		}
		fmt.Printf("  SSM key:     /km/config/remote-create/safe-phrase\n")
	}

	return nil
}

// runInitPartial runs a subset of init steps for fast iteration.
// --sidecars: rebuild km + sidecars, upload to S3, upload toolchain, force Lambda cold start.
// --lambdas: rebuild and deploy Lambda zips.
// Both can be combined.
func runInitPartial(cfg *config.Config, awsProfile, region string, verbose, sidecars, lambdas bool) error {
	ctx := context.Background()

	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}

	if cfg.ArtifactsBucket != "" && os.Getenv("KM_ARTIFACTS_BUCKET") == "" {
		os.Setenv("KM_ARTIFACTS_BUCKET", cfg.ArtifactsBucket)
	}

	repoRoot := findRepoRoot()

	if lambdas {
		printBanner("km init --lambdas", region)
		fmt.Println()
		fmt.Printf("Building Lambdas [%s]...\n", version.String())
		if err := buildLambdaZips(repoRoot); err != nil {
			return fmt.Errorf("lambda build failed: %w", err)
		}

		// Force cold start so new code takes effect.
		fmt.Println()
		fmt.Println("Forcing create-handler Lambda cold start...")
		if err := forceLambdaColdStart(ctx, awsCfg); err != nil {
			fmt.Printf("  [warn] Lambda cold start trigger failed: %v\n", err)
		} else {
			fmt.Printf("  ✓ Lambda environment updated\n")
		}
	}

	if sidecars {
		printBanner("km init --sidecars", region)

		if cfg.ArtifactsBucket == "" {
			return fmt.Errorf("artifacts_bucket not configured in km-config.yaml")
		}

		fmt.Println()
		fmt.Printf("Building and uploading sidecars [%s]...\n", version.String())
		if err := buildAndUploadSidecars(repoRoot, cfg.ArtifactsBucket); err != nil {
			return fmt.Errorf("sidecar build/upload failed: %w", err)
		}

		fmt.Println()
		fmt.Println("Uploading create-handler toolchain...")
		if err := uploadCreateHandlerToolchain(repoRoot, cfg.ArtifactsBucket); err != nil {
			return fmt.Errorf("toolchain upload failed: %w", err)
		}

		// Force cold start so Lambda picks up new km binary.
		fmt.Println()
		fmt.Println("Forcing create-handler Lambda cold start...")
		if err := forceLambdaColdStart(ctx, awsCfg); err != nil {
			fmt.Printf("  [warn] Lambda cold start trigger failed: %v\n", err)
		} else {
			fmt.Printf("  ✓ Lambda environment updated\n")
		}
	}

	fmt.Println()
	fmt.Println("Done.")
	return nil
}

// RunInitWithRunner implements the full init flow using an InitRunner interface.
// This function is the testable core — runInit wraps it with real runner construction.
// Exported for use by tests in cmd_test package.
func RunInitWithRunner(runner InitRunner, repoRoot, region string) error {
	ctx := context.Background()
	regionLabel := compiler.RegionLabel(region)

	// Create region directory structure: infra/live/<regionLabel>/sandboxes/
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)
	sandboxesDir := filepath.Join(regionDir, "sandboxes")

	if err := os.MkdirAll(sandboxesDir, 0o755); err != nil {
		return fmt.Errorf("creating sandboxes directory: %w", err)
	}

	// Write region.hcl for this region
	regionHCL := fmt.Sprintf(`locals {
  region_label = "%s"
  region_full  = "%s"
}
`, regionLabel, region)
	if err := os.WriteFile(filepath.Join(regionDir, "region.hcl"), []byte(regionHCL), 0o644); err != nil {
		return fmt.Errorf("writing region.hcl: %w", err)
	}

	modules := regionalModules(regionDir)

	// Module header already printed by runInit

	for _, mod := range modules {
		// Check if directory exists
		if _, err := os.Stat(mod.dir); os.IsNotExist(err) {
			fmt.Printf("  [skip] %s — directory not found (run 'km init' after creating module)\n", mod.name)
			continue
		}

		// Check required env vars
		skipped := false
		for _, envVar := range mod.envReqs {
			if os.Getenv(envVar) == "" {
				fmt.Printf("  [skip] %s — %s not set\n", mod.name, envVar)
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}

		fmt.Printf("  Applying %s...", mod.name)
		if err := runner.Apply(ctx, mod.dir); err != nil {
			fmt.Println() // newline after the "Applying X..." prefix on failure
			return fmt.Errorf("applying %s: %w", mod.name, err)
		}
		fmt.Println(" done")

		// After network module: capture and save outputs.json
		if mod.name == "network" {
			outputMap, err := runner.Output(ctx, mod.dir)
			if err != nil {
				return fmt.Errorf("reading network outputs: %w", err)
			}

			outputJSON, err := json.MarshalIndent(outputMap, "", "  ")
			if err != nil {
				return fmt.Errorf("serializing outputs: %w", err)
			}

			outputsFile := filepath.Join(mod.dir, "outputs.json")
			if err := os.WriteFile(outputsFile, outputJSON, 0o644); err != nil {
				return fmt.Errorf("writing outputs.json: %w", err)
			}

			// Display network summary
			fmt.Printf("\n  Network outputs for %s:\n", region)
			if v, ok := outputMap["vpc_id"]; ok {
				fmt.Printf("    VPC:     %v\n", extractValue(v))
			}
			if v, ok := outputMap["public_subnets"]; ok {
				fmt.Printf("    Subnets: %v\n", extractValue(v))
			}
			if v, ok := outputMap["availability_zones"]; ok {
				fmt.Printf("    AZs:     %v\n", extractValue(v))
			}
			fmt.Println()
		}

		// After efs module: capture outputs.json for compiler use
		if mod.name == "efs" {
			outputMap, err := runner.Output(ctx, mod.dir)
			if err != nil {
				return fmt.Errorf("reading efs outputs: %w", err)
			}
			outputJSON, err := json.MarshalIndent(outputMap, "", "  ")
			if err != nil {
				return fmt.Errorf("serializing efs outputs: %w", err)
			}
			outputsFile := filepath.Join(mod.dir, "outputs.json")
			if err := os.WriteFile(outputsFile, outputJSON, 0o644); err != nil {
				return fmt.Errorf("writing efs outputs.json: %w", err)
			}
			if v, ok := outputMap["filesystem_id"]; ok {
				fmt.Printf("  EFS filesystem: %v\n", extractValue(v))
			}
		}

		// After email-handler module: capture Lambda ARN for SES module
		if mod.name == "email-handler" {
			outputMap, outErr := runner.Output(ctx, mod.dir)
			if outErr == nil {
				if arnVal, ok := outputMap["lambda_function_arn"]; ok {
					arn := fmt.Sprintf("%v", extractValue(arnVal))
					os.Setenv("KM_EMAIL_HANDLER_ARN", arn)
					fmt.Printf("  Email handler ARN: %s\n", arn)
				}
			}
		}

		// After create-handler module: capture Lambda ARN for email-handler scheduling
		if mod.name == "create-handler" {
			outputMap, outErr := runner.Output(ctx, mod.dir)
			if outErr == nil {
				if arnVal, ok := outputMap["lambda_function_arn"]; ok {
					arn := fmt.Sprintf("%v", extractValue(arnVal))
					os.Setenv("KM_CREATE_HANDLER_ARN", arn)
					fmt.Printf("  Create handler ARN: %s\n", arn)
				}
			}
		}
	}

	fmt.Println()
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("Init complete for %s. Ready for: km create <profile.yaml>\n", region)
	return nil
}

func extractValue(v interface{}) interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		if val, exists := m["value"]; exists {
			return val
		}
	}
	return v
}

// LoadNetworkOutputs reads the shared network outputs for a specific region.
// If the local outputs.json is missing, it falls back to querying the remote
// Terraform state via `terragrunt output -json` and caches the result locally.
func LoadNetworkOutputs(repoRoot, regionLabel string) (*NetworkOutputs, error) {
	outputsFile := filepath.Join(repoRoot, "infra", "live", regionLabel, "network", "outputs.json")

	data, err := os.ReadFile(outputsFile)
	if err != nil {
		if os.IsNotExist(err) {
			data, err = fetchAndCacheOutputs(repoRoot, regionLabel, "network")
			if err != nil {
				return nil, fmt.Errorf("network not initialized for region %s and remote state query failed: %w", regionLabel, err)
			}
		} else {
			return nil, fmt.Errorf("reading network outputs: %w", err)
		}
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing network outputs: %w", err)
	}

	outputs := &NetworkOutputs{}
	if err := extractTFOutput(raw, "vpc_id", &outputs.VPCID); err != nil {
		return nil, err
	}
	if err := extractTFOutput(raw, "public_subnets", &outputs.PublicSubnets); err != nil {
		return nil, err
	}
	if err := extractTFOutput(raw, "availability_zones", &outputs.AvailabilityZones); err != nil {
		return nil, err
	}
	_ = extractTFOutput(raw, "sandbox_mgmt_sg_id", &outputs.SandboxMgmtSGID)

	return outputs, nil
}

// LoadEFSOutputs reads the EFS filesystem ID from efs/outputs.json for the given region.
// Returns ("", nil) when the file doesn't exist and remote state has no outputs (EFS not initialized).
func LoadEFSOutputs(repoRoot, regionLabel string) (string, error) {
	outputsFile := filepath.Join(repoRoot, "infra", "live", regionLabel, "efs", "outputs.json")
	data, err := os.ReadFile(outputsFile)
	if err != nil {
		if os.IsNotExist(err) {
			data, err = fetchAndCacheOutputs(repoRoot, regionLabel, "efs")
			if err != nil {
				// EFS is optional — if remote state also has nothing, that's fine.
				return "", nil
			}
		} else {
			return "", fmt.Errorf("reading efs outputs: %w", err)
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("parsing efs outputs: %w", err)
	}
	var fsID string
	if err := extractTFOutput(raw, "filesystem_id", &fsID); err != nil {
		return "", err
	}
	return fsID, nil
}

// fetchAndCacheOutputs reads the Terraform state file directly from S3 for the
// given module (e.g. "network", "efs") and extracts outputs. The result is cached
// to outputs.json locally. This allows commands like `km create` to work without
// terragrunt installed and without a prior `km init`, as long as the infrastructure
// was already provisioned.
//
// State bucket: tf-km-state-{regionLabel}
// State key:    tf-km/{regionLabel}/{module}/terraform.tfstate
func fetchAndCacheOutputs(repoRoot, regionLabel, module string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bucket := fmt.Sprintf("tf-km-state-%s", regionLabel)
	key := fmt.Sprintf("tf-km/%s/%s/terraform.tfstate", regionLabel, module)

	awsProfile := os.Getenv("AWS_PROFILE")
	if awsProfile == "" {
		awsProfile = "klanker-terraform"
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile(awsProfile),
	)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg)
	resp, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("reading state s3://%s/%s: %w", bucket, key, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading state body: %w", err)
	}

	// Parse the tfstate and extract outputs from the root module.
	var state struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, fmt.Errorf("parsing tfstate: %w", err)
	}
	if len(state.Outputs) == 0 {
		return nil, fmt.Errorf("no outputs in state s3://%s/%s", bucket, key)
	}

	// Re-serialize outputs in the same format as `terragrunt output -json`.
	data, err := json.MarshalIndent(state.Outputs, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serializing %s outputs: %w", module, err)
	}

	// Cache locally so subsequent calls use the file directly.
	moduleDir := filepath.Join(repoRoot, "infra", "live", regionLabel, module)
	outputsFile := filepath.Join(moduleDir, "outputs.json")
	if writeErr := os.MkdirAll(moduleDir, 0o755); writeErr == nil {
		if writeErr = os.WriteFile(outputsFile, data, 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not cache %s outputs: %v\n", module, writeErr)
		}
	}

	return data, nil
}

func extractTFOutput(raw map[string]json.RawMessage, key string, target interface{}) error {
	data, ok := raw[key]
	if !ok {
		return fmt.Errorf("missing output %q", key)
	}
	var wrapper struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return fmt.Errorf("parsing output %q: %w", key, err)
	}
	if err := json.Unmarshal(wrapper.Value, target); err != nil {
		return fmt.Errorf("parsing output %q value: %w", key, err)
	}
	return nil
}

// lambdaBuild describes a Lambda to cross-compile and zip.
type lambdaBuild struct {
	name   string // zip filename without extension
	srcDir string // Go source directory relative to repo root
}

// buildLambdaZips cross-compiles Lambda binaries for linux/arm64 and packages them as zips.
// Skips any Lambda whose zip already exists. Equivalent to `make build-lambdas`.
func buildLambdaZips(repoRoot string) error {
	buildDir := filepath.Join(repoRoot, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}

	lambdas := []lambdaBuild{
		{name: "ttl-handler", srcDir: "cmd/ttl-handler"},
		{name: "budget-enforcer", srcDir: "cmd/budget-enforcer"},
		{name: "github-token-refresher", srcDir: "cmd/github-token-refresher"},
		{name: "email-create-handler", srcDir: "cmd/email-create-handler"},
		{name: "create-handler", srcDir: "cmd/create-handler"},
		// Phase 63: Slack-notify bridge Lambda — accepts signed envelopes from sandboxes.
		{name: "km-slack-bridge", srcDir: "cmd/km-slack-bridge"},
	}

	// Ensure terraform binary is available for bundling with ttl-handler
	terraformPath := filepath.Join(buildDir, "terraform")
	if _, err := os.Stat(terraformPath); os.IsNotExist(err) {
		fmt.Printf("  Downloading terraform for linux/arm64...\n")
		if dlErr := downloadTerraform(buildDir); dlErr != nil {
			fmt.Printf("  [warn] terraform download failed: %v\n", dlErr)
			fmt.Printf("  TTL handler will use SDK-only teardown (less complete).\n")
		}
	}

	for _, lb := range lambdas {
		zipPath := filepath.Join(buildDir, lb.name+".zip")
		// Always rebuild — ensures code changes are picked up.
		os.Remove(zipPath)

		srcPath := filepath.Join(repoRoot, lb.srcDir)
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			fmt.Printf("  [skip] %s — source not found at %s\n", lb.name, lb.srcDir)
			continue
		}

		fmt.Printf("  Building %s Lambda (linux/arm64)...\n", lb.name)

		// Cross-compile with version ldflags
		bootstrapPath := filepath.Join(buildDir, "bootstrap")
		ldflags := fmt.Sprintf("-X github.com/whereiskurt/klankrmkr/pkg/version.Number=%s -X github.com/whereiskurt/klankrmkr/pkg/version.GitCommit=%s",
			version.Number, version.GitCommit)
		buildCmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", bootstrapPath, "./"+lb.srcDir+"/")
		buildCmd.Dir = repoRoot
		buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
		if out, err := buildCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("compile %s: %s: %w", lb.name, string(out), err)
		}

		// For ttl-handler, bundle terraform binary alongside bootstrap
		if lb.name == "ttl-handler" {
			filesToZip := []string{bootstrapPath}
			if _, tfErr := os.Stat(terraformPath); tfErr == nil {
				filesToZip = append(filesToZip, terraformPath)
				fmt.Printf("  Bundling terraform binary in ttl-handler.zip\n")
			}
			// Also bundle the ec2spot module for terraform destroy
			modulesDir := filepath.Join(repoRoot, "infra", "modules", "ec2spot", "v1.0.0")
			if _, modErr := os.Stat(modulesDir); modErr == nil {
				// Create a temporary directory structure for the zip
				tmpModDir := filepath.Join(buildDir, "lambda-modules", "infra", "modules", "ec2spot", "v1.0.0")
				os.MkdirAll(tmpModDir, 0o755)
				cpCmd := exec.Command("sh", "-c", fmt.Sprintf("cp %s/*.tf %s/", modulesDir, tmpModDir))
				cpCmd.CombinedOutput()
				// Add module files to zip with directory structure
				zipCmd := exec.Command("zip", "-j", zipPath, filesToZip[0])
				for _, f := range filesToZip[1:] {
					zipCmd.Args = append(zipCmd.Args, f)
				}
				if out, err := zipCmd.CombinedOutput(); err != nil {
					os.Remove(bootstrapPath)
					return fmt.Errorf("zip %s: %s: %w", lb.name, string(out), err)
				}
				// Add module directory structure
				addModCmd := exec.Command("zip", "-r", zipPath, "infra/")
				addModCmd.Dir = filepath.Join(buildDir, "lambda-modules")
				addModCmd.CombinedOutput()
				os.RemoveAll(filepath.Join(buildDir, "lambda-modules"))
			} else {
				zipCmd := exec.Command("zip", "-j", zipPath, filesToZip[0])
				for _, f := range filesToZip[1:] {
					zipCmd.Args = append(zipCmd.Args, f)
				}
				if out, err := zipCmd.CombinedOutput(); err != nil {
					os.Remove(bootstrapPath)
					return fmt.Errorf("zip %s: %s: %w", lb.name, string(out), err)
				}
			}
		} else {
			// Regular Lambda — just bootstrap
			zipCmd := exec.Command("zip", "-j", zipPath, bootstrapPath)
			if out, err := zipCmd.CombinedOutput(); err != nil {
				os.Remove(bootstrapPath)
				return fmt.Errorf("zip %s: %s: %w", lb.name, string(out), err)
			}
		}
		os.Remove(bootstrapPath)

		fmt.Printf("  Built %s.zip\n", lb.name)
	}

	return nil
}

// downloadTerraform downloads the terraform binary for linux/arm64 to the build directory.
func downloadTerraform(buildDir string) error {
	tfVersion := "1.6.6"
	url := fmt.Sprintf("https://releases.hashicorp.com/terraform/%s/terraform_%s_linux_arm64.zip", tfVersion, tfVersion)
	zipPath := filepath.Join(buildDir, "terraform_download.zip")

	// Download
	dlCmd := exec.Command("curl", "-sL", "-o", zipPath, url)
	if out, err := dlCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("download terraform: %s: %w", string(out), err)
	}

	// Unzip
	unzipCmd := exec.Command("unzip", "-o", zipPath, "terraform", "-d", buildDir)
	if out, err := unzipCmd.CombinedOutput(); err != nil {
		os.Remove(zipPath)
		return fmt.Errorf("unzip terraform: %s: %w", string(out), err)
	}
	os.Remove(zipPath)

	// Make executable
	os.Chmod(filepath.Join(buildDir, "terraform"), 0o755)
	return nil
}

// sidecarBuild describes a sidecar binary to cross-compile and upload to S3.
type sidecarBuild struct {
	name   string // binary name (also S3 key suffix)
	srcDir string // Go source directory relative to repo root
}

// buildAndUploadSidecars cross-compiles sidecar binaries for linux/amd64 and uploads
// them to s3://<bucket>/sidecars/. Also uploads the tracing config.yaml.
// Skips upload if the S3 object already exists.
func buildAndUploadSidecars(repoRoot, bucket string) error {
	buildDir := filepath.Join(repoRoot, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}

	sidecars := []sidecarBuild{
		{name: "dns-proxy", srcDir: "sidecars/dns-proxy"},
		{name: "http-proxy", srcDir: "sidecars/http-proxy"},
		{name: "audit-log", srcDir: "sidecars/audit-log/cmd"},
		{name: "km-slack", srcDir: "cmd/km-slack"},
	}

	// Build and upload km binary for EC2 instances (linux/amd64).
	// Instances download this at boot from s3://<bucket>/sidecars/km.
	{
		fmt.Printf("  Building km (linux/amd64)...\n")
		kmPath := filepath.Join(buildDir, "km-linux")
		kmLdflags := fmt.Sprintf("-X github.com/whereiskurt/klankrmkr/pkg/version.Number=%s -X github.com/whereiskurt/klankrmkr/pkg/version.GitCommit=%s",
			version.Number, version.GitCommit)
		buildCmd := exec.Command("go", "build", "-ldflags", kmLdflags, "-o", kmPath, "./cmd/km/")
		buildCmd.Dir = repoRoot
		buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		if out, err := buildCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("compile km: %s: %w", string(out), err)
		}
		fmt.Printf("  Uploading km to s3://%s/sidecars/km...\n", bucket)
		uploadCmd := exec.Command("aws", "s3", "cp", kmPath,
			fmt.Sprintf("s3://%s/sidecars/km", bucket),
			"--profile", "klanker-terraform")
		if out, err := uploadCmd.CombinedOutput(); err != nil {
			os.Remove(kmPath)
			return fmt.Errorf("upload km: %s: %w", string(out), err)
		}
		os.Remove(kmPath)
		fmt.Printf("  Uploaded sidecars/km\n")
	}

	for _, sc := range sidecars {
		s3Key := "sidecars/" + sc.name

		// Always rebuild and re-upload to ensure latest code.
		srcPath := filepath.Join(repoRoot, sc.srcDir)
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			fmt.Printf("  [skip] %s — source not found at %s\n", sc.name, sc.srcDir)
			continue
		}

		fmt.Printf("  Building sidecar %s (linux/amd64)...\n", sc.name)

		// Cross-compile for linux/amd64 (EC2 and Fargate x86) with version ldflags
		binaryPath := filepath.Join(buildDir, sc.name)
		scLdflags := fmt.Sprintf("-X github.com/whereiskurt/klankrmkr/pkg/version.Number=%s -X github.com/whereiskurt/klankrmkr/pkg/version.GitCommit=%s",
			version.Number, version.GitCommit)
		buildCmd := exec.Command("go", "build", "-ldflags", scLdflags, "-o", binaryPath, "./"+sc.srcDir+"/")
		buildCmd.Dir = repoRoot
		buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		if out, err := buildCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("compile sidecar %s: %s: %w", sc.name, string(out), err)
		}

		// Upload to S3
		fmt.Printf("  Uploading %s to s3://%s/%s...\n", sc.name, bucket, s3Key)
		uploadCmd := exec.Command("aws", "s3", "cp", binaryPath,
			fmt.Sprintf("s3://%s/%s", bucket, s3Key),
			"--profile", "klanker-terraform")
		if out, err := uploadCmd.CombinedOutput(); err != nil {
			os.Remove(binaryPath)
			return fmt.Errorf("upload sidecar %s: %s: %w", sc.name, string(out), err)
		}
		os.Remove(binaryPath)

		fmt.Printf("  Uploaded %s\n", sc.name)
	}

	// Always upload tracing config.yaml
	tracingConfig := filepath.Join(repoRoot, "sidecars", "tracing", "config.yaml")
	if _, err := os.Stat(tracingConfig); err == nil {
		s3Key := "sidecars/tracing/config.yaml"
		fmt.Printf("  Uploading tracing config.yaml...\n")
		uploadCmd := exec.Command("aws", "s3", "cp", tracingConfig,
			fmt.Sprintf("s3://%s/%s", bucket, s3Key),
			"--profile", "klanker-terraform")
		if out, err := uploadCmd.CombinedOutput(); err != nil {
			fmt.Printf("  [warn] tracing config upload failed: %s: %v\n", string(out), err)
		}
	}

	// Fetch and upload otelcol-contrib binary for EC2 km-tracing sidecar.
	// This is a third-party binary (not built from this repo).
	if err := fetchAndUploadOtelcolContrib(buildDir, bucket); err != nil {
		fmt.Printf("  [warn] otelcol-contrib upload failed: %v\n", err)
	}

	return nil
}

const otelcolContribVersion = "0.120.0"

// fetchAndUploadOtelcolContrib downloads the otelcol-contrib binary from the
// official GitHub releases and uploads it to s3://<bucket>/sidecars/otelcol-contrib.
// Skips the download if build/otelcol-contrib already exists.
func fetchAndUploadOtelcolContrib(buildDir, bucket string) error {
	binaryPath := filepath.Join(buildDir, "otelcol-contrib")

	// Skip download if already present
	if _, err := os.Stat(binaryPath); err == nil {
		fmt.Printf("  otelcol-contrib already in build/ (skip download)\n")
	} else {
		url := fmt.Sprintf(
			"https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v%s/otelcol-contrib_%s_linux_amd64.tar.gz",
			otelcolContribVersion, otelcolContribVersion,
		)
		fmt.Printf("  Downloading otelcol-contrib v%s...\n", otelcolContribVersion)

		// Download and extract in one pipeline: curl | tar
		dlCmd := exec.Command("bash", "-c",
			fmt.Sprintf("curl -sL %q | tar xz -C %q otelcol-contrib", url, buildDir))
		if out, err := dlCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("download otelcol-contrib: %s: %w", string(out), err)
		}
		if err := os.Chmod(binaryPath, 0o755); err != nil {
			return fmt.Errorf("chmod otelcol-contrib: %w", err)
		}
	}

	// Upload to S3
	s3Key := "sidecars/otelcol-contrib"
	fmt.Printf("  Uploading otelcol-contrib to s3://%s/%s...\n", bucket, s3Key)
	uploadCmd := exec.Command("aws", "s3", "cp", binaryPath,
		fmt.Sprintf("s3://%s/%s", bucket, s3Key),
		"--profile", "klanker-terraform")
	if out, err := uploadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("upload otelcol-contrib: %s: %w", string(out), err)
	}
	fmt.Printf("  Uploaded otelcol-contrib\n")
	return nil
}

// buildAndPushSandboxImage builds the km-sandbox container image for linux/amd64
// and pushes it to ECR with versioned and latest tags. This is the base container
// image for ECS/Docker/EKS sandbox substrates.
func buildAndPushSandboxImage(repoRoot string, cfg *config.Config) error {
	accountID := cfg.ApplicationAccountID
	region := cfg.PrimaryRegion
	if accountID == "" || region == "" {
		return fmt.Errorf("accounts_application and region required for ECR push")
	}
	ecrRegistry := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", accountID, region)
	imageTag := version.Number

	// Ensure ECR repo exists
	ensureCmd := exec.Command("aws", "ecr", "describe-repositories",
		"--region", region,
		"--repository-names", "km-sandbox",
		"--profile", "klanker-terraform")
	if out, err := ensureCmd.CombinedOutput(); err != nil {
		fmt.Printf("  Creating km-sandbox ECR repository...\n")
		createCmd := exec.Command("aws", "ecr", "create-repository",
			"--region", region,
			"--repository-name", "km-sandbox",
			"--profile", "klanker-terraform")
		if out, err := createCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("create ECR repo: %s: %w", string(out), err)
		}
	} else {
		_ = out // repo exists
	}

	// ECR login
	loginCmd := exec.Command("bash", "-c",
		fmt.Sprintf("aws ecr get-login-password --region %s --profile klanker-terraform | docker login --username AWS --password-stdin %s", region, ecrRegistry))
	if out, err := loginCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ECR login: %s: %w", string(out), err)
	}

	// Build and push
	dockerfilePath := filepath.Join(repoRoot, "containers", "sandbox", "Dockerfile")
	contextPath := filepath.Join(repoRoot, "containers", "sandbox")
	versionTag := fmt.Sprintf("%s/km-sandbox:%s", ecrRegistry, imageTag)
	latestTag := fmt.Sprintf("%s/km-sandbox:latest", ecrRegistry)

	fmt.Printf("  Building km-sandbox image (linux/amd64)...\n")
	buildCmd := exec.Command("docker", "buildx", "build",
		"--platform", "linux/amd64",
		"--file", dockerfilePath,
		"--tag", versionTag,
		"--tag", latestTag,
		"--push",
		contextPath)
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker build+push: %s: %w", string(out), err)
	}

	return nil
}

// buildAndPushSidecarImages builds and pushes all 4 sidecar Docker images to ECR.
// Images: km-dns-proxy, km-http-proxy, km-audit-log, km-tracing.
func buildAndPushSidecarImages(repoRoot string, cfg *config.Config) error {
	accountID := cfg.ApplicationAccountID
	region := cfg.PrimaryRegion
	if accountID == "" || region == "" {
		return fmt.Errorf("accounts_application and region required for ECR push")
	}
	ecrRegistry := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com", accountID, region)
	imageTag := version.Number

	// ECR login (may already be logged in from sandbox image push, but safe to repeat).
	loginCmd := exec.Command("bash", "-c",
		fmt.Sprintf("aws ecr get-login-password --region %s --profile klanker-terraform | docker login --username AWS --password-stdin %s", region, ecrRegistry))
	if out, err := loginCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ECR login: %s: %w", string(out), err)
	}

	type sidecar struct {
		name       string
		dockerfile string
		context    string
	}
	sidecars := []sidecar{
		{"km-dns-proxy", "sidecars/dns-proxy/Dockerfile", "."},
		{"km-http-proxy", "sidecars/http-proxy/Dockerfile", "."},
		{"km-audit-log", "sidecars/audit-log/Dockerfile", "."},
		{"km-tracing", "sidecars/tracing/Dockerfile", "sidecars/tracing/"},
	}

	for _, sc := range sidecars {
		// Ensure ECR repo exists.
		ensureCmd := exec.Command("aws", "ecr", "describe-repositories",
			"--region", region,
			"--repository-names", sc.name,
			"--profile", "klanker-terraform")
		if _, err := ensureCmd.CombinedOutput(); err != nil {
			fmt.Printf("  Creating %s ECR repository...\n", sc.name)
			createCmd := exec.Command("aws", "ecr", "create-repository",
				"--region", region,
				"--repository-name", sc.name,
				"--profile", "klanker-terraform")
			if out, err := createCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("create ECR repo %s: %s: %w", sc.name, string(out), err)
			}
		}

		versionTag := fmt.Sprintf("%s/%s:%s", ecrRegistry, sc.name, imageTag)
		latestTag := fmt.Sprintf("%s/%s:latest", ecrRegistry, sc.name)
		dockerfilePath := filepath.Join(repoRoot, sc.dockerfile)
		contextPath := filepath.Join(repoRoot, sc.context)

		fmt.Printf("  Building %s (linux/amd64)...\n", sc.name)
		buildCmd := exec.Command("docker", "buildx", "build",
			"--platform", "linux/amd64",
			"--file", dockerfilePath,
			"--tag", versionTag,
			"--tag", latestTag,
			"--push",
			contextPath)
		buildCmd.Dir = repoRoot
		if out, err := buildCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("docker build+push %s: %s: %w", sc.name, string(out), err)
		}
		fmt.Printf("  ✓ %s pushed\n", sc.name)
	}

	return nil
}

// uploadCreateHandlerToolchain builds km (linux/arm64), downloads terragrunt,
// creates infra.tar.gz (source files only), and uploads everything to
// s3://<bucket>/toolchain/. The create-handler Lambda downloads these at cold start.
func uploadCreateHandlerToolchain(repoRoot, bucket string) error {
	buildDir := filepath.Join(repoRoot, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}

	// 1. Build km binary for linux/arm64 (stripped)
	kmPath := filepath.Join(buildDir, "km-toolchain")
	fmt.Printf("  Building km (linux/arm64, stripped)...\n")
	ldflags := fmt.Sprintf("-s -w -X github.com/whereiskurt/klankrmkr/pkg/version.Number=%s -X github.com/whereiskurt/klankrmkr/pkg/version.GitCommit=%s",
		version.Number, version.GitCommit)
	buildCmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", kmPath, "./cmd/km/")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compile km: %s: %w", string(out), err)
	}
	defer os.Remove(kmPath)

	// Upload km
	s3Upload(kmPath, bucket, "toolchain/km")
	fmt.Printf("  Uploaded toolchain/km\n")

	// 2. Ensure terraform binary exists (already downloaded by buildLambdaZips)
	terraformPath := filepath.Join(buildDir, "terraform")
	if _, err := os.Stat(terraformPath); os.IsNotExist(err) {
		fmt.Printf("  Downloading terraform for linux/arm64...\n")
		if dlErr := downloadTerraform(buildDir); dlErr != nil {
			return fmt.Errorf("download terraform: %w", dlErr)
		}
	}
	s3Upload(terraformPath, bucket, "toolchain/terraform")
	fmt.Printf("  Uploaded toolchain/terraform\n")

	// 3. Download terragrunt arm64 (always re-download to match local version)
	terragruntPath := filepath.Join(buildDir, "terragrunt")
	{
		tgVersion := "0.99.1"
		fmt.Printf("  Downloading terragrunt v%s for linux/arm64...\n", tgVersion)
		dlCmd := exec.Command("curl", "-fsSL",
			fmt.Sprintf("https://github.com/gruntwork-io/terragrunt/releases/download/v%s/terragrunt_linux_arm64", tgVersion),
			"-o", terragruntPath)
		if out, err := dlCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("download terragrunt: %s: %w", string(out), err)
		}
		os.Chmod(terragruntPath, 0o755)
	}
	s3Upload(terragruntPath, bucket, "toolchain/terragrunt")
	fmt.Printf("  Uploaded toolchain/terragrunt\n")

	// 4. Create infra.tar.gz (source + outputs.json + config anchors + Lambda zips — no .terraform caches)
	tarPath := filepath.Join(buildDir, "infra.tar.gz")
	fmt.Printf("  Creating infra.tar.gz...\n")
	tarCmd := exec.Command("tar", "czf", tarPath,
		"--exclude", ".terraform",
		"--exclude", ".terragrunt-cache",
		"--exclude", "*.tfstate*",
		"CLAUDE.md", "km-config.yaml",
		"infra/modules", "infra/live", "infra/templates",
		"build/budget-enforcer.zip", "build/github-token-refresher.zip",
		"build/ttl-handler.zip")
	tarCmd.Dir = repoRoot
	if out, err := tarCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create infra tarball: %s: %w", string(out), err)
	}
	defer os.Remove(tarPath)
	s3Upload(tarPath, bucket, "toolchain/infra.tar.gz")
	fmt.Printf("  Uploaded toolchain/infra.tar.gz\n")

	// 5. Upload km-config.yaml for Lambda cold start
	kmConfigPath := filepath.Join(repoRoot, "km-config.yaml")
	if _, err := os.Stat(kmConfigPath); err == nil {
		s3Upload(kmConfigPath, bucket, "toolchain/km-config.yaml")
		fmt.Printf("  Uploaded toolchain/km-config.yaml\n")
	} else {
		fmt.Printf("  Warning: km-config.yaml not found at %s, skipping toolchain config upload\n", kmConfigPath)
	}

	return nil
}

// s3Upload uploads a local file to S3 using the AWS CLI.
func s3Upload(localPath, bucket, s3Key string) error {
	uploadCmd := exec.Command("aws", "s3", "cp", localPath,
		fmt.Sprintf("s3://%s/%s", bucket, s3Key),
		"--profile", "klanker-terraform")
	if out, err := uploadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("upload %s: %s: %w", s3Key, string(out), err)
	}
	return nil
}

// ensureProxyCACert generates a CA cert+key for the MITM proxy (if not already
// in S3) and uploads both to s3://<bucket>/sidecars/km-proxy-ca.{crt,key}.
// The cert is installed in sandboxes' system trust store at boot; the key is
// passed to the proxy via KM_PROXY_CA_CERT so it can sign leaf certificates.
func ensureProxyCACert(repoRoot, bucket string) error {
	// Check if cert already exists in S3
	checkCmd := exec.Command("aws", "s3", "ls",
		fmt.Sprintf("s3://%s/sidecars/km-proxy-ca.crt", bucket),
		"--profile", "klanker-terraform")
	if out, err := checkCmd.CombinedOutput(); err == nil && len(out) > 0 {
		fmt.Printf("  Proxy CA cert already exists in S3\n")
		return nil
	}

	fmt.Printf("  Generating proxy CA cert+key...\n")

	// Generate ECDSA P-256 private key
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	// Create self-signed CA certificate (valid 5 years)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "km-platform-ca"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(5 * 365 * 24 * time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA cert: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	// Write to temp files for S3 upload
	buildDir := filepath.Join(repoRoot, "build")
	os.MkdirAll(buildDir, 0o755)

	certPath := filepath.Join(buildDir, "km-proxy-ca.crt")
	keyPath := filepath.Join(buildDir, "km-proxy-ca.key")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write CA cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write CA key: %w", err)
	}
	defer os.Remove(certPath)
	defer os.Remove(keyPath)

	// Upload cert
	fmt.Printf("  Uploading proxy CA cert to s3://%s/sidecars/km-proxy-ca.crt...\n", bucket)
	uploadCert := exec.Command("aws", "s3", "cp", certPath,
		fmt.Sprintf("s3://%s/sidecars/km-proxy-ca.crt", bucket),
		"--profile", "klanker-terraform")
	if out, err := uploadCert.CombinedOutput(); err != nil {
		return fmt.Errorf("upload CA cert: %s: %w", string(out), err)
	}

	// Upload key
	fmt.Printf("  Uploading proxy CA key to s3://%s/sidecars/km-proxy-ca.key...\n", bucket)
	uploadKey := exec.Command("aws", "s3", "cp", keyPath,
		fmt.Sprintf("s3://%s/sidecars/km-proxy-ca.key", bucket),
		"--profile", "klanker-terraform")
	if out, err := uploadKey.CombinedOutput(); err != nil {
		return fmt.Errorf("upload CA key: %s: %w", string(out), err)
	}

	fmt.Printf("  Proxy CA cert+key generated and uploaded\n")
	return nil
}

// ensureSandboxHostedZone creates the sandboxes.{domain} hosted zone in the application
// account and sets up NS delegation from the parent zone in the management account.
// Returns the zone ID of the sandboxes zone.
func ensureSandboxHostedZone(ctx context.Context, cfg *config.Config) (string, error) {
	sandboxDomain := "sandboxes." + cfg.Domain

	fmt.Printf("  Setting up DNS zone for %s...\n", sandboxDomain)

	// 1. Create Route53 client for application account (where the zone will live)
	// Route53 is a global service but the SDK requires a region to resolve endpoints.
	appCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile("klanker-terraform"),
		awsconfig.WithRegion("us-east-1"),
	)
	if err != nil {
		return "", fmt.Errorf("load app AWS config: %w", err)
	}
	appR53 := route53.NewFromConfig(appCfg)

	// 2. Check if sandboxes.{domain} zone already exists in application account
	zoneID, err := findHostedZone(ctx, appR53, sandboxDomain)
	if err != nil {
		return "", fmt.Errorf("checking for existing zone: %w", err)
	}

	var nsRecords []string

	if zoneID != "" {
		fmt.Printf("  DNS zone %s already exists: %s\n", sandboxDomain, zoneID)

		// Fetch NS records from existing zone so we can verify delegation below.
		nsOut, nsErr := appR53.GetHostedZone(ctx, &route53.GetHostedZoneInput{
			Id: aws.String(zoneID),
		})
		if nsErr != nil {
			return zoneID, fmt.Errorf("zone exists but could not fetch NS records: %w", nsErr)
		}
		for _, ns := range nsOut.DelegationSet.NameServers {
			nsRecords = append(nsRecords, ns)
		}
	} else {
		// 3. Create the hosted zone
		callerRef := fmt.Sprintf("km-init-%d", time.Now().Unix())
		createOut, createErr := appR53.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
			Name:            aws.String(sandboxDomain),
			CallerReference: aws.String(callerRef),
			HostedZoneConfig: &route53types.HostedZoneConfig{
				Comment: aws.String("Sandbox email zone — created by km init"),
			},
		})
		if createErr != nil {
			return "", fmt.Errorf("create hosted zone %s: %w", sandboxDomain, createErr)
		}

		zoneID = strings.TrimPrefix(aws.ToString(createOut.HostedZone.Id), "/hostedzone/")
		fmt.Printf("  Created DNS zone %s: %s\n", sandboxDomain, zoneID)

		for _, ns := range createOut.DelegationSet.NameServers {
			nsRecords = append(nsRecords, ns)
		}
	}

	fmt.Printf("  NS records: %s\n", strings.Join(nsRecords, ", "))

	// 5. Create Route53 client for management account (where the parent zone lives)
	fmt.Println("  Checking NS delegation in management account (profile: klanker-management)...")
	mgmtCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile("klanker-management"),
		awsconfig.WithRegion("us-east-1"),
	)
	if err != nil {
		return zoneID, fmt.Errorf("could not load management AWS config (profile: klanker-management) for NS delegation: %w", err)
	}
	mgmtR53 := route53.NewFromConfig(mgmtCfg)

	// 6. Find the parent zone (cfg.Domain) in management account
	fmt.Printf("  Looking for parent zone %s in management account...\n", cfg.Domain)
	parentZoneID, err := findHostedZone(ctx, mgmtR53, cfg.Domain)
	if err != nil {
		return zoneID, fmt.Errorf("error searching for parent zone %s in management account: %w", cfg.Domain, err)
	}
	if parentZoneID == "" {
		return zoneID, fmt.Errorf("parent zone %s not found in management account — add NS delegation manually", cfg.Domain)
	}
	fmt.Printf("  Found parent zone: %s\n", parentZoneID)

	// 7. Check if NS delegation already exists in parent zone
	existingNS, err := mgmtR53.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(parentZoneID),
		StartRecordName: aws.String(sandboxDomain),
		StartRecordType: route53types.RRTypeNs,
		MaxItems:        aws.Int32(1),
	})
	if err == nil && len(existingNS.ResourceRecordSets) > 0 {
		rrs := existingNS.ResourceRecordSets[0]
		if strings.TrimSuffix(aws.ToString(rrs.Name), ".") == sandboxDomain && rrs.Type == route53types.RRTypeNs {
			fmt.Printf("  NS delegation for %s already exists in management account\n", sandboxDomain)
			return zoneID, nil
		}
	}

	// 8. Create NS delegation record in parent zone
	nsRRs := make([]route53types.ResourceRecord, 0, len(nsRecords))
	for _, ns := range nsRecords {
		nsRRs = append(nsRRs, route53types.ResourceRecord{Value: aws.String(ns)})
	}
	_, err = mgmtR53.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(parentZoneID),
		ChangeBatch: &route53types.ChangeBatch{
			Comment: aws.String("NS delegation for sandbox email zone — created by km init"),
			Changes: []route53types.Change{
				{
					Action: route53types.ChangeActionUpsert,
					ResourceRecordSet: &route53types.ResourceRecordSet{
						Name:            aws.String(sandboxDomain),
						Type:            route53types.RRTypeNs,
						TTL:             aws.Int64(300),
						ResourceRecords: nsRRs,
					},
				},
			},
		},
	})
	if err != nil {
		return zoneID, fmt.Errorf("zone exists but NS delegation failed: %w", err)
	}

	fmt.Printf("  NS delegation added to %s zone in management account\n", sandboxDomain)
	return zoneID, nil
}

// findHostedZone looks for a hosted zone by name. Returns zone ID or "" if not found.
func findHostedZone(ctx context.Context, client *route53.Client, domain string) (string, error) {
	// Ensure trailing dot for Route53 API
	if !strings.HasSuffix(domain, ".") {
		domain += "."
	}
	out, err := client.ListHostedZonesByName(ctx, &route53.ListHostedZonesByNameInput{
		DNSName:  aws.String(domain),
		MaxItems: aws.Int32(1),
	})
	if err != nil {
		return "", err
	}
	for _, zone := range out.HostedZones {
		if aws.ToString(zone.Name) == domain {
			return strings.TrimPrefix(aws.ToString(zone.Id), "/hostedzone/"), nil
		}
	}
	return "", nil
}

// persistRoute53ZoneID writes the zone ID back to km-config.yaml.
func persistRoute53ZoneID(zoneID string) error {
	configPath := filepath.Join(findRepoRoot(), "km-config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	// Parse, add field, re-serialize
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	raw["route53_zone_id"] = zoneID

	newData, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}

	header := "# km-config.yaml — generated by km configure\n# Add this file to .gitignore\n\n"
	return os.WriteFile(configPath, append([]byte(header), newData...), 0600)
}

// forceLambdaColdStart updates the create-handler Lambda's environment to
// force a cold start on the next invocation. The Lambda caches the toolchain
// via sync.Once per container — updating the environment invalidates the
// container and triggers a fresh toolchain download.
func forceLambdaColdStart(ctx context.Context, cfg aws.Config) error {
	client := lambda.NewFromConfig(cfg)
	_, err := client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("km-create-handler"),
		Environment: &lambdatypes.Environment{
			Variables: map[string]string{
				"TOOLCHAIN_VERSION": version.String(),
			},
		},
	})
	return err
}

// lambdaConfigUpdater is a narrow interface over the Lambda SDK client used by
// forceSlackBridgeColdStartWith — exists solely to enable unit-test mocking
// without a real AWS connection.
type lambdaConfigUpdater interface {
	UpdateFunctionConfiguration(ctx context.Context, input *lambda.UpdateFunctionConfigurationInput, optFns ...func(*lambda.Options)) (*lambda.UpdateFunctionConfigurationOutput, error)
}

// ForceSlackBridgeColdStartWith updates the km-slack-bridge Lambda's
// environment using the supplied client, forcing a new execution environment
// and invalidating the in-process SSMBotTokenFetcher cache (15-min TTL).
// Exported for unit testing; production code should call forceSlackBridgeColdStart.
func ForceSlackBridgeColdStartWith(ctx context.Context, client lambdaConfigUpdater) error {
	_, err := client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String("km-slack-bridge"),
		Environment: &lambdatypes.Environment{
			Variables: map[string]string{
				"TOKEN_ROTATION_TS": fmt.Sprintf("%d", time.Now().Unix()),
			},
		},
	})
	return err
}

// forceSlackBridgeColdStart updates the km-slack-bridge Lambda's environment
// to force a new execution environment, invalidating the in-process
// SSMBotTokenFetcher cache (15-min TTL). Used by km slack rotate-token to
// make a newly-persisted bot token effective immediately rather than waiting
// for the cache TTL to expire.
//
// Distinct from forceLambdaColdStart (which targets km-create-handler with
// TOOLCHAIN_VERSION) — uses TOKEN_ROTATION_TS to keep the namespaces clean.
func forceSlackBridgeColdStart(ctx context.Context, cfg aws.Config) error {
	return ForceSlackBridgeColdStartWith(ctx, lambda.NewFromConfig(cfg))
}

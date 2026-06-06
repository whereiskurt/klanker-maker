package cmd

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ses"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt/planreport"
	"github.com/whereiskurt/klanker-maker/pkg/version"
	"gopkg.in/yaml.v3"
)

// InitRunner is the interface for applying Terragrunt modules.
// It is implemented by *terragrunt.Runner and by test mocks.
type InitRunner interface {
	Apply(ctx context.Context, dir string) error
	Output(ctx context.Context, dir string) (map[string]interface{}, error)
	Reconfigure(ctx context.Context, dir string) error
	// Phase 84.2 additions (consumed by RunInitPlanWithRunner):
	PlanWithOutput(ctx context.Context, dir, planFile string, stdoutBuf *bytes.Buffer) error
	ShowPlanJSON(ctx context.Context, dir, planFile string) ([]byte, error)
}

// tfDesiredVersion is the canonical terraform binary version bundled with the
// TTL handler and toolchain by `km init --lambdas` / `km init --sidecars`.
//
// Phase 84.4.1: promoted from a local var in downloadTerraform to a package-level
// constant so that terraformIsCurrent() can compare against it without calling
// the downloaded binary (cross-arch exec problem on macOS → linux/arm64 binary).
//
// Closes TERRAFORM-VERSION-CACHE-INVALIDATION.
const tfDesiredVersion = "1.9.8"

// Compile-time assertion: *terragrunt.Runner must satisfy InitRunner.
// If Plan 03 methods are missing this fails at build, not at runtime.
var _ InitRunner = (*terragrunt.Runner)(nil)

// SESPreflightFunc is a function that validates the shared SES rule set exists
// before the regional ses module applies. It is called by RunInitWithRunner
// immediately before the ses module apply step.
//
// The default implementation calls SES ListReceiptRuleSets via the production
// AWS config. Tests replace this variable with a mock to avoid real AWS calls.
//
// Signature: func(ctx context.Context) error
// Returns nil when the preflight passes (rule set exists).
// Returns an actionable error when the shared rule set is missing.
type SESPreflightFunc func(ctx context.Context) error

// InitSESPreflight is the package-level SES preflight function used by RunInitWithRunner.
// Tests replace this variable to exercise the "missing rule set" branch.
var InitSESPreflight SESPreflightFunc = defaultSESPreflight

// RunInitPlanFunc is the package-level entry point for km init --plan, exported as a
// var so cmd_test can override it with a mock to verify routing without needing real
// AWS credentials / a real terragrunt binary. The default implementation is runInitPlan.
//
// Phase 84.2 test seam — mirrors ApplyTerragruntFunc (bootstrap.go) and InitSESPreflight
// (init.go). The var is initialized after runInitPlan is defined (init.go near
// RunInitWithRunner), so the zero-value is nil until then; Go initializes package vars
// in declaration order, so both runInitPlan and RunInitPlanFunc must be in the same
// file (this file) to guarantee ordering.
var RunInitPlanFunc = runInitPlan

// BuildLambdaZipsFunc is the testable seam for buildLambdaZips. Tests override
// this var to capture or mock the Lambda zip build step without invoking the real
// cross-compiler. The default points to the real buildLambdaZips implementation.
// Exported so cmd_test (external test package) can override it, mirroring the
// RunInitPlanFunc pattern above.
var BuildLambdaZipsFunc = buildLambdaZips

// defaultSESPreflight is the real implementation: loads AWS config and checks
// whether the shared SES receipt rule set exists.
func defaultSESPreflight(ctx context.Context) error {
	// Load AWS config using the application profile (same as runInit).
	awsCfg, err := awspkg.LoadAWSConfig(ctx, "klanker-application")
	if err != nil {
		// If we cannot load AWS config, skip the preflight rather than blocking init.
		// The apply will surface a more specific error if the rule set is truly absent.
		return nil
	}
	// Use the ses classic v1 client for ListReceiptRuleSets.
	lister := &realSESLister{
		sesClient: ses.NewFromConfig(awsCfg),
		// sesv2Client not needed for the rule-set check alone.
		sesv2Client: sesv2.NewFromConfig(awsCfg),
	}
	// Phase 84.1 Task 1 (C2): pass nil for the FoundationStateReader.
	// Preflight only checks rule-set existence in AWS — no state reader needed.
	// The nil branch preserves the pre-84.1 read-only AWS-reality behaviour.
	registerRS, _, err := detectSharedSESState(ctx, lister, nil, "sandbox-email-shared", "")
	if err != nil {
		// Treat detection error as a skip (network issue, permission issue, etc.)
		// rather than aborting init. The Terraform apply will surface the real error.
		return nil
	}
	if registerRS {
		// registerRS=true means the shared rule set does NOT exist.
		return fmt.Errorf("Foundation SES rule set 'sandbox-email-shared' not found. Run 'km bootstrap --shared-ses' first on a fresh account.")
	}
	return nil
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
	name            string
	dir             string
	envReqs         []string // environment variables required to apply this module
	upstreamOutputs []string // upstream module names whose outputs.json must exist before planning
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

// defaultModuleTimeout returns the apply timeout for a regional module.
// Modules with known long propagation (DNS, DKIM, IAM) get longer bounds.
//
// Phase 84.1-02 (GAP-5): closes the unbounded-Apply hang documented in
// 84-10-UAT.md by giving every regional module a sensible upper bound. The
// defaults are well above any normal-case apply duration — they exist to
// catch the wedged-terraform scenario, not to police normal latency.
func defaultModuleTimeout(name string) time.Duration {
	switch name {
	case "network", "ses-shared-rule-set":
		return 10 * time.Minute
	case "ses", "ttl-handler", "create-handler", "email-handler", "lambda-slack-bridge", "lambda-github-bridge":
		return 5 * time.Minute
	default:
		return 3 * time.Minute
	}
}

// ModuleTimeoutFunc is the package-level per-module timeout lookup used by
// RunInitWithRunner. Exported as a var so tests can override it with a short
// duration to exercise the timeout wrapper without waiting real minutes.
var ModuleTimeoutFunc = defaultModuleTimeout

// reconfigureTimeout bounds the per-module Reconfigure call in RunInitWithRunner.
// Reconfigure is normally seconds; a 2-minute bound is plenty to absorb backend
// re-bootstrap on a fresh terragrunt cache while still surfacing a hang quickly.
var reconfigureTimeout = 2 * time.Minute

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
			// Phase 84.3: upstreamOutputs probe skips efs in --plan mode when
			// network/outputs.json is absent (fresh install, network not yet applied).
			name:            "efs",
			dir:             filepath.Join(regionDir, "efs"),
			envReqs:         nil,
			upstreamOutputs: []string{"network"},
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
			// Phase 67: DynamoDB threads table mapping (channel_id, thread_ts) →
			// claude_session_id for Slack inbound. Bridge handler reads/writes it.
			name:    "dynamodb-slack-threads",
			dir:     filepath.Join(regionDir, "dynamodb-slack-threads"),
			envReqs: nil,
		},
		{
			// Phase 68: DynamoDB stream-messages table mapping (channel_id, slack_ts)
			// → transcript byte offset for per-turn Slack streaming. Hook script
			// writes rows; future Phase B reaction-fork reads them.
			name:    "dynamodb-slack-stream-messages",
			dir:     filepath.Join(regionDir, "dynamodb-slack-stream-messages"),
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
			// Phase 97 (gap GH-BRIDGE-DEPLOY): GitHub App bridge Lambda with Function URL
			// (auth=NONE; X-Hub-Signature-256 HMAC + nonce replay provide application-layer auth).
			// Depends on dynamodb-sandboxes (alias-index GSI) and dynamodb-slack-nonces (shared
			// nonce table). artifacts bucket needed for cold-create EventBridge dispatch.
			name:    "lambda-github-bridge",
			dir:     filepath.Join(regionDir, "lambda-github-bridge"),
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
	var plan bool
	var acceptDestroys bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize all regional infrastructure (network, DynamoDB, SES, S3 replication, TTL handler)",
		Long:  helpText("init"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" {
				awsProfile = "klanker-application"
			}
			// Phase 84.2: --plan always wins over --dry-run / sidecars / lambdas (Decision 1).
			if plan && (sidecarsOnly || lambdasOnly) {
				return fmt.Errorf("--plan cannot be combined with --sidecars or --lambdas")
			}
			if plan {
				return RunInitPlanFunc(cfg, awsProfile, region, verbose, acceptDestroys)
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
	cmd.Flags().BoolVar(&plan, "plan", false,
		"Run terragrunt plan per module with destroy-class safety gate; never applies (independent of --dry-run)")
	cmd.Flags().BoolVar(&acceptDestroys, "i-accept-destroys", false,
		"Clear --plan exit code from 1 to 0 when only failures are protected destroys (per-invocation; never persisted; does NOT auto-apply)")
	if err := cmd.Flags().MarkHidden("i-accept-destroys"); err != nil {
		// Hidden registration shouldn't fail — flag is being registered immediately above.
		// Surface as panic at startup so the bug is loud (matches MarkHidden usage in create.go).
		panic(fmt.Sprintf("MarkHidden i-accept-destroys: %v", err))
	}

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
		fmt.Printf("  1. DNS: ensure hosted zone and NS delegation for %s\n", cfg.GetEmailDomain())
	} else {
		fmt.Printf("  1. DNS: [skip] no domain configured\n")
	}

	// Builds
	fmt.Printf("  2. Build Lambda zips\n")
	if cfg.ArtifactsBucket != "" {
		fmt.Printf("  3. Build and upload sidecars to s3://%s\n", cfg.ArtifactsBucket)
		if cfg.ShouldBuildContainerImages() {
			fmt.Printf("  4. Build and push km-sandbox container image to ECR\n")
			fmt.Printf("  5. Build and push sidecar container images to ECR\n")
		} else {
			fmt.Printf("  4. [skip] km-sandbox ECR image — container_substrates_enabled=false\n")
			fmt.Printf("  5. [skip] sidecar ECR images — container_substrates_enabled=false\n")
		}
		fmt.Printf("  6. Upload create-handler toolchain to s3://%s\n", cfg.ArtifactsBucket)
		fmt.Printf("  7. Force create-handler Lambda cold start\n")
		fmt.Printf("  8. Ensure proxy CA certificate in s3://%s\n", cfg.ArtifactsBucket)
	} else {
		fmt.Printf("  3. [skip] artifacts_bucket not configured — sidecar/toolchain uploads skipped\n")
	}

	// SSM + identity
	if cfg.SafePhrase != "" {
		fmt.Printf("  9. Write safe phrase to SSM %sconfig/remote-create/safe-phrase\n", cfg.GetSsmPrefix())
	}
	fmt.Printf(" 10. Ensure operator email identity (Ed25519 signing key at %s)\n", awspkg.SigningKeyPath(cfg.GetResourcePrefix(), "operator"))

	// Terraform modules
	// Build a map of env vars that runInit would set from config before applying.
	configEnv := map[string]bool{
		"KM_ARTIFACTS_BUCKET": cfg.ArtifactsBucket != "",
		"KM_ROUTE53_ZONE_ID":  cfg.Route53ZoneID != "",
		"KM_DOMAIN":           cfg.Domain != "",
		"KM_REGION":           cfg.PrimaryRegion != "",
		"KM_OPERATOR_EMAIL":   cfg.OperatorEmail != "",
		"KM_SCHEDULER_ROLE_ARN": cfg.SchedulerRoleARN != "",
		"KM_ACCOUNTS_ORGANIZATION": cfg.OrganizationAccountID != "",
		"KM_ACCOUNTS_DNS_PARENT":   cfg.DNSParentAccountID != "",
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
	ExportTerragruntEnvVars(cfg)

	// Phase 91.1: auto-populate KM_SLACK_BOT_USER_ID from SSM at
	// {prefix}slack/bot-user-id (written by km slack init / rotate-token).
	// Non-fatal — first-install will silently skip when the param doesn't exist.
	EnsureSlackBotUserIDFromSSM(ctx, cfg, awsCfg)

	// Phase 84.3: hard-fail early when the artifacts bucket doesn't exist —
	// prevents confusing mid-flight failures in s3-replication, create-handler, etc.
	if cfg.ArtifactsBucket != "" {
		if err := ensureArtifactsBucketExists(ctx, cfg, os.Stderr, nil); err != nil {
			return err
		}
	}

	repoRoot := findRepoRoot()

	printBanner("km init", fmt.Sprintf("%s (%s)", region, compiler.RegionLabel(region)))

	// Always ensure the email subdomain hosted zone AND NS delegation exist.
	// Even if the zone ID is known, delegation in the DNS parent account may be missing.
	// Skip if domain is blank OR if dns_parent account is blank (delegation would have nowhere to go).
	if cfg.Domain != "" && cfg.DNSParentAccountID == "" {
		fmt.Printf("  [warn] DNS delegation skipped — accounts.dns_parent not set in km-config.yaml\n")
	}
	if cfg.Domain != "" && cfg.DNSParentAccountID != "" {
		fmt.Println()
		fmt.Printf("Ensuring DNS zone and NS delegation for %s...\n", cfg.GetEmailDomain())
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

	// Step 2a: Build and push km-sandbox container image to ECR.
	// Skip when container_substrates_enabled=false — container images are only
	// pulled by the docker and ecs substrates. EC2 sandboxes get raw binaries
	// from S3 (Step 2 above), so EC2-only deployments can save ~2–10 min/init
	// by disabling this in km-config.yaml.
	if cfg.ShouldBuildContainerImages() {
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
	} else {
		fmt.Println()
		fmt.Println("Skipping ECR image builds — container_substrates_enabled=false (EC2-only deployment)")
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
		if err := forceLambdaColdStart(ctx, awsCfg, cfg.GetResourcePrefix()); err != nil {
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

	// Auto-generate safe phrase if missing (avoids silently disabling KM-AUTH
	// email-to-create authorization). Persist to km-config.yaml BEFORE writing
	// to SSM so a YAML failure doesn't leave SSM holding a phrase no operator
	// command will ever read.
	if cfg.SafePhrase == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			fmt.Printf("  ⚠ Failed to generate safe phrase: %v\n", err)
		} else {
			generated := hex.EncodeToString(buf)
			if err := persistKMConfigFields(map[string]string{"safe_phrase": generated}); err != nil {
				fmt.Printf("  ⚠ Failed to persist generated safe phrase to km-config.yaml: %v\n", err)
			} else {
				cfg.SafePhrase = generated
				fmt.Printf("  Safe phrase generated and written to km-config.yaml\n")
			}
		}
	}

	// Write safe phrase to SSM (idempotent — overwrites to stay in sync with config).
	if cfg.SafePhrase != "" {
		ssmClient := ssm.NewFromConfig(awsCfg)
		safePhraseKey := cfg.GetSsmPrefix() + "config/remote-create/safe-phrase"
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

	// Step 4: Apply regional infrastructure
	fmt.Println()
	fmt.Println("Applying infrastructure...")
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = verbose
	if err := RunInitWithRunner(runner, repoRoot, region); err != nil {
		return err
	}

	// Step 5: Provision operator identity (Ed25519 signing key + DynamoDB public key).
	// MUST run AFTER terragrunt apply — the dynamodb-identities module above creates
	// the {prefix}-identities table that PublishIdentity writes to. Running it earlier
	// (where the safe-phrase / proxy CA steps live) used to fail on first install with
	// ResourceNotFoundException, leaving the operator with a private key in SSM but no
	// matching public-key row in DDB. Result: km slack test failed with unknown_sender,
	// km email send --from operator failed signature verification.
	//
	// EnsureSandboxIdentity is idempotent — returns the existing key on re-runs without
	// regenerating, so a private key created by an earlier-failing init carries forward
	// and only the publish step needs to succeed on the retry.
	{
		fmt.Println()
		fmt.Println("Ensuring operator email identity...")
		ssmClient := ssm.NewFromConfig(awsCfg)
		kmsKeyAlias := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
		if kmsKeyAlias == "" {
			kmsKeyAlias = cfg.GetPlatformKMSAlias()
		}
		operatorID := "operator"
		pubKey, identErr := awspkg.EnsureSandboxIdentity(ctx, ssmClient, cfg.GetResourcePrefix(), operatorID, kmsKeyAlias)
		if identErr != nil {
			fmt.Printf("  ⚠ Operator identity key generation failed: %v\n", identErr)
		} else {
			identityTableName := cfg.GetIdentityTableName()
			operatorEmail := fmt.Sprintf("operator@%s", cfg.GetEmailDomain())
			dynamoClient := dynamodb.NewFromConfig(awsCfg)
			if pubErr := awspkg.PublishIdentity(ctx, dynamoClient, identityTableName, operatorID, operatorEmail, pubKey, nil, "required", "required", "off", "operator", []string{"*"}); pubErr != nil {
				fmt.Printf("  ⚠ Operator identity publish failed: %v\n", pubErr)
			} else {
				fmt.Printf("  ✓ Operator identity: Ed25519 key at /%s/sandbox/operator/signing-key\n", cfg.GetResourcePrefix())
			}
		}
	}

	// Display email-to-create details if operator email and safe phrase are configured.
	if cfg.OperatorEmail != "" || cfg.SafePhrase != "" {
		fmt.Println()
		fmt.Println("Email-to-create:")
		fmt.Printf("  Send to:     operator@%s\n", cfg.GetEmailDomain())
		if cfg.OperatorEmail != "" {
			fmt.Printf("  Operator:    %s\n", cfg.OperatorEmail)
		}
		if cfg.SafePhrase != "" {
			fmt.Printf("  KM-AUTH:     %s\n", cfg.SafePhrase)
		}
		fmt.Printf("  SSM key:     %sconfig/remote-create/safe-phrase\n", cfg.GetSsmPrefix())
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
		if err := forceLambdaColdStart(ctx, awsCfg, cfg.GetResourcePrefix()); err != nil {
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
		if err := forceLambdaColdStart(ctx, awsCfg, cfg.GetResourcePrefix()); err != nil {
			fmt.Printf("  [warn] Lambda cold start trigger failed: %v\n", err)
		} else {
			fmt.Printf("  ✓ Lambda environment updated\n")
		}
	}

	fmt.Println()
	fmt.Println("Done.")
	return nil
}

// ExportTerragruntEnvVars exports the full set of env vars that Terragrunt's site.hcl
// (and the per-module terragrunt.hcl files) consume via get_env(). Exported so the
// cmd_test package can verify the correct vars are set without triggering a full
// runInit (which requires real AWS credentials).
//
// Every km command that invokes terragrunt must call this exactly once before the
// first terragrunt invocation. The current canonical set is:
//
//	KM_ARTIFACTS_BUCKET, KM_ACCOUNTS_ORGANIZATION, KM_ACCOUNTS_DNS_PARENT,
//	KM_ACCOUNTS_APPLICATION, KM_DOMAIN, KM_REGION, KM_REGION_LABEL,
//	KM_OPERATOR_EMAIL, KM_SCHEDULER_ROLE_ARN, KM_RESOURCE_PREFIX,
//	KM_EMAIL_SUBDOMAIN, KM_ROUTE53_ZONE_ID
//
// Phase 84.1 (plan 01) added KM_ROUTE53_ZONE_ID + KM_REGION_LABEL to close GAP-1 /
// GAP-7 from Phase 84 UAT (`km bootstrap --shared-ses` previously failed to apply
// the foundation MX/DKIM/verification records because KM_ROUTE53_ZONE_ID was unset).
// Renamed in plan 84.1-01 Task 2 from its prior, narrower-scoped name — single
// canonical helper across all 8 production callers, NO shim (H5, plan-checker rev 1).
func ExportTerragruntEnvVars(cfg *config.Config) {
	// Phase 84.3: warnAndSetEnv emits a drift WARN to stderr when an env var is
	// already set to a different value than the yaml-configured value, then sets
	// the env var only when it is currently unset. env-wins semantics preserved.
	// yamlKey is the dotted yaml path used in the WARN message (e.g. "region").
	//
	// Phase 84.3 gap closure 1: use cfg.YAMLDefaults[yamlKey] for the drift
	// comparison instead of cfgVal. After viper AutomaticEnv(), cfgVal already
	// equals envVal for env-bound keys (e.g. KM_REGION bakes into cfg.PrimaryRegion),
	// so the comparison cfgVal != envVal is always false and no WARN fires. The
	// YAMLDefaults snapshot captures the raw yaml value before env baking.
	warnAndSetEnv := func(envKey, yamlKey, cfgVal string) {
		envVal := os.Getenv(envKey)
		if envVal == "" {
			// env not set — no drift possible; set from cfgVal if available.
			if cfgVal != "" {
				os.Setenv(envKey, cfgVal) //nolint:errcheck
			}
			return
		}
		// Use yaml snapshot for drift comparison; fall back to cfgVal if no snapshot.
		// After viper AutomaticEnv(), cfgVal == envVal for env-bound keys, so the
		// snapshot is required to detect real yaml↔env divergence (Phase 84.3 gap 1).
		yamlVal := cfgVal
		if cfg.YAMLDefaults != nil {
			if snap, ok := cfg.YAMLDefaults[yamlKey]; ok {
				yamlVal = snap
			}
		}
		if yamlVal != "" && envVal != yamlVal {
			fmt.Fprintf(os.Stderr, "WARN: %s=%s (env) overrides km-config.yaml %s=%s\n", envKey, envVal, yamlKey, yamlVal)
		}
		// env wins — do not override with cfg value
	}

	// Phase 84.1: KM_ROUTE53_ZONE_ID — required by infra/live/use1/ses-shared-rule-set/
	// terragrunt.hcl get_env("KM_ROUTE53_ZONE_ID", "") for DKIM / MX / verification
	// records. Was previously set only inside runInit via ensureSandboxHostedZone,
	// which never fires from km bootstrap. Closes GAP-1 (Phase 84 UAT).
	warnAndSetEnv("KM_ROUTE53_ZONE_ID", "route53_zone_id", cfg.Route53ZoneID)

	// Phase 84.1: KM_REGION_LABEL — short region form (e.g. "use1") consumed by
	// site.hcl and various terragrunt.hcl files. Derived via compiler.RegionLabel.
	regionLabel := ""
	if cfg.PrimaryRegion != "" {
		regionLabel = compiler.RegionLabel(cfg.PrimaryRegion)
	}
	warnAndSetEnv("KM_REGION_LABEL", "region_label", regionLabel)

	warnAndSetEnv("KM_ARTIFACTS_BUCKET", "artifacts_bucket", cfg.ArtifactsBucket)

	// KM_ACCOUNTS_* — yaml-authoritative for reads (Phase 84.3 Plan 02); exports
	// still happen so terragrunt subprocesses see the correct values.
	warnAndSetEnv("KM_ACCOUNTS_ORGANIZATION", "accounts.organization", cfg.OrganizationAccountID)
	warnAndSetEnv("KM_ACCOUNTS_DNS_PARENT", "accounts.dns_parent", cfg.DNSParentAccountID)
	warnAndSetEnv("KM_ACCOUNTS_APPLICATION", "accounts.application", cfg.ApplicationAccountID)

	warnAndSetEnv("KM_DOMAIN", "domain", cfg.Domain)
	warnAndSetEnv("KM_REGION", "region", cfg.PrimaryRegion)
	warnAndSetEnv("KM_OPERATOR_EMAIL", "operator_email", cfg.OperatorEmail)
	warnAndSetEnv("KM_SCHEDULER_ROLE_ARN", "scheduler_role_arn", cfg.SchedulerRoleARN)

	// Phase 66: multi-instance prefix and email subdomain.
	// Always export these so site.hcl get_env("KM_RESOURCE_PREFIX", "km") picks up the value.
	// An empty string is a valid export (site.hcl fallback "km" kicks in).
	// No cfgVal guard — warnAndSetEnv only warns when cfgVal != ""; prefix can be "".
	//
	// Phase 84.3 gap closure 1: use YAMLDefaults for drift comparison so the WARN
	// fires even when env baking makes cfg.ResourcePrefix == envVal.
	prefix := cfg.GetResourcePrefix()
	yamlPrefix := prefix
	if cfg.YAMLDefaults != nil {
		if snap, ok := cfg.YAMLDefaults["resource_prefix"]; ok && snap != "" {
			yamlPrefix = snap
		}
	}
	if envVal := os.Getenv("KM_RESOURCE_PREFIX"); envVal != "" && yamlPrefix != "" && envVal != yamlPrefix {
		fmt.Fprintf(os.Stderr, "WARN: KM_RESOURCE_PREFIX=%s (env) overrides km-config.yaml resource_prefix=%s\n", envVal, yamlPrefix)
	}
	if os.Getenv("KM_RESOURCE_PREFIX") == "" {
		os.Setenv("KM_RESOURCE_PREFIX", prefix) //nolint:errcheck
	}

	// Phase 84.3 gap closure 1: use YAMLDefaults for drift comparison for email_subdomain.
	yamlSubdomain := cfg.EmailSubdomain
	if cfg.YAMLDefaults != nil {
		if snap, ok := cfg.YAMLDefaults["email_subdomain"]; ok && snap != "" {
			yamlSubdomain = snap
		}
	}
	if envVal := os.Getenv("KM_EMAIL_SUBDOMAIN"); envVal != "" && yamlSubdomain != "" && envVal != yamlSubdomain {
		fmt.Fprintf(os.Stderr, "WARN: KM_EMAIL_SUBDOMAIN=%s (env) overrides km-config.yaml email_subdomain=%s\n", envVal, yamlSubdomain)
	}
	if os.Getenv("KM_EMAIL_SUBDOMAIN") == "" {
		os.Setenv("KM_EMAIL_SUBDOMAIN", cfg.EmailSubdomain) //nolint:errcheck
	}

	// Phase 91.1: KM_SLACK_MENTION_ONLY — install-level polite-bot default
	// consumed by infra/live/use1/lambda-slack-bridge/terragrunt.hcl
	// get_env("KM_SLACK_MENTION_ONLY", "false"). Only export when the operator
	// has explicitly set slack.mention_only in km-config.yaml — absent
	// (cfg.Slack.MentionOnly == nil) leaves any existing env var untouched and
	// lets the terragrunt fallback ("false") apply. env-wins semantics preserved.
	if cfg.Slack.MentionOnly != nil {
		yamlSlackMentionOnly := strconv.FormatBool(*cfg.Slack.MentionOnly)
		if envVal := os.Getenv("KM_SLACK_MENTION_ONLY"); envVal != "" && envVal != yamlSlackMentionOnly {
			fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_MENTION_ONLY=%s (env) overrides km-config.yaml slack.mention_only=%s\n", envVal, yamlSlackMentionOnly)
		} else if envVal == "" {
			os.Setenv("KM_SLACK_MENTION_ONLY", yamlSlackMentionOnly) //nolint:errcheck
		}
	}

	// Phase 91.4: KM_SLACK_REACT_ALWAYS — install-level first-only-react default
	// consumed by infra/live/use1/lambda-slack-bridge/terragrunt.hcl
	// get_env("KM_SLACK_REACT_ALWAYS", "true"). Only export when the operator
	// has explicitly set slack.react_always in km-config.yaml — absent leaves
	// any existing env var untouched and lets the terragrunt fallback ("true")
	// apply. env-wins semantics preserved.
	if cfg.Slack.ReactAlways != nil {
		yamlSlackReactAlways := strconv.FormatBool(*cfg.Slack.ReactAlways)
		if envVal := os.Getenv("KM_SLACK_REACT_ALWAYS"); envVal != "" && envVal != yamlSlackReactAlways {
			fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_REACT_ALWAYS=%s (env) overrides km-config.yaml slack.react_always=%s\n", envVal, yamlSlackReactAlways)
		} else if envVal == "" {
			os.Setenv("KM_SLACK_REACT_ALWAYS", yamlSlackReactAlways) //nolint:errcheck
		}
	}

	// Phase 95: KM_SLACK_PEER_BRIDGES — comma-joined list of sibling bridge /events URLs.
	// Consumed by infra/live/use1/lambda-slack-bridge/terragrunt.hcl
	// get_env("KM_SLACK_PEER_BRIDGES", ""). Only export when the operator has explicitly
	// set slack.peer_bridges in km-config.yaml. Empty list => omit => terragrunt default ""
	// applies => federation off. env-wins semantics: when the env var is already set to a
	// DIFFERENT value, emit a drift WARN and do NOT overwrite it. strings.Join used instead
	// of strconv.FormatBool since the value is a []string, not a *bool.
	if len(cfg.Slack.PeerBridges) > 0 {
		yamlPeerBridges := strings.Join(cfg.Slack.PeerBridges, ",")
		if envVal := os.Getenv("KM_SLACK_PEER_BRIDGES"); envVal != "" && envVal != yamlPeerBridges {
			fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_PEER_BRIDGES=%s (env) overrides km-config.yaml slack.peer_bridges=%s\n", envVal, yamlPeerBridges)
		} else if envVal == "" {
			os.Setenv("KM_SLACK_PEER_BRIDGES", yamlPeerBridges) //nolint:errcheck
		}
	}

	// Phase 96: KM_SLACK_DEFAULT_ROUTER — front-door orphan-channel router toggle.
	// Consumed by infra/live/use1/lambda-slack-bridge/terragrunt.hcl
	// get_env("KM_SLACK_DEFAULT_ROUTER", "false"). Only export when the operator has
	// explicitly set slack.default_router in km-config.yaml (nil => omit => terragrunt
	// default "false" applies => router dormant, Phase 95 byte-identical). env-wins:
	// when the env var is already set to a DIFFERENT value, emit a drift WARN and do
	// NOT overwrite it. Mirrors the MentionOnly / ReactAlways *bool pattern exactly.
	if cfg.Slack.DefaultRouter != nil {
		yamlSlackDefaultRouter := strconv.FormatBool(*cfg.Slack.DefaultRouter)
		if envVal := os.Getenv("KM_SLACK_DEFAULT_ROUTER"); envVal != "" && envVal != yamlSlackDefaultRouter {
			fmt.Fprintf(os.Stderr, "WARN: KM_SLACK_DEFAULT_ROUTER=%s (env) overrides km-config.yaml slack.default_router=%s\n", envVal, yamlSlackDefaultRouter)
		} else if envVal == "" {
			os.Setenv("KM_SLACK_DEFAULT_ROUTER", yamlSlackDefaultRouter) //nolint:errcheck
		}
	}

	// Phase 97: KM_GITHUB_REPOS — JSON-encoded repository-to-sandbox mapping.
	// Consumed by the GitHub bridge Lambda to resolve which sandbox to dispatch
	// a PR comment to. Gate: only export when at least one repo entry is configured
	// (len > 0) — absent github: block leaves KM_GITHUB_REPOS unset (dormant
	// byte-identity, no new env on existing Lambdas). env-wins: when the env var
	// is already set to a DIFFERENT value, emit a drift WARN and do NOT overwrite.
	// Value is JSON-marshalled so the Lambda can parse the structured map without
	// bespoke split/decode logic. Struct kept inline; no new config helper needed.
	if len(cfg.Github.Repos) > 0 {
		type githubExportPayload struct {
			Repos          []config.GithubRepoEntry `json:"repos"`
			DefaultProfile string                   `json:"default_profile,omitempty"`
		}
		payload := githubExportPayload{
			Repos:          cfg.Github.Repos,
			DefaultProfile: cfg.Github.DefaultProfile,
		}
		jsonBytes, err := json.Marshal(payload)
		if err == nil {
			yamlGithubRepos := string(jsonBytes)
			if envVal := os.Getenv("KM_GITHUB_REPOS"); envVal != "" && envVal != yamlGithubRepos {
				fmt.Fprintf(os.Stderr, "WARN: KM_GITHUB_REPOS=%s (env) overrides km-config.yaml github.repos=%s\n", envVal, yamlGithubRepos)
			} else if envVal == "" {
				os.Setenv("KM_GITHUB_REPOS", yamlGithubRepos) //nolint:errcheck
			}
		}
	}
}

// EnsureSlackBotUserIDFromSSM auto-populates KM_SLACK_BOT_USER_ID from SSM at
// {prefix}slack/bot-user-id when the env var is not already set. Phase 91.1:
// removes the operator burden of `export KM_SLACK_BOT_USER_ID=$(aws ssm ...)`
// before every `km init`. The SSM parameter is written by `km slack init` /
// `km slack rotate-token` (Phase 91 Plan 04). env-wins semantics: when the
// operator has explicitly exported a value, this helper leaves it alone.
//
// Non-fatal on any error (missing param, network, IAM): logs a single WARN
// line to stderr and returns nil so the terragrunt.hcl fallback ("") applies.
// Callers should pass an awsCfg already validated by ValidateCredentials so we
// know the SSM Get can run.
func EnsureSlackBotUserIDFromSSM(ctx context.Context, cfg *config.Config, awsCfg aws.Config) {
	if os.Getenv("KM_SLACK_BOT_USER_ID") != "" {
		return // env wins
	}
	if cfg == nil {
		return
	}
	ssmClient := ssm.NewFromConfig(awsCfg)
	param := cfg.GetSsmPrefix() + "slack/bot-user-id"
	out, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(param)})
	if err != nil {
		// Common during first-install (param doesn't exist yet) — quiet WARN.
		fmt.Fprintf(os.Stderr, "  [info] KM_SLACK_BOT_USER_ID not auto-set (SSM %s unavailable: %v)\n", param, err)
		return
	}
	if out.Parameter == nil || out.Parameter.Value == nil || *out.Parameter.Value == "" {
		return
	}
	os.Setenv("KM_SLACK_BOT_USER_ID", *out.Parameter.Value) //nolint:errcheck
}

// ensureArtifactsBucketExists checks that the configured artifacts bucket exists
// in S3. If it does not exist (404), it returns a hard-fail error naming both
// recovery commands so the operator knows their next step. This runs before any
// terragrunt invocation in the apply path so the operator gets an actionable
// message immediately rather than a confusing mid-flight failure.
//
// The io.Writer parameter is reserved for future structured output; the error
// message carries all operator-visible text. The s3client variadic parameter
// is the test seam — production callers pass no client and the real S3 client
// is constructed from the ambient AWS config.
func ensureArtifactsBucketExists(ctx context.Context, cfg *config.Config, _ io.Writer, s3client S3HeadBucketAPI) error {
	var c S3HeadBucketAPI
	if s3client != nil {
		c = s3client
	} else {
		awsCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", cfg.PrimaryRegion)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		c = s3.NewFromConfig(awsCfg)
	}
	_, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(cfg.ArtifactsBucket)})
	if err == nil {
		return nil
	}
	// Check for 404 / NotFound using smithy error pattern (matches test mock).
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if code == "NotFound" || code == "NoSuchBucket" {
			return fmt.Errorf(`✗ artifacts bucket %s does not exist.

Run: km bootstrap --all --dry-run=false
(or `+"`km bootstrap --dry-run=false`"+` for just the SCP/artifacts subflow —
 `+"`km bootstrap --shared-ses`"+` alone does NOT create the artifacts bucket.)`, cfg.ArtifactsBucket)
		}
	}
	// Other errors (auth, network) propagate without masquerading as missing-bucket.
	return fmt.Errorf("HeadBucket %s: %w", cfg.ArtifactsBucket, err)
}

// upstreamOutputsExist checks whether each named upstream module has produced
// an outputs.json file. moduleDir is the directory of the module being probed
// (e.g. infra/live/use1/efs); upstream modules are resolved as siblings
// (infra/live/use1/<upstream>/outputs.json). Returns the names of any upstream
// modules whose outputs.json is missing.
//
// Phase 84.3 closure (d): used by RunInitPlanWithRunner to skip modules whose
// upstream dependencies have not yet been applied. Only efs has a non-empty
// upstreamOutputs list in Phase 84.3; all other modules carry a zero-value
// slice and are never probed.
func upstreamOutputsExist(moduleDir string, upstreamNames []string) (missing []string) {
	for _, up := range upstreamNames {
		outputsPath := filepath.Join(moduleDir, "..", up, "outputs.json")
		if _, err := os.Stat(outputsPath); errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, up)
		}
	}
	return missing
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

		// Phase 84: preflight check for the regional ses module.
		// The ses v2.0.0 module references "sandbox-email-shared" as a string constant —
		// no Terraform data source for SES rule sets exists. If the shared rule set
		// doesn't exist, the Terraform apply will fail mid-flight with RuleSetDoesNotExist.
		// Fail fast with a clear actionable message instead.
		if mod.name == "ses" && InitSESPreflight != nil {
			if preflightErr := InitSESPreflight(ctx); preflightErr != nil {
				return preflightErr
			}
		}

		// Phase 84: reconfigure the ses module before apply. The module source
		// changed from v1.0.0 to v2.0.0, which gives terragrunt a fresh cache
		// directory whose .terraform/ has never been initialized. Auto-init
		// doesn't fire when the backend config is new to this working tree.
		// Reconfigure is a no-op when the backend is already initialized.
		//
		// Phase 84.1-02: bound Reconfigure by reconfigureTimeout (default 2min).
		// Reconfigure is normally seconds; a 2-min bound surfaces a backend hang
		// quickly without artificially cutting off a slow first-init.
		if mod.name == "ses" {
			rcfgCtx, rcfgCancel := context.WithTimeout(ctx, reconfigureTimeout)
			err := runner.Reconfigure(rcfgCtx, mod.dir)
			rcfgCancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("reconfiguring ses backend wedged after %s — see OPERATOR-GUIDE.md § Phase 84.1 state-digest recovery: %w", reconfigureTimeout, err)
				}
				return fmt.Errorf("reconfiguring ses backend: %w", err)
			}
		}

		// Phase 84.1-02 (GAP-5): per-module Apply timeout. Without this bound a
		// wedged terragrunt blocks km init forever (see 84-10-UAT.md lines 53-72:
		// the original incident hung 10+ minutes before manual Ctrl-C). The
		// default per-module bound comes from ModuleTimeoutFunc (test-overridable);
		// production maps long-DNS/IAM-propagation modules to 10min, lambda+ses
		// modules to 5min, and everything else to 3min.
		fmt.Printf("  Applying %s...", mod.name)
		timeout := ModuleTimeoutFunc(mod.name)
		applyCtx, applyCancel := context.WithTimeout(ctx, timeout)
		err := runner.Apply(applyCtx, mod.dir)
		applyCancel()
		if err != nil {
			fmt.Println() // newline after the "Applying X..." prefix on failure
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("module %s wedged after %s — kill orphan terragrunt PID (see heartbeat above) and consult OPERATOR-GUIDE.md § Phase 84.1 state-digest recovery for S3 state / DDB lock-table repair: %w", mod.name, timeout, err)
			}
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
		// AND persist as create_handler_lambda_arn in km-config.yaml so subsequent
		// km at create invocations can find it without manual operator paste.
		if mod.name == "create-handler" {
			outputMap, outErr := runner.Output(ctx, mod.dir)
			if outErr == nil {
				if arnVal, ok := outputMap["lambda_function_arn"]; ok {
					arn := fmt.Sprintf("%v", extractValue(arnVal))
					os.Setenv("KM_CREATE_HANDLER_ARN", arn)
					fmt.Printf("  Create handler ARN: %s\n", arn)
					if err := persistKMConfigFields(map[string]string{"create_handler_lambda_arn": arn}); err != nil {
						fmt.Printf("  Warning: could not persist create_handler_lambda_arn to km-config.yaml: %v\n", err)
					}
				}
			}
		}

		// After ttl-handler module: persist TTL Lambda ARN + scheduler role ARN
		// to km-config.yaml so km create / km at can wire TTL teardown without
		// manual operator paste.
		if mod.name == "ttl-handler" {
			outputMap, outErr := runner.Output(ctx, mod.dir)
			if outErr == nil {
				updates := map[string]string{}
				if arnVal, ok := outputMap["lambda_function_arn"]; ok {
					arn := fmt.Sprintf("%v", extractValue(arnVal))
					updates["ttl_lambda_arn"] = arn
					fmt.Printf("  TTL handler ARN: %s\n", arn)
				}
				if roleVal, ok := outputMap["scheduler_role_arn"]; ok {
					role := fmt.Sprintf("%v", extractValue(roleVal))
					updates["scheduler_role_arn"] = role
					fmt.Printf("  Scheduler role ARN: %s\n", role)
				}
				if err := persistKMConfigFields(updates); err != nil {
					fmt.Printf("  Warning: could not persist ttl-handler ARNs to km-config.yaml: %v\n", err)
				}
			}
		}
	}

	fmt.Println()
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("Init complete for %s. Ready for: km create <profile.yaml>\n", region)
	return nil
}

// ─── Phase 84.2: km init --plan ──────────────────────────────────────────────

// runInitPlan is the production entry point for km init --plan. Loads AWS config,
// validates credentials, calls ExportTerragruntEnvVars (Phase 84.1-01 contract),
// constructs the real *terragrunt.Runner, then delegates to RunInitPlanWithRunner
// (the testable core). Mirrors the runInit → RunInitWithRunner split at init.go.
//
// Phase 84.2. Per CONTEXT.md decisions: --plan is independent of --dry-run; it
// NEVER applies. --i-accept-destroys clears the exit code from 1 to 0 but still
// prints the trip list (operator-visibility contract).
func runInitPlan(cfg *config.Config, awsProfile, region string, verbose, acceptDestroys bool) error {
	// Phase 84.3 gap 4: hard-fail early on placeholder artifacts_bucket.
	// config.Load() is the primary gate; this is defense-in-depth for the plan path.
	if err := validateArtifactsBucket(cfg.ArtifactsBucket); err != nil {
		return fmt.Errorf("artifacts_bucket misconfigured: %w", err)
	}

	ctx := context.Background()

	// 1. Validate AWS credentials (matches runInit credential check)
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}

	// 2. Export env vars — Phase 84.1-01 contract (init.go ExportTerragruntEnvVars pattern).
	//    MUST happen exactly once before any terragrunt invocation.
	ExportTerragruntEnvVars(cfg)

	// 3. Construct runner (Verbose=false — RunInitPlanWithRunner captures stdout
	//    per-module and echoes based on verbose flag post-hoc).
	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = false

	// 4. Delegate to testable core
	return RunInitPlanWithRunner(runner, repoRoot, region, verbose, acceptDestroys)
}

// runInitPlanWithWriter is the writer-aware test seam for runInitPlan.
// Production callers use runInitPlan (which writes to os.Stdout via fmt.Print*);
// cmd_test uses this directly to exercise the plan loop without real AWS.
// Note: output currently goes via fmt.Print* to os.Stdout; the w parameter is
// reserved for future full writer-routing and is accepted but not used here
// (bootstrap plan tests capture os.Stdout via pipe for trip/summary assertions).
//
// Phase 84.2 test seam — referenced by init_plan_test.go.
func runInitPlanWithWriter(cfg *config.Config, awsProfile, region string, w io.Writer, verbose, acceptDestroys bool) error {
	// Phase 84.3 gap 4: validate artifacts_bucket before any terragrunt work.
	// Defense-in-depth: runInitPlan also validates, but tests call this seam directly.
	if err := validateArtifactsBucket(cfg.ArtifactsBucket); err != nil {
		return fmt.Errorf("artifacts_bucket misconfigured: %w", err)
	}
	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = false
	_ = w // output goes via fmt.Print* to os.Stdout; w param reserved for future writer wiring
	return RunInitPlanWithRunner(runner, repoRoot, region, verbose, acceptDestroys)
}

// RunInitPlanWithRunner is the testable core of runInitPlan: runs the per-module
// plan loop + gate against an injected InitRunner. Mirrors RunInitWithRunner.
// Production callers go through runInitPlan which constructs the real *terragrunt.Runner;
// cmd_test uses this directly with a mockPlanRunner.
//
// The split keeps the production-only AWS setup (LoadAWSConfig, ValidateCredentials,
// ExportTerragruntEnvVars) out of the testable core so Wave 0 tests don't need
// real AWS / env-var manipulation.
func RunInitPlanWithRunner(runner InitRunner, repoRoot, region string, verbose, acceptDestroys bool) error {
	ctx := context.Background()
	regionLabel := compiler.RegionLabel(region)
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)

	if err := ensureRegionHCL(regionDir, regionLabel, region); err != nil {
		return err
	}

	fmt.Printf("km init --plan: %s (%s)\n", region, regionLabel)
	fmt.Println()

	// Gap #1 (Phase 84.4.1.1): build Lambda zips before planning so
	// filebase64sha256(build/create-handler.zip) succeeds on fresh clones.
	// Warn-and-continue mirrors runInit's behavior at init.go:491-496.
	fmt.Printf("Building Lambdas [%s]...\n", version.String())
	if err := BuildLambdaZipsFunc(repoRoot); err != nil {
		fmt.Printf("  [warn] Lambda build failed: %v\n", err)
	}

	// Module loop (matches RunInitWithRunner skip semantics)
	modules := regionalModules(regionDir)
	reports := make([]planreport.Report, 0, len(modules))

	for _, m := range modules {
		if _, err := os.Stat(m.dir); os.IsNotExist(err) {
			fmt.Printf("  [skip] %s — directory not found\n", m.name)
			continue
		}
		skipped := false
		for _, envVar := range m.envReqs {
			if os.Getenv(envVar) == "" {
				fmt.Printf("  [skip] %s — %s not set\n", m.name, envVar)
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}

		// Phase 84.3 closure (d): skip modules whose upstream outputs.json is missing.
		// This handles fresh installs where only some modules have been applied.
		// Skipped modules are NOT passed to planreport.Evaluate — gate only counts planned modules.
		if missing := upstreamOutputsExist(m.dir, m.upstreamOutputs); len(missing) > 0 {
			for _, up := range missing {
				fmt.Printf("  [skip] %s — depends on %s/outputs.json (apply %s first)\n", m.name, up, up)
			}
			continue
		}

		report, planErr := planModule(ctx, runner, m, verbose)
		if planErr != nil {
			// Hard plan failure — stop loop with module-named stderr.
			return fmt.Errorf("planning %s: %w", m.name, planErr)
		}
		reports = append(reports, report)
	}

	// Gate
	result := planreport.Evaluate(reports, acceptDestroys)
	if result.Blocked {
		printTripBlock("km init --plan", result.Trips)
		return fmt.Errorf("destroy-class gate tripped (re-run with --i-accept-destroys to override)")
	}
	if len(result.Trips) > 0 {
		// Override active — trips listed for visibility per CONTEXT.md Decision 3.
		printTripBlock("km init --plan", result.Trips)
		fmt.Println("  (override active via --i-accept-destroys — exit 0; no apply will run)")
	}

	printAggregateSummary(reports)
	fmt.Println("Run 'km init --dry-run=false' to apply.")
	return nil
}

// ensureRegionHCL writes infra/live/<regionLabel>/region.hcl if missing — the file
// is gitignored, so fresh clones don't have it, and the foundation+regional
// terragrunt.hcl files all `read_terragrunt_config("../region.hcl")`.
// Idempotent; matches the cluster.go bootstrap pattern.
func ensureRegionHCL(regionDir, regionLabel, region string) error {
	regionHCLPath := filepath.Join(regionDir, "region.hcl")
	if _, err := os.Stat(regionHCLPath); err == nil {
		return nil
	}
	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		return fmt.Errorf("creating region directory: %w", err)
	}
	regionHCL := fmt.Sprintf("locals {\n  region_label = %q\n  region_full  = %q\n}\n", regionLabel, region)
	if err := os.WriteFile(regionHCLPath, []byte(regionHCL), 0o644); err != nil {
		return fmt.Errorf("writing region.hcl: %w", err)
	}
	return nil
}

// planModule runs PlanWithOutput + ShowPlanJSON + planreport.Parse for a single module.
// Returns (Report{ParseFailed: true}, nil) when show/parse fails — the gate treats
// parse-fail as a conservative trip per the locked algorithm. Returns (Report{}, error)
// only when terragrunt plan itself fails (hard-stop signal for the caller).
//
// Per RESEARCH § Pitfall 1, the planFile path passed to PlanWithOutput MUST be absolute —
// os.CreateTemp returns abs paths on macOS/Linux.
//
// Per Pitfall 3, plan failure ≠ parse failure: hard-stop on plan failure (return error),
// conservative-trip on parse failure (return Report{ParseFailed: true}).
func planModule(ctx context.Context, runner InitRunner, m regionalModule, verbose bool) (planreport.Report, error) {
	// Per-module timeout via the existing ModuleTimeoutFunc (init.go).
	// Inherits Phase 84.1-02 runner-layer heartbeat for free via runBounded.
	timeout := ModuleTimeoutFunc(m.name)
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tmpFile, err := os.CreateTemp("", "km-plan-"+m.name+"-*.tfplan")
	if err != nil {
		// Temp-file create failed — log warning and conservative-trip.
		fmt.Fprintf(os.Stderr, "  warning: %s tempfile create: %v (conservative-trip)\n", m.name, err)
		return planreport.Report{Module: m.name, ParseFailed: true, ParseError: err}, nil
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close() // PlanWithOutput owns the file from here
	defer os.Remove(tmpPath)

	var stdoutBuf bytes.Buffer
	fmt.Printf("  Planning %s...", m.name)
	if err := runner.PlanWithOutput(pctx, m.dir, tmpPath, &stdoutBuf); err != nil {
		fmt.Println(" FAILED")
		if errors.Is(err, context.DeadlineExceeded) {
			return planreport.Report{}, fmt.Errorf("module %s wedged after %s: %w", m.name, timeout, err)
		}
		return planreport.Report{}, err
	}

	jsonBytes, err := runner.ShowPlanJSON(pctx, m.dir, tmpPath)
	if err != nil {
		fmt.Println(" parse-fail (warn)")
		fmt.Fprintf(os.Stderr, "  warning: %s show -json: %v (conservative-trip)\n", m.name, err)
		// cmd.Output() in ShowPlanJSON puts terragrunt's stderr in *exec.ExitError.Stderr.
		// Surface it so operators can see why the JSON render failed (provider mismatch,
		// missing lambda zip, stale terragrunt cache, etc.) instead of just "exit status 1".
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			indented := strings.ReplaceAll(strings.TrimRight(string(exitErr.Stderr), "\n"), "\n", "\n    ")
			fmt.Fprintf(os.Stderr, "    %s\n", indented)
		}
		return planreport.Report{Module: m.name, ParseFailed: true, ParseError: err}, nil
	}
	report, err := planreport.Parse(jsonBytes)
	if err != nil {
		fmt.Println(" parse-fail (warn)")
		fmt.Fprintf(os.Stderr, "  warning: %s planreport.Parse: %v (conservative-trip)\n", m.name, err)
		return planreport.Report{Module: m.name, ParseFailed: true, ParseError: err}, nil
	}
	report.Module = m.name
	fmt.Printf(" %s\n", summarizeReport(report))
	if verbose && stdoutBuf.Len() > 0 {
		os.Stdout.Write(stdoutBuf.Bytes())
		fmt.Println()
	}
	return report, nil
}

// summarizeReport returns a one-line summary like "2 to add, 0 to change, 1 to destroy".
// Appends " ⚠" when there is ≥1 destroy or replace, " ✓" when fully clean.
func summarizeReport(r planreport.Report) string {
	adds := len(r.Adds)
	changes := len(r.Changes)
	destroys := len(r.Destroys) + len(r.Replaces) // replaces are destructive
	suffix := " ✓"
	if destroys > 0 {
		suffix = " ⚠"
	}
	return fmt.Sprintf("%d to add, %d to change, %d to destroy%s", adds, changes, destroys, suffix)
}

// printTripBlock prints the destroy-class gate trip block per the locked format
// in CONTEXT.md decisions § Trip block format. Always full —
// never abbreviated by --verbose absence.
func printTripBlock(invoker string, trips []planreport.Trip) {
	fmt.Println()
	fmt.Printf("✗ %s would destroy %d protected resources:\n\n", invoker, len(trips))
	// Group by module for readable output
	byModule := map[string][]planreport.Trip{}
	moduleOrder := []string{}
	for _, t := range trips {
		if _, ok := byModule[t.Module]; !ok {
			moduleOrder = append(moduleOrder, t.Module)
		}
		byModule[t.Module] = append(byModule[t.Module], t)
	}
	for _, mod := range moduleOrder {
		fmt.Printf("  %s:\n", mod)
		for _, t := range byModule[mod] {
			if t.Action == "PARSE-FAIL" {
				fmt.Printf("    - <parse failed> [PARSE-FAIL] — %s\n", t.Reason)
				continue
			}
			fmt.Printf("    - %-40s [%s]\n", t.Address, t.Action)
		}
		fmt.Println()
	}
	fmt.Println("These resource types are on the protected list because past incidents")
	fmt.Println("caused unrecoverable data loss (see pkg/terragrunt/planreport/protected.go).")
	fmt.Println()
	fmt.Println("To proceed anyway, re-run with --i-accept-destroys (you must understand")
	fmt.Println("why each resource is destroying — terragrunt apply will not ask again).")
	fmt.Println()
}

// printAggregateSummary prints the cross-module roll-up after a clean (or override-cleared) plan.
func printAggregateSummary(reports []planreport.Report) {
	var totalAdds, totalChanges, totalDestroys int
	for _, r := range reports {
		totalAdds += len(r.Adds)
		totalChanges += len(r.Changes)
		totalDestroys += len(r.Destroys) + len(r.Replaces)
	}
	fmt.Println()
	fmt.Printf("Total across %d modules: %d to add, %d to change, %d to destroy\n",
		len(reports), totalAdds, totalChanges, totalDestroys)
	fmt.Println()
}

// ─────────────────────────────────────────────────────────────────────────────

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
// State bucket: tf-{prefix}-state-{regionLabel}  (prefix = KM_RESOURCE_PREFIX env var, default "km")
// State key:    tf-{prefix}/{regionLabel}/{module}/terraform.tfstate
func fetchAndCacheOutputs(repoRoot, regionLabel, module string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use KM_RESOURCE_PREFIX env var (set by ExportTerragruntEnvVars) to mirror site.hcl naming.
	resourcePrefix := os.Getenv("KM_RESOURCE_PREFIX")
	if resourcePrefix == "" {
		resourcePrefix = "km"
	}
	bucket := fmt.Sprintf("tf-%s-state-%s", resourcePrefix, regionLabel)
	key := fmt.Sprintf("tf-%s/%s/%s/terraform.tfstate", resourcePrefix, regionLabel, module)

	awsProfile := os.Getenv("AWS_PROFILE")
	if awsProfile == "" {
		awsProfile = "klanker-terraform"
	}
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
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

// lambdaBuilds returns the ordered list of Lambdas that `km init` cross-compiles
// and packages. This MUST stay in lockstep with the `build-lambdas` Makefile
// target — a Lambda missing here is silently never built by `km init`, so its
// terragrunt unit fails on filebase64sha256(missing-zip) at apply time.
func lambdaBuilds() []lambdaBuild {
	return []lambdaBuild{
		{name: "ttl-handler", srcDir: "cmd/ttl-handler"},
		{name: "budget-enforcer", srcDir: "cmd/budget-enforcer"},
		{name: "github-token-refresher", srcDir: "cmd/github-token-refresher"},
		{name: "email-create-handler", srcDir: "cmd/email-create-handler"},
		{name: "create-handler", srcDir: "cmd/create-handler"},
		// Phase 63: Slack-notify bridge Lambda — accepts signed envelopes from sandboxes.
		{name: "km-slack-bridge", srcDir: "cmd/km-slack-bridge"},
		// Phase 97: GitHub comment-trigger bridge Lambda — verifies webhooks, dispatches @-mentions.
		{name: "km-github-bridge", srcDir: "cmd/km-github-bridge"},
	}
}

// LambdaBuildNames returns the zip names `km init` builds. Exported for testing only.
func LambdaBuildNames() []string {
	builds := lambdaBuilds()
	names := make([]string, len(builds))
	for i, lb := range builds {
		names[i] = lb.name
	}
	return names
}

// buildLambdaZips cross-compiles Lambda binaries for linux/arm64 and packages them as zips.
// Always rebuilds each zip (removes any existing one first) so code changes are
// picked up. Equivalent to `make build-lambdas`.
func buildLambdaZips(repoRoot string) error {
	buildDir := filepath.Join(repoRoot, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return fmt.Errorf("create build dir: %w", err)
	}

	lambdas := lambdaBuilds()

	// Ensure terraform binary is current (version-aware cache via sidecar file).
	// Phase 84.4.1: replaced the os.IsNotExist-only check with terraformIsCurrent
	// so a stale 1.6.6 binary is re-downloaded when tfDesiredVersion bumps.
	terraformPath := filepath.Join(buildDir, "terraform")
	if !terraformIsCurrent(buildDir) {
		fmt.Printf("  Downloading terraform %s for linux/arm64...\n", tfDesiredVersion)
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
		ldflags := fmt.Sprintf("-X github.com/whereiskurt/klanker-maker/pkg/version.Number=%s -X github.com/whereiskurt/klanker-maker/pkg/version.GitCommit=%s",
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

// terraformIsCurrent returns true if the cached terraform binary at
// buildDir/terraform was downloaded for tfDesiredVersion, as recorded by the
// sidecar file buildDir/terraform.version.
//
// Phase 84.4.1: replaces the os.IsNotExist-only cache check at init.go:1594-1601.
// A pre-bump 1.6.6 binary was reused indefinitely because the old check only
// tested binary existence, not version match. This function reads the sidecar
// text file to compare. Cross-arch exec (linux/arm64 binary on macOS) is avoided.
//
// Closes TERRAFORM-VERSION-CACHE-INVALIDATION.
func terraformIsCurrent(buildDir string) bool {
	terraformPath := filepath.Join(buildDir, "terraform")
	if _, err := os.Stat(terraformPath); os.IsNotExist(err) {
		return false
	}
	versionFile := filepath.Join(buildDir, "terraform.version")
	cached, err := os.ReadFile(versionFile)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(cached)) == tfDesiredVersion
}

// downloadTerraform downloads the terraform binary for linux/arm64 to the build directory.
// Version must satisfy infra/live/root.hcl's `required_version` constraint (>= 1.7.0).
// Bumped from 1.6.6 → 1.9.8 in Phase 84.4-08 UAT: km create on the rg fresh install failed
// with `Unsupported Terraform Core version` because the toolchain shipped 1.6.6 but root.hcl
// requires >= 1.7.0. The canonical km install escaped this only because its toolchain was
// uploaded before the root.hcl bump.
func downloadTerraform(buildDir string) error {
	url := fmt.Sprintf("https://releases.hashicorp.com/terraform/%s/terraform_%s_linux_arm64.zip", tfDesiredVersion, tfDesiredVersion)
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

	// Phase 84.4.1: write sidecar version file so terraformIsCurrent() can
	// detect cached-binary staleness without exec'ing the cross-arch binary.
	// Closes TERRAFORM-VERSION-CACHE-INVALIDATION.
	versionFile := filepath.Join(buildDir, "terraform.version")
	if err := os.WriteFile(versionFile, []byte(tfDesiredVersion+"\n"), 0o644); err != nil {
		return fmt.Errorf("write terraform.version sidecar: %w", err)
	}
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
		{name: "km-presence", srcDir: "cmd/km-presence"},
	}

	// Build and upload km binary for EC2 instances (linux/amd64).
	// Instances download this at boot from s3://<bucket>/sidecars/km.
	{
		fmt.Printf("  Building km (linux/amd64)...\n")
		kmPath := filepath.Join(buildDir, "km-linux")
		kmLdflags := fmt.Sprintf("-X github.com/whereiskurt/klanker-maker/pkg/version.Number=%s -X github.com/whereiskurt/klanker-maker/pkg/version.GitCommit=%s",
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
		scLdflags := fmt.Sprintf("-X github.com/whereiskurt/klanker-maker/pkg/version.Number=%s -X github.com/whereiskurt/klanker-maker/pkg/version.GitCommit=%s",
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

	// Fetch and upload sops binary for Phase 89 SOPS secret injection.
	// S3 key binaries/sops is the contract consumed by 89-05's userdata block.
	if err := fetchAndUploadSops(buildDir, bucket); err != nil {
		return fmt.Errorf("fetchAndUploadSops: %w", err)
	}

	return nil
}

const otelcolContribVersion = "0.120.0"

const sopsVersion = "3.13.1"

// FetchAndUploadSops downloads sops v{sopsVersion} (cached in build/) and uploads
// to s3://{bucket}/binaries/sops via aws-cli.
//
// AWS profile: the "klanker-terraform" literal is the project-wide convention
// (init.go has ~17 occurrences as of 2026-05; there is no cfg.GetAWSProfile()
// helper today). Future refactor: if a config-driven AWS profile method is
// introduced, sweep all sidecar-upload helpers at once rather than one-off.
//
// Exported so it can be tested from the _test package (cmd_test).
func FetchAndUploadSops(buildDir, bucket string) error {
	return fetchAndUploadSops(buildDir, bucket)
}

// fetchAndUploadSops is the internal implementation — see FetchAndUploadSops.
func fetchAndUploadSops(buildDir, bucket string) error {
	binaryPath := filepath.Join(buildDir, "sops")
	if _, err := os.Stat(binaryPath); err == nil {
		fmt.Printf("  sops already in %s (skip download)\n", binaryPath)
	} else {
		url := fmt.Sprintf("https://github.com/getsops/sops/releases/download/v%s/sops-v%s.linux.amd64",
			sopsVersion, sopsVersion)
		fmt.Printf("  Downloading sops v%s...\n", sopsVersion)
		dlCmd := exec.Command("curl", "-fsSL", url, "-o", binaryPath)
		if out, err := dlCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("download sops: %s: %w", string(out), err)
		}
		if err := os.Chmod(binaryPath, 0o755); err != nil {
			return fmt.Errorf("chmod sops: %w", err)
		}
	}
	s3Key := "binaries/sops"
	fmt.Printf("  Uploading sops to s3://%s/%s...\n", bucket, s3Key)
	uploadCmd := exec.Command("aws", "s3", "cp", binaryPath,
		fmt.Sprintf("s3://%s/%s", bucket, s3Key),
		"--profile", "klanker-terraform")
	if out, err := uploadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("upload sops: %s: %w", string(out), err)
	}
	fmt.Printf("  Uploaded sops\n")
	return nil
}

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
	ldflags := fmt.Sprintf("-s -w -X github.com/whereiskurt/klanker-maker/pkg/version.Number=%s -X github.com/whereiskurt/klanker-maker/pkg/version.GitCommit=%s",
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

	// 2. Ensure terraform binary is current (version-aware cache via sidecar file).
	// Phase 84.4.1: replaced the os.IsNotExist-only check with terraformIsCurrent
	// so a stale 1.6.6 binary is re-downloaded when tfDesiredVersion bumps.
	terraformPath := filepath.Join(buildDir, "terraform")
	if !terraformIsCurrent(buildDir) {
		fmt.Printf("  Downloading terraform %s for linux/arm64...\n", tfDesiredVersion)
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
// account and sets up NS delegation from the parent zone in the account designated by
// accounts.dns_parent in km-config.yaml.
// Returns the zone ID of the sandboxes zone.
// Note: the AWS profile name 'klanker-management' is unchanged — it's just an SDK profile
// identifier, not the semantic field name. Renaming the profile is out of scope for phase 65.
func ensureSandboxHostedZone(ctx context.Context, cfg *config.Config) (string, error) {
	sandboxDomain := cfg.GetEmailDomain()

	fmt.Printf("  Setting up DNS zone for %s...\n", sandboxDomain)

	// 1. Create Route53 client for application account (where the zone will live)
	// Route53 is a global service but the SDK requires a region to resolve endpoints.
	appCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-terraform", "us-east-1")
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
	mgmtCfg, err := awspkg.LoadAWSConfigInRegion(ctx, "klanker-management", "us-east-1")
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

// persistKMConfigFields reads km-config.yaml, merges the given top-level fields
// into it, and writes it back. Existing values for the listed keys are
// overwritten; other keys are preserved. No-op when updates is empty.
func persistKMConfigFields(updates map[string]string) error {
	if len(updates) == 0 {
		return nil
	}
	configPath := filepath.Join(findRepoRoot(), "km-config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == nil {
		raw = make(map[string]interface{})
	}
	for k, v := range updates {
		raw[k] = v
	}

	newData, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}

	header := "# km-config.yaml — generated by km configure\n# Add this file to .gitignore\n\n"
	return os.WriteFile(configPath, append([]byte(header), newData...), 0600)
}

// persistRoute53ZoneID writes the zone ID back to km-config.yaml.
func persistRoute53ZoneID(zoneID string) error {
	return persistKMConfigFields(map[string]string{"route53_zone_id": zoneID})
}

// forceLambdaColdStart updates the create-handler Lambda's environment to
// force a cold start on the next invocation. The Lambda caches the toolchain
// via sync.Once per container — updating the environment invalidates the
// container and triggers a fresh toolchain download.
//
// resourcePrefix is the km-config.yaml resource_prefix (e.g. "km", "kph") so
// the function name matches the prefix-namespaced Lambda created by terragrunt.
func forceLambdaColdStart(ctx context.Context, awsCfg aws.Config, resourcePrefix string) error {
	return ForceCreateHandlerColdStartWith(ctx, lambda.NewFromConfig(awsCfg), resourcePrefix+"-create-handler")
}

// ForceCreateHandlerColdStartWith updates the create-handler Lambda's
// environment using the supplied client and functionName, forcing a new
// execution environment so the next invocation re-downloads the toolchain.
// Exported for unit testing; production code should call forceLambdaColdStart.
// functionName should be cfg.GetResourcePrefix() + "-create-handler".
func ForceCreateHandlerColdStartWith(ctx context.Context, client lambdaConfigUpdater, functionName string) error {
	return upsertLambdaEnvVar(ctx, client, functionName, "TOOLCHAIN_VERSION", version.String())
}

// lambdaConfigUpdater is a narrow interface over the Lambda SDK client used by
// the cold-start helpers — exists solely to enable unit-test mocking without a
// real AWS connection. Includes Get because UpdateFunctionConfiguration replaces
// (does not merge) the Environment.Variables map, so we must fetch the current
// set before writing.
type lambdaConfigUpdater interface {
	GetFunctionConfiguration(ctx context.Context, input *lambda.GetFunctionConfigurationInput, optFns ...func(*lambda.Options)) (*lambda.GetFunctionConfigurationOutput, error)
	UpdateFunctionConfiguration(ctx context.Context, input *lambda.UpdateFunctionConfigurationInput, optFns ...func(*lambda.Options)) (*lambda.UpdateFunctionConfigurationOutput, error)
}

// upsertLambdaEnvVar fetches the Lambda's current Environment.Variables, sets
// key=value (overwriting if already present), and writes the merged map back.
// UpdateFunctionConfiguration REPLACES Environment.Variables on the AWS side,
// so a naive update wipes every var terragrunt set (KM_SLACK_THREADS_TABLE,
// KM_RESOURCE_PREFIX, etc.) and leaves the Lambda crashing with os.Exit(1) on
// next cold start. This helper preserves the rest of the env.
func upsertLambdaEnvVar(ctx context.Context, client lambdaConfigUpdater, functionName, key, value string) error {
	cfg, err := client.GetFunctionConfiguration(ctx, &lambda.GetFunctionConfigurationInput{
		FunctionName: aws.String(functionName),
	})
	if err != nil {
		return fmt.Errorf("get %s configuration: %w", functionName, err)
	}
	vars := map[string]string{}
	if cfg.Environment != nil {
		for k, v := range cfg.Environment.Variables {
			vars[k] = v
		}
	}
	vars[key] = value
	_, err = client.UpdateFunctionConfiguration(ctx, &lambda.UpdateFunctionConfigurationInput{
		FunctionName: aws.String(functionName),
		Environment:  &lambdatypes.Environment{Variables: vars},
	})
	return err
}

// ForceSlackBridgeColdStartWith updates the Slack bridge Lambda's environment
// using the supplied client and functionName, forcing a new execution environment
// and invalidating the in-process SSMBotTokenFetcher cache (15-min TTL).
// Exported for unit testing; production code should call forceSlackBridgeColdStart.
// functionName should be cfg.GetResourcePrefix() + "-slack-bridge".
func ForceSlackBridgeColdStartWith(ctx context.Context, client lambdaConfigUpdater, functionName string) error {
	return upsertLambdaEnvVar(ctx, client, functionName, "TOKEN_ROTATION_TS", fmt.Sprintf("%d", time.Now().Unix()))
}

// forceSlackBridgeColdStart updates the km-slack-bridge Lambda's environment
// to force a new execution environment, invalidating the in-process
// SSMBotTokenFetcher cache (15-min TTL). Used by km slack rotate-token to
// make a newly-persisted bot token effective immediately rather than waiting
// for the cache TTL to expire.
//
// Distinct from forceLambdaColdStart (which targets km-create-handler with
// TOOLCHAIN_VERSION) — uses TOKEN_ROTATION_TS to keep the namespaces clean.
func forceSlackBridgeColdStart(ctx context.Context, awsCfgParam aws.Config, resourcePrefix string) error {
	return ForceSlackBridgeColdStartWith(ctx, lambda.NewFromConfig(awsCfgParam), resourcePrefix+"-slack-bridge")
}

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
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
			name:    "ses",
			dir:     filepath.Join(regionDir, "ses"),
			envReqs: []string{"KM_ROUTE53_ZONE_ID"},
		},
		{
			name:    "s3-replication",
			dir:     filepath.Join(regionDir, "s3-replication"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
		{
			name:    "ttl-handler",
			dir:     filepath.Join(regionDir, "ttl-handler"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
	}
}

func NewInitCmd(cfg *config.Config) *cobra.Command {
	var awsProfile string
	var region string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize all regional infrastructure (network, DynamoDB, SES, S3 replication, TTL handler)",
		Long:  helpText("init"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" {
				awsProfile = "klanker-application"
			}
			return runInit(cfg, awsProfile, region)
		},
	}

	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-application",
		"AWS CLI profile to use for provisioning")
	cmd.Flags().StringVar(&region, "region", "us-east-1",
		"AWS region to initialize (e.g. us-east-1, ca-central-1)")

	return cmd
}

func runInit(cfg *config.Config, awsProfile, region string) error {
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

	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	return RunInitWithRunner(runner, repoRoot, region)
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

	fmt.Printf("Initializing regional infrastructure for %s (%s)...\n", region, regionLabel)

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

		fmt.Printf("  Applying %s...\n", mod.name)
		if err := runner.Apply(ctx, mod.dir); err != nil {
			return fmt.Errorf("applying %s: %w", mod.name, err)
		}

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
	}

	fmt.Printf("\nRegional infrastructure initialized for %s.\n", region)
	fmt.Printf("Ready for: km create --region %s <profile.yaml>\n", region)
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
func LoadNetworkOutputs(repoRoot, regionLabel string) (*NetworkOutputs, error) {
	outputsFile := filepath.Join(repoRoot, "infra", "live", regionLabel, "network", "outputs.json")

	data, err := os.ReadFile(outputsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("network not initialized for region %s — run 'km init --region <region>' first", regionLabel)
		}
		return nil, fmt.Errorf("reading network outputs: %w", err)
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

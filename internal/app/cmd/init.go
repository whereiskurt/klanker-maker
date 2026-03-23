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

// NetworkOutputs holds the Terraform outputs from the shared network module.
type NetworkOutputs struct {
	VPCID             string   `json:"vpc_id"`
	PublicSubnets     []string `json:"public_subnets"`
	AvailabilityZones []string `json:"availability_zones"`
	SandboxMgmtSGID   string   `json:"sandbox_mgmt_sg_id"`
}

func NewInitCmd(cfg *config.Config) *cobra.Command {
	var awsProfile string
	var region string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize shared infrastructure (VPC, subnets, security groups) for a region",
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
	regionLabel := compiler.RegionLabel(region)

	// Validate AWS credentials
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}

	repoRoot := findRepoRoot()

	// Create region directory structure: infra/live/<region>/network/ and infra/live/<region>/sandboxes/
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)
	networkDir := filepath.Join(regionDir, "network")
	sandboxesDir := filepath.Join(regionDir, "sandboxes")

	if err := os.MkdirAll(networkDir, 0o755); err != nil {
		return fmt.Errorf("creating network directory: %w", err)
	}
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

	// Copy network terragrunt template
	templateSrc := filepath.Join(repoRoot, "infra", "templates", "network", "terragrunt.hcl")
	networkTgDst := filepath.Join(networkDir, "terragrunt.hcl")
	if _, err := os.Stat(networkTgDst); os.IsNotExist(err) {
		srcData, readErr := os.ReadFile(templateSrc)
		if readErr != nil {
			return fmt.Errorf("reading network template: %w", readErr)
		}
		if writeErr := os.WriteFile(networkTgDst, srcData, 0o644); writeErr != nil {
			return fmt.Errorf("writing network terragrunt.hcl: %w", writeErr)
		}
	}

	fmt.Printf("Initializing shared network for %s (%s)...\n", region, regionLabel)

	// Run terragrunt apply
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	if err := runner.Apply(ctx, networkDir); err != nil {
		return fmt.Errorf("network provisioning failed: %w", err)
	}

	// Capture outputs
	outputMap, err := runner.Output(ctx, networkDir)
	if err != nil {
		return fmt.Errorf("reading network outputs: %w", err)
	}

	// Serialize and save outputs
	outputJSON, err := json.MarshalIndent(outputMap, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing outputs: %w", err)
	}

	outputsFile := filepath.Join(networkDir, "outputs.json")
	if err := os.WriteFile(outputsFile, outputJSON, 0o644); err != nil {
		return fmt.Errorf("writing outputs.json: %w", err)
	}

	// Display summary
	fmt.Printf("\nShared network initialized for %s:\n", region)
	if v, ok := outputMap["vpc_id"]; ok {
		fmt.Printf("  VPC:     %v\n", extractValue(v))
	}
	if v, ok := outputMap["public_subnets"]; ok {
		fmt.Printf("  Subnets: %v\n", extractValue(v))
	}
	if v, ok := outputMap["availability_zones"]; ok {
		fmt.Printf("  AZs:     %v\n", extractValue(v))
	}

	fmt.Printf("\nReady for: km create --region %s <profile.yaml>\n", region)
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

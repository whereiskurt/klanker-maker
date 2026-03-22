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
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// NetworkOutputs holds the Terraform outputs from the shared network module.
// These are written to infra/live/network/outputs.json and read by km create.
type NetworkOutputs struct {
	VPCID             string   `json:"vpc_id"`
	PublicSubnets     []string `json:"public_subnets"`
	AvailabilityZones []string `json:"availability_zones"`
	SandboxMgmtSGID   string   `json:"sandbox_mgmt_sg_id"`
}

func NewInitCmd(cfg *config.Config) *cobra.Command {
	var awsProfile string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize shared infrastructure (VPC, subnets, security groups)",
		Long: `Provisions the shared VPC and networking that all sandboxes use.
Run this once before your first km create. Safe to re-run — idempotent via Terraform.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" {
				awsProfile = "klanker-application"
			}
			return runInit(cfg, awsProfile)
		},
	}

	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-application",
		"AWS CLI profile to use for provisioning")

	return cmd
}

func runInit(cfg *config.Config, awsProfile string) error {
	ctx := context.Background()

	// Validate AWS credentials first
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed — check that profile %q is configured: %w", awsProfile, err)
	}

	repoRoot := findRepoRoot()
	networkDir := filepath.Join(repoRoot, "infra", "live", "network")

	if _, err := os.Stat(networkDir); os.IsNotExist(err) {
		return fmt.Errorf("network directory not found at %s", networkDir)
	}

	fmt.Println("Initializing shared network infrastructure...")

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
	if err := os.WriteFile(outputsFile, outputJSON, 0644); err != nil {
		return fmt.Errorf("writing outputs.json: %w", err)
	}

	// Display summary
	fmt.Printf("\nShared network initialized:\n")
	if v, ok := outputMap["vpc_id"]; ok {
		fmt.Printf("  VPC:     %v\n", extractValue(v))
	}
	if v, ok := outputMap["public_subnets"]; ok {
		fmt.Printf("  Subnets: %v\n", extractValue(v))
	}
	if v, ok := outputMap["availability_zones"]; ok {
		fmt.Printf("  AZs:     %v\n", extractValue(v))
	}

	fmt.Println("\nReady for km create.")
	return nil
}

// extractValue extracts the "value" field from a Terraform output map.
func extractValue(v interface{}) interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		if val, exists := m["value"]; exists {
			return val
		}
	}
	return v
}

// LoadNetworkOutputs reads the shared network outputs from outputs.json.
func LoadNetworkOutputs(repoRoot string) (*NetworkOutputs, error) {
	outputsFile := filepath.Join(repoRoot, "infra", "live", "network", "outputs.json")

	data, err := os.ReadFile(outputsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("shared network not initialized — run 'km init' first")
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
	// Optional
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

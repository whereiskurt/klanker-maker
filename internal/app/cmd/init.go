package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/compiler"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
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

	// Auto-create sandboxes.{domain} hosted zone and NS delegation if not already set.
	if os.Getenv("KM_ROUTE53_ZONE_ID") == "" && cfg.Route53ZoneID == "" && cfg.Domain != "" {
		zoneID, err := ensureSandboxHostedZone(ctx, cfg)
		if err != nil {
			fmt.Printf("  [warn] DNS zone setup failed: %v\n", err)
			fmt.Printf("  SES will be skipped. Set KM_ROUTE53_ZONE_ID manually to enable.\n")
		} else {
			os.Setenv("KM_ROUTE53_ZONE_ID", zoneID)
			cfg.Route53ZoneID = zoneID
			// Persist to km-config.yaml so future runs don't repeat this
			if persistErr := persistRoute53ZoneID(zoneID); persistErr != nil {
				fmt.Printf("  [warn] Could not save route53_zone_id to km-config.yaml: %v\n", persistErr)
			}
		}
	} else if cfg.Route53ZoneID != "" && os.Getenv("KM_ROUTE53_ZONE_ID") == "" {
		os.Setenv("KM_ROUTE53_ZONE_ID", cfg.Route53ZoneID)
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
	if zoneID != "" {
		fmt.Printf("  DNS zone %s already exists: %s\n", sandboxDomain, zoneID)
		return zoneID, nil
	}

	// 3. Create the hosted zone
	callerRef := fmt.Sprintf("km-init-%d", time.Now().Unix())
	createOut, err := appR53.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String(sandboxDomain),
		CallerReference: aws.String(callerRef),
		HostedZoneConfig: &route53types.HostedZoneConfig{
			Comment: aws.String("Sandbox email zone — created by km init"),
		},
	})
	if err != nil {
		return "", fmt.Errorf("create hosted zone %s: %w", sandboxDomain, err)
	}

	zoneID = strings.TrimPrefix(aws.ToString(createOut.HostedZone.Id), "/hostedzone/")
	fmt.Printf("  Created DNS zone %s: %s\n", sandboxDomain, zoneID)

	// 4. Get the NS records for the new zone
	nsRecords := make([]string, 0, len(createOut.DelegationSet.NameServers))
	for _, ns := range createOut.DelegationSet.NameServers {
		nsRecords = append(nsRecords, ns)
	}
	fmt.Printf("  NS records: %s\n", strings.Join(nsRecords, ", "))

	// 5. Create Route53 client for management account (where the parent zone lives)
	mgmtCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithSharedConfigProfile("klanker-management"),
		awsconfig.WithRegion("us-east-1"),
	)
	if err != nil {
		return zoneID, fmt.Errorf("zone created but could not load management AWS config for NS delegation: %w", err)
	}
	mgmtR53 := route53.NewFromConfig(mgmtCfg)

	// 6. Find the parent zone (cfg.Domain) in management account
	parentZoneID, err := findHostedZone(ctx, mgmtR53, cfg.Domain)
	if err != nil || parentZoneID == "" {
		return zoneID, fmt.Errorf("zone created but parent zone %s not found in management account — add NS delegation manually", cfg.Domain)
	}

	// 7. Create NS delegation record in parent zone
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
		return zoneID, fmt.Errorf("zone created but NS delegation failed: %w", err)
	}

	fmt.Printf("  NS delegation added to %s zone in management account\n", cfg.Domain)
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

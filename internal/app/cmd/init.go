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
			name:    "s3-replication",
			dir:     filepath.Join(regionDir, "s3-replication"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
		{
			name:    "ttl-handler",
			dir:     filepath.Join(regionDir, "ttl-handler"),
			envReqs: []string{"KM_ARTIFACTS_BUCKET"},
		},
		{
			// SES must apply LAST because it owns the consolidated S3 bucket policy.
			// The TTL handler may destroy its old bucket policy on apply — if SES ran
			// before TTL handler, the bucket policy would be wiped.
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

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize all regional infrastructure (network, DynamoDB, SES, S3 replication, TTL handler)",
		Long:  helpText("init"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if awsProfile == "" {
				awsProfile = "klanker-application"
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

	return cmd
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

	repoRoot := findRepoRoot()

	fmt.Println()
	fmt.Printf("km init — %s (%s)\n", region, compiler.RegionLabel(region))
	fmt.Println(strings.Repeat("─", 50))

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
	fmt.Println("Building Lambdas...")
	if err := buildLambdaZips(repoRoot); err != nil {
		fmt.Printf("  [warn] Lambda build failed: %v\n", err)
	}

	// Step 2: Build and upload sidecars
	fmt.Println()
	fmt.Println("Building and uploading sidecars...")
	if cfg.ArtifactsBucket != "" {
		if err := buildAndUploadSidecars(repoRoot, cfg.ArtifactsBucket); err != nil {
			fmt.Printf("  [warn] Sidecar build/upload failed: %v\n", err)
		}
	} else {
		fmt.Printf("  [skip] artifacts_bucket not configured\n")
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

	// Step 4: Apply regional infrastructure
	fmt.Println()
	fmt.Println("Applying infrastructure...")
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	runner.Verbose = verbose
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

		// Cross-compile
		bootstrapPath := filepath.Join(buildDir, "bootstrap")
		buildCmd := exec.Command("go", "build", "-o", bootstrapPath, "./"+lb.srcDir+"/")
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

		// Cross-compile for linux/amd64 (EC2 and Fargate x86)
		binaryPath := filepath.Join(buildDir, sc.name)
		buildCmd := exec.Command("go", "build", "-o", binaryPath, "./"+sc.srcDir+"/")
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

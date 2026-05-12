// Package cmd provides the Cobra command tree for the km CLI.
// This file implements `km cluster {add,list,rm}` — cross-account IRSA role management.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
)

// ======================== Types ================================================

// clusterAddOpts holds the parsed flags for km cluster add.
type clusterAddOpts struct {
	name           string
	oidcProviderARN string
	namespace      string
	serviceAccount string
	awsProfile     string
	region         string
	verbose        bool
	dryRun         bool
}

// ClusterRunner is the seam tests use to inject a mockClusterRunner.
// Must mirror the subset of *terragrunt.Runner methods that cluster.go calls.
// Exposed (exported) so the cmd_test package can build mockClusterRunner values
// that satisfy this interface.
type ClusterRunner interface {
	Plan(ctx context.Context, dir string) error
	Apply(ctx context.Context, dir string) error
	Destroy(ctx context.Context, dir string) error
	Reconfigure(ctx context.Context, dir string) error
	Output(ctx context.Context, dir string) (map[string]interface{}, error)
}

// NewClusterRunnerFunc is the factory tests override to inject a mockClusterRunner.
// Production wires *terragrunt.Runner (which satisfies ClusterRunner after the
// Plan method was added in Plan 80-05).
// Exported so the cmd_test package can replace it via t.Cleanup.
var NewClusterRunnerFunc = func(profile, repoRoot string) ClusterRunner {
	r := terragrunt.NewRunner(profile, repoRoot)
	return r
}

// PersistClustersConfigFunc is the seam TestClusterAddPersistFailure overrides
// to simulate a km-config.yaml write failure AFTER terragrunt apply succeeds.
// Production points at the real PersistClustersConfig (with configPath derived
// from findRepoRoot). Exported so cmd_test can replace it.
var PersistClustersConfigFunc = func(clusters []config.ClusterConfig) error {
	configPath := filepath.Join(findRepoRoot(), "km-config.yaml")
	return PersistClustersConfig(configPath, clusters)
}

// ======================== HCL Template =========================================

// clusterTerragruntHCLTemplate is the verbatim HCL template for a cluster IRSA
// terragrunt stack. The four {PLACEHOLDER} markers (no $ prefix) are replaced by
// generateClusterHCL; all HCL ${...} interpolations remain unchanged.
//
// IMPORTANT: The terraform { source } path uses the // double-slash pattern so
// Terragrunt copies the entire infra/modules/ directory into its cache, making
// the sibling km-operator-policy/v1.0.0/ module resolvable via the relative path
// "../../km-operator-policy/v1.0.0" inside cluster-irsa/v1.0.0/main.tf.
const clusterTerragruntHCLTemplate = `locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  account_id    = get_aws_account_id()
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

remote_state {
  backend = "s3"
  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }
  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/cluster-{CLUSTER_NAME}/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  # Use // so Terragrunt copies infra/modules/ into the cache (not just cluster-irsa/v1.0.0),
  # making the sibling km-operator-policy/v1.0.0/ module resolvable via the relative path
  # "../../km-operator-policy/v1.0.0" in cluster-irsa/v1.0.0/main.tf.
  source = "${local.repo_root}/infra/modules//cluster-irsa/v1.0.0"
}

inputs = {
  cluster_name              = "{CLUSTER_NAME}"
  oidc_provider_arn         = "{OIDC_PROVIDER_ARN}"
  namespace                 = "{NAMESPACE}"
  service_account_name      = "{SERVICE_ACCOUNT_NAME}"
  resource_prefix           = local.site_vars.locals.site.label
  state_bucket              = local.site_vars.locals.backend.bucket
  artifact_bucket_arn       = "arn:aws:s3:::${local.site_vars.locals.backend.bucket}"
  dynamodb_table_name       = local.site_vars.locals.backend.dynamodb_table
  dynamodb_budget_table_arn = "arn:aws:dynamodb:${local.region_config.locals.region_full}:${local.account_id}:table/${local.site_vars.locals.site.label}-budgets"
  sandbox_table_name        = "${local.site_vars.locals.site.label}-sandboxes"
  identities_table_name     = "${local.site_vars.locals.site.label}-identities"
}
`

// GenerateClusterHCL substitutes the four {PLACEHOLDER} markers in
// clusterTerragruntHCLTemplate using strings.NewReplacer so the HCL ${...}
// interpolations are never touched by Go's string replacement.
// Exported for unit tests in the cmd_test package (TestGenerateClusterHCL).
func GenerateClusterHCL(clusterName, oidcProviderARN, namespace, serviceAccount string) string {
	r := strings.NewReplacer(
		"{CLUSTER_NAME}", clusterName,
		"{OIDC_PROVIDER_ARN}", oidcProviderARN,
		"{NAMESPACE}", namespace,
		"{SERVICE_ACCOUNT_NAME}", serviceAccount,
	)
	return r.Replace(clusterTerragruntHCLTemplate)
}

// ======================== Config Persistence ===================================

// PersistClustersConfig writes the cluster list back to the km-config.yaml at
// configPath. It reads the existing file, unmarshals it into a raw map (preserving
// all other top-level keys), sets raw["clusters"], marshals back, and writes with
// a standard header. Field ordering and YAML comments are not preserved — accepted
// tradeoff matching persistKMConfigFields in init.go.
// Exported for unit tests (TestPersistClusters passes cfgPath directly).
func PersistClustersConfig(configPath string, clusters []config.ClusterConfig) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading km-config.yaml: %w", err)
	}
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing km-config.yaml: %w", err)
	}
	if raw == nil {
		raw = make(map[string]interface{})
	}
	// Marshal clusters as []interface{} to guarantee a well-formed YAML list.
	// yaml.Marshal on []ClusterConfig would also work (yaml tags are set on the
	// struct), but going through []interface{} gives us explicit key control.
	list := make([]interface{}, len(clusters))
	for i, c := range clusters {
		list[i] = map[string]interface{}{
			"name":             c.Name,
			"oidc_provider_arn": c.OIDCProviderARN,
			"namespace":        c.Namespace,
			"service_account":  c.ServiceAccount,
			"role_arn":         c.RoleARN,
		}
	}
	raw["clusters"] = list

	newData, err := yaml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshaling km-config.yaml: %w", err)
	}
	header := "# km-config.yaml — generated by km configure\n# Add this file to .gitignore\n\n"
	return os.WriteFile(configPath, append([]byte(header), newData...), 0o600)
}

// ======================== Cobra Command Tree ===================================

// NewClusterCmd returns the "km cluster" parent command with add/list/rm subcommands.
func NewClusterCmd(cfg *config.Config) *cobra.Command {
	clusterCmd := &cobra.Command{
		Use:          "cluster",
		Short:        "Manage cross-account IRSA roles for k8s integrations",
		SilenceUsage: true,
	}
	clusterCmd.AddCommand(newClusterAddCmd(cfg))
	clusterCmd.AddCommand(newClusterListCmd(cfg))
	clusterCmd.AddCommand(newClusterRmCmd(cfg))
	return clusterCmd
}

func newClusterAddCmd(cfg *config.Config) *cobra.Command {
	opts := clusterAddOpts{}
	cmd := &cobra.Command{
		Use:          "add",
		Short:        "Provision a cross-account IRSA role for a k8s cluster",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return RunClusterAdd(cfg, opts.name, opts.oidcProviderARN, opts.namespace, opts.serviceAccount, opts.awsProfile, opts.region, opts.verbose, opts.dryRun, findRepoRoot())
		},
	}
	cmd.Flags().StringVar(&opts.name, "name", "", "cluster name (required)")
	cmd.Flags().StringVar(&opts.oidcProviderARN, "oidc-provider-arn", "", "OIDC provider ARN in the cluster's AWS account (required)")
	cmd.Flags().StringVar(&opts.namespace, "namespace", "*", "k8s namespace allowed to assume the role")
	cmd.Flags().StringVar(&opts.serviceAccount, "service-account", "km", "k8s service account name allowed to assume the role")
	cmd.Flags().StringVar(&opts.awsProfile, "aws-profile", "klanker-application", "AWS profile for terragrunt apply")
	cmd.Flags().StringVar(&opts.region, "region", "us-east-1", "AWS region for the role")
	cmd.Flags().BoolVar(&opts.verbose, "verbose", false, "stream terragrunt output")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", true, "plan only; set --dry-run=false to apply")
	if err := cmd.MarkFlagRequired("name"); err != nil {
		panic(err)
	}
	if err := cmd.MarkFlagRequired("oidc-provider-arn"); err != nil {
		panic(err)
	}
	return cmd
}

func newClusterListCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List configured cluster IRSA roles",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterList(cmd.OutOrStdout(), cfg)
		},
	}
}

func newClusterRmCmd(cfg *config.Config) *cobra.Command {
	var (
		awsProfile string
		region     string
		verbose    bool
		dryRun     bool
	)
	cmd := &cobra.Command{
		Use:          "rm <cluster-name>",
		Short:        "Destroy a cluster IRSA role",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return RunClusterRm(cfg, args[0], awsProfile, region, verbose, dryRun, findRepoRoot())
		},
	}
	cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-application", "AWS profile for terragrunt destroy")
	cmd.Flags().StringVar(&region, "region", "us-east-1", "AWS region")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "stream terragrunt output")
	cmd.Flags().BoolVar(&dryRun, "dry-run", true, "plan only; set --dry-run=false to apply destroy")
	return cmd
}

// ======================== RunClusterAdd ========================================

// RunClusterAdd implements the km cluster add flow. Exported so cmd_test can call
// it directly with injected seams. repoRoot is passed explicitly (tests supply
// t.TempDir(); production passes findRepoRoot()).
//
// Flow:
//  1. Idempotency: if name already in cfg.Clusters, print existing ARN and return.
//  2. Pre-flight: AWS credential validation via LoadAWSConfig + ValidateCredentials.
//  3. ExportConfigEnvVars — required before any terragrunt invocation.
//  4. Compute regionLabel, dirs; bootstrap region.hcl if missing (Pitfall 1).
//  5. Write cluster terragrunt.hcl.
//  6. If dryRun: runner.Plan → print note → return (no state mutation).
//  7. runner.Apply → runner.Output → extract role_arn → append to cfg.Clusters.
//  8. PersistClustersConfigFunc — if it fails, return error mentioning "km cluster rm"
//     WITHOUT calling runner.Destroy (rollback contract from CONTEXT.md).
//  9. Print handoff output: banner + ServiceAccount YAML + 4-item bullet list.
func RunClusterAdd(cfg *config.Config, name, oidcProviderARN, namespace, serviceAccount, awsProfile, region string, verbose, dryRun bool, repoRoot string) error {
	ctx := context.Background()

	// 1. Idempotency: if name already registered, print existing ARN and exit 0.
	for _, c := range cfg.Clusters {
		if c.Name == name {
			fmt.Printf("Cluster %q already registered: %s\n", name, c.RoleARN)
			return nil
		}
	}

	// 2. Pre-flight credential validation.
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}

	// 3. Export config env vars BEFORE any terragrunt invocation.
	// Avoids 403 HeadBucket on non-default resource_prefix installs (RESEARCH.md Pitfall).
	ExportConfigEnvVars(cfg)

	// 4. Compute paths.
	regionLabel := compiler.RegionLabel(region)
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)
	stackDir := filepath.Join(regionDir, "cluster-"+name)

	// 4a. Bootstrap region.hcl if missing (RESEARCH.md Pitfall 1).
	regionHCLPath := filepath.Join(regionDir, "region.hcl")
	if _, err := os.Stat(regionHCLPath); os.IsNotExist(err) {
		fmt.Printf("Writing region.hcl for %s (idempotent)\n", regionLabel)
		regionHCL := fmt.Sprintf("locals {\n  region_label = %q\n  region_full  = %q\n}\n", regionLabel, region)
		if err := os.MkdirAll(regionDir, 0o755); err != nil {
			return fmt.Errorf("creating region directory: %w", err)
		}
		if err := os.WriteFile(regionHCLPath, []byte(regionHCL), 0o644); err != nil {
			return fmt.Errorf("writing region.hcl: %w", err)
		}
	}

	// 5. Create stack directory and write terragrunt.hcl.
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		return fmt.Errorf("creating cluster stack directory: %w", err)
	}
	hclContent := GenerateClusterHCL(name, oidcProviderARN, namespace, serviceAccount)
	hclPath := filepath.Join(stackDir, "terragrunt.hcl")
	if err := os.WriteFile(hclPath, []byte(hclContent), 0o644); err != nil {
		return fmt.Errorf("writing terragrunt.hcl: %w", err)
	}

	// Build runner.
	runner := NewClusterRunnerFunc(awsProfile, repoRoot)
	if r, ok := runner.(*terragrunt.Runner); ok {
		r.Verbose = verbose
	}

	// 6. Dry-run path: plan only, no state mutation.
	if dryRun {
		if err := runner.Plan(ctx, stackDir); err != nil {
			return fmt.Errorf("terragrunt plan failed: %w", err)
		}
		fmt.Println("(dry-run) terragrunt plan complete — re-run with --dry-run=false to apply")
		return nil
	}

	// 7. Apply path.
	if err := runner.Apply(ctx, stackDir); err != nil {
		return fmt.Errorf("terragrunt apply failed: %w", err)
	}

	// Capture outputs.
	outputs, err := runner.Output(ctx, stackDir)
	if err != nil {
		return fmt.Errorf("getting cluster outputs: %w", err)
	}
	roleARN := ""
	if v, ok := outputs["role_arn"]; ok {
		roleARN = fmt.Sprintf("%v", extractValue(v))
	}

	// 8. Append to cfg.Clusters and persist.
	cfg.Clusters = append(cfg.Clusters, config.ClusterConfig{
		Name:            name,
		OIDCProviderARN: oidcProviderARN,
		Namespace:       namespace,
		ServiceAccount:  serviceAccount,
		RoleARN:         roleARN,
	})

	if err := PersistClustersConfigFunc(cfg.Clusters); err != nil {
		// Rollback contract (CONTEXT.md LOCKED): leave IAM role in place, NO auto-destroy.
		return fmt.Errorf(
			"apply succeeded but persisting km-config.yaml failed: %w\n"+
				"IAM role %s was created. To clean up, run: km cluster rm %s --dry-run=false",
			err, roleARN, name,
		)
	}

	// 9. Handoff output.
	nsDisplay := namespace
	if nsDisplay == "*" {
		nsDisplay = "<your-namespace>"
	}
	fmt.Printf("Cluster %q provisioned: %s\n", name, roleARN)
	fmt.Println()
	fmt.Println("Apply the following ServiceAccount manifest in your k8s cluster:")
	fmt.Println()
	fmt.Printf("apiVersion: v1\n")
	fmt.Printf("kind: ServiceAccount\n")
	fmt.Printf("metadata:\n")
	fmt.Printf("  name: %s\n", serviceAccount)
	fmt.Printf("  namespace: %s\n", nsDisplay)
	fmt.Printf("  annotations:\n")
	fmt.Printf("    eks.amazonaws.com/role-arn: %s\n", roleARN)
	fmt.Printf("    eks.amazonaws.com/token-expiration: \"3600\"\n")
	fmt.Println()
	fmt.Printf("Next steps:\n")
	fmt.Printf("  1. Apply the ServiceAccount manifest in your k8s cluster\n")
	fmt.Printf("  2. Annotate pods with `serviceAccountName: %s`\n", serviceAccount)
	fmt.Printf("  3. Verify AssumeRoleWithWebIdentity from a pod: `aws sts get-caller-identity`\n")
	fmt.Printf("  4. Remove with `km cluster rm %s` when no longer needed\n", name)
	return nil
}

// ======================== runClusterList =======================================

// runClusterList prints registered clusters as a tabwriter table.
// w is parameterized so tests can capture output via bytes.Buffer.
func runClusterList(w io.Writer, cfg *config.Config) error {
	if len(cfg.Clusters) == 0 {
		fmt.Fprintln(w, "(no clusters configured)")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tNAMESPACE\tSERVICE ACCOUNT\tROLE ARN")
	for _, c := range cfg.Clusters {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, c.Namespace, c.ServiceAccount, c.RoleARN)
	}
	return tw.Flush()
}

// ======================== RunClusterRm =========================================

// RunClusterRm implements the km cluster rm flow. Exported so cmd_test can call
// it directly. repoRoot is passed explicitly.
//
// Flow:
//  1. Find cluster by name in cfg.Clusters; error if not found.
//  2. Pre-flight credential validation.
//  3. ExportConfigEnvVars.
//  4. Compute stackDir; build runner.
//  5. If dryRun: runner.Plan (previews the destroy) → return without mutation.
//  6. runner.Reconfigure → runner.Destroy (Pitfall 5: handles backend-config drift).
//  7. Remove cluster from cfg.Clusters; PersistClustersConfigFunc; os.RemoveAll(stackDir).
//  8. Print confirmation.
func RunClusterRm(cfg *config.Config, name, awsProfile, region string, verbose, dryRun bool, repoRoot string) error {
	ctx := context.Background()

	// 1. Find cluster.
	found := false
	for _, c := range cfg.Clusters {
		if c.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("cluster %q not found in km-config.yaml", name)
	}

	// 2. Pre-flight credential validation.
	awsCfg, err := awspkg.LoadAWSConfig(ctx, awsProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config (profile=%s): %w", awsProfile, err)
	}
	if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}

	// 3. Export config env vars BEFORE any terragrunt invocation.
	ExportConfigEnvVars(cfg)

	// 4. Compute paths.
	regionLabel := compiler.RegionLabel(region)
	regionDir := filepath.Join(repoRoot, "infra", "live", regionLabel)
	stackDir := filepath.Join(regionDir, "cluster-"+name)

	// Build runner.
	runner := NewClusterRunnerFunc(awsProfile, repoRoot)
	if r, ok := runner.(*terragrunt.Runner); ok {
		r.Verbose = verbose
	}

	// 5. Dry-run: plan the destroy for preview.
	if dryRun {
		if err := runner.Plan(ctx, stackDir); err != nil {
			return fmt.Errorf("terragrunt plan failed: %w", err)
		}
		fmt.Println("(dry-run) terragrunt plan complete — re-run with --dry-run=false to destroy")
		return nil
	}

	// 6. Reconfigure backend before destroy (Pitfall 5).
	if err := runner.Reconfigure(ctx, stackDir); err != nil {
		return fmt.Errorf("terragrunt reconfigure failed: %w", err)
	}
	if err := runner.Destroy(ctx, stackDir); err != nil {
		return fmt.Errorf("terragrunt destroy failed: %w", err)
	}

	// 7. Remove from cfg.Clusters and persist.
	updated := make([]config.ClusterConfig, 0, len(cfg.Clusters)-1)
	for _, c := range cfg.Clusters {
		if c.Name != name {
			updated = append(updated, c)
		}
	}
	cfg.Clusters = updated

	if err := PersistClustersConfigFunc(cfg.Clusters); err != nil {
		return fmt.Errorf("removing cluster from km-config.yaml: %w", err)
	}

	if err := os.RemoveAll(stackDir); err != nil {
		// Non-fatal — log the warning but don't fail the command.
		fmt.Fprintf(os.Stderr, "warning: could not remove stack directory %s: %v\n", stackDir, err)
	}

	// 8. Confirmation.
	fmt.Printf("Cluster %q destroyed\n", name)
	return nil
}

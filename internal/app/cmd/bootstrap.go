package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/terragrunt"
)

// TerragruntApplyFunc is the function signature for applying a Terragrunt unit.
// It is exported so external test packages can inject a fake without executing terragrunt.
type TerragruntApplyFunc func(ctx context.Context, dir string) error

// ApplyTerragruntFunc is the package-level apply function used by runBootstrap.
// Tests replace this variable to capture apply calls without executing terragrunt.
var ApplyTerragruntFunc TerragruntApplyFunc = defaultApplyTerragrunt

// defaultApplyTerragrunt runs `terragrunt apply -auto-approve` on the given directory
// using the management-account AWS profile.
func defaultApplyTerragrunt(ctx context.Context, dir string) error {
	awsProfile := "klanker-terraform"
	repoRoot := findRepoRoot()
	runner := terragrunt.NewRunner(awsProfile, repoRoot)
	return runner.Apply(ctx, dir)
}

// NewBootstrapCmd creates the "km bootstrap" command using os.Stdout for output.
func NewBootstrapCmd(cfg *config.Config) *cobra.Command {
	return NewBootstrapCmdWithWriter(cfg, os.Stdout)
}

// NewBootstrapCmdWithWriter creates the "km bootstrap" command writing output to w.
// Pass nil to use os.Stdout. Used in tests for output capture.
//
// bootstrap validates that km-config.yaml exists and is loadable, then
// (with --dry-run, the default) prints what infrastructure would be created.
// With --dry-run=false, it deploys the SCP containment policy to the management account.
func NewBootstrapCmdWithWriter(cfg *config.Config, w io.Writer) *cobra.Command {
	if w == nil {
		w = os.Stdout
	}

	var dryRun bool

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Validate config and show what infrastructure bootstrap would create",
		Long:  helpText("bootstrap"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBootstrap(cmd.Context(), cfg, dryRun, w)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", true,
		"Print what would be created without making any changes (default: true)")

	return cmd
}

// findKMConfigPath locates km-config.yaml by checking (in order):
//  1. The current working directory
//  2. The repo root (as determined by findRepoRoot)
func findKMConfigPath() string {
	cwd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(cwd, "km-config.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join(findRepoRoot(), "km-config.yaml")
}

// runBootstrap implements bootstrap validation, dry-run output, and SCP deployment.
func runBootstrap(ctx context.Context, cfg *config.Config, dryRun bool, w io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Use cfg directly when provided (for testing); otherwise load from disk.
	var loadedCfg *config.Config
	if cfg != nil && (cfg.ManagementAccountID != "" || cfg.ApplicationAccountID != "" || cfg.Domain != "") {
		// Config injected by caller (test or programmatic use) — use as-is without
		// requiring km-config.yaml on disk.
		loadedCfg = cfg
	} else {
		// Validate km-config.yaml exists and is loadable.
		// Check current working directory first (for scripted/test usage),
		// then fall back to the repo root anchor.
		kmConfigPath := findKMConfigPath()

		if _, err := os.Stat(kmConfigPath); os.IsNotExist(err) {
			return fmt.Errorf("km-config.yaml not found at %s\nRun 'km configure' first", kmConfigPath)
		}

		// Load config to verify it parses
		var err error
		loadedCfg, err = config.Load()
		if err != nil {
			return fmt.Errorf("invalid km-config.yaml: %w", err)
		}

		fmt.Fprintf(w, "Config: %s\n", kmConfigPath)
	}

	fmt.Fprintf(w, "Domain:  %s\n", loadedCfg.Domain)
	fmt.Fprintf(w, "Region:  %s\n", loadedCfg.PrimaryRegion)
	fmt.Fprintf(w, "Management account: %s\n", loadedCfg.ManagementAccountID)
	fmt.Fprintf(w, "Application account: %s\n", loadedCfg.ApplicationAccountID)
	fmt.Fprintln(w)

	if dryRun {
		fmt.Fprintln(w, "Dry run — the following infrastructure would be created:")
		fmt.Fprintln(w)

		stateBucket := ""
		if cfg != nil {
			stateBucket = cfg.StateBucket
		}
		if stateBucket == "" {
			stateBucket = "km-terraform-state-<hash>"
		}

		budgetTable := loadedCfg.BudgetTableName
		if budgetTable == "" {
			budgetTable = "km-budgets"
		}

		fmt.Fprintf(w, "  S3 bucket:         %s\n", stateBucket)
		fmt.Fprintf(w, "    Purpose:         Terraform state and sandbox metadata\n")
		fmt.Fprintf(w, "    Encryption:      aws:kms (KMS key below)\n")
		fmt.Fprintf(w, "    Versioning:      enabled\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  DynamoDB table:    km-terraform-lock\n")
		fmt.Fprintf(w, "    Purpose:         Terraform state locking\n")
		fmt.Fprintf(w, "    Billing:         PAY_PER_REQUEST\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  KMS key:           km-terraform-state\n")
		fmt.Fprintf(w, "    Purpose:         S3 state bucket encryption\n")
		fmt.Fprintf(w, "    Deletion window: 30 days\n")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  DynamoDB table:    %s\n", budgetTable)
		fmt.Fprintf(w, "    Purpose:         Sandbox budget enforcement tracking\n")
		fmt.Fprintf(w, "    Billing:         PAY_PER_REQUEST\n")
		fmt.Fprintln(w)

		// SCP policy section
		if loadedCfg.ManagementAccountID != "" {
			fmt.Fprintf(w, "  SCP Policy:        km-sandbox-containment\n")
			fmt.Fprintf(w, "    Target:          Application account (%s)\n", loadedCfg.ApplicationAccountID)
			fmt.Fprintf(w, "    Threat coverage: SG mutation, network escape, instance mutation,\n")
			fmt.Fprintf(w, "                     IAM escalation, storage exfiltration, SSM pivot,\n")
			fmt.Fprintf(w, "                     Organizations discovery, region lock\n")
			fmt.Fprintf(w, "    Trusted roles:   AWSReservedSSO_*_*, km-provisioner-*, km-lifecycle-*,\n")
			fmt.Fprintf(w, "                     km-ecs-spot-handler, km-ttl-handler\n")
			fmt.Fprintf(w, "    Deploy via:      km bootstrap (management account credentials required)\n")
		} else {
			fmt.Fprintf(w, "  SCP Policy:        [SKIPPED — no management account configured]\n")
			fmt.Fprintf(w, "    Run 'km configure' and set accounts.management to enable SCP deployment.\n")
		}
		fmt.Fprintln(w)

		fmt.Fprintln(w, "Run 'km bootstrap --dry-run=false' to provision.")
		return nil
	}

	// Non-dry-run: deploy SCP sandbox-containment policy.
	if loadedCfg.ManagementAccountID != "" {
		scpDir := filepath.Join(findRepoRoot(), "infra", "live", "management", "scp")
		fmt.Fprintln(w, "Deploying SCP sandbox-containment policy...")
		if err := ApplyTerragruntFunc(ctx, scpDir); err != nil {
			return fmt.Errorf("scp bootstrap: %w", err)
		}
		fmt.Fprintln(w, "SCP sandbox-containment policy deployed to application account.")
	} else {
		fmt.Fprintln(w, "Skipping SCP deployment — no management account configured.")
	}

	return nil
}

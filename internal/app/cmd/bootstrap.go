package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// NewBootstrapCmd creates the "km bootstrap" command stub.
//
// bootstrap validates that km-config.yaml exists and is loadable, then
// (with --dry-run, the default) prints what infrastructure would be created.
// Full provisioning is deferred to a later plan.
func NewBootstrapCmd(cfg *config.Config) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Validate config and show what infrastructure bootstrap would create",
		Long: `Bootstrap validates km-config.yaml and shows (with --dry-run) the
infrastructure that would be created for the platform:

  - S3 bucket for Terraform state and sandbox metadata
  - DynamoDB table for Terraform state locking
  - KMS key for state bucket encryption

Full implementation (actual provisioning) is a future plan.
--dry-run is the default and safe to run in any environment.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBootstrap(cfg, dryRun)
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

// runBootstrap implements bootstrap validation and dry-run output.
func runBootstrap(cfg *config.Config, dryRun bool) error {
	// Validate km-config.yaml exists and is loadable.
	// Check current working directory first (for scripted/test usage),
	// then fall back to the repo root anchor.
	kmConfigPath := findKMConfigPath()

	if _, err := os.Stat(kmConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("km-config.yaml not found at %s\nRun 'km configure' first", kmConfigPath)
	}

	// Load config to verify it parses
	loadedCfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid km-config.yaml: %w", err)
	}

	fmt.Printf("Config: %s\n", kmConfigPath)
	fmt.Printf("Domain:  %s\n", loadedCfg.Domain)
	fmt.Printf("Region:  %s\n", loadedCfg.PrimaryRegion)
	fmt.Printf("Management account: %s\n", loadedCfg.ManagementAccountID)
	fmt.Printf("Application account: %s\n", loadedCfg.ApplicationAccountID)
	fmt.Println()

	if dryRun {
		fmt.Println("Dry run — the following infrastructure would be created:")
		fmt.Println()

		stateBucket := cfg.StateBucket
		if stateBucket == "" {
			stateBucket = "km-terraform-state-<hash>"
		}

		budgetTable := loadedCfg.BudgetTableName
		if budgetTable == "" {
			budgetTable = "km-budgets"
		}

		fmt.Printf("  S3 bucket:         %s\n", stateBucket)
		fmt.Printf("    Purpose:         Terraform state and sandbox metadata\n")
		fmt.Printf("    Encryption:      aws:kms (KMS key below)\n")
		fmt.Printf("    Versioning:      enabled\n")
		fmt.Println()
		fmt.Printf("  DynamoDB table:    km-terraform-lock\n")
		fmt.Printf("    Purpose:         Terraform state locking\n")
		fmt.Printf("    Billing:         PAY_PER_REQUEST\n")
		fmt.Println()
		fmt.Printf("  KMS key:           km-terraform-state\n")
		fmt.Printf("    Purpose:         S3 state bucket encryption\n")
		fmt.Printf("    Deletion window: 30 days\n")
		fmt.Println()
		fmt.Printf("  DynamoDB table:    %s\n", budgetTable)
		fmt.Printf("    Purpose:         Sandbox budget enforcement tracking\n")
		fmt.Printf("    Billing:         PAY_PER_REQUEST\n")
		fmt.Println()
		fmt.Println("Run 'km bootstrap' (without --dry-run) to provision. [Not yet implemented]")
	}

	return nil
}

package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/version"
)

// NewInfoCmd creates the "km info" subcommand.
// Displays platform configuration, account topology, and operational details
// like email-to-create address and operator contact.
func NewInfoCmd(cfg *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show platform configuration and operational details",
		Long:  helpText("info"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInfo(cfg, cmd.OutOrStdout())
		},
	}
	return cmd
}

func runInfo(cfg *config.Config, w io.Writer) error {
	domain := cfg.Domain
	if domain == "" {
		domain = "(not configured)"
	}

	fmt.Fprintf(w, "\nkm info  [%s]\n", version.String())
	fmt.Fprintf(w, "------------------------------------------------------------\n\n")

	// Platform
	fmt.Fprintf(w, "Platform\n")
	fmt.Fprintf(w, "  Domain:           %s\n", domain)
	fmt.Fprintf(w, "  Sandbox domain:   sandboxes.%s\n", cfg.Domain)
	fmt.Fprintf(w, "  Region:           %s\n", valOrDash(cfg.PrimaryRegion))
	fmt.Fprintf(w, "  Version:          %s\n", version.String())
	fmt.Fprintf(w, "\n")

	// Accounts
	fmt.Fprintf(w, "AWS Accounts\n")
	fmt.Fprintf(w, "  Management:       %s\n", valOrDash(cfg.ManagementAccountID))
	fmt.Fprintf(w, "  Terraform:        %s\n", valOrDash(cfg.TerraformAccountID))
	fmt.Fprintf(w, "  Application:      %s\n", valOrDash(cfg.ApplicationAccountID))
	fmt.Fprintf(w, "\n")

	// SSO
	fmt.Fprintf(w, "AWS SSO\n")
	fmt.Fprintf(w, "  Start URL:        %s\n", valOrDash(cfg.SSOStartURL))
	fmt.Fprintf(w, "  Region:           %s\n", valOrDash(cfg.SSORegion))
	fmt.Fprintf(w, "\n")

	// Infrastructure
	fmt.Fprintf(w, "Infrastructure\n")
	fmt.Fprintf(w, "  Artifacts bucket: %s\n", valOrDash(cfg.ArtifactsBucket))
	fmt.Fprintf(w, "  Route53 zone:     %s\n", valOrDash(cfg.Route53ZoneID))
	fmt.Fprintf(w, "  Budget table:     %s\n", valOrDefault(cfg.BudgetTableName, "km-budgets"))
	fmt.Fprintf(w, "  Identity table:   %s\n", valOrDefault(cfg.IdentityTableName, "km-identities"))
	fmt.Fprintf(w, "\n")

	// Operator
	fmt.Fprintf(w, "Operator\n")
	fmt.Fprintf(w, "  Email:            %s\n", valOrDash(cfg.OperatorEmail))
	if cfg.Domain != "" {
		fmt.Fprintf(w, "  Inbox:            operator@sandboxes.%s\n", cfg.Domain)
	}
	fmt.Fprintf(w, "\n")

	// Email-to-create
	if cfg.OperatorEmail != "" || cfg.SafePhrase != "" {
		fmt.Fprintf(w, "Email-to-Create\n")
		if cfg.Domain != "" {
			fmt.Fprintf(w, "  Send to:          operator@sandboxes.%s\n", cfg.Domain)
		}
		fmt.Fprintf(w, "  Operator:         %s\n", valOrDash(cfg.OperatorEmail))
		if cfg.SafePhrase != "" {
			fmt.Fprintf(w, "  Safe phrase:      %s\n", cfg.SafePhrase)
		} else {
			fmt.Fprintf(w, "  Safe phrase:      (not configured)\n")
		}
		fmt.Fprintf(w, "  SSM key:          /km/config/remote-create/safe-phrase\n")
		fmt.Fprintf(w, "\n")
	}

	// Config file location
	configPath := "km-config.yaml"
	if _, err := os.Stat(configPath); err == nil {
		fmt.Fprintf(w, "Config file: %s\n", configPath)
	} else {
		fmt.Fprintf(w, "Config file: (not found - run km configure)\n")
	}
	fmt.Fprintf(w, "\n")

	return nil
}

func valOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func valOrDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

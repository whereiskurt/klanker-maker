package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/version"
	"time"
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
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			return runInfo(ctx, cfg, cmd.OutOrStdout())
		},
	}
	return cmd
}

func runInfo(ctx context.Context, cfg *config.Config, w io.Writer) error {
	domain := cfg.Domain
	if domain == "" {
		domain = "(not configured)"
	}

	fmt.Fprintf(w, "\nkm info  [%s]\n", version.String())
	fmt.Fprintf(w, "------------------------------------------------------------\n\n")

	// Platform
	fmt.Fprintf(w, "Platform\n")
	fmt.Fprintf(w, "  Domain:           %s\n", domain)
	fmt.Fprintf(w, "  Region:           %s\n", valOrDash(cfg.PrimaryRegion))
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

	// Infrastructure — storage
	fmt.Fprintf(w, "Storage\n")
	fmt.Fprintf(w, "  State bucket:     %s\n", valOrDash(cfg.StateBucket))
	fmt.Fprintf(w, "  Artifacts bucket: %s\n", valOrDash(cfg.ArtifactsBucket))
	fmt.Fprintf(w, "  Route53 zone:     %s\n", valOrDash(cfg.Route53ZoneID))
	fmt.Fprintf(w, "\n")

	// Infrastructure — DynamoDB tables
	fmt.Fprintf(w, "DynamoDB Tables\n")
	fmt.Fprintf(w, "  Sandboxes:        %s\n", valOrDefault(cfg.SandboxTableName, "km-sandboxes"))
	fmt.Fprintf(w, "  Budgets:          %s\n", valOrDefault(cfg.BudgetTableName, "km-budgets"))
	fmt.Fprintf(w, "  Identities:       %s\n", valOrDefault(cfg.IdentityTableName, "km-identities"))
	fmt.Fprintf(w, "  Schedules:        %s\n", valOrDefault(cfg.SchedulesTableName, "km-schedules"))
	fmt.Fprintf(w, "\n")

	// Email — operator + sandbox email system
	fmt.Fprintf(w, "Email\n")
	fmt.Fprintf(w, "  Operator:         %s\n", valOrDash(cfg.OperatorEmail))
	if cfg.Domain != "" {
		fmt.Fprintf(w, "  Sandbox domain:   @sandboxes.%s\n", cfg.Domain)
	}
	fmt.Fprintf(w, "  Signing:          Ed25519 (keys in SSM, pubkeys in identities table)\n")
	fmt.Fprintf(w, "  In-sandbox:       km-send / km-recv\n")
	fmt.Fprintf(w, "\n")

	// Email-to-create
	if cfg.OperatorEmail != "" || cfg.SafePhrase != "" {
		fmt.Fprintf(w, "Email-to-Create\n")
		if cfg.Domain != "" {
			fmt.Fprintf(w, "  Send to:          operator@sandboxes.%s\n", cfg.Domain)
		}
		if cfg.SafePhrase != "" {
			fmt.Fprintf(w, "  Safe phrase:      %s\n", cfg.SafePhrase)
		} else {
			fmt.Fprintf(w, "  Safe phrase:      (not configured)\n")
		}
		fmt.Fprintf(w, "\n")
	}

	// AWS usage — SES quota and account MTD spend (best-effort, non-fatal)
	awsCfg, awsErr := kmaws.LoadAWSConfig(ctx, cfg.AWSProfile)
	if awsErr == nil {
		fmt.Fprintf(w, "AWS Usage\n")

		// SES send quota
		sesClient := sesv2.NewFromConfig(awsCfg)
		if acct, err := sesClient.GetAccount(ctx, &sesv2.GetAccountInput{}); err == nil {
			if sq := acct.SendQuota; sq != nil {
				fmt.Fprintf(w, "  SES daily quota:  %.0f emails\n", sq.Max24HourSend)
				fmt.Fprintf(w, "  SES sent (24h):   %.0f emails\n", sq.SentLast24Hours)
				remaining := sq.Max24HourSend - sq.SentLast24Hours
				fmt.Fprintf(w, "  SES remaining:    %.0f emails\n", remaining)
			}
		} else {
			fmt.Fprintf(w, "  SES quota:        (unavailable)\n")
		}

		// Account MTD spend via Cost Explorer
		ceClient := costexplorer.NewFromConfig(awsCfg)
		now := time.Now().UTC()
		startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		ceOut, ceErr := ceClient.GetCostAndUsage(ctx, &costexplorer.GetCostAndUsageInput{
			TimePeriod: &costexplorertypes.DateInterval{
				Start: strPtr(startOfMonth.Format("2006-01-02")),
				End:   strPtr(now.Format("2006-01-02")),
			},
			Granularity: costexplorertypes.GranularityMonthly,
			Metrics:     []string{"UnblendedCost"},
		})
		if ceErr == nil && len(ceOut.ResultsByTime) > 0 {
			if cost, ok := ceOut.ResultsByTime[0].Total["UnblendedCost"]; ok && cost.Amount != nil {
				fmt.Fprintf(w, "  Account MTD:      $%s %s\n", *cost.Amount, valOrDefault(*cost.Unit, "USD"))
			}
		} else {
			fmt.Fprintf(w, "  Account MTD:      (unavailable)\n")
		}
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

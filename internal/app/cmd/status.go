package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ANSI color codes for terminal output.
// Disabled when output is not a TTY.
const (
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiReset  = "\033[0m"
)

// BudgetFetcher abstracts fetching budget data for a sandbox.
type BudgetFetcher interface {
	FetchBudget(ctx context.Context, sandboxID string) (*kmaws.BudgetSummary, error)
}

// IdentityFetcher abstracts fetching identity data for a sandbox.
type IdentityFetcher interface {
	FetchIdentity(ctx context.Context, sandboxID string) (*kmaws.IdentityRecord, error)
}

// NewStatusCmd creates the "km status" subcommand.
// Usage: km status <sandbox-id>
//
// Prints detailed state for a sandbox: resources (ARNs), metadata (profile, substrate),
// and timestamps (created, TTL expiry), plus budget breakdown if available.
func NewStatusCmd(cfg *config.Config) *cobra.Command {
	return NewStatusCmdWithFetcher(cfg, nil)
}

// NewStatusCmdWithFetcher builds the status command with an optional custom fetcher.
// If fetcher is nil, the real AWS-backed fetcher is used. Used in tests for DI.
func NewStatusCmdWithFetcher(cfg *config.Config, fetcher SandboxFetcher) *cobra.Command {
	return NewStatusCmdWithFetchers(cfg, fetcher, nil)
}

// NewStatusCmdWithFetchers builds the status command with optional custom fetchers for
// both sandbox metadata and budget data. Pass nil for real AWS-backed clients.
func NewStatusCmdWithFetchers(cfg *config.Config, fetcher SandboxFetcher, budgetFetcher BudgetFetcher) *cobra.Command {
	return NewStatusCmdWithAllFetchers(cfg, fetcher, budgetFetcher, nil)
}

// NewStatusCmdWithAllFetchers builds the status command with optional custom fetchers for
// sandbox metadata, budget, and identity data. Pass nil for real AWS-backed clients.
func NewStatusCmdWithAllFetchers(cfg *config.Config, fetcher SandboxFetcher, budgetFetcher BudgetFetcher, identityFetcher IdentityFetcher) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Show detailed state for a sandbox",
		Long:         helpText("status"),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, cfg, fetcher, budgetFetcher, identityFetcher, args[0])
		},
	}
	return cmd
}

// SandboxFetcher abstracts fetching a single sandbox's full status.
type SandboxFetcher interface {
	FetchSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxRecord, error)
}

// runStatus is the command RunE logic for km status.
func runStatus(cmd *cobra.Command, cfg *config.Config, fetcher SandboxFetcher, budgetFetcher BudgetFetcher, identityFetcher IdentityFetcher, sandboxID string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if fetcher == nil {
		if cfg.StateBucket == "" {
			return fmt.Errorf("state bucket not configured: set KM_STATE_BUCKET or state_bucket in km-config.yaml")
		}
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket)

		// Also initialize real budget fetcher if not injected
		if budgetFetcher == nil {
			tableName := cfg.BudgetTableName
			if tableName == "" {
				tableName = "km-budgets"
			}
			budgetFetcher = &realBudgetFetcher{
				client:    dynamodb.NewFromConfig(awsCfg),
				tableName: tableName,
			}
		}

		// Also initialize real identity fetcher if not injected
		if identityFetcher == nil {
			tableName := cfg.IdentityTableName
			if tableName == "" {
				tableName = "km-identities"
			}
			identityFetcher = &realIdentityFetcher{
				client:    dynamodb.NewFromConfig(awsCfg),
				tableName: tableName,
			}
		}
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, kmaws.ErrSandboxNotFound) {
			return fmt.Errorf("sandbox not found: %s", sandboxID)
		}
		return fmt.Errorf("fetch sandbox status: %w", err)
	}

	// Fetch budget data (graceful degradation — may be nil if sandbox has no budget)
	var budget *kmaws.BudgetSummary
	if budgetFetcher != nil {
		budget, _ = budgetFetcher.FetchBudget(ctx, sandboxID)
		// Ignore error: sandbox may have no budget defined. Budget section simply omitted.
	}

	// Fetch identity data (graceful degradation — may be nil if sandbox has no identity)
	var identity *kmaws.IdentityRecord
	if identityFetcher != nil {
		identity, _ = identityFetcher.FetchIdentity(ctx, sandboxID)
		// Ignore error: sandbox may have no identity published. Identity section simply omitted.
	}

	isTTY := isTerminal(cmd.OutOrStdout())
	printSandboxStatus(cmd, rec, budget, identity, isTTY)
	return nil
}

// awsSandboxFetcher is the real AWS-backed SandboxFetcher.
type awsSandboxFetcher struct {
	s3Client  kmaws.S3ListAPI
	tagClient kmaws.TagAPI
	bucket    string
}

// newRealFetcher creates an awsSandboxFetcher from an AWS config.
func newRealFetcher(awsCfg awssdk.Config, bucket string) *awsSandboxFetcher {
	return &awsSandboxFetcher{
		s3Client:  s3.NewFromConfig(awsCfg),
		tagClient: resourcegroupstaggingapi.NewFromConfig(awsCfg),
		bucket:    bucket,
	}
}

// FetchSandbox reads metadata from S3 and resource ARNs from the tagging API.
func (f *awsSandboxFetcher) FetchSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxRecord, error) {
	// Read metadata.json from S3
	meta, err := kmaws.ReadSandboxMetadata(ctx, f.s3Client, f.bucket, sandboxID)
	if err != nil {
		return nil, err
	}

	// Get resource ARNs via tag API
	loc, err := kmaws.FindSandboxByID(ctx, f.tagClient, sandboxID)
	if err != nil && !errors.Is(err, kmaws.ErrSandboxNotFound) {
		return nil, fmt.Errorf("fetch resources for sandbox %s: %w", sandboxID, err)
	}

	rec := &kmaws.SandboxRecord{
		SandboxID: meta.SandboxID,
		Profile:   meta.ProfileName,
		Substrate: meta.Substrate,
		Region:    meta.Region,
		Status:    "running",
		CreatedAt: meta.CreatedAt,
		TTLExpiry: meta.TTLExpiry,
	}
	if loc != nil {
		rec.Resources = loc.ResourceARNs
	}

	return rec, nil
}

// realBudgetFetcher is the real AWS-backed BudgetFetcher.
type realBudgetFetcher struct {
	client    kmaws.BudgetAPI
	tableName string
}

// FetchBudget reads budget data from DynamoDB for a sandbox.
func (f *realBudgetFetcher) FetchBudget(ctx context.Context, sandboxID string) (*kmaws.BudgetSummary, error) {
	return kmaws.GetBudget(ctx, f.client, f.tableName, sandboxID)
}

// realIdentityFetcher is the real AWS-backed IdentityFetcher.
type realIdentityFetcher struct {
	client    kmaws.IdentityTableAPI
	tableName string
}

// FetchIdentity reads identity data from DynamoDB for a sandbox.
func (f *realIdentityFetcher) FetchIdentity(ctx context.Context, sandboxID string) (*kmaws.IdentityRecord, error) {
	return kmaws.FetchPublicKey(ctx, f.client, f.tableName, sandboxID)
}

// isTerminal returns true if the writer is a real TTY (supports ANSI codes).
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil {
			return false
		}
		// Check for character device (terminal)
		return (fi.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

// colorPercent wraps a percentage string with ANSI color codes based on threshold.
// green < 80%, yellow 80-99%, red >= 100%.
func colorPercent(percent float64, isTTY bool) string {
	if !isTTY {
		return fmt.Sprintf("%.1f%%", percent)
	}
	var colorCode string
	switch {
	case percent >= 100.0:
		colorCode = ansiRed
	case percent >= 80.0:
		colorCode = ansiYellow
	default:
		colorCode = ansiGreen
	}
	return fmt.Sprintf("%s%.1f%%%s", colorCode, percent, ansiReset)
}

// printSandboxStatus prints detailed sandbox information including optional budget and identity sections.
func printSandboxStatus(cmd *cobra.Command, rec *kmaws.SandboxRecord, budget *kmaws.BudgetSummary, identity *kmaws.IdentityRecord, isTTY bool) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Sandbox ID:  %s\n", rec.SandboxID)
	fmt.Fprintf(out, "Profile:     %s\n", rec.Profile)
	fmt.Fprintf(out, "Substrate:   %s\n", rec.Substrate)
	fmt.Fprintf(out, "Region:      %s\n", rec.Region)
	fmt.Fprintf(out, "Status:      %s\n", rec.Status)
	fmt.Fprintf(out, "Created At:  %s\n", rec.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"))
	if rec.TTLExpiry != nil {
		fmt.Fprintf(out, "TTL Expiry:  %s\n", rec.TTLExpiry.UTC().Format("2006-01-02T15:04:05Z"))
	}
	if len(rec.Resources) > 0 {
		fmt.Fprintf(out, "Resources (%d):\n", len(rec.Resources))
		for _, arn := range rec.Resources {
			fmt.Fprintf(out, "  - %s\n", arn)
		}
	}

	// Budget section — only printed when budget data is available and has non-zero limits
	if budget != nil && (budget.ComputeLimit > 0 || budget.AILimit > 0) {
		fmt.Fprintf(out, "Budget:\n")

		if budget.ComputeLimit > 0 {
			pct := 0.0
			if budget.ComputeLimit > 0 {
				pct = (budget.ComputeSpent / budget.ComputeLimit) * 100
			}
			fmt.Fprintf(out, "  Compute: $%.2f / $%.2f (%s)\n",
				budget.ComputeSpent, budget.ComputeLimit, colorPercent(pct, isTTY))
		}

		if budget.AILimit > 0 {
			pct := 0.0
			if budget.AILimit > 0 {
				pct = (budget.AISpent / budget.AILimit) * 100
			}
			fmt.Fprintf(out, "  AI:      $%.2f / $%.2f (%s)\n",
				budget.AISpent, budget.AILimit, colorPercent(pct, isTTY))

			// Per-model breakdown (sorted for deterministic output)
			if len(budget.AIByModel) > 0 {
				models := make([]string, 0, len(budget.AIByModel))
				for modelID := range budget.AIByModel {
					models = append(models, modelID)
				}
				sort.Strings(models)
				for _, modelID := range models {
					ms := budget.AIByModel[modelID]
					fmt.Fprintf(out, "    %-30s $%.2f (%dK in / %dK out)\n",
						modelID+":",
						ms.SpentUSD,
						ms.InputTokens/1000,
						ms.OutputTokens/1000,
					)
				}
			}
		}

		warnPct := budget.WarningThreshold
		if warnPct == 0 {
			warnPct = 0.80
		}
		fmt.Fprintf(out, "  Warning threshold: %.0f%%\n", warnPct*100)
	}

	// Identity section — only printed when identity record is available and has a public key
	if identity != nil && identity.PublicKeyB64 != "" {
		fmt.Fprintf(out, "Identity:\n")

		// Truncate public key to first 16 chars for display
		displayKey := identity.PublicKeyB64
		if len(displayKey) > 16 {
			displayKey = displayKey[:16] + "..."
		}
		fmt.Fprintf(out, "  Public Key:      %s\n", displayKey)
	}
}

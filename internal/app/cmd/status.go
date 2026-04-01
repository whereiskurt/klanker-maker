package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ANSI color codes for terminal output.
// Disabled when output is not a TTY.
const (
	ansiGreen     = "\033[32m"
	ansiYellow    = "\033[33m"
	ansiRed       = "\033[31m"
	ansiMagenta   = "\033[35m"
	ansiBoldWhite = "\033[1;37m"
	ansiReset     = "\033[0m"
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
		Use:          "status <sandbox-id | #number>",
		Short:        "Show detailed state for a sandbox",
		Long:         helpText("status"),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			sandboxID, err := ResolveSandboxID(ctx, cfg, args[0])
			if err != nil {
				return err
			}
			return runStatus(cmd, cfg, fetcher, budgetFetcher, identityFetcher, sandboxID)
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
		awsProfile := "klanker-terraform"
		awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		tableName := cfg.SandboxTableName
		if tableName == "" {
			tableName = "km-sandboxes"
		}
		fetcher = newRealFetcher(awsCfg, cfg.StateBucket, tableName)

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

	fprintBanner(cmd.OutOrStdout(), "km status", sandboxID)
	isTTY := isTerminal(cmd.OutOrStdout())
	printSandboxStatus(cmd, rec, budget, identity, isTTY)
	return nil
}

// awsSandboxFetcher is the real AWS-backed SandboxFetcher.
type awsSandboxFetcher struct {
	s3Client     kmaws.S3ListAPI
	tagClient    kmaws.TagAPI
	dynamoClient kmaws.SandboxMetadataAPI
	bucket       string
	tableName    string
}

// newRealFetcher creates an awsSandboxFetcher from an AWS config.
func newRealFetcher(awsCfg awssdk.Config, bucket, tableName string) *awsSandboxFetcher {
	return &awsSandboxFetcher{
		s3Client:     s3.NewFromConfig(awsCfg),
		tagClient:    resourcegroupstaggingapi.NewFromConfig(awsCfg),
		dynamoClient: dynamodb.NewFromConfig(awsCfg),
		bucket:       bucket,
		tableName:    tableName,
	}
}

// FetchSandbox reads metadata from DynamoDB (with S3 fallback) and resource ARNs from the tagging API.
func (f *awsSandboxFetcher) FetchSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxRecord, error) {
	// Read metadata from DynamoDB; fall back to S3 on ResourceNotFoundException.
	meta, err := kmaws.ReadSandboxMetadataDynamo(ctx, f.dynamoClient, f.tableName, sandboxID)
	if err != nil {
		var rnf *dynamodbtypes.ResourceNotFoundException
		if errors.As(err, &rnf) && f.bucket != "" {
			// Table doesn't exist — fall back to S3
			meta, err = kmaws.ReadSandboxMetadata(ctx, f.s3Client, f.bucket, sandboxID)
		}
		if err != nil {
			return nil, err
		}
	}

	// Get resource ARNs via tag API
	loc, err := kmaws.FindSandboxByID(ctx, f.tagClient, sandboxID)
	if err != nil && !errors.Is(err, kmaws.ErrSandboxNotFound) {
		return nil, fmt.Errorf("fetch resources for sandbox %s: %w", sandboxID, err)
	}

	status := meta.Status
	if status == "" {
		status = "running" // backward compat: old metadata without status field
	}

	rec := &kmaws.SandboxRecord{
		SandboxID:   meta.SandboxID,
		Profile:     meta.ProfileName,
		Substrate:   meta.Substrate,
		Region:      meta.Region,
		Status:      status,
		CreatedAt:   meta.CreatedAt,
		TTLExpiry:   meta.TTLExpiry,
		IdleTimeout: meta.IdleTimeout,
	}
	if loc != nil {
		rec.Resources = loc.ResourceARNs
	}

	// Live EC2 instance check: detect killed/stopped/terminated
	if rec.Substrate == "ec2" && rec.Status == "running" {
		awsCfg, cfgErr := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
		if cfgErr == nil {
			ec2Client := ec2.NewFromConfig(awsCfg)
			rec.Status = checkEC2InstanceStatus(ctx, ec2Client, sandboxID)
		}
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
	statusDisplay := rec.Status
	if isTTY {
		statusDisplay = colorizeListStatus(rec.Status)
	}
	fmt.Fprintf(out, "Status:      %s\n", statusDisplay)
	fmt.Fprintf(out, "Created At:  %s\n", rec.CreatedAt.Local().Format("2006-01-02 3:04:05 PM MST"))
	if rec.TTLExpiry != nil {
		fmt.Fprintf(out, "TTL Expiry:  %s\n", rec.TTLExpiry.Local().Format("2006-01-02 3:04:05 PM MST"))
	}

	// Show idle countdown — try metadata first, fall back to profile default
	if rec.Status == "running" {
		idleTimeout := rec.IdleTimeout
		if idleTimeout == "" {
			idleTimeout = "15m" // default if not in metadata (old sandboxes)
		}
		idleStr := getIdleCountdown(context.Background(), rec.SandboxID, idleTimeout, rec.CreatedAt, isTTY)
		if idleStr != "" {
			fmt.Fprintf(out, "Idle Kill:   %s\n", idleStr)
		}
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
			fmt.Fprintf(out, "  Compute: $%.4f / $%.4f (%s)\n",
				budget.ComputeSpent, budget.ComputeLimit, colorPercent(pct, isTTY))
		}

		if budget.AILimit > 0 {
			pct := 0.0
			if budget.AILimit > 0 {
				pct = (budget.AISpent / budget.AILimit) * 100
			}
			fmt.Fprintf(out, "  AI:      $%.4f / $%.4f (%s)\n",
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
					fmt.Fprintf(out, "    %-30s $%.4f (%dK in / %dK out)\n",
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

		signing := identity.Signing
		if signing == "" {
			signing = "unknown"
		}
		fmt.Fprintf(out, "  Signing:         %s\n", signing)

		verifyInbound := identity.VerifyInbound
		if verifyInbound == "" {
			verifyInbound = "unknown"
		}
		fmt.Fprintf(out, "  Verify Inbound:  %s\n", verifyInbound)

		encryption := identity.Encryption
		if encryption == "" {
			encryption = "unknown"
		}
		fmt.Fprintf(out, "  Encryption:      %s\n", encryption)

		if identity.Alias != "" {
			fmt.Fprintf(out, "  Alias:            %s\n", identity.Alias)
		}
		if len(identity.AllowedSenders) > 0 {
			fmt.Fprintf(out, "  Allowed Senders:  %s\n", strings.Join(identity.AllowedSenders, ", "))
		} else {
			fmt.Fprintf(out, "  Allowed Senders:  not configured\n")
		}
	}
}

// getIdleCountdown checks CloudWatch for the most recent audit event and returns
// a countdown string like "12m remaining" colored by urgency.
// createdAt is used as the fallback "last activity" when no CW events are found
// (e.g. new sandbox, missing log stream, or permission error).
func getIdleCountdown(ctx context.Context, sandboxID, idleTimeout string, createdAt time.Time, isTTY bool) string {
	idleDur, parseErr := time.ParseDuration(idleTimeout)
	if parseErr != nil || idleDur == 0 {
		return ""
	}

	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return ""
	}
	cwClient := cloudwatchlogs.NewFromConfig(awsCfg)
	logGroup := "/km/sandboxes/" + sandboxID + "/"

	events, err := kmaws.GetLogEvents(ctx, cwClient, logGroup, "audit", 1)

	// Determine the most recent activity timestamp.
	// If CW events are available, use the latest event; otherwise fall back to
	// sandbox creation time so the countdown reflects actual elapsed idle time.
	var lastActivity time.Time
	if err != nil || len(events) == 0 {
		lastActivity = createdAt
	} else {
		lastActivity = time.UnixMilli(events[len(events)-1].Timestamp)
	}
	remaining := idleDur - time.Since(lastActivity)

	if remaining < 0 {
		remaining = 0
	}
	remaining = remaining.Round(time.Second)

	label := fmt.Sprintf("%s remaining", remaining)
	if remaining == 0 {
		label = "imminent"
	}

	if !isTTY {
		return label
	}

	switch {
	case remaining < 5*time.Minute:
		return ansiRed + label + ansiReset
	case remaining < 15*time.Minute:
		return ansiYellow + label + ansiReset
	default:
		return ansiGreen + label + ansiReset
	}
}

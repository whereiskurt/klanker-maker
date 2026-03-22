package cmd

import (
	"context"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// NewStatusCmd creates the "km status" subcommand.
// Usage: km status <sandbox-id>
//
// Prints detailed state for a sandbox: resources (ARNs), metadata (profile, substrate),
// and timestamps (created, TTL expiry).
func NewStatusCmd(cfg *config.Config) *cobra.Command {
	return NewStatusCmdWithFetcher(cfg, nil)
}

// NewStatusCmdWithFetcher builds the status command with an optional custom fetcher.
// If fetcher is nil, the real AWS-backed fetcher is used. Used in tests for DI.
func NewStatusCmdWithFetcher(_ *config.Config, fetcher SandboxFetcher) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Show detailed state for a sandbox",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, fetcher, args[0])
		},
	}
	return cmd
}

// SandboxFetcher abstracts fetching a single sandbox's full status.
type SandboxFetcher interface {
	FetchSandbox(ctx context.Context, sandboxID string) (*kmaws.SandboxRecord, error)
}

// runStatus is the command RunE logic for km status.
func runStatus(cmd *cobra.Command, fetcher SandboxFetcher, sandboxID string) error {
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
		fetcher = newRealFetcher(awsCfg, defaultStateBucket)
	}

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, kmaws.ErrSandboxNotFound) {
			return fmt.Errorf("sandbox not found: %s", sandboxID)
		}
		return fmt.Errorf("fetch sandbox status: %w", err)
	}

	printSandboxStatus(cmd, rec)
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

// printSandboxStatus prints detailed sandbox information.
func printSandboxStatus(cmd *cobra.Command, rec *kmaws.SandboxRecord) {
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
}

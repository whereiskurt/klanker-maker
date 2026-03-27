// Package aws provides AWS SDK helpers for the Klanker Maker sandbox system.
// It handles config loading, tag-based sandbox discovery, and spot instance termination.
package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const awsRegion = "us-east-1"

// LoadAWSConfig loads AWS configuration using a named shared config profile.
// Region is hardcoded to us-east-1 (the single-region deployment model).
func LoadAWSConfig(ctx context.Context, profile string) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(awsRegion),
	}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load AWS config (profile=%s): %w", profile, err)
	}
	return cfg, nil
}

// ValidateCredentials calls STS GetCallerIdentity to verify that the loaded
// AWS credentials are valid before any provisioning operation begins.
// This pre-flight check surfaces auth errors early rather than mid-apply.
func ValidateCredentials(ctx context.Context, cfg aws.Config) error {
	client := sts.NewFromConfig(cfg)
	if _, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err != nil {
		return fmt.Errorf("AWS credential validation failed: %w", err)
	}
	return nil
}

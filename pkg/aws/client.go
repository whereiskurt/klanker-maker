// Package aws provides AWS SDK helpers for the Klanker Maker sandbox system.
// It handles config loading, tag-based sandbox discovery, and spot instance termination.
package aws

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/rs/zerolog/log"
)

const awsRegion = "us-east-1"

var managedIdentityWarn sync.Once

// LoadAWSConfig loads AWS configuration using a named shared config profile.
// Region is hardcoded to us-east-1 (the single-region deployment model).
//
// AWS_DEFAULT_REGION is set as a fallback so credential providers (SSO,
// AssumeRole) that need a region during config loading work in clean shells
// without requiring the user to export AWS_REGION beforehand.
//
// When running inside a managed-identity environment (EKS pod, Lambda, ECS
// task), the supplied profile name is ignored — the SDK's default credential
// chain picks up the runtime-injected web-identity token automatically and
// no ~/.aws/config file is needed. CLI callers that hard-code
// "klanker-terraform" therefore work unchanged in-pod via the SA annotation.
func LoadAWSConfig(ctx context.Context, profile string) (aws.Config, error) {
	// Ensure credential providers have a region available during config loading.
	if os.Getenv("AWS_REGION") == "" && os.Getenv("AWS_DEFAULT_REGION") == "" {
		os.Setenv("AWS_DEFAULT_REGION", awsRegion)
	}

	if profile != "" && isManagedIdentityEnv() {
		managedIdentityWarn.Do(func() {
			log.Info().Str("requested_profile", profile).
				Msg("managed-identity environment detected; ignoring AWS profile and using default credential chain")
		})
		profile = ""
	}

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

// isManagedIdentityEnv reports whether the process is running inside an
// environment where the AWS SDK's default credential chain has a runtime-
// injected identity available — i.e. asking it to use a named ~/.aws/config
// profile would be wrong. Union of:
//   - EKS pod (KUBERNETES_SERVICE_HOST, set by every kubelet)
//   - Lambda (AWS_LAMBDA_FUNCTION_NAME, set by the Lambda runtime)
//   - ECS / App Runner / CodeBuild (AWS_EXECUTION_ENV, set by those runtimes)
//
// None of these are set on operator workstations under normal conditions.
func isManagedIdentityEnv() bool {
	return os.Getenv("KUBERNETES_SERVICE_HOST") != "" ||
		os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" ||
		os.Getenv("AWS_EXECUTION_ENV") != ""
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

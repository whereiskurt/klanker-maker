package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
)

// ECRClient is the subset of the AWS ECR API used by pkg/check.
type ECRClient interface {
	CreateRepository(ctx context.Context, params *ecr.CreateRepositoryInput, optFns ...func(*ecr.Options)) (*ecr.CreateRepositoryOutput, error)
	SetRepositoryPolicy(ctx context.Context, params *ecr.SetRepositoryPolicyInput, optFns ...func(*ecr.Options)) (*ecr.SetRepositoryPolicyOutput, error)
	DescribeRepositories(ctx context.Context, params *ecr.DescribeRepositoriesInput, optFns ...func(*ecr.Options)) (*ecr.DescribeRepositoriesOutput, error)
}

// NewECRClient constructs an AWS ECR client from an aws.Config.
func NewECRClient(awsCfg aws.Config) ECRClient {
	return ecr.NewFromConfig(awsCfg)
}

// ecrLambdaPullPolicy is the ECR repository policy granting lambda.amazonaws.com pull access.
// This allows any Lambda in the account to pull images from the shared {prefix}-checks ECR repo.
var ecrLambdaPullPolicy = map[string]interface{}{
	"Version": "2012-10-17",
	"Statement": []map[string]interface{}{
		{
			"Sid":    "AllowLambdaPull",
			"Effect": "Allow",
			"Principal": map[string]interface{}{
				"Service": "lambda.amazonaws.com",
			},
			"Action": []string{
				"ecr:BatchGetImage",
				"ecr:GetDownloadUrlForLayer",
			},
		},
	},
}

// EnsureECRRepo lazily creates the shared {prefix}-checks ECR repository if absent,
// sets a repository policy granting lambda.amazonaws.com pull access, and returns
// the repository URI. Idempotent: RepositoryAlreadyExistsException is treated as ok.
func EnsureECRRepo(ctx context.Context, client ECRClient, repoName string) (repoURI string, err error) {
	// Try to create — idempotent via RepositoryAlreadyExistsException handling.
	createOut, err := client.CreateRepository(ctx, &ecr.CreateRepositoryInput{
		RepositoryName:     aws.String(repoName),
		ImageTagMutability: ecrtypes.ImageTagMutabilityMutable,
		ImageScanningConfiguration: &ecrtypes.ImageScanningConfiguration{
			ScanOnPush: false,
		},
		EncryptionConfiguration: &ecrtypes.EncryptionConfiguration{
			EncryptionType: ecrtypes.EncryptionTypeAes256,
		},
	})
	if err != nil {
		// Ignore "repository already exists" — this is the idempotent path.
		var alreadyExists *ecrtypes.RepositoryAlreadyExistsException
		if !errors.As(err, &alreadyExists) {
			return "", fmt.Errorf("EnsureECRRepo CreateRepository %q: %w", repoName, err)
		}
		// Describe to get the URI.
		descOut, descErr := client.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
			RepositoryNames: []string{repoName},
		})
		if descErr != nil {
			return "", fmt.Errorf("EnsureECRRepo DescribeRepositories %q: %w", repoName, descErr)
		}
		if len(descOut.Repositories) == 0 {
			return "", fmt.Errorf("EnsureECRRepo: no repository found for %q after creation", repoName)
		}
		repoURI = aws.ToString(descOut.Repositories[0].RepositoryUri)
	} else {
		repoURI = aws.ToString(createOut.Repository.RepositoryUri)
	}

	// Set the lambda pull policy.
	policyBytes, err := json.Marshal(ecrLambdaPullPolicy)
	if err != nil {
		return "", fmt.Errorf("EnsureECRRepo marshal policy: %w", err)
	}
	_, err = client.SetRepositoryPolicy(ctx, &ecr.SetRepositoryPolicyInput{
		RepositoryName: aws.String(repoName),
		PolicyText:     aws.String(string(policyBytes)),
	})
	if err != nil {
		return "", fmt.Errorf("EnsureECRRepo SetRepositoryPolicy %q: %w", repoName, err)
	}

	return repoURI, nil
}

// ECRRepoName returns the ECR repo name for the check fleet: {prefix}-checks.
func ECRRepoName(prefix string) string {
	return fmt.Sprintf("%s-checks", prefix)
}

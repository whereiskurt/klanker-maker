// Package aws — kms.go
// Narrow KMS helpers. Follows the narrow-interface pattern established in
// ses.go / budget.go / artifacts.go.
package aws

import (
	"context"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// KMSDescribeKeyAPI is the narrow KMS interface needed to resolve a key reference
// (alias name, alias ARN, key id, or key ARN) to the underlying key ARN.
// *kms.Client satisfies this.
type KMSDescribeKeyAPI interface {
	DescribeKey(ctx context.Context, params *kms.DescribeKeyInput, optFns ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
}

// ResolveKeyARN resolves keyIDOrAlias (e.g. "alias/km-platform-km-use1") to the
// underlying KMS key ARN via DescribeKey. A key ARN — not an alias — is required
// when scoping IAM kms:Decrypt resource statements (alias ARNs are not valid IAM
// resources) and when setting a Lambda function's KMSKeyArn deterministically.
//
// Returns an error for empty input (no AWS round-trip) or when DescribeKey fails
// (e.g. the alias does not exist yet on a fresh install) so callers can fail-soft.
func ResolveKeyARN(ctx context.Context, client KMSDescribeKeyAPI, keyIDOrAlias string) (string, error) {
	if keyIDOrAlias == "" {
		return "", fmt.Errorf("ResolveKeyARN: empty key id/alias")
	}
	out, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{
		KeyId: awssdk.String(keyIDOrAlias),
	})
	if err != nil {
		return "", fmt.Errorf("ResolveKeyARN: describe key %q: %w", keyIDOrAlias, err)
	}
	if out.KeyMetadata == nil || out.KeyMetadata.Arn == nil || *out.KeyMetadata.Arn == "" {
		return "", fmt.Errorf("ResolveKeyARN: key %q has no ARN in DescribeKey response", keyIDOrAlias)
	}
	return *out.KeyMetadata.Arn, nil
}

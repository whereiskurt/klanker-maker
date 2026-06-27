// Package bridge — quota_fake_test.go
// Test-exported helper for BRG-01 tests: a fake QuotaAPI that returns a
// configurable post-increment count, making quota.Record produce a predictable
// Decision without a real DynamoDB table.
package bridge

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// FakeQuotaClient is an exported test helper that satisfies QuotaAPI.
// Every UpdateItem call returns the configured count in the Attributes map,
// making quota.Record produce a Decision with that count.
type FakeQuotaClient struct {
	Count int64
}

// NewFakeQuotaClient constructs a FakeQuotaClient returning count on every UpdateItem.
func NewFakeQuotaClient(count int64) *FakeQuotaClient {
	return &FakeQuotaClient{Count: count}
}

// UpdateItem returns a canned ALL_NEW attributes map with count = f.Count.
func (f *FakeQuotaClient) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"count": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", f.Count)},
		},
	}, nil
}

// Verify compile-time that FakeQuotaClient satisfies QuotaAPI.
var _ QuotaAPI = (*FakeQuotaClient)(nil)

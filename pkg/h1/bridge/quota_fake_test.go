// Package bridge — quota_fake_test.go
// Test-exported helper: a fake H1QuotaAPI that returns a configurable count.
package bridge

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// FakeH1QuotaClient is an exported test helper that satisfies H1QuotaAPI.
// Every UpdateItem call returns the configured count in Attributes["count"].
type FakeH1QuotaClient struct {
	Count int64
}

// NewFakeH1QuotaClient constructs a FakeH1QuotaClient returning count on every UpdateItem.
func NewFakeH1QuotaClient(count int64) *FakeH1QuotaClient {
	return &FakeH1QuotaClient{Count: count}
}

// UpdateItem returns a canned ALL_NEW attributes map with count = f.Count.
func (f *FakeH1QuotaClient) UpdateItem(_ context.Context, _ *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"count": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", f.Count)},
		},
	}, nil
}

// Verify compile-time that FakeH1QuotaClient satisfies H1QuotaAPI.
var _ H1QuotaAPI = (*FakeH1QuotaClient)(nil)

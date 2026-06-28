package capacity

import (
	"context"
	"fmt"
	"strconv"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ICETTLSeconds is the DynamoDB TTL duration (in seconds) applied to ICE rows.
// 2700 seconds = 45 minutes. Success rows never set a TTL.
const ICETTLSeconds = int64(2700)

// CapacityDDBClient is the minimal DynamoDB interface required by DynamoCapacityStore.
// Only UpdateItem (for writes) and GetItem (for reads) are needed.
type CapacityDDBClient interface {
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
}

// CapacityStore is the interface for recording and querying AZ capacity history.
type CapacityStore interface {
	// RecordICE records an InsufficientCapacityException for (instanceType, az).
	// The entry is TTL'd to expire after ICETTLSeconds (45 min).
	RecordICE(ctx context.Context, instanceType, az string) error
	// RecordSuccess records a successful launch in (instanceType, az).
	// Success rows have no TTL and persist indefinitely (used for sticky ranking).
	RecordSuccess(ctx context.Context, instanceType, az string) error
	// Get returns the capacity history entry for (instanceType, az).
	// Always returns a non-nil *CapacityEntry; nil time.Time fields indicate absence.
	Get(ctx context.Context, instanceType, az string) (*CapacityEntry, error)
}

// CapacityEntry holds the stored capacity history for a single (instanceType, az) pair.
type CapacityEntry struct {
	InstanceType  string
	AZ            string
	LastICEAt     *time.Time // nil if no recent ICE (or row absent)
	LastSuccessAt *time.Time // nil if never succeeded
}

// DynamoCapacityStore implements CapacityStore backed by a DynamoDB table.
//
// Table schema:
//
//	hash key:  instanceType (S)  — e.g. "g6e.12xlarge"
//	range key: az             (S)  — e.g. "us-east-1c"
//	attrs:     last_ice_at     (N)  — epoch seconds; ICE rows only
//	           last_success_at (N)  — epoch seconds; success rows only
//	           ttl             (N)  — epoch seconds; ICE rows set ttl=now+ICETTLSeconds
//	                                  success rows omit ttl (persist forever)
type DynamoCapacityStore struct {
	client    CapacityDDBClient
	tableName string
}

// NewDynamoCapacityStore creates a DynamoCapacityStore backed by the given DDB client.
func NewDynamoCapacityStore(client CapacityDDBClient, tableName string) *DynamoCapacityStore {
	return &DynamoCapacityStore{client: client, tableName: tableName}
}

// RecordICE writes last_ice_at=now and ttl=now+ICETTLSeconds for (instanceType, az).
func (s *DynamoCapacityStore) RecordICE(ctx context.Context, instanceType, az string) error {
	now := time.Now().Unix()
	ttl := now + ICETTLSeconds

	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(s.tableName),
		Key:       itemKey(instanceType, az),
		UpdateExpression: awssdk.String(
			"SET last_ice_at = :ice, #ttl = :ttl",
		),
		ExpressionAttributeNames: map[string]string{
			"#ttl": "ttl", // ttl is a DynamoDB reserved word
		},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":ice": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
			":ttl": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(ttl, 10)},
		},
	})
	if err != nil {
		return fmt.Errorf("capacity RecordICE (%s/%s): %w", instanceType, az, err)
	}
	return nil
}

// RecordSuccess writes last_success_at=now for (instanceType, az) with no TTL.
func (s *DynamoCapacityStore) RecordSuccess(ctx context.Context, instanceType, az string) error {
	now := time.Now().Unix()

	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(s.tableName),
		Key:       itemKey(instanceType, az),
		UpdateExpression: awssdk.String(
			"SET last_success_at = :success",
		),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":success": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
		},
	})
	if err != nil {
		return fmt.Errorf("capacity RecordSuccess (%s/%s): %w", instanceType, az, err)
	}
	return nil
}

// Get retrieves the capacity history for (instanceType, az).
// Returns a non-nil *CapacityEntry with nil time.Time fields if the item is absent.
func (s *DynamoCapacityStore) Get(ctx context.Context, instanceType, az string) (*CapacityEntry, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: awssdk.String(s.tableName),
		Key:       itemKey(instanceType, az),
	})
	if err != nil {
		return nil, fmt.Errorf("capacity Get (%s/%s): %w", instanceType, az, err)
	}

	entry := &CapacityEntry{
		InstanceType: instanceType,
		AZ:           az,
	}
	if out.Item == nil {
		return entry, nil
	}

	if v, ok := out.Item["last_ice_at"]; ok {
		if nv, ok := v.(*dynamodbtypes.AttributeValueMemberN); ok {
			if epoch, err := strconv.ParseInt(nv.Value, 10, 64); err == nil {
				t := time.Unix(epoch, 0)
				entry.LastICEAt = &t
			}
		}
	}
	if v, ok := out.Item["last_success_at"]; ok {
		if nv, ok := v.(*dynamodbtypes.AttributeValueMemberN); ok {
			if epoch, err := strconv.ParseInt(nv.Value, 10, 64); err == nil {
				t := time.Unix(epoch, 0)
				entry.LastSuccessAt = &t
			}
		}
	}

	return entry, nil
}

// itemKey constructs the DynamoDB composite key for a (instanceType, az) pair.
func itemKey(instanceType, az string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"instanceType": &dynamodbtypes.AttributeValueMemberS{Value: instanceType},
		"az":           &dynamodbtypes.AttributeValueMemberS{Value: az},
	}
}

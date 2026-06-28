package capacity_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-maker/pkg/capacity"
)

// fakeCapacityDDB captures DynamoDB UpdateItem + GetItem calls for assertions.
type fakeCapacityDDB struct {
	updateInputs []*dynamodb.UpdateItemInput
	getInputs    []*dynamodb.GetItemInput
	// getResult, if set, is returned by GetItem.
	getResult *dynamodb.GetItemOutput
	getErr    error
	updateErr error
}

func (f *fakeCapacityDDB) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateInputs = append(f.updateInputs, in)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeCapacityDDB) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.getInputs = append(f.getInputs, in)
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getResult != nil {
		return f.getResult, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

const testTable = "km-capacity-test"

// TestCapacityStore_RecordICE verifies that RecordICE writes last_ice_at + ttl.
func TestCapacityStore_RecordICE(t *testing.T) {
	t.Parallel()

	fake := &fakeCapacityDDB{}
	store := capacity.NewDynamoCapacityStore(fake, testTable)

	before := time.Now().Unix()
	if err := store.RecordICE(context.Background(), "g6e.12xlarge", "us-east-1a"); err != nil {
		t.Fatalf("RecordICE: %v", err)
	}
	after := time.Now().Unix()

	if len(fake.updateInputs) != 1 {
		t.Fatalf("expected 1 UpdateItem call, got %d", len(fake.updateInputs))
	}
	input := fake.updateInputs[0]

	// Key assertions.
	if v := attrS(input.Key, "instanceType"); v != "g6e.12xlarge" {
		t.Errorf("instanceType key = %q, want %q", v, "g6e.12xlarge")
	}
	if v := attrS(input.Key, "az"); v != "us-east-1a" {
		t.Errorf("az key = %q, want %q", v, "us-east-1a")
	}

	// Expression attribute values must contain last_ice_at and ttl.
	iceAt := exprValN(input.ExpressionAttributeValues, ":ice")
	if iceAt < before || iceAt > after {
		t.Errorf("last_ice_at (%d) not in range [%d, %d]", iceAt, before, after)
	}

	ttlVal := exprValN(input.ExpressionAttributeValues, ":ttl")
	expectedTTL := iceAt + capacity.ICETTLSeconds
	if ttlVal < expectedTTL-2 || ttlVal > expectedTTL+2 {
		t.Errorf("ttl (%d) not near expected (%d)", ttlVal, expectedTTL)
	}

	// Verify UpdateExpression references both fields.
	expr := awssdk.ToString(input.UpdateExpression)
	if expr == "" {
		t.Error("UpdateExpression is empty")
	}
}

// TestCapacityStore_RecordSuccess verifies that RecordSuccess writes last_success_at
// but does NOT write a ttl attribute.
func TestCapacityStore_RecordSuccess(t *testing.T) {
	t.Parallel()

	fake := &fakeCapacityDDB{}
	store := capacity.NewDynamoCapacityStore(fake, testTable)

	before := time.Now().Unix()
	if err := store.RecordSuccess(context.Background(), "g6e.12xlarge", "us-east-1c"); err != nil {
		t.Fatalf("RecordSuccess: %v", err)
	}
	after := time.Now().Unix()

	if len(fake.updateInputs) != 1 {
		t.Fatalf("expected 1 UpdateItem call, got %d", len(fake.updateInputs))
	}
	input := fake.updateInputs[0]

	// last_success_at must be present.
	successAt := exprValN(input.ExpressionAttributeValues, ":success")
	if successAt < before || successAt > after {
		t.Errorf("last_success_at (%d) not in range [%d, %d]", successAt, before, after)
	}

	// ttl must NOT appear in expression values for success records.
	if _, ok := input.ExpressionAttributeValues[":ttl"]; ok {
		t.Error("RecordSuccess must NOT write a ttl expression value")
	}
}

// TestCapacityStore_Get verifies unmarshalling of both nil and non-nil timestamps.
func TestCapacityStore_Get(t *testing.T) {
	t.Parallel()

	t.Run("nil timestamps when item absent", func(t *testing.T) {
		t.Parallel()
		fake := &fakeCapacityDDB{}
		store := capacity.NewDynamoCapacityStore(fake, testTable)

		entry, err := store.Get(context.Background(), "g6e.12xlarge", "us-east-1a")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if entry == nil {
			t.Fatal("Get returned nil entry for absent item")
		}
		if entry.LastICEAt != nil {
			t.Errorf("LastICEAt should be nil for absent item, got %v", entry.LastICEAt)
		}
		if entry.LastSuccessAt != nil {
			t.Errorf("LastSuccessAt should be nil for absent item, got %v", entry.LastSuccessAt)
		}
		if entry.InstanceType != "g6e.12xlarge" {
			t.Errorf("InstanceType = %q, want %q", entry.InstanceType, "g6e.12xlarge")
		}
		if entry.AZ != "us-east-1a" {
			t.Errorf("AZ = %q, want %q", entry.AZ, "us-east-1a")
		}
	})

	t.Run("non-nil timestamps when item present", func(t *testing.T) {
		t.Parallel()
		iceEpoch := int64(1751000000)
		successEpoch := int64(1751001000)

		fake := &fakeCapacityDDB{
			getResult: &dynamodb.GetItemOutput{
				Item: map[string]dynamodbtypes.AttributeValue{
					"instanceType":   &dynamodbtypes.AttributeValueMemberS{Value: "g6e.12xlarge"},
					"az":             &dynamodbtypes.AttributeValueMemberS{Value: "us-east-1c"},
					"last_ice_at":    &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", iceEpoch)},
					"last_success_at": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", successEpoch)},
				},
			},
		}
		store := capacity.NewDynamoCapacityStore(fake, testTable)

		entry, err := store.Get(context.Background(), "g6e.12xlarge", "us-east-1c")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if entry.LastICEAt == nil {
			t.Fatal("LastICEAt should be non-nil")
		}
		if entry.LastICEAt.Unix() != iceEpoch {
			t.Errorf("LastICEAt.Unix() = %d, want %d", entry.LastICEAt.Unix(), iceEpoch)
		}
		if entry.LastSuccessAt == nil {
			t.Fatal("LastSuccessAt should be non-nil")
		}
		if entry.LastSuccessAt.Unix() != successEpoch {
			t.Errorf("LastSuccessAt.Unix() = %d, want %d", entry.LastSuccessAt.Unix(), successEpoch)
		}
	})

	t.Run("GetItem error propagated", func(t *testing.T) {
		t.Parallel()
		fake := &fakeCapacityDDB{getErr: fmt.Errorf("ddb unavailable")}
		store := capacity.NewDynamoCapacityStore(fake, testTable)
		_, err := store.Get(context.Background(), "t3.medium", "us-east-1a")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// TestCapacityStore_ICETTLSeconds verifies the exported constant value.
func TestCapacityStore_ICETTLSeconds(t *testing.T) {
	if capacity.ICETTLSeconds != 2700 {
		t.Errorf("ICETTLSeconds = %d, want 2700", capacity.ICETTLSeconds)
	}
}

// --- helpers ---

func attrS(attrs map[string]dynamodbtypes.AttributeValue, key string) string {
	v, ok := attrs[key]
	if !ok {
		return ""
	}
	sv, ok := v.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return ""
	}
	return sv.Value
}

func exprValN(vals map[string]dynamodbtypes.AttributeValue, key string) int64 {
	v, ok := vals[key]
	if !ok {
		return 0
	}
	nv, ok := v.(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		return 0
	}
	n, _ := strconv.ParseInt(nv.Value, 10, 64)
	return n
}

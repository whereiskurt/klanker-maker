// Package quota tests — quota_test.go
// Wave 0 test stubs for QUO-01..05. Bucket-math tests (QUO-01) use real
// assertions against the exported helpers; the remaining tests (QUO-02..05)
// are guarded with t.Skip("Wave 1 — plan 02") because the full Record/Resolve
// logic is implemented in plan 02.
package quota

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeQuotaClient is a test double for the QuotaAPI interface.
type fakeQuotaClient struct {
	updateItemCalls []*dynamodb.UpdateItemInput
	// countReturn is the count value returned in ALL_NEW attributes.
	countReturn int64
}

func (f *fakeQuotaClient) UpdateItem(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateItemCalls = append(f.updateItemCalls, input)
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"count": &dynamodbtypes.AttributeValueMemberN{Value: "1"},
		},
	}, nil
}

// TestRecord (QUO-01) — fixed-window bucket math.
// Asserts hourBucket uses epoch/3600 and dayBucket uses epoch/86400 for a
// known, deterministic Unix timestamp. This test is fully asserting (not
// skipped) because the helpers are already implemented in the skeleton.
func TestRecord(t *testing.T) {
	// Known timestamp: 2024-01-15 12:30:00 UTC = Unix 1705318200
	ts := time.Unix(1705318200, 0).UTC()

	wantHour := int64(1705318200 / 3600) // 473699
	wantDay := int64(1705318200 / 86400) // 19736

	gotHour := hourBucket(ts)
	gotDay := dayBucket(ts)

	if gotHour != wantHour {
		t.Errorf("hourBucket: got %d, want %d (epoch/3600)", gotHour, wantHour)
	}
	if gotDay != wantDay {
		t.Errorf("dayBucket: got %d, want %d (epoch/86400)", gotDay, wantDay)
	}

	// Verify bucket boundary: two timestamps in the same hour share the same bucket.
	ts2 := time.Unix(1705318200+1799, 0).UTC() // 29m59s later — still same hour
	if hourBucket(ts) != hourBucket(ts2) {
		t.Errorf("timestamps in the same hour should share the same hourBucket")
	}

	// Verify bucket boundary: one more second crosses into the next hour.
	ts3 := time.Unix(1705318200+1800, 0).UTC() // 30m00s later — next bucket
	if hourBucket(ts) == hourBucket(ts3) {
		t.Errorf("timestamps in different hours should have different hourBuckets")
	}
}

// TestResolveLimits (QUO-02) — per-(action,window) resolution: profile →
// install default → unlimited. Guarded pending plan 02.
func TestResolveLimits(t *testing.T) {
	t.Skip("Wave 1 — plan 02: ResolveLimits function not yet implemented")

	// When implemented, assert:
	// 1. Profile value wins over install default for the same action+window.
	// 2. A profile that sets only perHour still inherits install-level lifetime.
	// 3. When neither profile nor install sets a window, that window returns nil (unlimited).
}

// TestDecision (QUO-03) — Tripped=true when ANY window exceeds its limit.
// Guarded pending plan 02.
func TestDecision(t *testing.T) {
	t.Skip("Wave 1 — plan 02: Record breach detection not yet implemented")

	// When implemented, assert:
	// 1. Decision{Tripped:true} when count > limit on any window.
	// 2. Decision{Tripped:false} when all counts are at or below their limits.
	// 3. WorstWindow is set to the SK of the first/worst exceeded window.
	// 4. OnBreach reflects the resolved policy from ActionLimit.OnBreach.
}

// TestAtomicADD (QUO-04) — Record uses atomic ADD (not read-modify-write).
// Asserts the UpdateItem expression contains "ADD" (not GetItem+PutItem).
// Guarded pending plan 02.
func TestAtomicADD(t *testing.T) {
	t.Skip("Wave 1 — plan 02: Record atomic-ADD not yet implemented")

	// When implemented, assert:
	// 1. The fakeQuotaClient.UpdateItem is called (not GetItem + PutItem).
	// 2. The UpdateExpression contains "ADD count" (atomic increment).
	// 3. The ExpressionAttributeValues contains ":one" = 1.

	fake := &fakeQuotaClient{countReturn: 1}
	ctx := context.Background()
	limit := ActionLimit{PerHour: ptr64(15)}
	_, err := Record(ctx, fake, "test-table", "sb-123", ActionGithubPR, limit)
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if len(fake.updateItemCalls) == 0 {
		t.Fatal("expected UpdateItem to be called for atomic ADD")
	}
	call := fake.updateItemCalls[0]
	if call.UpdateExpression == nil {
		t.Fatal("expected UpdateExpression to be set")
	}
	expr := *call.UpdateExpression
	if !strings.Contains(expr, "ADD") {
		t.Errorf("UpdateExpression should contain ADD, got: %q", expr)
	}
}

// TestTTL (QUO-05) — lifetime rows carry no TTL; hour/day rows carry TTL ~2h/~2d.
// Guarded pending plan 02.
func TestTTL(t *testing.T) {
	t.Skip("Wave 1 — plan 02: TTL attributes not yet implemented")

	// When implemented, assert:
	// 1. The UpdateItem call for the "lifetime" SK has no "ttl" in ExpressionAttributeValues.
	// 2. The UpdateItem call for a "hour#<bucket>" SK sets ttl ≈ now + 2h (within 5m tolerance).
	// 3. The UpdateItem call for a "day#<bucket>" SK sets ttl ≈ now + 2d (within 5m tolerance).
}

// ptr64 is a test helper that returns a pointer to an int64.
func ptr64(v int64) *int64 { return &v }

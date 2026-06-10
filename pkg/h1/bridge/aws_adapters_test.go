// aws_adapters_test.go — Phase 103 Plan 04 tests for the H1 AWS adapters.
//
// Covers:
//   - DynamoH1ThreadStore: Upsert (conditional PutItem keyed report_id+target),
//     LookupSandbox (GetItem), UpdateSession (UpdateItem), and N-target no-collision.
//   - DynamoH1NonceStore: CheckAndStore dedupes a replayed GUID with the
//     "h1-delivery:" key prefix (shared nonces table, 24h TTL).
//
// Mirrors pkg/github/bridge/thread_store_test.go (the port source). The thread store
// is keyed by (report_id, target) instead of (repo, number) — multi-target fanout
// means N targets on one report each need their own continuity row.
package bridge_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

// ============================================================
// Fake DynamoDB client (satisfies DynamoQueryPutter + UpdateItem).
// ============================================================

type fakeH1ThreadDynamo struct {
	putItems     []*dynamodb.PutItemInput
	putErr       error
	getItem      *dynamodb.GetItemOutput
	getErr       error
	updateInputs []*dynamodb.UpdateItemInput
	updateErr    error
}

func (f *fakeH1ThreadDynamo) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getItem != nil {
		return f.getItem, nil
	}
	return &dynamodb.GetItemOutput{}, nil // item not found
}

func (f *fakeH1ThreadDynamo) PutItem(_ context.Context, params *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putItems = append(f.putItems, params)
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeH1ThreadDynamo) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

func (f *fakeH1ThreadDynamo) UpdateItem(_ context.Context, params *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateInputs = append(f.updateInputs, params)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

// ============================================================
// TestThreadStore_Upsert — conditional PutItem keyed report_id + target
// ============================================================

func TestThreadStore_Upsert(t *testing.T) {
	fake := &fakeH1ThreadDynamo{}
	store := &bridge.DynamoH1ThreadStore{Client: fake, TableName: "km-h1-threads"}

	before := time.Now().Unix()
	if err := store.Upsert(context.Background(), "1234", "h1-km-sandbox", "sb-abc123"); err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	after := time.Now().Unix()

	if len(fake.putItems) != 1 {
		t.Fatalf("expected 1 PutItem call; got %d", len(fake.putItems))
	}
	put := fake.putItems[0]
	if *put.TableName != "km-h1-threads" {
		t.Errorf("TableName = %q; want km-h1-threads", *put.TableName)
	}
	if put.ConditionExpression == nil || *put.ConditionExpression == "" {
		t.Fatal("ConditionExpression is nil/empty; want attribute_not_exists(report_id)")
	}

	// Hash key report_id (S).
	if v, ok := put.Item["report_id"].(*dynamodbtypes.AttributeValueMemberS); !ok || v.Value != "1234" {
		t.Errorf("item.report_id = %v; want S{1234}", put.Item["report_id"])
	}
	// Range key target (S).
	if v, ok := put.Item["target"].(*dynamodbtypes.AttributeValueMemberS); !ok || v.Value != "h1-km-sandbox" {
		t.Errorf("item.target = %v; want S{h1-km-sandbox}", put.Item["target"])
	}
	// sandbox_id (S).
	if v, ok := put.Item["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS); !ok || v.Value != "sb-abc123" {
		t.Errorf("item.sandbox_id = %v; want S{sb-abc123}", put.Item["sandbox_id"])
	}
	// ttl_expiry (N) approx now + 30 days.
	ttlV, ok := put.Item["ttl_expiry"].(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatalf("item.ttl_expiry not N; got %T", put.Item["ttl_expiry"])
	}
	var ttlEpoch int64
	fmt.Sscanf(ttlV.Value, "%d", &ttlEpoch)
	thirtyDays := int64(30 * 24 * 3600)
	if ttlEpoch < before+thirtyDays || ttlEpoch > after+thirtyDays+60 {
		t.Errorf("ttl_expiry = %d; want ~now+30d", ttlEpoch)
	}
}

// TestThreadStore_MultiTarget verifies N targets for ONE report write N distinct
// rows that do not collide (the range key target differs per row).
func TestThreadStore_MultiTarget(t *testing.T) {
	fake := &fakeH1ThreadDynamo{}
	store := &bridge.DynamoH1ThreadStore{Client: fake, TableName: "km-h1-threads"}

	for _, tgt := range []string{"h1-prog-a", "h1-prog-b"} {
		if err := store.Upsert(context.Background(), "5678", tgt, "sb-"+tgt); err != nil {
			t.Fatalf("Upsert(%s) error: %v", tgt, err)
		}
	}
	if len(fake.putItems) != 2 {
		t.Fatalf("expected 2 PutItem calls (one per target); got %d", len(fake.putItems))
	}
	// Same report_id, distinct target range keys.
	t0 := fake.putItems[0].Item["target"].(*dynamodbtypes.AttributeValueMemberS).Value
	t1 := fake.putItems[1].Item["target"].(*dynamodbtypes.AttributeValueMemberS).Value
	if t0 == t1 {
		t.Errorf("both rows share target %q; want distinct targets (collision)", t0)
	}
	r0 := fake.putItems[0].Item["report_id"].(*dynamodbtypes.AttributeValueMemberS).Value
	r1 := fake.putItems[1].Item["report_id"].(*dynamodbtypes.AttributeValueMemberS).Value
	if r0 != "5678" || r1 != "5678" {
		t.Errorf("report_id mismatch: %q, %q; want both 5678", r0, r1)
	}
}

// TestThreadStore_LookupSandbox_Found verifies LookupSandbox returns the row fields.
func TestThreadStore_LookupSandbox_Found(t *testing.T) {
	fake := &fakeH1ThreadDynamo{
		getItem: &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id":       &dynamodbtypes.AttributeValueMemberS{Value: "sb-found"},
				"agent_session_id": &dynamodbtypes.AttributeValueMemberS{Value: "sess-xyz"},
				"agent_type":       &dynamodbtypes.AttributeValueMemberS{Value: "codex"},
			},
		},
	}
	store := &bridge.DynamoH1ThreadStore{Client: fake, TableName: "km-h1-threads"}

	sandboxID, sessionID, agentType, err := store.LookupSandbox(context.Background(), "7", "h1-km-sandbox")
	if err != nil {
		t.Fatalf("LookupSandbox error: %v", err)
	}
	if sandboxID != "sb-found" || sessionID != "sess-xyz" || agentType != "codex" {
		t.Errorf("got (%q,%q,%q); want (sb-found,sess-xyz,codex)", sandboxID, sessionID, agentType)
	}
}

// TestThreadStore_LookupSandbox_NotFound verifies an absent row returns ("","","",nil).
func TestThreadStore_LookupSandbox_NotFound(t *testing.T) {
	fake := &fakeH1ThreadDynamo{}
	store := &bridge.DynamoH1ThreadStore{Client: fake, TableName: "km-h1-threads"}

	sandboxID, sessionID, agentType, err := store.LookupSandbox(context.Background(), "99", "h1-km-sandbox")
	if err != nil {
		t.Fatalf("LookupSandbox(absent) error: %v; want nil", err)
	}
	if sandboxID != "" || sessionID != "" || agentType != "" {
		t.Errorf("got (%q,%q,%q); want all empty", sandboxID, sessionID, agentType)
	}
}

// TestThreadStore_UpdateSession verifies UpdateItem sets agent_session_id + agent_type.
func TestThreadStore_UpdateSession(t *testing.T) {
	fake := &fakeH1ThreadDynamo{}
	store := &bridge.DynamoH1ThreadStore{Client: fake, TableName: "km-h1-threads"}

	if err := store.UpdateSession(context.Background(), "42", "h1-km-sandbox", "session-abc", "codex"); err != nil {
		t.Fatalf("UpdateSession error: %v", err)
	}
	if len(fake.updateInputs) != 1 {
		t.Fatalf("expected 1 UpdateItem call; got %d", len(fake.updateInputs))
	}
	upd := fake.updateInputs[0]
	if upd.UpdateExpression == nil || !h1Contains(*upd.UpdateExpression, "agent_type") {
		t.Errorf("UpdateExpression %v does not set agent_type", upd.UpdateExpression)
	}
	foundSession, foundAgent := false, false
	for _, v := range upd.ExpressionAttributeValues {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok {
			if sv.Value == "session-abc" {
				foundSession = true
			}
			if sv.Value == "codex" {
				foundAgent = true
			}
		}
	}
	if !foundSession || !foundAgent {
		t.Errorf("missing session/agent values: session=%v agent=%v", foundSession, foundAgent)
	}
}

// TestThreadStore_InvalidateStaleSession verifies the stale-session overwrite path.
func TestThreadStore_InvalidateStaleSession(t *testing.T) {
	fake := &fakeH1ThreadDynamo{}
	store := &bridge.DynamoH1ThreadStore{Client: fake, TableName: "km-h1-threads"}

	if err := store.InvalidateStaleSession(context.Background(), "42", "h1-km-sandbox", "sb-new"); err != nil {
		t.Fatalf("InvalidateStaleSession error: %v", err)
	}
	if len(fake.updateInputs) != 1 {
		t.Fatalf("expected 1 UpdateItem; got %d", len(fake.updateInputs))
	}
	expr := *fake.updateInputs[0].UpdateExpression
	if !h1Contains(expr, "REMOVE") || !h1Contains(expr, "agent_session_id") {
		t.Errorf("UpdateExpression %q must REMOVE agent_session_id", expr)
	}
}

// ============================================================
// TestNonceStore — h1-delivery: prefix dedup
// ============================================================

// fakeNonceDynamo satisfies DynamoQueryPutter; PutItem returns the configured
// error (e.g. a ConditionalCheckFailedException to simulate a replay).
type fakeNonceDynamo struct {
	putItems []*dynamodb.PutItemInput
	putErr   error
}

func (f *fakeNonceDynamo) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}
func (f *fakeNonceDynamo) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}
func (f *fakeNonceDynamo) PutItem(_ context.Context, params *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putItems = append(f.putItems, params)
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &dynamodb.PutItemOutput{}, nil
}

func TestNonceStore_FirstInsertion(t *testing.T) {
	fake := &fakeNonceDynamo{}
	store := &bridge.DynamoH1NonceStore{Client: fake, TableName: "km-slack-bridge-nonces"}

	key := bridge.H1DeliveryNoncePrefix + "guid-123"
	replayed, err := store.CheckAndStore(context.Background(), key, bridge.H1DeliveryNonceTTLSeconds)
	if err != nil {
		t.Fatalf("CheckAndStore error: %v", err)
	}
	if replayed {
		t.Error("first insertion must return replayed=false")
	}
	if len(fake.putItems) != 1 {
		t.Fatalf("expected 1 PutItem; got %d", len(fake.putItems))
	}
	// Key must carry the h1-delivery: prefix.
	nonceV, ok := fake.putItems[0].Item["nonce"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || nonceV.Value != "h1-delivery:guid-123" {
		t.Errorf("nonce = %v; want S{h1-delivery:guid-123}", fake.putItems[0].Item["nonce"])
	}
}

func TestNonceStore_Replay(t *testing.T) {
	fake := &fakeNonceDynamo{
		putErr: &dynamodbtypes.ConditionalCheckFailedException{},
	}
	store := &bridge.DynamoH1NonceStore{Client: fake, TableName: "km-slack-bridge-nonces"}

	replayed, err := store.CheckAndStore(context.Background(), bridge.H1DeliveryNoncePrefix+"dup", bridge.H1DeliveryNonceTTLSeconds)
	if err != nil {
		t.Fatalf("replay must NOT be an error; got %v", err)
	}
	if !replayed {
		t.Error("a ConditionalCheckFailed must return replayed=true")
	}
}

// h1Contains is a small substring helper (avoids importing strings just for tests).
func h1Contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

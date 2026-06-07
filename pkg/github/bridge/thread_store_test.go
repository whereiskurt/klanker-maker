// thread_store_test.go — GREEN unit tests for DynamoGitHubThreadStore (98-02).
//
// Implements: GH-X-CONTINUITY requirement.
// Tests: Upsert (conditional PutItem), LookupSandbox (GetItem), UpdateSession (UpdateItem).
package bridge_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// Fake DynamoDB client for thread store tests.
// Satisfies the superset interface (DynamoQueryPutter + UpdateItem).
// ============================================================

type fakeGitHubThreadDynamo struct {
	putItems     []*dynamodb.PutItemInput
	putErr       error
	getItem      *dynamodb.GetItemOutput
	getErr       error
	updateInputs []*dynamodb.UpdateItemInput
	updateErr    error
}

func (f *fakeGitHubThreadDynamo) GetItem(_ context.Context, _ *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getItem != nil {
		return f.getItem, nil
	}
	return &dynamodb.GetItemOutput{}, nil // item not found
}

func (f *fakeGitHubThreadDynamo) PutItem(_ context.Context, params *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putItems = append(f.putItems, params)
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeGitHubThreadDynamo) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

func (f *fakeGitHubThreadDynamo) UpdateItem(_ context.Context, params *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateInputs = append(f.updateInputs, params)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

// ============================================================
// TestGitHubThreadStore (GH-X-CONTINUITY)
// ============================================================

// TestGitHubThreadStore_Upsert verifies that Upsert performs a conditional PutItem
// with attribute_not_exists(repo) condition, keys repo(S)/number(N)/sandbox_id(S),
// and ttl_expiry(N) approximately 30 days in the future.
func TestGitHubThreadStore_Upsert(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{}
	store := &bridge.DynamoGitHubThreadStore{
		Client:    fake,
		TableName: "km-github-threads",
	}

	before := time.Now().Unix()
	err := store.Upsert(context.Background(), "owner/repo", 42, "sb-abc123")
	after := time.Now().Unix()

	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	if len(fake.putItems) != 1 {
		t.Fatalf("expected 1 PutItem call; got %d", len(fake.putItems))
	}

	put := fake.putItems[0]
	if *put.TableName != "km-github-threads" {
		t.Errorf("TableName = %q; want km-github-threads", *put.TableName)
	}

	// Verify condition expression uses attribute_not_exists(repo).
	if put.ConditionExpression == nil || *put.ConditionExpression == "" {
		t.Fatal("ConditionExpression is nil/empty; want attribute_not_exists(repo)")
	}

	// Verify hash key repo (S).
	repoVal, ok := put.Item["repo"]
	if !ok {
		t.Fatal("PutItem item missing 'repo' key")
	}
	repoS, ok := repoVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || repoS.Value != "owner/repo" {
		t.Errorf("item.repo = %v; want S{owner/repo}", repoVal)
	}

	// Verify range key number (N).
	numVal, ok := put.Item["number"]
	if !ok {
		t.Fatal("PutItem item missing 'number' key")
	}
	numN, ok := numVal.(*dynamodbtypes.AttributeValueMemberN)
	if !ok || numN.Value != "42" {
		t.Errorf("item.number = %v; want N{42}", numVal)
	}

	// Verify sandbox_id (S).
	sidVal, ok := put.Item["sandbox_id"]
	if !ok {
		t.Fatal("PutItem item missing 'sandbox_id' key")
	}
	sidS, ok := sidVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || sidS.Value != "sb-abc123" {
		t.Errorf("item.sandbox_id = %v; want S{sb-abc123}", sidVal)
	}

	// Verify ttl_expiry (N) is approx now + 30 days.
	ttlVal, ok := put.Item["ttl_expiry"]
	if !ok {
		t.Fatal("PutItem item missing 'ttl_expiry' key")
	}
	ttlN, ok := ttlVal.(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatalf("item.ttl_expiry is not N; got %T", ttlVal)
	}
	var ttlEpoch int64
	fmt.Sscanf(ttlN.Value, "%d", &ttlEpoch)
	thirtyDays := int64(30 * 24 * 3600)
	minTTL := before + thirtyDays
	maxTTL := after + thirtyDays + 60 // 60s slack
	if ttlEpoch < minTTL || ttlEpoch > maxTTL {
		t.Errorf("ttl_expiry = %d; want in [%d, %d] (now+30d)", ttlEpoch, minTTL, maxTTL)
	}
}

// TestGitHubThreadStore_LookupSandbox_Found verifies that LookupSandbox returns
// sandboxID and sessionID when the item exists.
func TestGitHubThreadStore_LookupSandbox_Found(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{
		getItem: &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id":       &dynamodbtypes.AttributeValueMemberS{Value: "sb-found"},
				"agent_session_id": &dynamodbtypes.AttributeValueMemberS{Value: "sess-xyz"},
			},
		},
	}
	store := &bridge.DynamoGitHubThreadStore{
		Client:    fake,
		TableName: "km-github-threads",
	}

	sandboxID, sessionID, err := store.LookupSandbox(context.Background(), "owner/repo", 7)
	if err != nil {
		t.Fatalf("LookupSandbox returned error: %v", err)
	}
	if sandboxID != "sb-found" {
		t.Errorf("sandboxID = %q; want sb-found", sandboxID)
	}
	if sessionID != "sess-xyz" {
		t.Errorf("sessionID = %q; want sess-xyz", sessionID)
	}
}

// TestGitHubThreadStore_LookupSandbox_NotFound verifies that a missing item
// returns ("", "", nil) — NOT an error (absent = first-dispatch, not a failure).
func TestGitHubThreadStore_LookupSandbox_NotFound(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{} // getItem nil → returns empty GetItemOutput
	store := &bridge.DynamoGitHubThreadStore{
		Client:    fake,
		TableName: "km-github-threads",
	}

	sandboxID, sessionID, err := store.LookupSandbox(context.Background(), "owner/repo", 99)
	if err != nil {
		t.Fatalf("LookupSandbox(absent) returned error: %v; want nil", err)
	}
	if sandboxID != "" {
		t.Errorf("sandboxID = %q; want empty string", sandboxID)
	}
	if sessionID != "" {
		t.Errorf("sessionID = %q; want empty string", sessionID)
	}
}

// TestGitHubThreadStore_UpdateSession verifies that UpdateSession issues an
// UpdateItem that sets agent_session_id on the (repo, number) row.
func TestGitHubThreadStore_UpdateSession(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{}
	store := &bridge.DynamoGitHubThreadStore{
		Client:    fake,
		TableName: "km-github-threads",
	}

	err := store.UpdateSession(context.Background(), "owner/repo", 42, "session-abc")
	if err != nil {
		t.Fatalf("UpdateSession returned error: %v", err)
	}
	if len(fake.updateInputs) != 1 {
		t.Fatalf("expected 1 UpdateItem call; got %d", len(fake.updateInputs))
	}

	upd := fake.updateInputs[0]
	if *upd.TableName != "km-github-threads" {
		t.Errorf("TableName = %q; want km-github-threads", *upd.TableName)
	}

	// Verify the update expression sets agent_session_id.
	if upd.UpdateExpression == nil || *upd.UpdateExpression == "" {
		t.Error("UpdateExpression is nil/empty; want SET agent_session_id = ...")
	}

	// Verify the ExpressionAttributeValues includes the session ID.
	found := false
	for _, v := range upd.ExpressionAttributeValues {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value == "session-abc" {
			found = true
		}
	}
	if !found {
		t.Errorf("ExpressionAttributeValues does not contain 'session-abc'; got %v", upd.ExpressionAttributeValues)
	}
}

// Ensure the DynamoGitHubThreadStore type reference causes compilation failure
// until 98-02 implements it (RED proof).
var _ = &bridge.DynamoGitHubThreadStore{}

// thread_store_test.go — GREEN unit tests for DynamoGitHubThreadStore (98-02).
//
// Implements: GH-X-CONTINUITY requirement.
// Tests: Upsert (unconditional if_not_exists UpdateItem), LookupSandbox (GetItem), UpdateSession (UpdateItem).
package bridge_test

import (
	"context"
	"fmt"
	"strings"
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

// TestGitHubThreadStore_Upsert verifies that Upsert is an unconditional UpdateItem
// keyed repo(S)/number(N) that sets sandbox_id and ttl_expiry only where absent
// (if_not_exists), never a conditional PutItem. A conditional PutItem could never
// claim a pre-v1.9.0 row that exists without sandbox_id, so km-slack session on
// that thread answered 404 forever.
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
	if len(fake.putItems) != 0 {
		t.Fatalf("Upsert must not PutItem; got %d PutItem calls", len(fake.putItems))
	}
	if len(fake.updateInputs) != 1 {
		t.Fatalf("expected 1 UpdateItem call; got %d", len(fake.updateInputs))
	}

	upd := fake.updateInputs[0]
	if *upd.TableName != "km-github-threads" {
		t.Errorf("TableName = %q; want km-github-threads", *upd.TableName)
	}
	if upd.ConditionExpression != nil {
		t.Errorf("ConditionExpression = %q; want none (an existing ownerless row must be claimable)", *upd.ConditionExpression)
	}

	// Key: repo (S) + number (N).
	if v, ok := upd.Key["repo"].(*dynamodbtypes.AttributeValueMemberS); !ok || v.Value != "owner/repo" {
		t.Errorf("key.repo = %v; want S{owner/repo}", upd.Key["repo"])
	}
	if v, ok := upd.Key["number"].(*dynamodbtypes.AttributeValueMemberN); !ok || v.Value != "42" {
		t.Errorf("key.number = %v; want N{42}", upd.Key["number"])
	}

	expr := awsString(upd.UpdateExpression)
	for _, want := range []string{"sandbox_id = if_not_exists(sandbox_id, :sb)", "ttl_expiry = if_not_exists(ttl_expiry, :ttl)"} {
		if !strings.Contains(expr, want) {
			t.Errorf("UpdateExpression %q missing %q", expr, want)
		}
	}
	for _, banned := range []string{"agent_session_id", "agent_type"} {
		if strings.Contains(expr, banned) {
			t.Errorf("UpdateExpression %q touches %s; the poller owns it", expr, banned)
		}
	}

	if v, ok := upd.ExpressionAttributeValues[":sb"].(*dynamodbtypes.AttributeValueMemberS); !ok || v.Value != "sb-abc123" {
		t.Errorf(":sb = %v; want S{sb-abc123}", upd.ExpressionAttributeValues[":sb"])
	}
	ttlN, ok := upd.ExpressionAttributeValues[":ttl"].(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatalf(":ttl is not N; got %T", upd.ExpressionAttributeValues[":ttl"])
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

// TestGitHubThreadStore_Upsert_ClaimsOwnerlessRow: a pre-v1.9.0 row that exists
// WITHOUT sandbox_id gains it, and its session fields survive.
func TestGitHubThreadStore_Upsert_ClaimsOwnerlessRow(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{}
	store := &bridge.DynamoGitHubThreadStore{Client: fake, TableName: "km-github-threads"}
	if err := store.Upsert(context.Background(), "owner/repo", 7, "sb-new"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	row := map[string]dynamodbtypes.AttributeValue{
		"repo":             &dynamodbtypes.AttributeValueMemberS{Value: "owner/repo"},
		"number":           &dynamodbtypes.AttributeValueMemberN{Value: "7"},
		"agent_session_id": &dynamodbtypes.AttributeValueMemberS{Value: "sess-old"},
		"agent_type":       &dynamodbtypes.AttributeValueMemberS{Value: "claude"},
		"ttl_expiry":       &dynamodbtypes.AttributeValueMemberN{Value: "123"},
	}
	applyIfNotExistsSet(t, row, fake.updateInputs[0])

	if v := row["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS).Value; v != "sb-new" {
		t.Errorf("sandbox_id = %q; want sb-new (ownerless row must be claimed)", v)
	}
	if v := row["agent_session_id"].(*dynamodbtypes.AttributeValueMemberS).Value; v != "sess-old" {
		t.Errorf("agent_session_id = %q; want sess-old (untouched)", v)
	}
	if v := row["ttl_expiry"].(*dynamodbtypes.AttributeValueMemberN).Value; v != "123" {
		t.Errorf("ttl_expiry = %q; want 123 (existing value kept)", v)
	}
}

// TestGitHubThreadStore_Upsert_KeepsExistingOwner: a row owned by another sandbox
// keeps its sandbox_id.
func TestGitHubThreadStore_Upsert_KeepsExistingOwner(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{}
	store := &bridge.DynamoGitHubThreadStore{Client: fake, TableName: "km-github-threads"}
	if err := store.Upsert(context.Background(), "owner/repo", 7, "sb-new"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	row := map[string]dynamodbtypes.AttributeValue{
		"repo":       &dynamodbtypes.AttributeValueMemberS{Value: "owner/repo"},
		"number":     &dynamodbtypes.AttributeValueMemberN{Value: "7"},
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-owner"},
	}
	applyIfNotExistsSet(t, row, fake.updateInputs[0])
	if v := row["sandbox_id"].(*dynamodbtypes.AttributeValueMemberS).Value; v != "sb-owner" {
		t.Errorf("sandbox_id = %q; want sb-owner (existing owner kept)", v)
	}
	if _, ok := row["ttl_expiry"]; !ok {
		t.Error("ttl_expiry absent after upsert; want it set when missing")
	}
}

// TestGitHubThreadStore_LookupSandbox_Found verifies that LookupSandbox returns
// sandboxID, sessionID, and agentType when the item exists.
func TestGitHubThreadStore_LookupSandbox_Found(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{
		getItem: &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id":       &dynamodbtypes.AttributeValueMemberS{Value: "sb-found"},
				"agent_session_id": &dynamodbtypes.AttributeValueMemberS{Value: "sess-xyz"},
				"agent_type":       &dynamodbtypes.AttributeValueMemberS{Value: "codex"},
			},
		},
	}
	store := &bridge.DynamoGitHubThreadStore{
		Client:    fake,
		TableName: "km-github-threads",
	}

	sandboxID, sessionID, agentType, err := store.LookupSandbox(context.Background(), "owner/repo", 7)
	if err != nil {
		t.Fatalf("LookupSandbox returned error: %v", err)
	}
	if sandboxID != "sb-found" {
		t.Errorf("sandboxID = %q; want sb-found", sandboxID)
	}
	if sessionID != "sess-xyz" {
		t.Errorf("sessionID = %q; want sess-xyz", sessionID)
	}
	if agentType != "codex" {
		t.Errorf("agentType = %q; want codex", agentType)
	}
}

// TestGitHubThreadStore_LookupSandbox_NoAgentType verifies that LookupSandbox returns
// agentType=="" for a row that pre-dates Phase 102 (no agent_type attribute).
// The absent attribute must return "" (treated as profile default downstream).
func TestGitHubThreadStore_LookupSandbox_NoAgentType(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{
		getItem: &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{
				"sandbox_id":       &dynamodbtypes.AttributeValueMemberS{Value: "sb-old"},
				"agent_session_id": &dynamodbtypes.AttributeValueMemberS{Value: "sess-old"},
				// agent_type intentionally absent — pre-Phase-102 row
			},
		},
	}
	store := &bridge.DynamoGitHubThreadStore{
		Client:    fake,
		TableName: "km-github-threads",
	}

	sandboxID, sessionID, agentType, err := store.LookupSandbox(context.Background(), "owner/repo", 8)
	if err != nil {
		t.Fatalf("LookupSandbox(no-agent_type) returned error: %v", err)
	}
	if sandboxID != "sb-old" {
		t.Errorf("sandboxID = %q; want sb-old", sandboxID)
	}
	if sessionID != "sess-old" {
		t.Errorf("sessionID = %q; want sess-old", sessionID)
	}
	if agentType != "" {
		t.Errorf("agentType = %q; want empty (back-compat with pre-Phase-102 rows)", agentType)
	}
}

// TestGitHubThreadStore_LookupSandbox_NotFound verifies that a missing item
// returns ("", "", "", nil) — NOT an error (absent = first-dispatch, not a failure).
func TestGitHubThreadStore_LookupSandbox_NotFound(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{} // getItem nil → returns empty GetItemOutput
	store := &bridge.DynamoGitHubThreadStore{
		Client:    fake,
		TableName: "km-github-threads",
	}

	sandboxID, sessionID, agentType, err := store.LookupSandbox(context.Background(), "owner/repo", 99)
	if err != nil {
		t.Fatalf("LookupSandbox(absent) returned error: %v; want nil", err)
	}
	if sandboxID != "" {
		t.Errorf("sandboxID = %q; want empty string", sandboxID)
	}
	if sessionID != "" {
		t.Errorf("sessionID = %q; want empty string", sessionID)
	}
	if agentType != "" {
		t.Errorf("agentType = %q; want empty string (row absent)", agentType)
	}
}

// TestGitHubThreadStore_UpdateSession verifies that UpdateSession issues an
// UpdateItem that sets agent_session_id AND agent_type on the (repo, number) row.
func TestGitHubThreadStore_UpdateSession(t *testing.T) {
	fake := &fakeGitHubThreadDynamo{}
	store := &bridge.DynamoGitHubThreadStore{
		Client:    fake,
		TableName: "km-github-threads",
	}

	err := store.UpdateSession(context.Background(), "owner/repo", 42, "session-abc", "codex")
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

	// Verify the update expression sets both agent_session_id and agent_type.
	if upd.UpdateExpression == nil || *upd.UpdateExpression == "" {
		t.Error("UpdateExpression is nil/empty; want SET agent_session_id = :sid, agent_type = :at")
	}
	expr := *upd.UpdateExpression
	if !contains(expr, "agent_type") {
		t.Errorf("UpdateExpression %q does not mention agent_type; want SET ... agent_type = :at", expr)
	}

	// Verify the ExpressionAttributeValues includes the session ID.
	foundSession := false
	for _, v := range upd.ExpressionAttributeValues {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value == "session-abc" {
			foundSession = true
		}
	}
	if !foundSession {
		t.Errorf("ExpressionAttributeValues does not contain 'session-abc'; got %v", upd.ExpressionAttributeValues)
	}

	// Verify the ExpressionAttributeValues includes agent_type = "codex".
	foundAgent := false
	for _, v := range upd.ExpressionAttributeValues {
		if sv, ok := v.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value == "codex" {
			foundAgent = true
		}
	}
	if !foundAgent {
		t.Errorf("ExpressionAttributeValues does not contain 'codex' for agent_type; got %v", upd.ExpressionAttributeValues)
	}
}

// contains is a simple substring helper used by UpdateSession assertions.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}

// Ensure the DynamoGitHubThreadStore type reference causes compilation failure
// until 98-02 implements it (RED proof).
var _ = &bridge.DynamoGitHubThreadStore{}

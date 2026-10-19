package bridge_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// recordUpdates returns a mockDDBUpdateItem capturing every UpdateItemInput.
func recordUpdates() (*mockDDBUpdateItem, *[]*dynamodb.UpdateItemInput) {
	var seen []*dynamodb.UpdateItemInput
	m := &mockDDBUpdateItem{}
	m.updateItem = func(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
		seen = append(seen, in)
		return &dynamodb.UpdateItemOutput{}, nil
	}
	return m, &seen
}

// assertOwnerCondition: every session write is conditioned on the row naming
// the caller, which also fails on an absent item (no row is ever created).
func assertOwnerCondition(t *testing.T, in *dynamodb.UpdateItemInput, owner string) {
	t.Helper()
	if in.ConditionExpression == nil || *in.ConditionExpression != "sandbox_id = :sb" {
		t.Fatalf("condition = %v, want sandbox_id = :sb", in.ConditionExpression)
	}
	sb, ok := in.ExpressionAttributeValues[":sb"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok || sb.Value != owner {
		t.Fatalf(":sb = %v, want %q", in.ExpressionAttributeValues[":sb"], owner)
	}
}

var neverNamed = []string{"sandbox_id", "last_dispatch_ts", "created_at", "turn_count", "last_assistant_msg", "last_processed_event_ts"}

func assertSessionOnly(t *testing.T, in *dynamodb.UpdateItemInput, attr, owner string) {
	t.Helper()
	expr := *in.UpdateExpression
	for _, want := range []string{attr + " = :sid", "agent_type = :at", "last_turn_ts = :lt", "ttl_expiry = :ttl"} {
		if !strings.Contains(expr, want) {
			t.Errorf("expression lacks %s: %s", want, expr)
		}
	}
	for _, never := range neverNamed {
		if strings.Contains(expr, never) {
			t.Errorf("expression names %s: %s", never, expr)
		}
	}
	assertOwnerCondition(t, in, owner)
	if len(in.ExpressionAttributeNames) != 0 {
		t.Errorf("unexpected attribute names %v", in.ExpressionAttributeNames)
	}
}

func TestDDBThreadStore_SetSession_SetsOnlySessionAttributes(t *testing.T) {
	m, seen := recordUpdates()
	s := &bridge.DDBThreadStore{Client: m, TableName: "km-slack-threads"}
	if err := s.SetSession(context.Background(), "C1", "1.1", "sess-1", "codex", "sb-1"); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 {
		t.Fatalf("want one UpdateItem, got %d", len(*seen))
	}
	in := (*seen)[0]
	assertSessionOnly(t, in, "claude_session_id", "sb-1")
	if in.Key["channel_id"].(*dynamodbtypes.AttributeValueMemberS).Value != "C1" || in.Key["thread_ts"].(*dynamodbtypes.AttributeValueMemberS).Value != "1.1" {
		t.Fatalf("key = %v", in.Key)
	}
	if in.ExpressionAttributeValues[":sid"].(*dynamodbtypes.AttributeValueMemberS).Value != "sess-1" ||
		in.ExpressionAttributeValues[":at"].(*dynamodbtypes.AttributeValueMemberS).Value != "codex" {
		t.Fatalf("values = %v", in.ExpressionAttributeValues)
	}
}

func TestDDBThreadStore_SetSession_EmptyRemovesSession(t *testing.T) {
	m, seen := recordUpdates()
	s := &bridge.DDBThreadStore{Client: m, TableName: "t"}
	if err := s.SetSession(context.Background(), "C1", "1.1", "", "claude", "sb-2"); err != nil {
		t.Fatal(err)
	}
	in := (*seen)[0]
	expr := *in.UpdateExpression
	if !strings.Contains(expr, "REMOVE claude_session_id") || !strings.Contains(expr, "agent_type = :at") {
		t.Fatalf("empty session must REMOVE the session attribute and still SET agent_type: %s", expr)
	}
	if _, ok := in.ExpressionAttributeValues[":sid"]; ok {
		t.Fatal(":sid must not be bound when the expression does not use it (DynamoDB rejects unused values)")
	}
	assertOwnerCondition(t, in, "sb-2")
}

func TestGitHubThreadSessions_KeyAndAttribute(t *testing.T) {
	m, seen := recordUpdates()
	s := &bridge.GitHubThreadSessions{Client: m, TableName: "km-github-threads"}
	if err := s.SetSession(context.Background(), bridge.ThreadKey{Repo: "o/r", Number: 7}, "sess-1", "claude", "sb-gh"); err != nil {
		t.Fatal(err)
	}
	in := (*seen)[0]
	if *in.TableName != "km-github-threads" {
		t.Fatalf("table = %s", *in.TableName)
	}
	if in.Key["repo"].(*dynamodbtypes.AttributeValueMemberS).Value != "o/r" || in.Key["number"].(*dynamodbtypes.AttributeValueMemberN).Value != "7" {
		t.Fatalf("key = %v", in.Key)
	}
	assertSessionOnly(t, in, "agent_session_id", "sb-gh")
}

func TestH1ThreadSessions_KeyAndAttribute(t *testing.T) {
	m, seen := recordUpdates()
	s := &bridge.H1ThreadSessions{Client: m, TableName: "km-h1-threads"}
	if err := s.SetSession(context.Background(), bridge.ThreadKey{ReportID: "r1", Target: "ir-bot"}, "", "claude", "sb-h1"); err != nil {
		t.Fatal(err)
	}
	in := (*seen)[0]
	if in.Key["report_id"].(*dynamodbtypes.AttributeValueMemberS).Value != "r1" || in.Key["target"].(*dynamodbtypes.AttributeValueMemberS).Value != "ir-bot" {
		t.Fatalf("key = %v", in.Key)
	}
	if !strings.Contains(*in.UpdateExpression, "REMOVE agent_session_id") {
		t.Fatalf("expr = %s", *in.UpdateExpression)
	}
	assertOwnerCondition(t, in, "sb-h1")
}

func TestThreadSessions_LookupSandbox(t *testing.T) {
	m := &mockDDBUpdateItem{}
	var gotKey map[string]dynamodbtypes.AttributeValue
	m.getItem = func(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
		gotKey = in.Key
		return &dynamodb.GetItemOutput{Item: map[string]dynamodbtypes.AttributeValue{
			"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-1"},
		}}, nil
	}
	gh := &bridge.GitHubThreadSessions{Client: m, TableName: "g"}
	if sb, err := gh.LookupSandbox(context.Background(), bridge.ThreadKey{Repo: "o/r", Number: 7}); err != nil || sb != "sb-1" {
		t.Fatalf("got %q %v", sb, err)
	}
	if gotKey["number"].(*dynamodbtypes.AttributeValueMemberN).Value != "7" {
		t.Fatalf("key = %v", gotKey)
	}
	m.getItem = nil // empty item
	h1 := &bridge.H1ThreadSessions{Client: m, TableName: "h"}
	if sb, err := h1.LookupSandbox(context.Background(), bridge.ThreadKey{ReportID: "r1", Target: "t"}); err != nil || sb != "" {
		t.Fatalf("absent row: got %q %v", sb, err)
	}
}

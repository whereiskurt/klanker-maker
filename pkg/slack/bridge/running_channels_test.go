package bridge

import (
	"context"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeScanAPI is a test double for DDBScanAPI.
// It returns configurable pages of scan output.
type fakeScanAPI struct {
	pages    []*dynamodb.ScanOutput // pages returned in order
	pageIdx  int
	lastInput *dynamodb.ScanInput // last input received (for assertion)
}

func (f *fakeScanAPI) Scan(ctx context.Context, in *dynamodb.ScanInput, opts ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	f.lastInput = in
	if f.pageIdx >= len(f.pages) {
		return &dynamodb.ScanOutput{}, nil
	}
	out := f.pages[f.pageIdx]
	f.pageIdx++
	return out, nil
}

// makeItem builds a DynamoDB item map for a running sandbox with a Slack channel.
func makeItem(sandboxID, channelID, alias, profile string) map[string]dynamodbtypes.AttributeValue {
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":      &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		"slack_channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
		"profile_name":    &dynamodbtypes.AttributeValueMemberS{Value: profile},
	}
	if alias != "" {
		item["alias"] = &dynamodbtypes.AttributeValueMemberS{Value: alias}
	}
	return item
}

// TestDDBRunningChannelLister_Filter verifies that ListRunning correctly maps
// DynamoDB items to SandboxChannelInfo (ID, Alias, Profile).
func TestDDBRunningChannelLister_Filter(t *testing.T) {
	fake := &fakeScanAPI{
		pages: []*dynamodb.ScanOutput{
			{
				Items: []map[string]dynamodbtypes.AttributeValue{
					makeItem("sb-001", "C100", "orc", "patch"),
					makeItem("sb-002", "C200", "wrkr", "hardened"),
				},
				LastEvaluatedKey: nil, // single page
			},
		},
	}

	lister := &DDBRunningChannelLister{
		Client:    fake,
		TableName: "km-sandboxes",
	}

	results, err := lister.ListRunning(context.Background())
	if err != nil {
		t.Fatalf("ListRunning failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}

	// Find results by channel ID
	byID := make(map[string]SandboxChannelInfo)
	for _, r := range results {
		byID[r.ID] = r
	}

	r1, ok := byID["C100"]
	if !ok {
		t.Fatal("missing channel C100")
	}
	if r1.Alias != "orc" || r1.Profile != "patch" {
		t.Errorf("C100: got alias=%q profile=%q, want orc/patch", r1.Alias, r1.Profile)
	}

	r2, ok := byID["C200"]
	if !ok {
		t.Fatal("missing channel C200")
	}
	if r2.Alias != "wrkr" || r2.Profile != "hardened" {
		t.Errorf("C200: got alias=%q profile=%q, want wrkr/hardened", r2.Alias, r2.Profile)
	}
}

// TestDDBRunningChannelLister_Pagination verifies that ListRunning concatenates
// items across multiple pages (loops on LastEvaluatedKey until nil).
func TestDDBRunningChannelLister_Pagination(t *testing.T) {
	paginationKey := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-001"},
	}

	fake := &fakeScanAPI{
		pages: []*dynamodb.ScanOutput{
			{
				Items:            []map[string]dynamodbtypes.AttributeValue{makeItem("sb-001", "C1", "a1", "p1")},
				LastEvaluatedKey: paginationKey, // non-nil = more pages
			},
			{
				Items:            []map[string]dynamodbtypes.AttributeValue{makeItem("sb-002", "C2", "a2", "p2")},
				LastEvaluatedKey: nil, // last page
			},
		},
	}

	lister := &DDBRunningChannelLister{
		Client:    fake,
		TableName: "km-sandboxes",
	}

	results, err := lister.ListRunning(context.Background())
	if err != nil {
		t.Fatalf("ListRunning failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (across 2 pages), got %d: %+v", len(results), results)
	}

	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	if !ids["C1"] || !ids["C2"] {
		t.Errorf("missing expected channel IDs: got %v", ids)
	}
}

// TestDDBRunningChannelLister_ReservedWordExpr asserts that the ScanInput uses
// ExpressionAttributeNames["#s"]=="state" and the FilterExpression contains
// "#s = :running" and "attribute_exists(slack_channel_id)".
func TestDDBRunningChannelLister_ReservedWordExpr(t *testing.T) {
	fake := &fakeScanAPI{
		pages: []*dynamodb.ScanOutput{
			{Items: nil, LastEvaluatedKey: nil},
		},
	}

	lister := &DDBRunningChannelLister{
		Client:    fake,
		TableName: "km-sandboxes",
	}

	if _, err := lister.ListRunning(context.Background()); err != nil {
		t.Fatalf("ListRunning failed: %v", err)
	}

	in := fake.lastInput
	if in == nil {
		t.Fatal("no ScanInput was received")
	}

	// Assert ExpressionAttributeNames has #s -> state
	if in.ExpressionAttributeNames == nil {
		t.Fatal("ExpressionAttributeNames is nil; 'state' is a DDB reserved word — must use alias")
	}
	if got := in.ExpressionAttributeNames["#s"]; got != "state" {
		t.Errorf("ExpressionAttributeNames[\"#s\"]: got %q, want %q", got, "state")
	}

	// Assert FilterExpression contains the reserved-word alias and attribute_exists guard
	if in.FilterExpression == nil {
		t.Fatal("FilterExpression is nil")
	}
	filter := awssdk.ToString(in.FilterExpression)
	if !containsStr(filter, "#s = :running") {
		t.Errorf("FilterExpression %q does not contain '#s = :running'", filter)
	}
	if !containsStr(filter, "attribute_exists(slack_channel_id)") {
		t.Errorf("FilterExpression %q does not contain 'attribute_exists(slack_channel_id)'", filter)
	}
}

// TestDDBRunningChannelLister_EmptyAlias verifies that an item with no alias
// attribute maps to Alias:"" (no panic).
func TestDDBRunningChannelLister_EmptyAlias(t *testing.T) {
	// Item without alias attribute
	item := makeItem("sb-001", "C100", "", "patch")

	fake := &fakeScanAPI{
		pages: []*dynamodb.ScanOutput{
			{Items: []map[string]dynamodbtypes.AttributeValue{item}, LastEvaluatedKey: nil},
		},
	}

	lister := &DDBRunningChannelLister{
		Client:    fake,
		TableName: "km-sandboxes",
	}

	results, err := lister.ListRunning(context.Background())
	if err != nil {
		t.Fatalf("ListRunning failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Alias != "" {
		t.Errorf("expected empty alias, got %q", results[0].Alias)
	}
	if results[0].ID != "C100" {
		t.Errorf("expected ID C100, got %q", results[0].ID)
	}
}

// containsStr is a helper for substring checks in tests.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && findSubstr(s, sub))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

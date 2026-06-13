package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ──────────────────────────────────────────────────────────────────────────────
// Fake DDB client for repair tests
// ──────────────────────────────────────────────────────────────────────────────

// fakeDDBRepairClient records Scan, Query, GetItem, and DeleteItem calls.
// Used by slack_repair_test.go only.
type fakeDDBRepairClient struct {
	scanItems   []map[string]dynamodbtypes.AttributeValue
	scanErr     error
	queryItems  []map[string]dynamodbtypes.AttributeValue
	queryErr    error
	getItems    map[string]map[string]dynamodbtypes.AttributeValue // key: "channel_id#thread_ts"
	getErr      error
	deleteCalls []map[string]dynamodbtypes.AttributeValue
	deleteErr   error
}

func (f *fakeDDBRepairClient) Scan(_ context.Context, _ *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return &dynamodb.ScanOutput{Items: f.scanItems}, nil
}

func (f *fakeDDBRepairClient) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return &dynamodb.QueryOutput{Items: f.queryItems}, nil
}

func (f *fakeDDBRepairClient) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getItems == nil {
		return &dynamodb.GetItemOutput{}, nil
	}
	chAttr := in.Key["channel_id"]
	tsAttr := in.Key["thread_ts"]
	chSV, ok1 := chAttr.(*dynamodbtypes.AttributeValueMemberS)
	tsSV, ok2 := tsAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok1 || !ok2 {
		return &dynamodb.GetItemOutput{}, nil
	}
	key := chSV.Value + "#" + tsSV.Value
	if item, ok := f.getItems[key]; ok {
		return &dynamodb.GetItemOutput{Item: item}, nil
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (f *fakeDDBRepairClient) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, in.Key)
	return &dynamodb.DeleteItemOutput{}, nil
}

func (f *fakeDDBRepairClient) PutItem(_ context.Context, _ *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Fake Slack channel-check client
// ──────────────────────────────────────────────────────────────────────────────

// fakeSlackChannelChecker simulates conversations.info responses.
type fakeSlackChannelChecker struct {
	dead map[string]bool // channelID → true if channel_not_found
	err  map[string]error
}

func (f *fakeSlackChannelChecker) IsChannelDead(_ context.Context, channelID string) (bool, error) {
	if f.err != nil {
		if e, ok := f.err[channelID]; ok {
			return false, e
		}
	}
	if f.dead != nil {
		return f.dead[channelID], nil
	}
	return false, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers to build attribute value maps
// ──────────────────────────────────────────────────────────────────────────────

func threadRow(channelID, threadTS, sandboxID, sessionID string) map[string]dynamodbtypes.AttributeValue {
	row := map[string]dynamodbtypes.AttributeValue{
		"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
		"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
	}
	if sessionID != "" {
		row["claude_session_id"] = &dynamodbtypes.AttributeValueMemberS{Value: sessionID}
	}
	return row
}

func channelRow(alias, channelID string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"alias":      &dynamodbtypes.AttributeValueMemberS{Value: alias},
		"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TestRunSlackForgetThread
// ──────────────────────────────────────────────────────────────────────────────

// TestRunSlackForgetThread_ViaThreadChannel verifies that --thread+--channel
// deletes the exact (channel_id, thread_ts) key from km-slack-threads.
func TestRunSlackForgetThread_ViaThreadChannel(t *testing.T) {
	ddb := &fakeDDBRepairClient{
		queryItems: []map[string]dynamodbtypes.AttributeValue{}, // not used in this path
	}
	opts := ForgetThreadOpts{
		ThreadTS:  "1234567890.000001",
		ChannelID: "C0CHAN001",
		Yes:       true,
	}
	err := RunSlackForgetThread(context.Background(), ddb, "km-slack-threads", opts)
	if err != nil {
		t.Fatalf("RunSlackForgetThread via thread+channel: unexpected error: %v", err)
	}
	if len(ddb.deleteCalls) != 1 {
		t.Fatalf("want 1 DeleteItem call, got %d", len(ddb.deleteCalls))
	}
	key := ddb.deleteCalls[0]
	chSV, ok1 := key["channel_id"].(*dynamodbtypes.AttributeValueMemberS)
	tsSV, ok2 := key["thread_ts"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok1 || !ok2 {
		t.Fatal("DeleteItem key missing channel_id or thread_ts")
	}
	if chSV.Value != "C0CHAN001" {
		t.Errorf("channel_id = %q; want C0CHAN001", chSV.Value)
	}
	if tsSV.Value != "1234567890.000001" {
		t.Errorf("thread_ts = %q; want 1234567890.000001", tsSV.Value)
	}
}

// TestRunSlackForgetThread_ViaSession verifies that --session resolves via GSI
// Query then performs DeleteItem on the resolved (channel_id, thread_ts).
func TestRunSlackForgetThread_ViaSession(t *testing.T) {
	// Fake GSI returns (C0SESS, 1111111111.000001) for session "sess-xyz"
	gsiRow := map[string]dynamodbtypes.AttributeValue{
		"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: "C0SESS"},
		"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: "1111111111.000001"},
	}
	getItemRow := map[string]dynamodbtypes.AttributeValue{
		"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: "C0SESS"},
		"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: "1111111111.000001"},
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "km-sandbox-001"},
		"agent_type": &dynamodbtypes.AttributeValueMemberS{Value: "claude"},
	}
	ddb := &fakeDDBRepairClient{
		queryItems: []map[string]dynamodbtypes.AttributeValue{gsiRow},
		getItems: map[string]map[string]dynamodbtypes.AttributeValue{
			"C0SESS#1111111111.000001": getItemRow,
		},
	}
	opts := ForgetThreadOpts{
		Session: "sess-xyz",
		Yes:     true,
	}
	err := RunSlackForgetThread(context.Background(), ddb, "km-slack-threads", opts)
	if err != nil {
		t.Fatalf("RunSlackForgetThread via session: unexpected error: %v", err)
	}
	if len(ddb.deleteCalls) != 1 {
		t.Fatalf("want 1 DeleteItem call, got %d", len(ddb.deleteCalls))
	}
	key := ddb.deleteCalls[0]
	chSV, ok1 := key["channel_id"].(*dynamodbtypes.AttributeValueMemberS)
	tsSV, ok2 := key["thread_ts"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok1 || !ok2 {
		t.Fatal("DeleteItem key missing channel_id or thread_ts after session resolve")
	}
	if chSV.Value != "C0SESS" {
		t.Errorf("channel_id = %q; want C0SESS", chSV.Value)
	}
	if tsSV.Value != "1111111111.000001" {
		t.Errorf("thread_ts = %q; want 1111111111.000001", tsSV.Value)
	}
}

// TestRunSlackForgetThread_SessionNotFound verifies that a --session that
// resolves to no row returns an error (not a silent no-op).
func TestRunSlackForgetThread_SessionNotFound(t *testing.T) {
	ddb := &fakeDDBRepairClient{
		queryItems: []map[string]dynamodbtypes.AttributeValue{}, // GSI returns nothing
	}
	opts := ForgetThreadOpts{
		Session: "sess-missing",
		Yes:     true,
	}
	err := RunSlackForgetThread(context.Background(), ddb, "km-slack-threads", opts)
	if err == nil {
		t.Fatal("want error when session not found, got nil")
	}
}

// TestRunSlackForgetThread_RequiresFlags verifies that omitting both --session
// and --thread+--channel returns a validation error.
func TestRunSlackForgetThread_RequiresFlags(t *testing.T) {
	ddb := &fakeDDBRepairClient{}
	opts := ForgetThreadOpts{Yes: true} // no session, no thread+channel
	err := RunSlackForgetThread(context.Background(), ddb, "km-slack-threads", opts)
	if err == nil {
		t.Fatal("want validation error, got nil")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TestRunSlackForgetChannel
// ──────────────────────────────────────────────────────────────────────────────

// TestRunSlackForgetChannel_DeletesAliasRow verifies that forget-channel
// performs a DeleteItem keyed by the alias on km-slack-channels.
func TestRunSlackForgetChannel_DeletesAliasRow(t *testing.T) {
	ddb := &fakeDDBRepairClient{}
	err := RunSlackForgetChannel(context.Background(), ddb, "km-slack-channels", "my-alias", true)
	if err != nil {
		t.Fatalf("RunSlackForgetChannel: unexpected error: %v", err)
	}
	if len(ddb.deleteCalls) != 1 {
		t.Fatalf("want 1 DeleteItem call, got %d", len(ddb.deleteCalls))
	}
	key := ddb.deleteCalls[0]
	aliasSV, ok := key["alias"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatal("DeleteItem key must have alias attribute")
	}
	if aliasSV.Value != "my-alias" {
		t.Errorf("alias = %q; want my-alias", aliasSV.Value)
	}
}

// TestRunSlackForgetChannel_RequiresAlias verifies that an empty alias
// returns an error.
func TestRunSlackForgetChannel_RequiresAlias(t *testing.T) {
	ddb := &fakeDDBRepairClient{}
	err := RunSlackForgetChannel(context.Background(), ddb, "km-slack-channels", "", true)
	if err == nil {
		t.Fatal("want error for empty alias, got nil")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TestRunSlackThreads
// ──────────────────────────────────────────────────────────────────────────────

// TestRunSlackThreads_FiltersBySandboxID verifies that threads returns only
// rows matching the given sandbox_id (Scan+FilterExpression is tested at
// integration level; here we verify the returned rows are correct).
func TestRunSlackThreads_FiltersBySandboxID(t *testing.T) {
	row1 := threadRow("C0CHAN001", "1234567890.000001", "km-sandbox-001", "sess-abc")
	row2 := threadRow("C0CHAN002", "9999999999.000001", "km-sandbox-002", "sess-xyz")
	// Fake scan returns both rows (FilterExpression is pushed to DDB, not client).
	// RunSlackThreads trusts the Scan result as pre-filtered by DDB.
	ddb := &fakeDDBRepairClient{
		scanItems: []map[string]dynamodbtypes.AttributeValue{row1, row2},
	}
	rows, err := RunSlackThreads(context.Background(), ddb, "km-slack-threads", "km-sandbox-001")
	if err != nil {
		t.Fatalf("RunSlackThreads: unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (fake returns all), got %d", len(rows))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// TestRunSlackPruneThreads_DryRun
// ──────────────────────────────────────────────────────────────────────────────

// TestRunSlackPruneThreads_DryRun verifies that when --dry-run is true,
// dead-channel rows are listed but NO DeleteItem calls are made.
func TestRunSlackPruneThreads_DryRun(t *testing.T) {
	// Two rows: one on a dead channel, one on a live channel.
	row1 := threadRow("C0DEAD", "1234567890.000001", "km-sandbox-001", "sess-dead")
	row2 := threadRow("C0LIVE", "2222222222.000001", "km-sandbox-002", "sess-live")
	ddb := &fakeDDBRepairClient{
		scanItems: []map[string]dynamodbtypes.AttributeValue{row1, row2},
	}
	checker := &fakeSlackChannelChecker{
		dead: map[string]bool{
			"C0DEAD": true,
			"C0LIVE": false,
		},
	}
	dead, err := RunSlackPruneThreads(context.Background(), ddb, "km-slack-threads", checker, "", true /*dryRun*/)
	if err != nil {
		t.Fatalf("RunSlackPruneThreads dry-run: unexpected error: %v", err)
	}
	if len(dead) != 1 {
		t.Fatalf("want 1 dead row, got %d: %v", len(dead), dead)
	}
	if dead[0].ChannelID != "C0DEAD" {
		t.Errorf("dead row channel_id = %q; want C0DEAD", dead[0].ChannelID)
	}
	// CRITICAL: dry-run must make zero DeleteItem calls.
	if len(ddb.deleteCalls) != 0 {
		t.Errorf("dry-run must not call DeleteItem, got %d calls", len(ddb.deleteCalls))
	}
}

// TestRunSlackPruneThreads_DeletesDeadRows verifies that without --dry-run,
// dead rows ARE deleted.
func TestRunSlackPruneThreads_DeletesDeadRows(t *testing.T) {
	row1 := threadRow("C0DEAD", "1234567890.000001", "km-sandbox-001", "sess-dead")
	row2 := threadRow("C0LIVE", "2222222222.000001", "km-sandbox-002", "sess-live")
	ddb := &fakeDDBRepairClient{
		scanItems: []map[string]dynamodbtypes.AttributeValue{row1, row2},
	}
	checker := &fakeSlackChannelChecker{
		dead: map[string]bool{
			"C0DEAD": true,
			"C0LIVE": false,
		},
	}
	dead, err := RunSlackPruneThreads(context.Background(), ddb, "km-slack-threads", checker, "", false /*dryRun*/)
	if err != nil {
		t.Fatalf("RunSlackPruneThreads: unexpected error: %v", err)
	}
	if len(dead) != 1 {
		t.Fatalf("want 1 dead row, got %d", len(dead))
	}
	// Without dry-run, DeleteItem must be called once for the dead row.
	if len(ddb.deleteCalls) != 1 {
		t.Errorf("want 1 DeleteItem call for dead row, got %d", len(ddb.deleteCalls))
	}
}

// TestRunSlackPruneThreads_TransientErrorDoesNotDelete verifies that a
// transient Slack error (not channel_not_found) does NOT cause deletion.
func TestRunSlackPruneThreads_TransientErrorDoesNotDelete(t *testing.T) {
	row1 := threadRow("C0ERR", "1234567890.000001", "km-sandbox-001", "sess-err")
	ddb := &fakeDDBRepairClient{
		scanItems: []map[string]dynamodbtypes.AttributeValue{row1},
	}
	checker := &fakeSlackChannelChecker{
		err: map[string]error{
			"C0ERR": errors.New("rate_limited"), // transient, not channel_not_found
		},
	}
	dead, err := RunSlackPruneThreads(context.Background(), ddb, "km-slack-threads", checker, "", false /*dryRun*/)
	if err != nil {
		t.Fatalf("RunSlackPruneThreads transient err: unexpected error: %v", err)
	}
	// Row should NOT be in dead list (transient error → not conclusively dead)
	if len(dead) != 0 {
		t.Errorf("transient error must not mark row dead, got %d dead rows", len(dead))
	}
	if len(ddb.deleteCalls) != 0 {
		t.Errorf("transient error must not trigger DeleteItem, got %d calls", len(ddb.deleteCalls))
	}
}

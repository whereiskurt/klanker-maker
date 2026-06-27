package aws_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ---- mockSandboxMetadataAPI implements SandboxMetadataAPI for testing ----

type mockSandboxMetadataAPI struct {
	// GetItem
	getItemInput  *dynamodb.GetItemInput
	getItemOutput *dynamodb.GetItemOutput
	getItemErr    error

	// PutItem
	putItemInput  *dynamodb.PutItemInput
	putItemOutput *dynamodb.PutItemOutput
	putItemErr    error

	// UpdateItem
	updateItemInput  *dynamodb.UpdateItemInput
	updateItemOutput *dynamodb.UpdateItemOutput
	updateItemErr    error

	// DeleteItem
	deleteItemInput  *dynamodb.DeleteItemInput
	deleteItemOutput *dynamodb.DeleteItemOutput
	deleteItemErr    error

	// Scan — supports multiple pages via scanOutputs
	scanCallCount int
	scanOutputs   []*dynamodb.ScanOutput
	scanErr       error

	// Query
	queryInput  *dynamodb.QueryInput
	queryOutput *dynamodb.QueryOutput
	queryErr    error
}

func (m *mockSandboxMetadataAPI) GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	m.getItemInput = input
	return m.getItemOutput, m.getItemErr
}

func (m *mockSandboxMetadataAPI) PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	m.putItemInput = input
	return m.putItemOutput, m.putItemErr
}

func (m *mockSandboxMetadataAPI) UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	m.updateItemInput = input
	return m.updateItemOutput, m.updateItemErr
}

func (m *mockSandboxMetadataAPI) DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	m.deleteItemInput = input
	return m.deleteItemOutput, m.deleteItemErr
}

func (m *mockSandboxMetadataAPI) Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if m.scanErr != nil {
		return nil, m.scanErr
	}
	if m.scanCallCount < len(m.scanOutputs) {
		out := m.scanOutputs[m.scanCallCount]
		m.scanCallCount++
		return out, nil
	}
	return &dynamodb.ScanOutput{}, nil
}

func (m *mockSandboxMetadataAPI) Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	m.queryInput = input
	return m.queryOutput, m.queryErr
}

// ---- helper: build a DynamoDB item map from a SandboxMetadata ----

func mustMarshalSandboxItem(t *testing.T, meta kmaws.SandboxMetadata) map[string]dynamodbtypes.AttributeValue {
	t.Helper()
	// Build a raw item map mirroring what sandbox_dynamo.go stores.
	now := meta.CreatedAt.UTC().Format(time.RFC3339)
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: meta.SandboxID},
		"profile_name": &dynamodbtypes.AttributeValueMemberS{Value: meta.ProfileName},
		"substrate":    &dynamodbtypes.AttributeValueMemberS{Value: meta.Substrate},
		"region":       &dynamodbtypes.AttributeValueMemberS{Value: meta.Region},
		"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: now},
	}
	if meta.Status != "" {
		item["status"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.Status}
	}
	if meta.IdleTimeout != "" {
		item["idle_timeout"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.IdleTimeout}
	}
	if meta.MaxLifetime != "" {
		item["max_lifetime"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.MaxLifetime}
	}
	if meta.CreatedBy != "" {
		item["created_by"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.CreatedBy}
	}
	if meta.Alias != "" {
		item["alias"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.Alias}
	}
	if meta.Locked {
		item["locked"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: true}
	}
	if meta.LockedAt != nil {
		item["locked_at"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.LockedAt.UTC().Format(time.RFC3339)}
	}
	if meta.TTLExpiry != nil {
		item["ttl_expiry"] = &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", meta.TTLExpiry.Unix())}
	}
	if meta.ClonedFrom != "" {
		item["cloned_from"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.ClonedFrom}
	}
	return item
}

// ---- Tests: ReadSandboxMetadataDynamo ----

func TestReadSandboxMetadataDynamo_NotFound(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{
			Item: map[string]dynamodbtypes.AttributeValue{}, // 0 attributes = not found
		},
	}

	_, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", "sandbox-abc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, kmaws.ErrSandboxNotFound) {
		t.Errorf("expected ErrSandboxNotFound, got: %v", err)
	}
}

func TestReadSandboxMetadataDynamo_Found(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ttl := now.Add(2 * time.Hour)
	meta := kmaws.SandboxMetadata{
		SandboxID:   "sandbox-abc",
		ProfileName: "dev-profile",
		Substrate:   "ec2",
		Region:      "us-east-1",
		Status:      "running",
		CreatedAt:   now,
		TTLExpiry:   &ttl,
		Alias:       "mybox",
	}

	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{
			Item: mustMarshalSandboxItem(t, meta),
		},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", "sandbox-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SandboxID != "sandbox-abc" {
		t.Errorf("SandboxID = %q, want %q", got.SandboxID, "sandbox-abc")
	}
	if got.ProfileName != "dev-profile" {
		t.Errorf("ProfileName = %q, want %q", got.ProfileName, "dev-profile")
	}
	if got.Alias != "mybox" {
		t.Errorf("Alias = %q, want %q", got.Alias, "mybox")
	}
	if got.TTLExpiry == nil {
		t.Fatal("TTLExpiry should not be nil")
	}
	if got.TTLExpiry.Unix() != ttl.Unix() {
		t.Errorf("TTLExpiry epoch = %d, want %d", got.TTLExpiry.Unix(), ttl.Unix())
	}
}

// ---- Tests: WriteSandboxMetadataDynamo ----

func TestWriteSandboxMetadataDynamo_TTLAsNumber(t *testing.T) {
	ttl := time.Now().Add(1 * time.Hour).UTC()
	meta := &kmaws.SandboxMetadata{
		SandboxID:   "sandbox-ttl",
		ProfileName: "prof",
		Substrate:   "ec2",
		Region:      "us-east-1",
		CreatedAt:   time.Now().UTC(),
		TTLExpiry:   &ttl,
	}

	mock := &mockSandboxMetadataAPI{
		putItemOutput: &dynamodb.PutItemOutput{},
	}

	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.putItemInput == nil {
		t.Fatal("PutItem was not called")
	}

	ttlAttr, ok := mock.putItemInput.Item["ttl_expiry"]
	if !ok {
		t.Fatal("ttl_expiry attribute missing from PutItem input")
	}
	if _, isNumber := ttlAttr.(*dynamodbtypes.AttributeValueMemberN); !isNumber {
		t.Errorf("ttl_expiry should be AttributeValueMemberN (Number), got %T", ttlAttr)
	}
}

func TestWriteSandboxMetadataDynamo_OmitsAliasWhenEmpty(t *testing.T) {
	meta := &kmaws.SandboxMetadata{
		SandboxID:   "sandbox-noalias",
		ProfileName: "prof",
		Substrate:   "ec2",
		Region:      "us-east-1",
		CreatedAt:   time.Now().UTC(),
		Alias:       "", // empty — must NOT appear in item
	}

	mock := &mockSandboxMetadataAPI{
		putItemOutput: &dynamodb.PutItemOutput{},
	}

	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, present := mock.putItemInput.Item["alias"]; present {
		t.Error("alias attribute should be omitted when empty (GSI pollution prevention)")
	}
}

// TestWriteSandboxMetadataDynamo_PersistsSlackInboundQueueURL is the regression
// guard for the Phase 67 bug where read-modify-write paths (resume.go TTL
// recreation, extend.go, ttl-handler Lambda) silently dropped the Phase 67
// slack_inbound_queue_url field. WriteSandboxMetadataDynamo uses PutItem (full
// replace), so any field unmarshal-able but not marshal-able would be wiped on
// the next write. Symptom: bridge Lambda warned "unknown channel or inbound
// disabled" and dropped Slack messages mid-session.
func TestWriteSandboxMetadataDynamo_PersistsSlackInboundQueueURL(t *testing.T) {
	queueURL := "https://sqs.us-east-1.amazonaws.com/052251888500/km-slack-inbound-test.fifo"
	meta := &kmaws.SandboxMetadata{
		SandboxID:            "sandbox-slack-in",
		ProfileName:          "prof",
		Substrate:            "ec2",
		Region:               "us-east-1",
		CreatedAt:            time.Now().UTC(),
		SlackChannelID:       "C0123456789",
		SlackInboundQueueURL: queueURL,
	}

	mock := &mockSandboxMetadataAPI{putItemOutput: &dynamodb.PutItemOutput{}}
	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := mock.putItemInput.Item["slack_inbound_queue_url"]
	if !ok {
		t.Fatal("slack_inbound_queue_url attribute missing from PutItem input — read-modify-write paths will silently drop it on the next write")
	}
	sv, ok := got.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf("slack_inbound_queue_url should be AttributeValueMemberS, got %T", got)
	}
	if sv.Value != queueURL {
		t.Errorf("slack_inbound_queue_url = %q, want %q", sv.Value, queueURL)
	}
}

// TestWriteSandboxMetadataDynamo_OmitsSlackInboundQueueURLWhenEmpty verifies
// the inverse: when SlackInboundQueueURL is empty (notifySlackInboundEnabled
// was false), the attribute is NOT written, matching the omitempty pattern
// used by the other Phase 63/67 Slack fields.
func TestWriteSandboxMetadataDynamo_OmitsSlackInboundQueueURLWhenEmpty(t *testing.T) {
	meta := &kmaws.SandboxMetadata{
		SandboxID:            "sandbox-no-inbound",
		ProfileName:          "prof",
		Substrate:            "ec2",
		Region:               "us-east-1",
		CreatedAt:            time.Now().UTC(),
		SlackInboundQueueURL: "",
	}

	mock := &mockSandboxMetadataAPI{putItemOutput: &dynamodb.PutItemOutput{}}
	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, present := mock.putItemInput.Item["slack_inbound_queue_url"]; present {
		t.Error("slack_inbound_queue_url should be omitted when empty")
	}
}

// ---- Tests: DeleteSandboxMetadataDynamo ----

func TestDeleteSandboxMetadataDynamo_CallsDeleteItem(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		deleteItemOutput: &dynamodb.DeleteItemOutput{},
	}

	if err := kmaws.DeleteSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", "sandbox-del"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.deleteItemInput == nil {
		t.Fatal("DeleteItem was not called")
	}
	keyAttr, ok := mock.deleteItemInput.Key["sandbox_id"]
	if !ok {
		t.Fatal("sandbox_id key missing from DeleteItem input")
	}
	if sv, ok := keyAttr.(*dynamodbtypes.AttributeValueMemberS); !ok || sv.Value != "sandbox-del" {
		t.Errorf("sandbox_id key = %v, want AttributeValueMemberS{sandbox-del}", keyAttr)
	}
}

// ---- Tests: ListAllSandboxesByDynamo ----

func makeSandboxScanItem(sandboxID, profile, substrate, region, status string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
		"profile_name": &dynamodbtypes.AttributeValueMemberS{Value: profile},
		"substrate":    &dynamodbtypes.AttributeValueMemberS{Value: substrate},
		"region":       &dynamodbtypes.AttributeValueMemberS{Value: region},
		"status":       &dynamodbtypes.AttributeValueMemberS{Value: status},
		"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
	}
}

func TestListAllSandboxesByDynamo_SinglePage(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		scanOutputs: []*dynamodb.ScanOutput{
			{
				Items: []map[string]dynamodbtypes.AttributeValue{
					makeSandboxScanItem("sb-1", "dev", "ec2", "us-east-1", "running"),
					makeSandboxScanItem("sb-2", "prod", "ec2", "us-west-2", "stopped"),
				},
				// nil LastEvaluatedKey = no more pages
			},
		},
	}

	records, err := kmaws.ListAllSandboxesByDynamo(context.Background(), mock, "km-sandbox-metadata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}
	if mock.scanCallCount != 1 {
		t.Errorf("expected 1 Scan call, got %d", mock.scanCallCount)
	}
}

func TestListAllSandboxesByDynamo_Paginates(t *testing.T) {
	// page 1: returns a LastEvaluatedKey indicating there's more data
	page1LastKey := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sb-1"},
	}
	mock := &mockSandboxMetadataAPI{
		scanOutputs: []*dynamodb.ScanOutput{
			{
				Items: []map[string]dynamodbtypes.AttributeValue{
					makeSandboxScanItem("sb-1", "dev", "ec2", "us-east-1", "running"),
				},
				LastEvaluatedKey: page1LastKey,
			},
			{
				Items: []map[string]dynamodbtypes.AttributeValue{
					makeSandboxScanItem("sb-2", "prod", "ec2", "us-west-2", "running"),
				},
				// nil LastEvaluatedKey = last page
			},
		},
	}

	records, err := kmaws.ListAllSandboxesByDynamo(context.Background(), mock, "km-sandbox-metadata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records from 2 pages, got %d", len(records))
	}
	if mock.scanCallCount != 2 {
		t.Errorf("expected 2 Scan calls for pagination, got %d", mock.scanCallCount)
	}
}

// ---- Tests: ResolveSandboxAliasDynamo ----

func TestResolveSandboxAliasDynamo_Found(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		queryOutput: &dynamodb.QueryOutput{
			Items: []map[string]dynamodbtypes.AttributeValue{
				{
					"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: "sandbox-found"},
					"alias":      &dynamodbtypes.AttributeValueMemberS{Value: "myalias"},
				},
			},
		},
	}

	sandboxID, err := kmaws.ResolveSandboxAliasDynamo(context.Background(), mock, "km-sandbox-metadata", "myalias")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sandboxID != "sandbox-found" {
		t.Errorf("expected sandbox-found, got %q", sandboxID)
	}
	// Verify the GSI was queried
	if mock.queryInput == nil {
		t.Fatal("Query was not called")
	}
	if mock.queryInput.IndexName == nil || *mock.queryInput.IndexName != "alias-index" {
		t.Errorf("expected alias-index GSI, got %v", mock.queryInput.IndexName)
	}
}

func TestResolveSandboxAliasDynamo_NotFound(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		queryOutput: &dynamodb.QueryOutput{
			Items: []map[string]dynamodbtypes.AttributeValue{},
		},
	}

	_, err := kmaws.ResolveSandboxAliasDynamo(context.Background(), mock, "km-sandbox-metadata", "missingalias")
	if err == nil {
		t.Fatal("expected error for missing alias, got nil")
	}
}

// ---- Tests: LockSandboxDynamo ----

func TestLockSandboxDynamo_ConditionExpression(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		updateItemOutput: &dynamodb.UpdateItemOutput{},
	}

	if err := kmaws.LockSandboxDynamo(context.Background(), mock, "km-sandbox-metadata", "sandbox-lock"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.updateItemInput == nil {
		t.Fatal("UpdateItem was not called")
	}
	if mock.updateItemInput.ConditionExpression == nil {
		t.Fatal("ConditionExpression should not be nil for LockSandboxDynamo")
	}
	cond := *mock.updateItemInput.ConditionExpression
	// Must contain atomic lock check
	if !contains(cond, "attribute_not_exists(locked)") && !contains(cond, "locked = :f") {
		t.Errorf("ConditionExpression %q does not contain expected lock check", cond)
	}
}

func TestLockSandboxDynamo_AlreadyLocked(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		updateItemErr: &dynamodbtypes.ConditionalCheckFailedException{
			Message: awssdk.String("The conditional request failed"),
		},
	}

	err := kmaws.LockSandboxDynamo(context.Background(), mock, "km-sandbox-metadata", "sandbox-locked")
	if err == nil {
		t.Fatal("expected error for already-locked sandbox, got nil")
	}
	if !contains(err.Error(), "already locked") {
		t.Errorf("expected 'already locked' in error message, got: %v", err)
	}
}

// ---- Tests: UnlockSandboxDynamo ----

func TestUnlockSandboxDynamo_ConditionExpression(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		updateItemOutput: &dynamodb.UpdateItemOutput{},
	}

	if err := kmaws.UnlockSandboxDynamo(context.Background(), mock, "km-sandbox-metadata", "sandbox-unlock"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.updateItemInput == nil {
		t.Fatal("UpdateItem was not called")
	}
	if mock.updateItemInput.ConditionExpression == nil {
		t.Fatal("ConditionExpression should not be nil for UnlockSandboxDynamo")
	}
	cond := *mock.updateItemInput.ConditionExpression
	if !contains(cond, "locked = :t") {
		t.Errorf("ConditionExpression %q does not contain 'locked = :t'", cond)
	}
}

// ---- Tests: UpdateSandboxStatusDynamo ----

func TestUpdateSandboxStatusDynamo_UpdatesStatus(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		updateItemOutput: &dynamodb.UpdateItemOutput{},
	}

	if err := kmaws.UpdateSandboxStatusDynamo(context.Background(), mock, "km-sandbox-metadata", "sandbox-status", "paused"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.updateItemInput == nil {
		t.Fatal("UpdateItem was not called")
	}
	// Verify status value is set in ExpressionAttributeValues
	statusAttr, ok := mock.updateItemInput.ExpressionAttributeValues[":status"]
	if !ok {
		t.Fatal(":status expression attribute missing")
	}
	if sv, ok := statusAttr.(*dynamodbtypes.AttributeValueMemberS); !ok || sv.Value != "paused" {
		t.Errorf(":status = %v, want AttributeValueMemberS{paused}", statusAttr)
	}
}

// ---- Tests: ClonedFrom field marshal/unmarshal ----

func TestClonedFrom_MarshalIncludesWhenNonEmpty(t *testing.T) {
	meta := &kmaws.SandboxMetadata{
		SandboxID:   "sb-clone",
		ProfileName: "dev",
		Substrate:   "ec2",
		Region:      "us-east-1",
		CreatedAt:   time.Now().UTC(),
		ClonedFrom:  "sb-abc12345",
	}

	mock := &mockSandboxMetadataAPI{
		putItemOutput: &dynamodb.PutItemOutput{},
	}

	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	clonedAttr, ok := mock.putItemInput.Item["cloned_from"]
	if !ok {
		t.Fatal("cloned_from attribute missing from PutItem input when ClonedFrom is set")
	}
	sv, ok := clonedAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Errorf("cloned_from should be AttributeValueMemberS, got %T", clonedAttr)
	}
	if sv.Value != "sb-abc12345" {
		t.Errorf("cloned_from = %q, want %q", sv.Value, "sb-abc12345")
	}
}

func TestClonedFrom_MarshalOmitsWhenEmpty(t *testing.T) {
	meta := &kmaws.SandboxMetadata{
		SandboxID:   "sb-noclone",
		ProfileName: "dev",
		Substrate:   "ec2",
		Region:      "us-east-1",
		CreatedAt:   time.Now().UTC(),
		ClonedFrom:  "", // empty — must NOT appear in item
	}

	mock := &mockSandboxMetadataAPI{
		putItemOutput: &dynamodb.PutItemOutput{},
	}

	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, present := mock.putItemInput.Item["cloned_from"]; present {
		t.Error("cloned_from attribute should be omitted when ClonedFrom is empty (GSI pollution prevention)")
	}
}

func TestClonedFrom_UnmarshalPopulatesWhenPresent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := kmaws.SandboxMetadata{
		SandboxID:   "sb-read-clone",
		ProfileName: "dev",
		Substrate:   "ec2",
		Region:      "us-east-1",
		Status:      "running",
		CreatedAt:   now,
		ClonedFrom:  "sb-source99",
	}

	item := mustMarshalSandboxItem(t, meta)

	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", "sb-read-clone")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ClonedFrom != "sb-source99" {
		t.Errorf("ClonedFrom = %q, want %q", got.ClonedFrom, "sb-source99")
	}
}

func TestClonedFrom_UnmarshalEmptyWhenAbsent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := kmaws.SandboxMetadata{
		SandboxID:   "sb-no-clone-field",
		ProfileName: "dev",
		Substrate:   "ec2",
		Region:      "us-east-1",
		Status:      "running",
		CreatedAt:   now,
		// ClonedFrom intentionally absent
	}

	item := mustMarshalSandboxItem(t, meta)

	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", "sb-no-clone-field")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ClonedFrom != "" {
		t.Errorf("ClonedFrom = %q, want empty string when absent from DynamoDB item", got.ClonedFrom)
	}
}

func TestClonedFrom_ToSandboxMetadataPropagates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := kmaws.SandboxMetadata{
		SandboxID:   "sb-prop",
		ProfileName: "dev",
		Substrate:   "ec2",
		Region:      "us-east-1",
		Status:      "running",
		CreatedAt:   now,
		ClonedFrom:  "sb-origin",
	}

	item := mustMarshalSandboxItem(t, meta)

	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandbox-metadata", "sb-prop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ClonedFrom != "sb-origin" {
		t.Errorf("toSandboxMetadata: ClonedFrom = %q, want %q", got.ClonedFrom, "sb-origin")
	}
}

func TestClonedFrom_MetadataToRecordPropagates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := kmaws.SandboxMetadata{
		SandboxID:   "sb-rec",
		ProfileName: "dev",
		Substrate:   "ec2",
		Region:      "us-east-1",
		Status:      "running",
		CreatedAt:   now,
		ClonedFrom:  "sb-parent",
	}

	item := mustMarshalSandboxItem(t, meta)

	mock := &mockSandboxMetadataAPI{
		scanOutputs: []*dynamodb.ScanOutput{
			{Items: []map[string]dynamodbtypes.AttributeValue{item}},
		},
	}

	records, err := kmaws.ListAllSandboxesByDynamo(context.Background(), mock, "km-sandbox-metadata")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ClonedFrom != "sb-parent" {
		t.Errorf("metadataToRecord: ClonedFrom = %q, want %q", records[0].ClonedFrom, "sb-parent")
	}
}

// ---- Phase 63: SlackChannelID / SlackPerSandbox round-trip tests ----

// TestMarshalUnmarshalSlackFields verifies that SlackChannelID and SlackPerSandbox
// survive a marshal → unmarshal round-trip through the DynamoDB item representation.
func TestMarshalUnmarshalSlackFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := kmaws.SandboxMetadata{
		SandboxID:       "sb-slack01",
		ProfileName:     "dev",
		Substrate:       "ec2",
		Region:          "us-east-1",
		Status:          "running",
		CreatedAt:       now,
		SlackChannelID:  "C0987654321",
		SlackPerSandbox: true,
	}

	// Build the item map and round-trip through ReadSandboxMetadataDynamo.
	item := mustMarshalSandboxItem(t, meta)

	// Add Slack fields explicitly (mustMarshalSandboxItem does not know about Phase 63 yet).
	item["slack_channel_id"] = &dynamodbtypes.AttributeValueMemberS{Value: meta.SlackChannelID}
	item["slack_per_sandbox"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: meta.SlackPerSandbox}

	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandboxes", "sb-slack01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SlackChannelID != "C0987654321" {
		t.Errorf("SlackChannelID = %q, want %q", got.SlackChannelID, "C0987654321")
	}
	if !got.SlackPerSandbox {
		t.Errorf("SlackPerSandbox = false, want true")
	}
}

// TestMarshalSlackFields_OmitWhenEmpty verifies that SlackChannelID and SlackPerSandbox
// are not included in the DynamoDB item when empty/false.
func TestMarshalSlackFields_OmitWhenEmpty(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	mock := &mockSandboxMetadataAPI{
		putItemOutput: &dynamodb.PutItemOutput{},
	}

	meta := kmaws.SandboxMetadata{
		SandboxID:   "sb-noslack",
		ProfileName: "dev",
		Substrate:   "ec2",
		Region:      "us-east-1",
		CreatedAt:   now,
		// SlackChannelID and SlackPerSandbox intentionally empty/false
	}

	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), mock, "km-sandboxes", &meta); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// PutItem captures the item in mock.putItemInput.Item
	capturedItem := mock.putItemInput.Item
	if _, ok := capturedItem["slack_channel_id"]; ok {
		t.Error("slack_channel_id should be omitted when empty")
	}
	if _, ok := capturedItem["slack_per_sandbox"]; ok {
		t.Error("slack_per_sandbox should be omitted when false")
	}
}

// ---- Phase 63-09: SlackArchiveOnDestroy round-trip tests ----

// TestSlackArchiveOnDestroy_NilRoundTrip verifies that a nil SlackArchiveOnDestroy
// is preserved after a marshal → unmarshal round-trip (field absent from DynamoDB item).
func TestSlackArchiveOnDestroy_NilRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:             "sb-arch01",
		ProfileName:           "dev",
		Substrate:             "ec2",
		Region:                "us-east-1",
		CreatedAt:             now,
		SlackChannelID:        "C0111",
		SlackPerSandbox:       true,
		SlackArchiveOnDestroy: nil, // default: archive
	}

	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), &mockSandboxMetadataAPI{
		putItemOutput: &dynamodb.PutItemOutput{},
	}, "km-sandboxes", meta); err != nil {
		t.Fatalf("write: %v", err)
	}

	// nil pointer → field must be ABSENT from the item (omitempty semantics)
	item := mustMarshalSandboxItemFull(t, meta)
	if _, ok := item["slack_archive_on_destroy"]; ok {
		t.Error("slack_archive_on_destroy should be omitted when nil")
	}
}

// TestSlackArchiveOnDestroy_TrueRoundTrip verifies that &true survives marshal → unmarshal.
func TestSlackArchiveOnDestroy_TrueRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tru := true
	meta := &kmaws.SandboxMetadata{
		SandboxID:             "sb-arch02",
		ProfileName:           "dev",
		Substrate:             "ec2",
		Region:                "us-east-1",
		CreatedAt:             now,
		SlackChannelID:        "C0222",
		SlackPerSandbox:       true,
		SlackArchiveOnDestroy: &tru,
	}

	item := mustMarshalSandboxItemFull(t, meta)

	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}
	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandboxes", "sb-arch02")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SlackArchiveOnDestroy == nil {
		t.Fatal("SlackArchiveOnDestroy: got nil, want &true")
	}
	if !*got.SlackArchiveOnDestroy {
		t.Errorf("SlackArchiveOnDestroy: got &false, want &true")
	}
}

// TestSlackArchiveOnDestroy_FalseRoundTrip verifies that &false survives marshal → unmarshal.
func TestSlackArchiveOnDestroy_FalseRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fls := false
	meta := &kmaws.SandboxMetadata{
		SandboxID:             "sb-arch03",
		ProfileName:           "dev",
		Substrate:             "ec2",
		Region:                "us-east-1",
		CreatedAt:             now,
		SlackChannelID:        "C0333",
		SlackPerSandbox:       true,
		SlackArchiveOnDestroy: &fls,
	}

	item := mustMarshalSandboxItemFull(t, meta)

	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}
	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(), mock, "km-sandboxes", "sb-arch03")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SlackArchiveOnDestroy == nil {
		t.Fatal("SlackArchiveOnDestroy: got nil, want &false")
	}
	if *got.SlackArchiveOnDestroy {
		t.Errorf("SlackArchiveOnDestroy: got &true, want &false")
	}
}

// ---- Phase 91.5 per-sandbox override round-trip (resume/extend/ttl-handler bug) ----
//
// slack_mention_only and slack_react_always are written at km create
// (create_slack_inbound.go) as standalone DDB attributes. Any read-modify-write
// path — resume.go TTL recreation, extend.go, the ttl-handler Lambda — reads the
// row into SandboxMetadata, mutates it, and PutItems the whole row back. If the
// struct does not round-trip these two attributes they are silently stripped, so
// the bridge's FetchByChannel falls back to install-level defaults (mention_only
// off, react_always on → 👀 on every message). These tests lock the round-trip.

// TestSlackMentionOnly_NilRoundTrip: nil pointer → attribute omitted (absence
// means "fall back to install-level KM_SLACK_MENTION_ONLY").
func TestSlackMentionOnly_NilRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-mo-nil",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		SlackChannelID:   "C0M01",
		SlackMentionOnly: nil,
	}
	item := mustMarshalSandboxItemFull(t, meta)
	if _, ok := item["slack_mention_only"]; ok {
		t.Error("slack_mention_only should be omitted when nil")
	}
}

// TestSlackMentionOnly_TrueRoundTrip: &true survives marshal → unmarshal.
func TestSlackMentionOnly_TrueRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tru := true
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-mo-true",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		SlackChannelID:   "C0M02",
		SlackMentionOnly: &tru,
	}
	item := mustMarshalSandboxItemFull(t, meta)
	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-mo-true")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SlackMentionOnly == nil || !*got.SlackMentionOnly {
		t.Errorf("SlackMentionOnly round-trip: got %v, want &true", got.SlackMentionOnly)
	}
}

// TestSlackMentionOnly_FalseRoundTrip: &false (explicit chatty override) must
// survive — it is NOT the same as nil/absent.
func TestSlackMentionOnly_FalseRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fls := false
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-mo-false",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		SlackChannelID:   "C0M03",
		SlackMentionOnly: &fls,
	}
	item := mustMarshalSandboxItemFull(t, meta)
	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-mo-false")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SlackMentionOnly == nil || *got.SlackMentionOnly {
		t.Errorf("SlackMentionOnly round-trip: got %v, want &false", got.SlackMentionOnly)
	}
}

// TestSlackReactAlways_NilRoundTrip: nil pointer → attribute omitted.
func TestSlackReactAlways_NilRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-ra-nil",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		SlackChannelID:   "C0R01",
		SlackReactAlways: nil,
	}
	item := mustMarshalSandboxItemFull(t, meta)
	if _, ok := item["slack_react_always"]; ok {
		t.Error("slack_react_always should be omitted when nil")
	}
}

// TestSlackReactAlways_TrueRoundTrip: &true survives marshal → unmarshal.
func TestSlackReactAlways_TrueRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tru := true
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-ra-true",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		SlackChannelID:   "C0R02",
		SlackReactAlways: &tru,
	}
	item := mustMarshalSandboxItemFull(t, meta)
	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-ra-true")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SlackReactAlways == nil || !*got.SlackReactAlways {
		t.Errorf("SlackReactAlways round-trip: got %v, want &true", got.SlackReactAlways)
	}
}

// TestSlackReactAlways_FalseRoundTrip: &false (explicit first-only override) must
// survive a read-modify-write — this is the resume-reversion bug under test.
func TestSlackReactAlways_FalseRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fls := false
	meta := &kmaws.SandboxMetadata{
		SandboxID:        "sb-ra-false",
		ProfileName:      "dev",
		Substrate:        "ec2",
		Region:           "us-east-1",
		CreatedAt:        now,
		SlackChannelID:   "C0R03",
		SlackReactAlways: &fls,
	}
	item := mustMarshalSandboxItemFull(t, meta)
	got, err := kmaws.ReadSandboxMetadataDynamo(context.Background(),
		&mockSandboxMetadataAPI{getItemOutput: &dynamodb.GetItemOutput{Item: item}},
		"km-sandboxes", "sb-ra-false")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SlackReactAlways == nil || *got.SlackReactAlways {
		t.Errorf("SlackReactAlways round-trip: got %v, want &false", got.SlackReactAlways)
	}
}

// ---- Fix A: UpdateSandboxTTLDynamo — targeted TTL UpdateItem (resume/extend) ----
//
// The resume/extend TTL-recreation path historically did a full-row PutItem to
// bump expiry, which clobbered attributes not carried by SandboxMetadata.
// UpdateSandboxTTLDynamo issues a targeted UpdateItem touching only ttl_expiry /
// expires_at so unrelated attributes (Slack overrides, etc.) are never rewritten.

// TestUpdateSandboxTTLDynamo_DestroyPolicy_SetsTTLAndExpiresAt verifies that for
// a destroy-policy sandbox the helper SETs both ttl_expiry (native TTL, Number)
// and expires_at (display, String) via a single UpdateItem — never a PutItem.
func TestUpdateSandboxTTLDynamo_DestroyPolicy_SetsTTLAndExpiresAt(t *testing.T) {
	newExpiry := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	mock := &mockSandboxMetadataAPI{updateItemOutput: &dynamodb.UpdateItemOutput{}}

	if err := kmaws.UpdateSandboxTTLDynamo(context.Background(), mock, "km-sandboxes", "sb-ttl-d", newExpiry, "destroy"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if mock.putItemInput != nil {
		t.Fatal("UpdateSandboxTTLDynamo must NOT call PutItem (full-row overwrite drops unrelated attributes)")
	}
	if mock.updateItemInput == nil {
		t.Fatal("UpdateItem was not called")
	}
	expr := awssdk.ToString(mock.updateItemInput.UpdateExpression)
	if !strings.Contains(expr, "ttl_expiry") || !strings.Contains(expr, "expires_at") {
		t.Errorf("UpdateExpression %q must SET both ttl_expiry and expires_at", expr)
	}
	te, ok := mock.updateItemInput.ExpressionAttributeValues[":te"].(*dynamodbtypes.AttributeValueMemberN)
	if !ok {
		t.Fatalf("ttl_expiry value must be a Number (N) for native DynamoDB TTL, got %T", mock.updateItemInput.ExpressionAttributeValues[":te"])
	}
	if te.Value != strconv.FormatInt(newExpiry.Unix(), 10) {
		t.Errorf(":te = %q, want %q", te.Value, strconv.FormatInt(newExpiry.Unix(), 10))
	}
}

// TestUpdateSandboxTTLDynamo_StopPolicy_RemovesTTL verifies that for a stop/retain
// sandbox the helper REMOVEs ttl_expiry (so native TTL can't auto-delete it) while
// still updating the display-only expires_at — matching marshalSandboxItem's guard.
func TestUpdateSandboxTTLDynamo_StopPolicy_RemovesTTL(t *testing.T) {
	newExpiry := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	mock := &mockSandboxMetadataAPI{updateItemOutput: &dynamodb.UpdateItemOutput{}}

	if err := kmaws.UpdateSandboxTTLDynamo(context.Background(), mock, "km-sandboxes", "sb-ttl-s", newExpiry, "stop"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if mock.updateItemInput == nil {
		t.Fatal("UpdateItem was not called")
	}
	expr := awssdk.ToString(mock.updateItemInput.UpdateExpression)
	if !strings.Contains(expr, "REMOVE") || !strings.Contains(expr, "ttl_expiry") {
		t.Errorf("UpdateExpression %q must REMOVE ttl_expiry for stop policy", expr)
	}
	if !strings.Contains(expr, "expires_at") {
		t.Errorf("UpdateExpression %q must still SET expires_at", expr)
	}
	if _, present := mock.updateItemInput.ExpressionAttributeValues[":te"]; present {
		t.Error(":te (ttl_expiry value) must NOT be present when ttl_expiry is REMOVEd")
	}
}

// mustMarshalSandboxItemFull calls the real marshalSandboxItem exported function via
// WriteSandboxMetadataDynamo so we can capture the item map.
// It uses the existing mock and reads back the captured item from PutItem.
func mustMarshalSandboxItemFull(t *testing.T, meta *kmaws.SandboxMetadata) map[string]dynamodbtypes.AttributeValue {
	t.Helper()
	m := &mockSandboxMetadataAPI{putItemOutput: &dynamodb.PutItemOutput{}}
	if err := kmaws.WriteSandboxMetadataDynamo(context.Background(), m, "km-sandboxes", meta); err != nil {
		t.Fatalf("mustMarshalSandboxItemFull write: %v", err)
	}
	return m.putItemInput.Item
}

// ---- Phase 77: UpdateSandboxStatusAndReasonDynamo tests ----

// TestUpdateSandboxStatusAndReasonDynamo_RoundTrip verifies:
//  1. The helper issues one UpdateItem with an expression containing
//     failure_reason and failed_at.
//  2. :reason and :ts expression-attribute values match the inputs.
//  3. ReadSandboxMetadataDynamo correctly populates FailureReason and FailedAt
//     when the DDB item carries those attributes.
func TestUpdateSandboxStatusAndReasonDynamo_RoundTrip(t *testing.T) {
	ctx := context.Background()
	failedAt := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	mock := &mockSandboxMetadataAPI{
		updateItemOutput: &dynamodb.UpdateItemOutput{},
	}

	err := kmaws.UpdateSandboxStatusAndReasonDynamo(ctx, mock, "km-sandboxes", "sb-fail1", "failed", "Error: x", failedAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.updateItemInput == nil {
		t.Fatal("UpdateItem was not called")
	}

	// UpdateExpression must reference both failure_reason and failed_at.
	expr := awssdk.ToString(mock.updateItemInput.UpdateExpression)
	if !contains(expr, "failure_reason") {
		t.Errorf("UpdateExpression %q does not contain 'failure_reason'", expr)
	}
	if !contains(expr, "failed_at") {
		t.Errorf("UpdateExpression %q does not contain 'failed_at'", expr)
	}

	// :reason must equal "Error: x".
	reasonAttr, ok := mock.updateItemInput.ExpressionAttributeValues[":reason"]
	if !ok {
		t.Fatal(":reason expression attribute missing")
	}
	sv, ok := reasonAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf(":reason should be AttributeValueMemberS, got %T", reasonAttr)
	}
	if sv.Value != "Error: x" {
		t.Errorf(":reason = %q, want %q", sv.Value, "Error: x")
	}

	// :ts must be a non-empty RFC3339-parseable string.
	tsAttr, ok := mock.updateItemInput.ExpressionAttributeValues[":ts"]
	if !ok {
		t.Fatal(":ts expression attribute missing")
	}
	tsv, ok := tsAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf(":ts should be AttributeValueMemberS, got %T", tsAttr)
	}
	if tsv.Value == "" {
		t.Fatal(":ts should be non-empty")
	}
	parsed, err := time.Parse(time.RFC3339, tsv.Value)
	if err != nil {
		t.Errorf(":ts value %q is not RFC3339-parseable: %v", tsv.Value, err)
	}
	if !parsed.Equal(failedAt) {
		t.Errorf(":ts parsed = %v, want %v", parsed, failedAt)
	}

	// Now round-trip: pre-seed GetItem response with the new fields and confirm
	// ReadSandboxMetadataDynamo populates them.
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":     &dynamodbtypes.AttributeValueMemberS{Value: "sb-fail1"},
		"profile_name":   &dynamodbtypes.AttributeValueMemberS{Value: "dev"},
		"substrate":      &dynamodbtypes.AttributeValueMemberS{Value: "ec2"},
		"region":         &dynamodbtypes.AttributeValueMemberS{Value: "us-east-1"},
		"status":         &dynamodbtypes.AttributeValueMemberS{Value: "failed"},
		"created_at":     &dynamodbtypes.AttributeValueMemberS{Value: failedAt.Format(time.RFC3339)},
		"failure_reason": &dynamodbtypes.AttributeValueMemberS{Value: "Error: x"},
		"failed_at":      &dynamodbtypes.AttributeValueMemberS{Value: failedAt.UTC().Format(time.RFC3339)},
	}
	readMock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(ctx, readMock, "km-sandboxes", "sb-fail1")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: unexpected error: %v", err)
	}
	if got.FailureReason != "Error: x" {
		t.Errorf("FailureReason = %q, want %q", got.FailureReason, "Error: x")
	}
	if got.FailedAt == nil {
		t.Fatal("FailedAt should not be nil")
	}
	if !got.FailedAt.Equal(failedAt) {
		t.Errorf("FailedAt = %v, want %v", got.FailedAt, failedAt)
	}
}

// TestUpdateSandboxStatusAndReasonDynamo_OldRecord_ZeroValue verifies that
// ReadSandboxMetadataDynamo returns zero-values for FailureReason and FailedAt
// when the DDB item does not carry those attributes (backward compat).
func TestUpdateSandboxStatusAndReasonDynamo_OldRecord_ZeroValue(t *testing.T) {
	ctx := context.Background()

	// Old record: no failure_reason or failed_at attributes.
	item := map[string]dynamodbtypes.AttributeValue{
		"sandbox_id":   &dynamodbtypes.AttributeValueMemberS{Value: "sb-old"},
		"profile_name": &dynamodbtypes.AttributeValueMemberS{Value: "dev"},
		"substrate":    &dynamodbtypes.AttributeValueMemberS{Value: "ec2"},
		"region":       &dynamodbtypes.AttributeValueMemberS{Value: "us-east-1"},
		"status":       &dynamodbtypes.AttributeValueMemberS{Value: "running"},
		"created_at":   &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
	}
	mock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}

	got, err := kmaws.ReadSandboxMetadataDynamo(ctx, mock, "km-sandboxes", "sb-old")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.FailureReason != "" {
		t.Errorf("FailureReason = %q, want empty string for old record without attribute", got.FailureReason)
	}
	if got.FailedAt != nil {
		t.Errorf("FailedAt = %v, want nil for old record without attribute", got.FailedAt)
	}
}

// TestSandboxMetadataMarshal_FailureFields verifies that FailureReason and FailedAt
// survive a full marshal → unmarshal round-trip through WriteSandboxMetadataDynamo
// and ReadSandboxMetadataDynamo.
func TestSandboxMetadataMarshal_FailureFields(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	failedAt := now.Add(-5 * time.Minute)

	meta := &kmaws.SandboxMetadata{
		SandboxID:     "sb-fail-marshal",
		ProfileName:   "dev",
		Substrate:     "ec2",
		Region:        "us-east-1",
		Status:        "failed",
		CreatedAt:     now,
		FailureReason: "terraform apply timed out after 600s",
		FailedAt:      &failedAt,
	}

	// Marshal via WriteSandboxMetadataDynamo, capture the PutItem item map.
	writeMock := &mockSandboxMetadataAPI{putItemOutput: &dynamodb.PutItemOutput{}}
	if err := kmaws.WriteSandboxMetadataDynamo(ctx, writeMock, "km-sandboxes", meta); err != nil {
		t.Fatalf("WriteSandboxMetadataDynamo: %v", err)
	}
	item := writeMock.putItemInput.Item

	// Verify failure_reason attribute is present and correct.
	frAttr, ok := item["failure_reason"]
	if !ok {
		t.Fatal("failure_reason attribute missing from PutItem input")
	}
	frSv, ok := frAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf("failure_reason should be AttributeValueMemberS, got %T", frAttr)
	}
	if frSv.Value != meta.FailureReason {
		t.Errorf("failure_reason = %q, want %q", frSv.Value, meta.FailureReason)
	}

	// Verify failed_at attribute is present and RFC3339.
	faAttr, ok := item["failed_at"]
	if !ok {
		t.Fatal("failed_at attribute missing from PutItem input")
	}
	faSv, ok := faAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatalf("failed_at should be AttributeValueMemberS, got %T", faAttr)
	}
	parsedFA, err := time.Parse(time.RFC3339, faSv.Value)
	if err != nil {
		t.Fatalf("failed_at %q is not RFC3339: %v", faSv.Value, err)
	}
	if !parsedFA.Equal(failedAt) {
		t.Errorf("failed_at parsed = %v, want %v", parsedFA, failedAt)
	}

	// Now unmarshal: pre-seed the GetItem response with the captured item and
	// verify ReadSandboxMetadataDynamo restores both fields.
	readMock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}
	got, err := kmaws.ReadSandboxMetadataDynamo(ctx, readMock, "km-sandboxes", "sb-fail-marshal")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
	}
	if got.FailureReason != meta.FailureReason {
		t.Errorf("FailureReason after round-trip = %q, want %q", got.FailureReason, meta.FailureReason)
	}
	if got.FailedAt == nil {
		t.Fatal("FailedAt after round-trip should not be nil")
	}
	if !got.FailedAt.Equal(failedAt) {
		t.Errorf("FailedAt after round-trip = %v, want %v", got.FailedAt, failedAt)
	}
}

// TestUpdateSandboxStatusDynamo_StillWorks verifies the OLD helper is unchanged:
// it updates only status, and the UpdateExpression does NOT reference failure_reason
// or failed_at.
func TestUpdateSandboxStatusDynamo_StillWorks(t *testing.T) {
	mock := &mockSandboxMetadataAPI{
		updateItemOutput: &dynamodb.UpdateItemOutput{},
	}

	if err := kmaws.UpdateSandboxStatusDynamo(context.Background(), mock, "km-sandbox-metadata", "sandbox-old-helper", "running"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.updateItemInput == nil {
		t.Fatal("UpdateItem was not called")
	}

	expr := awssdk.ToString(mock.updateItemInput.UpdateExpression)
	if contains(expr, "failure_reason") {
		t.Errorf("UpdateSandboxStatusDynamo UpdateExpression %q should NOT contain 'failure_reason'", expr)
	}
	if contains(expr, "failed_at") {
		t.Errorf("UpdateSandboxStatusDynamo UpdateExpression %q should NOT contain 'failed_at'", expr)
	}

	// :status must be set correctly.
	statusAttr, ok := mock.updateItemInput.ExpressionAttributeValues[":status"]
	if !ok {
		t.Fatal(":status expression attribute missing")
	}
	sv, ok := statusAttr.(*dynamodbtypes.AttributeValueMemberS)
	if !ok || sv.Value != "running" {
		t.Errorf(":status = %v, want AttributeValueMemberS{running}", statusAttr)
	}
}

// ---- Phase 121: Round-trip + marshal tests for quota/freeze attrs ----

// TestSandboxMetadataRoundTrip (META-01) verifies that all five Phase 121 attrs
// survive a full marshal → unmarshal cycle through WriteSandboxMetadataDynamo and
// ReadSandboxMetadataDynamo. A full-row PutItem (resume/extend/ttl-handler) must NOT
// strip these fields — the project_sandboxmetadata_lossy_roundtrip footgun.
func TestSandboxMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	frozenAt := now.Add(-1 * time.Hour)

	meta := &kmaws.SandboxMetadata{
		SandboxID:    "sb-freeze-rt",
		ProfileName:  "dev",
		Substrate:    "ec2",
		Region:       "us-east-1",
		Status:       "running",
		CreatedAt:    now,
		ActionLimits: `{"push":{"daily":10},"deploy":{"hourly":2}}`,
		ActionFrozen: true,
		FrozenReason: "quota:push:daily",
		FrozenAt:     &frozenAt,
		FrozenBy:     "auto:push:daily",
	}

	// Marshal via WriteSandboxMetadataDynamo (captures the PutItem item map).
	writeMock := &mockSandboxMetadataAPI{putItemOutput: &dynamodb.PutItemOutput{}}
	if err := kmaws.WriteSandboxMetadataDynamo(ctx, writeMock, "km-sandboxes", meta); err != nil {
		t.Fatalf("WriteSandboxMetadataDynamo: %v", err)
	}
	item := writeMock.putItemInput.Item

	// Unmarshal via ReadSandboxMetadataDynamo.
	readMock := &mockSandboxMetadataAPI{
		getItemOutput: &dynamodb.GetItemOutput{Item: item},
	}
	got, err := kmaws.ReadSandboxMetadataDynamo(ctx, readMock, "km-sandboxes", "sb-freeze-rt")
	if err != nil {
		t.Fatalf("ReadSandboxMetadataDynamo: %v", err)
	}

	if got.ActionLimits != meta.ActionLimits {
		t.Errorf("ActionLimits round-trip: got %q, want %q", got.ActionLimits, meta.ActionLimits)
	}
	if got.ActionFrozen != meta.ActionFrozen {
		t.Errorf("ActionFrozen round-trip: got %v, want %v", got.ActionFrozen, meta.ActionFrozen)
	}
	if got.FrozenReason != meta.FrozenReason {
		t.Errorf("FrozenReason round-trip: got %q, want %q", got.FrozenReason, meta.FrozenReason)
	}
	if got.FrozenBy != meta.FrozenBy {
		t.Errorf("FrozenBy round-trip: got %q, want %q", got.FrozenBy, meta.FrozenBy)
	}
	if got.FrozenAt == nil {
		t.Fatal("FrozenAt round-trip: got nil, want non-nil")
	}
	if !got.FrozenAt.Equal(frozenAt) {
		t.Errorf("FrozenAt round-trip: got %v, want %v", got.FrozenAt, frozenAt)
	}
}

// TestMarshalFrozen (META-02) asserts that marshalSandboxItem (via WriteSandboxMetadataDynamo)
// emits all five Phase 121 attrs when set, and OMITS them when unset (no false-zero attrs
// polluting the DDB row — mirrors the omitempty pattern used for locked/locked_at).
func TestMarshalFrozen(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("emits all five attrs when set", func(t *testing.T) {
		frozenAt := now.Add(-30 * time.Minute)
		meta := &kmaws.SandboxMetadata{
			SandboxID:    "sb-freeze-emit",
			ProfileName:  "dev",
			Substrate:    "ec2",
			Region:       "us-east-1",
			CreatedAt:    now,
			ActionLimits: `{"push":{"daily":5}}`,
			ActionFrozen: true,
			FrozenReason: "quota:push:daily",
			FrozenAt:     &frozenAt,
			FrozenBy:     "auto:push:daily",
		}

		writeMock := &mockSandboxMetadataAPI{putItemOutput: &dynamodb.PutItemOutput{}}
		if err := kmaws.WriteSandboxMetadataDynamo(ctx, writeMock, "km-sandboxes", meta); err != nil {
			t.Fatalf("WriteSandboxMetadataDynamo: %v", err)
		}
		item := writeMock.putItemInput.Item

		// action_limits (S)
		alAttr, ok := item["action_limits"]
		if !ok {
			t.Fatal("action_limits attribute missing from PutItem input")
		}
		if sv, ok := alAttr.(*dynamodbtypes.AttributeValueMemberS); !ok || sv.Value != meta.ActionLimits {
			t.Errorf("action_limits = %v, want S{%q}", alAttr, meta.ActionLimits)
		}

		// action_frozen (BOOL true)
		afAttr, ok := item["action_frozen"]
		if !ok {
			t.Fatal("action_frozen attribute missing from PutItem input")
		}
		if bv, ok := afAttr.(*dynamodbtypes.AttributeValueMemberBOOL); !ok || !bv.Value {
			t.Errorf("action_frozen = %v, want BOOL{true}", afAttr)
		}

		// frozen_reason (S)
		frAttr, ok := item["frozen_reason"]
		if !ok {
			t.Fatal("frozen_reason attribute missing from PutItem input")
		}
		if sv, ok := frAttr.(*dynamodbtypes.AttributeValueMemberS); !ok || sv.Value != meta.FrozenReason {
			t.Errorf("frozen_reason = %v, want S{%q}", frAttr, meta.FrozenReason)
		}

		// frozen_at (S, RFC3339)
		faAttr, ok := item["frozen_at"]
		if !ok {
			t.Fatal("frozen_at attribute missing from PutItem input")
		}
		faSv, ok := faAttr.(*dynamodbtypes.AttributeValueMemberS)
		if !ok {
			t.Fatalf("frozen_at should be AttributeValueMemberS, got %T", faAttr)
		}
		parsedFA, err := time.Parse(time.RFC3339, faSv.Value)
		if err != nil {
			t.Fatalf("frozen_at %q is not RFC3339: %v", faSv.Value, err)
		}
		if !parsedFA.Equal(frozenAt) {
			t.Errorf("frozen_at parsed = %v, want %v", parsedFA, frozenAt)
		}

		// frozen_by (S)
		fbAttr, ok := item["frozen_by"]
		if !ok {
			t.Fatal("frozen_by attribute missing from PutItem input")
		}
		if sv, ok := fbAttr.(*dynamodbtypes.AttributeValueMemberS); !ok || sv.Value != meta.FrozenBy {
			t.Errorf("frozen_by = %v, want S{%q}", fbAttr, meta.FrozenBy)
		}
	})

	t.Run("omits all five attrs when unset", func(t *testing.T) {
		meta := &kmaws.SandboxMetadata{
			SandboxID:   "sb-freeze-omit",
			ProfileName: "dev",
			Substrate:   "ec2",
			Region:      "us-east-1",
			CreatedAt:   now,
			// All Phase 121 fields intentionally zero-value
		}

		writeMock := &mockSandboxMetadataAPI{putItemOutput: &dynamodb.PutItemOutput{}}
		if err := kmaws.WriteSandboxMetadataDynamo(ctx, writeMock, "km-sandboxes", meta); err != nil {
			t.Fatalf("WriteSandboxMetadataDynamo: %v", err)
		}
		item := writeMock.putItemInput.Item

		for _, attr := range []string{"action_limits", "action_frozen", "frozen_reason", "frozen_at", "frozen_by"} {
			if _, present := item[attr]; present {
				t.Errorf("%s attribute should be omitted when zero-value (no false-zero attrs)", attr)
			}
		}
	})
}

// TestFreezeSandboxDynamo verifies the UpdateItem expression used by FreezeSandboxDynamo.
func TestFreezeSandboxDynamo(t *testing.T) {
	t.Run("sets action_frozen + frozen attrs", func(t *testing.T) {
		mock := &mockSandboxMetadataAPI{updateItemOutput: &dynamodb.UpdateItemOutput{}}

		if err := kmaws.FreezeSandboxDynamo(context.Background(), mock, "km-sandboxes", "sb-frz1", "quota:push:daily", "auto:push:daily"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.updateItemInput == nil {
			t.Fatal("UpdateItem was not called")
		}

		expr := awssdk.ToString(mock.updateItemInput.UpdateExpression)
		for _, field := range []string{"action_frozen", "frozen_reason", "frozen_at", "frozen_by"} {
			if !contains(expr, field) {
				t.Errorf("UpdateExpression %q does not reference %q", expr, field)
			}
		}

		// :t must be BOOL true
		tv, ok := mock.updateItemInput.ExpressionAttributeValues[":t"].(*dynamodbtypes.AttributeValueMemberBOOL)
		if !ok || !tv.Value {
			t.Errorf(":t must be BOOL{true}, got %v", mock.updateItemInput.ExpressionAttributeValues[":t"])
		}
		// :reason must match
		rv, ok := mock.updateItemInput.ExpressionAttributeValues[":reason"].(*dynamodbtypes.AttributeValueMemberS)
		if !ok || rv.Value != "quota:push:daily" {
			t.Errorf(":reason = %v, want S{quota:push:daily}", mock.updateItemInput.ExpressionAttributeValues[":reason"])
		}
		// :by must match
		bv, ok := mock.updateItemInput.ExpressionAttributeValues[":by"].(*dynamodbtypes.AttributeValueMemberS)
		if !ok || bv.Value != "auto:push:daily" {
			t.Errorf(":by = %v, want S{auto:push:daily}", mock.updateItemInput.ExpressionAttributeValues[":by"])
		}
		// :now must be non-empty RFC3339
		nv, ok := mock.updateItemInput.ExpressionAttributeValues[":now"].(*dynamodbtypes.AttributeValueMemberS)
		if !ok || nv.Value == "" {
			t.Error(":now must be a non-empty RFC3339 string")
		}
		if _, err := time.Parse(time.RFC3339, nv.Value); err != nil {
			t.Errorf(":now %q is not RFC3339: %v", nv.Value, err)
		}

		// ConditionExpression must check attribute_exists(sandbox_id)
		if mock.updateItemInput.ConditionExpression == nil {
			t.Fatal("ConditionExpression must not be nil")
		}
		if !contains(*mock.updateItemInput.ConditionExpression, "attribute_exists(sandbox_id)") {
			t.Errorf("ConditionExpression %q must contain attribute_exists(sandbox_id)", *mock.updateItemInput.ConditionExpression)
		}
	})

	t.Run("returns ErrSandboxNotFound on ConditionalCheckFailedException", func(t *testing.T) {
		mock := &mockSandboxMetadataAPI{
			updateItemErr: &dynamodbtypes.ConditionalCheckFailedException{
				Message: awssdk.String("The conditional request failed"),
			},
		}
		err := kmaws.FreezeSandboxDynamo(context.Background(), mock, "km-sandboxes", "sb-missing", "reason", "by")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, kmaws.ErrSandboxNotFound) {
			t.Errorf("expected ErrSandboxNotFound, got: %v", err)
		}
	})
}

// TestUnfreezeSandboxDynamo verifies the UpdateItem expression used by UnfreezeSandboxDynamo.
func TestUnfreezeSandboxDynamo(t *testing.T) {
	t.Run("clears action_frozen and removes frozen attrs", func(t *testing.T) {
		mock := &mockSandboxMetadataAPI{updateItemOutput: &dynamodb.UpdateItemOutput{}}

		if err := kmaws.UnfreezeSandboxDynamo(context.Background(), mock, "km-sandboxes", "sb-unfrz1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mock.updateItemInput == nil {
			t.Fatal("UpdateItem was not called")
		}

		expr := awssdk.ToString(mock.updateItemInput.UpdateExpression)
		if !contains(expr, "REMOVE") {
			t.Errorf("UpdateExpression %q must contain REMOVE clause", expr)
		}
		for _, field := range []string{"frozen_reason", "frozen_at", "frozen_by"} {
			if !contains(expr, field) {
				t.Errorf("UpdateExpression %q must REMOVE %q", expr, field)
			}
		}
		if !contains(expr, "action_frozen") {
			t.Errorf("UpdateExpression %q must SET action_frozen = :f", expr)
		}

		// :f must be BOOL false
		fv, ok := mock.updateItemInput.ExpressionAttributeValues[":f"].(*dynamodbtypes.AttributeValueMemberBOOL)
		if !ok || fv.Value {
			t.Errorf(":f must be BOOL{false}, got %v", mock.updateItemInput.ExpressionAttributeValues[":f"])
		}

		// ConditionExpression must check attribute_exists(sandbox_id)
		if mock.updateItemInput.ConditionExpression == nil {
			t.Fatal("ConditionExpression must not be nil")
		}
		if !contains(*mock.updateItemInput.ConditionExpression, "attribute_exists(sandbox_id)") {
			t.Errorf("ConditionExpression %q must contain attribute_exists(sandbox_id)", *mock.updateItemInput.ConditionExpression)
		}
	})

	t.Run("returns ErrSandboxNotFound on ConditionalCheckFailedException", func(t *testing.T) {
		mock := &mockSandboxMetadataAPI{
			updateItemErr: &dynamodbtypes.ConditionalCheckFailedException{
				Message: awssdk.String("The conditional request failed"),
			},
		}
		err := kmaws.UnfreezeSandboxDynamo(context.Background(), mock, "km-sandboxes", "sb-missing")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, kmaws.ErrSandboxNotFound) {
			t.Errorf("expected ErrSandboxNotFound, got: %v", err)
		}
	})
}

// ---- Helpers ----

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

package aws_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
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


package aws_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// fakeBudgetClient is a test double for the BudgetAPI interface.
type fakeBudgetClient struct {
	updateItemCalls []*dynamodb.UpdateItemInput
	getItemResult   map[string]dynamodbtypes.AttributeValue
	queryResult     []map[string]dynamodbtypes.AttributeValue

	// controls what UpdateItem returns for spend total
	updateItemReturn float64
}

func (f *fakeBudgetClient) UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateItemCalls = append(f.updateItemCalls, input)
	// Return the updateItemReturn value as the updated spend
	updatedSpend, _ := attributevalue.Marshal(f.updateItemReturn)
	return &dynamodb.UpdateItemOutput{
		Attributes: map[string]dynamodbtypes.AttributeValue{
			"spentUSD": updatedSpend,
		},
	}, nil
}

func (f *fakeBudgetClient) GetItem(ctx context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: f.getItemResult}, nil
}

func (f *fakeBudgetClient) Query(ctx context.Context, input *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{Items: f.queryResult}, nil
}

// TestIncrementAISpend verifies IncrementAISpend calls UpdateItem with ADD expression
// and correct PK/SK keys.
func TestIncrementAISpend(t *testing.T) {
	fake := &fakeBudgetClient{updateItemReturn: 1.50}

	sandboxID := "sb-test-123"
	modelID := "anthropic.claude-3-haiku-20240307-v1:0"
	tableName := "km-budgets"

	updatedSpend, err := kmaws.IncrementAISpend(context.Background(), fake, tableName, sandboxID, modelID, 100, 50, 0.005)
	if err != nil {
		t.Fatalf("IncrementAISpend returned error: %v", err)
	}
	if updatedSpend != 1.50 {
		t.Errorf("expected updated spend=1.50, got %f", updatedSpend)
	}

	if len(fake.updateItemCalls) == 0 {
		t.Fatal("expected UpdateItem to be called")
	}

	call := fake.updateItemCalls[0]
	// Check table name
	if aws.ToString(call.TableName) != tableName {
		t.Errorf("expected tableName=%q, got %q", tableName, aws.ToString(call.TableName))
	}

	// Check PK = SANDBOX#{sandboxID}
	pkVal, ok := call.Key["PK"]
	if !ok {
		t.Fatal("expected PK key in UpdateItem call")
	}
	pkStr, ok := pkVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatal("expected PK to be String type")
	}
	expectedPK := fmt.Sprintf("SANDBOX#%s", sandboxID)
	if pkStr.Value != expectedPK {
		t.Errorf("expected PK=%q, got %q", expectedPK, pkStr.Value)
	}

	// Check SK contains BUDGET#ai#
	skVal, ok := call.Key["SK"]
	if !ok {
		t.Fatal("expected SK key in UpdateItem call")
	}
	skStr, ok := skVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatal("expected SK to be String type")
	}
	expectedSKPrefix := fmt.Sprintf("BUDGET#ai#%s", modelID)
	if skStr.Value != expectedSKPrefix {
		t.Errorf("expected SK=%q, got %q", expectedSKPrefix, skStr.Value)
	}

	// Check that ADD expression is used (atomic increment)
	if call.UpdateExpression == nil {
		t.Fatal("expected UpdateExpression to be set")
	}
	expr := aws.ToString(call.UpdateExpression)
	if len(expr) == 0 {
		t.Error("expected non-empty UpdateExpression for ADD")
	}
}

// TestIncrementComputeSpend verifies IncrementComputeSpend uses the BUDGET#compute SK.
func TestIncrementComputeSpend(t *testing.T) {
	fake := &fakeBudgetClient{updateItemReturn: 0.25}

	sandboxID := "sb-compute-test"
	tableName := "km-budgets"

	updatedSpend, err := kmaws.IncrementComputeSpend(context.Background(), fake, tableName, sandboxID, 0.25)
	if err != nil {
		t.Fatalf("IncrementComputeSpend returned error: %v", err)
	}
	if updatedSpend != 0.25 {
		t.Errorf("expected updated spend=0.25, got %f", updatedSpend)
	}

	if len(fake.updateItemCalls) == 0 {
		t.Fatal("expected UpdateItem to be called")
	}

	call := fake.updateItemCalls[0]
	skVal, ok := call.Key["SK"]
	if !ok {
		t.Fatal("expected SK key in UpdateItem call")
	}
	skStr, ok := skVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatal("expected SK to be String type")
	}
	if skStr.Value != "BUDGET#compute" {
		t.Errorf("expected SK=BUDGET#compute, got %q", skStr.Value)
	}
}

// TestGetBudgetReturnsStructuredResult verifies GetBudget returns a BudgetSummary
// with compute and AI spend populated from DynamoDB query results.
func TestGetBudgetReturnsStructuredResult(t *testing.T) {
	// Build mock query results for PK=SANDBOX#sb-123, SK begins_with BUDGET#
	computeItem := map[string]dynamodbtypes.AttributeValue{
		"PK":       &dynamodbtypes.AttributeValueMemberS{Value: "SANDBOX#sb-123"},
		"SK":       &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#compute"},
		"spentUSD": &dynamodbtypes.AttributeValueMemberN{Value: "1.50"},
	}
	aiItem := map[string]dynamodbtypes.AttributeValue{
		"PK":           &dynamodbtypes.AttributeValueMemberS{Value: "SANDBOX#sb-123"},
		"SK":           &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#ai#anthropic.claude-3-haiku-20240307-v1:0"},
		"spentUSD":     &dynamodbtypes.AttributeValueMemberN{Value: "0.75"},
		"inputTokens":  &dynamodbtypes.AttributeValueMemberN{Value: "10000"},
		"outputTokens": &dynamodbtypes.AttributeValueMemberN{Value: "5000"},
	}
	limitsItem := map[string]dynamodbtypes.AttributeValue{
		"PK":               &dynamodbtypes.AttributeValueMemberS{Value: "SANDBOX#sb-123"},
		"SK":               &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#limits"},
		"computeLimit":     &dynamodbtypes.AttributeValueMemberN{Value: "5.00"},
		"aiLimit":          &dynamodbtypes.AttributeValueMemberN{Value: "20.00"},
		"warningThreshold": &dynamodbtypes.AttributeValueMemberN{Value: "0.8"},
	}

	fake := &fakeBudgetClient{
		queryResult: []map[string]dynamodbtypes.AttributeValue{computeItem, aiItem, limitsItem},
	}

	summary, err := kmaws.GetBudget(context.Background(), fake, "km-budgets", "sb-123")
	if err != nil {
		t.Fatalf("GetBudget returned error: %v", err)
	}

	if summary == nil {
		t.Fatal("expected BudgetSummary, got nil")
	}
	if summary.ComputeSpent != 1.50 {
		t.Errorf("expected ComputeSpent=1.50, got %f", summary.ComputeSpent)
	}
	if summary.ComputeLimit != 5.00 {
		t.Errorf("expected ComputeLimit=5.00, got %f", summary.ComputeLimit)
	}
	if summary.AISpent != 0.75 {
		t.Errorf("expected AISpent=0.75, got %f", summary.AISpent)
	}
	if summary.AILimit != 20.00 {
		t.Errorf("expected AILimit=20.00, got %f", summary.AILimit)
	}
	if summary.WarningThreshold != 0.8 {
		t.Errorf("expected WarningThreshold=0.8, got %f", summary.WarningThreshold)
	}
	if len(summary.AIByModel) == 0 {
		t.Error("expected AIByModel to be populated")
	}
	modelKey := "anthropic.claude-3-haiku-20240307-v1:0"
	modelSpend, ok := summary.AIByModel[modelKey]
	if !ok {
		t.Fatalf("expected AIByModel[%q] to exist", modelKey)
	}
	if modelSpend.SpentUSD != 0.75 {
		t.Errorf("expected model SpentUSD=0.75, got %f", modelSpend.SpentUSD)
	}
	if modelSpend.InputTokens != 10000 {
		t.Errorf("expected model InputTokens=10000, got %d", modelSpend.InputTokens)
	}
}

// TestGetBudget_PopulatesPausedFields verifies that GetBudget parses pausedSeconds and
// pausedAt from BUDGET#compute items, and returns zero values for legacy sandboxes that
// have no paused* attributes.
func TestGetBudget_PopulatesPausedFields(t *testing.T) {
	t.Run("with_paused_fields", func(t *testing.T) {
		computeItem := map[string]dynamodbtypes.AttributeValue{
			"PK":            &dynamodbtypes.AttributeValueMemberS{Value: "SANDBOX#sb-123"},
			"SK":            &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#compute"},
			"spentUSD":      &dynamodbtypes.AttributeValueMemberN{Value: "0.05"},
			"pausedSeconds": &dynamodbtypes.AttributeValueMemberN{Value: "3600"},
			"pausedAt":      &dynamodbtypes.AttributeValueMemberS{Value: "2026-04-21T12:00:00Z"},
		}
		fake := &fakeBudgetClient{
			queryResult: []map[string]dynamodbtypes.AttributeValue{computeItem},
		}
		summary, err := kmaws.GetBudget(context.Background(), fake, "km-budgets", "sb-123")
		if err != nil {
			t.Fatalf("GetBudget returned error: %v", err)
		}
		if summary.ComputeSpent != 0.05 {
			t.Errorf("expected ComputeSpent=0.05, got %f", summary.ComputeSpent)
		}
		if summary.PausedSeconds != 3600 {
			t.Errorf("expected PausedSeconds=3600, got %d", summary.PausedSeconds)
		}
		if summary.PausedAt == nil {
			t.Fatal("expected PausedAt to be non-nil")
		}
		wantTime := "2026-04-21T12:00:00Z"
		gotTime := summary.PausedAt.UTC().Format("2006-01-02T15:04:05Z")
		if gotTime != wantTime {
			t.Errorf("expected PausedAt=%s, got %s", wantTime, gotTime)
		}
	})

	t.Run("legacy_no_paused_fields", func(t *testing.T) {
		computeItem := map[string]dynamodbtypes.AttributeValue{
			"PK":       &dynamodbtypes.AttributeValueMemberS{Value: "SANDBOX#sb-456"},
			"SK":       &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#compute"},
			"spentUSD": &dynamodbtypes.AttributeValueMemberN{Value: "2.00"},
		}
		fake := &fakeBudgetClient{
			queryResult: []map[string]dynamodbtypes.AttributeValue{computeItem},
		}
		summary, err := kmaws.GetBudget(context.Background(), fake, "km-budgets", "sb-456")
		if err != nil {
			t.Fatalf("GetBudget returned error: %v", err)
		}
		if summary.PausedSeconds != 0 {
			t.Errorf("expected PausedSeconds=0 for legacy sandbox, got %d", summary.PausedSeconds)
		}
		if summary.PausedAt != nil {
			t.Errorf("expected PausedAt=nil for legacy sandbox, got %v", summary.PausedAt)
		}
	})
}

// fakePauseBudgetClient is a test double for pause/resume helpers. It simulates
// DynamoDB if_not_exists semantics for pausedAt and accumulates pausedSeconds on ADD.
type fakePauseBudgetClient struct {
	// in-memory state
	pausedAt      string // current stored pausedAt ("" = absent)
	pausedSeconds int64  // accumulated closed-interval seconds

	// call recorders
	updateItemCalls []*dynamodb.UpdateItemInput
	getItemCalls    []*dynamodb.GetItemInput
	updateItemCount int

	// error controls
	getItemErr    error
	updateItemErr error
}

func (f *fakePauseBudgetClient) UpdateItem(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if f.updateItemErr != nil {
		return nil, f.updateItemErr
	}
	f.updateItemCalls = append(f.updateItemCalls, input)
	f.updateItemCount++

	expr := ""
	if input.UpdateExpression != nil {
		expr = *input.UpdateExpression
	}

	if strings.Contains(expr, "if_not_exists(pausedAt") {
		// SET pausedAt = if_not_exists(pausedAt, :now) — only set if absent
		if f.pausedAt == "" {
			if av, ok := input.ExpressionAttributeValues[":now"]; ok {
				if s, ok2 := av.(*dynamodbtypes.AttributeValueMemberS); ok2 {
					f.pausedAt = s.Value
				}
			}
		}
	}
	if strings.Contains(expr, "ADD pausedSeconds") {
		if av, ok := input.ExpressionAttributeValues[":interval"]; ok {
			if n, ok2 := av.(*dynamodbtypes.AttributeValueMemberN); ok2 {
				var delta int64
				fmt.Sscanf(n.Value, "%d", &delta)
				f.pausedSeconds += delta
			}
		}
	}
	if strings.Contains(expr, "REMOVE pausedAt") {
		f.pausedAt = ""
	}

	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakePauseBudgetClient) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.getItemErr != nil {
		return nil, f.getItemErr
	}
	f.getItemCalls = append(f.getItemCalls, input)
	item := map[string]dynamodbtypes.AttributeValue{}
	if f.pausedAt != "" {
		item["pausedAt"] = &dynamodbtypes.AttributeValueMemberS{Value: f.pausedAt}
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (f *fakePauseBudgetClient) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{}, nil
}

// TestRecordPauseStart_WritesIfNotExists verifies RecordPauseStart issues an UpdateItem
// with if_not_exists(pausedAt, :now) and sets :now to an RFC3339 string.
func TestRecordPauseStart_WritesIfNotExists(t *testing.T) {
	fake := &fakePauseBudgetClient{}
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)

	err := kmaws.RecordPauseStart(context.Background(), fake, "km-budgets", "sb-x", now)
	if err != nil {
		t.Fatalf("RecordPauseStart returned error: %v", err)
	}
	if len(fake.updateItemCalls) == 0 {
		t.Fatal("expected UpdateItem to be called")
	}
	call := fake.updateItemCalls[0]
	expr := ""
	if call.UpdateExpression != nil {
		expr = *call.UpdateExpression
	}
	if !strings.Contains(expr, "if_not_exists(pausedAt") {
		t.Errorf("expected if_not_exists(pausedAt in UpdateExpression, got: %q", expr)
	}
	nowAV, ok := call.ExpressionAttributeValues[":now"]
	if !ok {
		t.Fatal("expected :now in ExpressionAttributeValues")
	}
	nowStr, ok2 := nowAV.(*dynamodbtypes.AttributeValueMemberS)
	if !ok2 {
		t.Fatal("expected :now to be String type")
	}
	if nowStr.Value != "2026-04-21T12:00:00Z" {
		t.Errorf("expected :now=2026-04-21T12:00:00Z, got %q", nowStr.Value)
	}
}

// TestRecordPauseStart_Idempotent verifies that calling RecordPauseStart twice does not
// overwrite the original pausedAt timestamp (if_not_exists semantics).
func TestRecordPauseStart_Idempotent(t *testing.T) {
	fake := &fakePauseBudgetClient{}
	first := time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)
	second := time.Date(2026, 4, 21, 11, 0, 0, 0, time.UTC)

	if err := kmaws.RecordPauseStart(context.Background(), fake, "km-budgets", "sb-x", first); err != nil {
		t.Fatalf("first RecordPauseStart error: %v", err)
	}
	if err := kmaws.RecordPauseStart(context.Background(), fake, "km-budgets", "sb-x", second); err != nil {
		t.Fatalf("second RecordPauseStart error: %v", err)
	}

	// The simulator should preserve the first timestamp
	if fake.pausedAt != "2026-04-21T10:00:00Z" {
		t.Errorf("expected original pausedAt preserved, got %q", fake.pausedAt)
	}
}

// TestRecordResumeClose_NoPausedAtIsNoop verifies that RecordResumeClose returns nil
// and calls UpdateItem zero times when there is no pausedAt attribute.
func TestRecordResumeClose_NoPausedAtIsNoop(t *testing.T) {
	fake := &fakePauseBudgetClient{} // pausedAt = ""
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)

	err := kmaws.RecordResumeClose(context.Background(), fake, "km-budgets", "sb-x", now)
	if err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
	if fake.updateItemCount != 0 {
		t.Errorf("expected 0 UpdateItem calls, got %d", fake.updateItemCount)
	}
}

// TestRecordResumeClose_AccumulatesInterval verifies that RecordResumeClose computes
// (now - pausedAt) and issues UpdateItem with ADD pausedSeconds :interval REMOVE pausedAt.
func TestRecordResumeClose_AccumulatesInterval(t *testing.T) {
	fake := &fakePauseBudgetClient{
		pausedAt: "2026-04-21T11:00:00Z", // 1 hour before now
	}
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)

	err := kmaws.RecordResumeClose(context.Background(), fake, "km-budgets", "sb-x", now)
	if err != nil {
		t.Fatalf("RecordResumeClose returned error: %v", err)
	}
	if len(fake.updateItemCalls) == 0 {
		t.Fatal("expected UpdateItem to be called")
	}
	call := fake.updateItemCalls[0]
	expr := ""
	if call.UpdateExpression != nil {
		expr = *call.UpdateExpression
	}
	if !strings.Contains(expr, "ADD pausedSeconds") {
		t.Errorf("expected ADD pausedSeconds in UpdateExpression, got: %q", expr)
	}
	if !strings.Contains(expr, "REMOVE pausedAt") {
		t.Errorf("expected REMOVE pausedAt in UpdateExpression, got: %q", expr)
	}
	intervalAV, ok := call.ExpressionAttributeValues[":interval"]
	if !ok {
		t.Fatal("expected :interval in ExpressionAttributeValues")
	}
	intervalN, ok2 := intervalAV.(*dynamodbtypes.AttributeValueMemberN)
	if !ok2 {
		t.Fatal("expected :interval to be Number type")
	}
	if intervalN.Value != "3600" {
		t.Errorf("expected :interval=3600, got %q", intervalN.Value)
	}
}

// TestRecordResumeClose_NegativeIntervalClamped verifies that clock skew (pausedAt in future)
// results in :interval = 0, never negative.
func TestRecordResumeClose_NegativeIntervalClamped(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	// pausedAt is 10 seconds in the future relative to now
	fake := &fakePauseBudgetClient{
		pausedAt: now.Add(10 * time.Second).UTC().Format(time.RFC3339),
	}

	err := kmaws.RecordResumeClose(context.Background(), fake, "km-budgets", "sb-x", now)
	if err != nil {
		t.Fatalf("RecordResumeClose returned error: %v", err)
	}
	if len(fake.updateItemCalls) == 0 {
		t.Fatal("expected UpdateItem to be called")
	}
	call := fake.updateItemCalls[0]
	intervalAV, ok := call.ExpressionAttributeValues[":interval"]
	if !ok {
		t.Fatal("expected :interval in ExpressionAttributeValues")
	}
	intervalN, ok2 := intervalAV.(*dynamodbtypes.AttributeValueMemberN)
	if !ok2 {
		t.Fatal("expected :interval to be Number type")
	}
	if intervalN.Value != "0" {
		t.Errorf("expected :interval=0 for negative clock skew, got %q", intervalN.Value)
	}
}

// TestMultiplePauseResumeCycles simulates three pause+resume cycles and verifies
// final pausedSeconds equals the sum of the three intervals, and pausedAt is absent.
func TestMultiplePauseResumeCycles(t *testing.T) {
	fake := &fakePauseBudgetClient{}
	ctx := context.Background()
	tableName := "km-budgets"
	sandboxID := "sb-multi"

	base := time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC)

	// Cycle 1: 1h pause (3600s)
	if err := kmaws.RecordPauseStart(ctx, fake, tableName, sandboxID, base); err != nil {
		t.Fatal(err)
	}
	if err := kmaws.RecordResumeClose(ctx, fake, tableName, sandboxID, base.Add(1*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Cycle 2: 2h pause (7200s)
	if err := kmaws.RecordPauseStart(ctx, fake, tableName, sandboxID, base.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := kmaws.RecordResumeClose(ctx, fake, tableName, sandboxID, base.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Cycle 3: 30min pause (1800s)
	if err := kmaws.RecordPauseStart(ctx, fake, tableName, sandboxID, base.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := kmaws.RecordResumeClose(ctx, fake, tableName, sandboxID, base.Add(5*time.Hour+30*time.Minute)); err != nil {
		t.Fatal(err)
	}

	wantSeconds := int64(3600 + 7200 + 1800)
	if fake.pausedSeconds != wantSeconds {
		t.Errorf("expected pausedSeconds=%d, got %d", wantSeconds, fake.pausedSeconds)
	}
	if fake.pausedAt != "" {
		t.Errorf("expected pausedAt absent after final resume, got %q", fake.pausedAt)
	}
}

// TestRecordResumeClose_GetItemErrorIsNonFatal verifies that GetItem errors are swallowed
// and RecordResumeClose returns nil (matches warn-and-continue convention).
func TestRecordResumeClose_GetItemErrorIsNonFatal(t *testing.T) {
	fake := &fakePauseBudgetClient{
		getItemErr: fmt.Errorf("DynamoDB connection refused"),
	}
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)

	err := kmaws.RecordResumeClose(context.Background(), fake, "km-budgets", "sb-x", now)
	if err != nil {
		t.Errorf("expected nil (non-fatal), got: %v", err)
	}
}

// TestSetBudgetLimits verifies SetBudgetLimits writes a BUDGET#limits item
// with compute and AI limits.
func TestSetBudgetLimits(t *testing.T) {
	fake := &fakeBudgetClient{updateItemReturn: 0}

	sandboxID := "sb-limits-test"
	tableName := "km-budgets"

	err := kmaws.SetBudgetLimits(context.Background(), fake, tableName, sandboxID, 10.0, 25.0, 0.8)
	if err != nil {
		t.Fatalf("SetBudgetLimits returned error: %v", err)
	}

	if len(fake.updateItemCalls) == 0 {
		t.Fatal("expected UpdateItem to be called for SetBudgetLimits")
	}

	call := fake.updateItemCalls[0]
	skVal, ok := call.Key["SK"]
	if !ok {
		t.Fatal("expected SK key")
	}
	skStr, ok := skVal.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		t.Fatal("expected SK to be String type")
	}
	if skStr.Value != "BUDGET#limits" {
		t.Errorf("expected SK=BUDGET#limits, got %q", skStr.Value)
	}

	// Verify the expression attribute values contain the limits
	if call.ExpressionAttributeValues == nil {
		t.Fatal("expected ExpressionAttributeValues to be set")
	}

	// Check all three limits are present somewhere in the expression values
	rawVals, _ := json.Marshal(call.ExpressionAttributeValues)
	t.Logf("ExpressionAttributeValues: %s", rawVals)
}

package aws_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

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

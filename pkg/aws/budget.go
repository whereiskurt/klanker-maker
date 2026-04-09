// Package aws — budget.go
// BudgetAPI interface and helper functions for DynamoDB-backed budget tracking.
// Follows the narrow-interface pattern established in ses.go and artifacts.go.
//
// DynamoDB key design:
//
//	PK = SANDBOX#{sandboxID}   (partition key, string)
//	SK = BUDGET#compute        (compute spend row)
//	SK = BUDGET#ai#{modelID}   (per-model AI spend row)
//	SK = BUDGET#limits         (budget limits configuration row)
package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// BudgetAPI is the minimal DynamoDB interface required by budget tracking functions.
// Implemented by *dynamodb.Client.
type BudgetAPI interface {
	UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// BudgetSummary is the structured result of a GetBudget query for a sandbox.
type BudgetSummary struct {
	ComputeSpent     float64
	ComputeLimit     float64
	AISpent          float64 // total across all models
	AILimit          float64
	WarningThreshold float64
	AIByModel        map[string]ModelSpend // keyed by model ID
	LastAIActivity   *time.Time            // most recent AI spend update across all models
}

// ModelSpend holds per-model AI token and cost spend.
type ModelSpend struct {
	SpentUSD     float64
	InputTokens  int
	OutputTokens int
}

// sandboxPK returns the DynamoDB partition key for a given sandboxID.
func sandboxPK(sandboxID string) string {
	return fmt.Sprintf("SANDBOX#%s", sandboxID)
}

// IncrementAISpend atomically increments the AI spend for a sandbox+model in DynamoDB.
// Uses the ADD expression for atomic updates without read-modify-write races.
// Returns the updated total spend for this model after the increment.
func IncrementAISpend(ctx context.Context, client BudgetAPI, tableName, sandboxID, modelID string, inputTokens, outputTokens int, costUSD float64) (float64, error) {
	pk := sandboxPK(sandboxID)
	sk := fmt.Sprintf("BUDGET#ai#%s", modelID)

	inputTokensAV, err := attributevalue.Marshal(inputTokens)
	if err != nil {
		return 0, fmt.Errorf("marshal inputTokens: %w", err)
	}
	outputTokensAV, err := attributevalue.Marshal(outputTokens)
	if err != nil {
		return 0, fmt.Errorf("marshal outputTokens: %w", err)
	}
	costAV, err := attributevalue.Marshal(costUSD)
	if err != nil {
		return 0, fmt.Errorf("marshal costUSD: %w", err)
	}

	out, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
			"SK": &dynamodbtypes.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression:    awssdk.String("ADD spentUSD :cost, inputTokens :inputTokens, outputTokens :outputTokens SET last_updated = :now"),
		ReturnValues:        dynamodbtypes.ReturnValueAllNew,
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":cost":         costAV,
			":inputTokens":  inputTokensAV,
			":outputTokens": outputTokensAV,
			":now":          &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
	})
	if err != nil {
		return 0, fmt.Errorf("increment AI spend for sandbox %s model %s: %w", sandboxID, modelID, err)
	}

	// Extract updated spentUSD from response attributes
	if out.Attributes == nil {
		return costUSD, nil
	}
	var updatedSpend float64
	if spentAV, ok := out.Attributes["spentUSD"]; ok {
		if err := attributevalue.Unmarshal(spentAV, &updatedSpend); err != nil {
			return costUSD, nil
		}
	}
	return updatedSpend, nil
}

// IncrementComputeSpend atomically increments compute (EC2/Fargate) spend for a sandbox.
// Uses the ADD expression for atomic updates. Returns the updated total compute spend.
func IncrementComputeSpend(ctx context.Context, client BudgetAPI, tableName, sandboxID string, costUSD float64) (float64, error) {
	pk := sandboxPK(sandboxID)
	sk := "BUDGET#compute"

	costAV, err := attributevalue.Marshal(costUSD)
	if err != nil {
		return 0, fmt.Errorf("marshal costUSD: %w", err)
	}

	out, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
			"SK": &dynamodbtypes.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression: awssdk.String("ADD spentUSD :cost"),
		ReturnValues:     dynamodbtypes.ReturnValueAllNew,
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":cost": costAV,
		},
	})
	if err != nil {
		return 0, fmt.Errorf("increment compute spend for sandbox %s: %w", sandboxID, err)
	}

	if out.Attributes == nil {
		return costUSD, nil
	}
	var updatedSpend float64
	if spentAV, ok := out.Attributes["spentUSD"]; ok {
		if err := attributevalue.Unmarshal(spentAV, &updatedSpend); err != nil {
			return costUSD, nil
		}
	}
	return updatedSpend, nil
}

// GetBudget queries all BUDGET# items for a sandbox and returns a structured BudgetSummary.
// It reads compute spend, per-model AI spend, and limits from separate SK rows.
func GetBudget(ctx context.Context, client BudgetAPI, tableName, sandboxID string) (*BudgetSummary, error) {
	pk := sandboxPK(sandboxID)

	pkAV, err := attributevalue.Marshal(pk)
	if err != nil {
		return nil, fmt.Errorf("marshal PK: %w", err)
	}
	prefixAV, err := attributevalue.Marshal("BUDGET#")
	if err != nil {
		return nil, fmt.Errorf("marshal SK prefix: %w", err)
	}

	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              awssdk.String(tableName),
		KeyConditionExpression: awssdk.String("PK = :pk AND begins_with(SK, :skPrefix)"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":pk":       pkAV,
			":skPrefix": prefixAV,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("query budget for sandbox %s: %w", sandboxID, err)
	}

	summary := &BudgetSummary{
		AIByModel: make(map[string]ModelSpend),
	}

	for _, item := range out.Items {
		skAV, ok := item["SK"]
		if !ok {
			continue
		}
		var sk string
		if err := attributevalue.Unmarshal(skAV, &sk); err != nil {
			continue
		}

		switch {
		case sk == "BUDGET#compute":
			var spend float64
			if av, ok := item["spentUSD"]; ok {
				_ = attributevalue.Unmarshal(av, &spend)
			}
			summary.ComputeSpent = spend

		case sk == "BUDGET#limits":
			if av, ok := item["computeLimit"]; ok {
				_ = attributevalue.Unmarshal(av, &summary.ComputeLimit)
			}
			if av, ok := item["aiLimit"]; ok {
				_ = attributevalue.Unmarshal(av, &summary.AILimit)
			}
			if av, ok := item["warningThreshold"]; ok {
				_ = attributevalue.Unmarshal(av, &summary.WarningThreshold)
			}

		case strings.HasPrefix(sk, "BUDGET#ai#"):
			modelID := strings.TrimPrefix(sk, "BUDGET#ai#")
			ms := ModelSpend{}
			if av, ok := item["spentUSD"]; ok {
				_ = attributevalue.Unmarshal(av, &ms.SpentUSD)
			}
			if av, ok := item["inputTokens"]; ok {
				_ = attributevalue.Unmarshal(av, &ms.InputTokens)
			}
			if av, ok := item["outputTokens"]; ok {
				_ = attributevalue.Unmarshal(av, &ms.OutputTokens)
			}
			summary.AIByModel[modelID] = ms
			summary.AISpent += ms.SpentUSD
			// Track the most recent AI activity across all models.
			if av, ok := item["last_updated"]; ok {
				var ts string
				if err := attributevalue.Unmarshal(av, &ts); err == nil {
					if t, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil {
						if summary.LastAIActivity == nil || t.After(*summary.LastAIActivity) {
							summary.LastAIActivity = &t
						}
					}
				}
			}
		}
	}

	return summary, nil
}

// SetBudgetLimits writes (or overwrites) the BUDGET#limits item for a sandbox,
// storing compute limit, AI limit, and warning threshold.
func SetBudgetLimits(ctx context.Context, client BudgetAPI, tableName, sandboxID string, computeLimit, aiLimit, warningThreshold float64) error {
	pk := sandboxPK(sandboxID)

	computeLimitAV, err := attributevalue.Marshal(computeLimit)
	if err != nil {
		return fmt.Errorf("marshal computeLimit: %w", err)
	}
	aiLimitAV, err := attributevalue.Marshal(aiLimit)
	if err != nil {
		return fmt.Errorf("marshal aiLimit: %w", err)
	}
	thresholdAV, err := attributevalue.Marshal(warningThreshold)
	if err != nil {
		return fmt.Errorf("marshal warningThreshold: %w", err)
	}

	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: awssdk.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
			"SK": &dynamodbtypes.AttributeValueMemberS{Value: "BUDGET#limits"},
		},
		UpdateExpression: awssdk.String("SET computeLimit = :computeLimit, aiLimit = :aiLimit, warningThreshold = :warningThreshold"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
			":computeLimit":     computeLimitAV,
			":aiLimit":          aiLimitAV,
			":warningThreshold": thresholdAV,
		},
	})
	if err != nil {
		return fmt.Errorf("set budget limits for sandbox %s: %w", sandboxID, err)
	}
	return nil
}

package check

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ChecksDDBAPI is the narrow DynamoDB interface for check row operations.
type ChecksDDBAPI interface {
	PutItem(ctx context.Context, input *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Scan(ctx context.Context, input *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// NewChecksDDBClient constructs a DynamoDB client from an aws.Config.
func NewChecksDDBClient(awsCfg aws.Config) ChecksDDBAPI {
	return dynamodb.NewFromConfig(awsCfg)
}

// ChecksTableName returns the DDB table name for the given resource prefix.
// Pattern: {prefix}-checks.
func ChecksTableName(prefix string) string {
	return fmt.Sprintf("%s-checks", prefix)
}

// CheckRow is the DynamoDB row for a deployed check Lambda.
// Fields mirror the CONTEXT.md DDB schema: name, arn, runtime, packageType,
// imageUri, memory, timeout, schedule, env, secretPaths, sourceHash,
// triggerSummary, createdAt, updatedAt.
//
// All dynamodbav tags use the exact DDB attribute names (snake_case).
// secretPaths stores ONLY paths — never secret values.
// env stores only non-secret key names (not values).
type CheckRow struct {
	// Name is the hash key (check name, e.g. "qotd").
	Name string `dynamodbav:"name"`
	// ARN is the Lambda function ARN.
	ARN string `dynamodbav:"arn,omitempty"`
	// Runtime is "python3.13" for zip checks; empty for image checks.
	Runtime string `dynamodbav:"runtime,omitempty"`
	// PackageType is "zip" or "image".
	PackageType string `dynamodbav:"package_type,omitempty"`
	// ImageURI is the ECR image URI (set when PackageType=image).
	ImageURI string `dynamodbav:"image_uri,omitempty"`
	// Memory is the Lambda memory in MB.
	Memory int32 `dynamodbav:"memory,omitempty"`
	// Timeout is the Lambda timeout in seconds.
	Timeout int32 `dynamodbav:"timeout,omitempty"`
	// Schedule is the EventBridge Scheduler expression (e.g. "rate(1 hour)").
	Schedule string `dynamodbav:"schedule,omitempty"`
	// EnvJSON is the non-secret env key→value map, JSON-encoded.
	// Values are stored (keys are the user-supplied --env K=V pairs).
	// Secret values are NEVER stored here — only SSM paths are (in SecretPathsJSON).
	EnvJSON string `dynamodbav:"env,omitempty"`
	// SecretPathsJSON is a JSON list of SSM secret paths (no values).
	SecretPathsJSON string `dynamodbav:"secret_paths,omitempty"`
	// SourceHash is the SHA-256 of the resolved KM_CHECK_TRIGGER JSON.
	SourceHash string `dynamodbav:"source_hash,omitempty"`
	// TriggerSummary is a human-readable trigger description.
	TriggerSummary string `dynamodbav:"trigger_summary,omitempty"`
	// CreatedAt is the ISO-8601 creation timestamp.
	CreatedAt string `dynamodbav:"created_at,omitempty"`
	// UpdatedAt is the ISO-8601 last-update timestamp.
	UpdatedAt string `dynamodbav:"updated_at,omitempty"`
}

// CheckRowInput is the data needed to create or update a check row.
type CheckRowInput struct {
	Name           string
	ARN            string
	Runtime        string
	PackageType    string
	ImageURI       string
	Memory         int32
	Timeout        int32
	Schedule       string
	Env            map[string]string
	SecretPaths    []string
	SourceHash     string
	TriggerSummary string
}

// PutCheckRow writes a new row for a freshly deployed check Lambda.
// Use UpdateCheckRow for subsequent updates to avoid the lossy round-trip
// overwrite problem (project_sandboxmetadata_lossy_roundtrip).
func PutCheckRow(ctx context.Context, client ChecksDDBAPI, tableName string, in CheckRowInput) error {
	now := time.Now().UTC().Format(time.RFC3339)
	row := CheckRow{
		Name:           in.Name,
		ARN:            in.ARN,
		Runtime:        in.Runtime,
		PackageType:    in.PackageType,
		ImageURI:       in.ImageURI,
		Memory:         in.Memory,
		Timeout:        in.Timeout,
		Schedule:       in.Schedule,
		SourceHash:     in.SourceHash,
		TriggerSummary: in.TriggerSummary,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if len(in.Env) > 0 {
		b, _ := json.Marshal(in.Env)
		row.EnvJSON = string(b)
	}
	if len(in.SecretPaths) > 0 {
		b, _ := json.Marshal(in.SecretPaths)
		row.SecretPathsJSON = string(b)
	}

	item, err := attributevalue.MarshalMap(row)
	if err != nil {
		return fmt.Errorf("PutCheckRow marshal: %w", err)
	}
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("PutCheckRow PutItem %q: %w", in.Name, err)
	}
	return nil
}

// UpdateCheckRow updates an existing check row using UpdateItem (NEVER PutItem
// on existing rows — preserves any attributes we don't own; see
// project_sandboxmetadata_lossy_roundtrip).
func UpdateCheckRow(ctx context.Context, client ChecksDDBAPI, tableName string, in CheckRowInput) error {
	now := time.Now().UTC().Format(time.RFC3339)

	exprNames := map[string]string{
		"#arn":             "arn",
		"#runtime":         "runtime",
		"#package_type":    "package_type",
		"#image_uri":       "image_uri",
		"#memory":          "memory",
		"#timeout":         "timeout",
		"#schedule":        "schedule",
		"#source_hash":     "source_hash",
		"#trigger_summary": "trigger_summary",
		"#updated_at":      "updated_at",
		"#env":             "env",
		"#secret_paths":    "secret_paths",
	}

	exprValues := map[string]dynamodbtypes.AttributeValue{
		":arn":             &dynamodbtypes.AttributeValueMemberS{Value: in.ARN},
		":runtime":         &dynamodbtypes.AttributeValueMemberS{Value: in.Runtime},
		":package_type":    &dynamodbtypes.AttributeValueMemberS{Value: in.PackageType},
		":image_uri":       &dynamodbtypes.AttributeValueMemberS{Value: in.ImageURI},
		":source_hash":     &dynamodbtypes.AttributeValueMemberS{Value: in.SourceHash},
		":trigger_summary": &dynamodbtypes.AttributeValueMemberS{Value: in.TriggerSummary},
		":updated_at":      &dynamodbtypes.AttributeValueMemberS{Value: now},
	}

	// Numeric fields.
	exprValues[":memory"] = numberAttr(int64(in.Memory))
	exprValues[":timeout"] = numberAttr(int64(in.Timeout))

	// JSON-encoded env and secretPaths.
	envJSON := "{}"
	if len(in.Env) > 0 {
		b, _ := json.Marshal(in.Env)
		envJSON = string(b)
	}
	exprValues[":env"] = &dynamodbtypes.AttributeValueMemberS{Value: envJSON}

	spJSON := "[]"
	if len(in.SecretPaths) > 0 {
		b, _ := json.Marshal(in.SecretPaths)
		spJSON = string(b)
	}
	exprValues[":secret_paths"] = &dynamodbtypes.AttributeValueMemberS{Value: spJSON}

	// schedule is optional — always write it (empty string = no schedule).
	exprValues[":schedule"] = &dynamodbtypes.AttributeValueMemberS{Value: in.Schedule}

	updateExpr := `SET #arn = :arn, #runtime = :runtime, #package_type = :package_type,
		#image_uri = :image_uri, #memory = :memory, #timeout = :timeout,
		#schedule = :schedule, #source_hash = :source_hash,
		#trigger_summary = :trigger_summary, #updated_at = :updated_at,
		#env = :env, #secret_paths = :secret_paths`

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"name": &dynamodbtypes.AttributeValueMemberS{Value: in.Name},
		},
		UpdateExpression:          aws.String(updateExpr),
		ExpressionAttributeNames:  exprNames,
		ExpressionAttributeValues: exprValues,
	})
	if err != nil {
		return fmt.Errorf("UpdateCheckRow UpdateItem %q: %w", in.Name, err)
	}
	return nil
}

// GetCheckRow fetches a single check row by name. Returns nil, nil when not found.
func GetCheckRow(ctx context.Context, client ChecksDDBAPI, tableName, name string) (*CheckRow, error) {
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"name": &dynamodbtypes.AttributeValueMemberS{Value: name},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("GetCheckRow GetItem %q: %w", name, err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	var row CheckRow
	if err := attributevalue.UnmarshalMap(out.Item, &row); err != nil {
		return nil, fmt.Errorf("GetCheckRow unmarshal %q: %w", name, err)
	}
	return &row, nil
}

// ListCheckRows returns all rows in the checks table via full scan.
// For the small fleet size expected (<1000 checks), a scan is acceptable.
func ListCheckRows(ctx context.Context, client ChecksDDBAPI, tableName string) ([]CheckRow, error) {
	var rows []CheckRow
	var lastKey map[string]dynamodbtypes.AttributeValue
	for {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(tableName),
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("ListCheckRows Scan: %w", err)
		}
		for _, item := range out.Items {
			var row CheckRow
			if err := attributevalue.UnmarshalMap(item, &row); err != nil {
				return nil, fmt.Errorf("ListCheckRows unmarshal: %w", err)
			}
			rows = append(rows, row)
		}
		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return rows, nil
}

// DeleteCheckRow removes a check row by name.
func DeleteCheckRow(ctx context.Context, client ChecksDDBAPI, tableName, name string) error {
	_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(tableName),
		Key: map[string]dynamodbtypes.AttributeValue{
			"name": &dynamodbtypes.AttributeValueMemberS{Value: name},
		},
	})
	if err != nil {
		return fmt.Errorf("DeleteCheckRow DeleteItem %q: %w", name, err)
	}
	return nil
}

// numberAttr returns a DynamoDB Number attribute value for an int64.
func numberAttr(n int64) dynamodbtypes.AttributeValue {
	return &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", n)}
}

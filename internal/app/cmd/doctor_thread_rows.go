package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoScanAPI is the Scan-only DynamoDB view used by checkThreadRowsWithoutOwner.
// *dynamodb.Client satisfies it.
type DynamoScanAPI interface {
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// checkThreadRowsWithoutOwner counts thread rows that carry no sandbox_id in the
// three per-install thread tables. Before ec2spot v1.9.0 the sandbox pollers
// wrote these rows themselves, sometimes without sandbox_id; the bridges now own
// the row and their Upsert sets sandbox_id with if_not_exists, so such a row is
// claimed on its thread's next dispatch. Until then km-slack session on that
// thread answers 404. The check is read-only (Select COUNT) and advisory.
// A table that does not exist is skipped silently (an install without that bridge).
func checkThreadRowsWithoutOwner(ctx context.Context, scanner DynamoScanAPI, prefix string) CheckResult {
	const name = "Thread rows without owner"
	if scanner == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "DynamoDB scan client unavailable"}
	}

	tables := []string{prefix + "-slack-threads", prefix + "-github-threads", prefix + "-h1-threads"}
	var counts, scanErrs []string
	for _, table := range tables {
		n, err := countRowsWithoutSandboxID(ctx, scanner, table)
		if err != nil {
			var rnf *dynamodbtypes.ResourceNotFoundException
			if errors.As(err, &rnf) {
				continue
			}
			scanErrs = append(scanErrs, fmt.Sprintf("%s: %v", table, err))
			continue
		}
		if n > 0 {
			counts = append(counts, fmt.Sprintf("%s: %d", table, n))
		}
	}

	if len(counts) == 0 && len(scanErrs) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "every thread row names its sandbox"}
	}

	var parts []string
	if len(counts) > 0 {
		parts = append(parts, "rows without sandbox_id ("+strings.Join(counts, ", ")+")")
	}
	if len(scanErrs) > 0 {
		parts = append(parts, "could not scan ("+strings.Join(scanErrs, "; ")+")")
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: strings.Join(parts, "; "),
		Remediation: "rows self-heal on that thread's next dispatch (the bridge upserts sandbox_id); " +
			"nothing to do unless a thread never dispatches again — then they expire by TTL or can be deleted",
	}
}

// countRowsWithoutSandboxID runs a paginated Select COUNT scan for rows lacking sandbox_id.
func countRowsWithoutSandboxID(ctx context.Context, scanner DynamoScanAPI, table string) (int64, error) {
	var total int64
	var startKey map[string]dynamodbtypes.AttributeValue
	for {
		out, err := scanner.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(table),
			FilterExpression:  aws.String("attribute_not_exists(sandbox_id)"),
			Select:            dynamodbtypes.SelectCount,
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return 0, err
		}
		total += int64(out.Count)
		if len(out.LastEvaluatedKey) == 0 {
			return total, nil
		}
		startKey = out.LastEvaluatedKey
	}
}

package cmd

import (
	"context"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// attrStr reads a DynamoDB S attribute from an item map, returning "" when
// the key is absent or the value is not an S type.
func attrStr(item map[string]dynamodbtypes.AttributeValue, key string) string {
	v, ok := item[key]
	if !ok {
		return ""
	}
	s, ok := v.(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return ""
	}
	return s.Value
}

// ddbDeleteOp captures a single pending DeleteItem operation with its table
// name and key map.
type ddbDeleteOp struct {
	table string
	key   map[string]dynamodbtypes.AttributeValue
}

// checkOrphanedDDBRows scans the four per-sandbox DynamoDB tables
// (budgets, identities, slack-threads, sandboxes) and detects rows whose
// sandbox-id is no longer in the active sandbox set.
//
// Classification rules:
//   - budgets:       BUDGET#ai# rows are NEVER deleted (AI spend history);
//     BUDGET#compute and BUDGET#limits are queued for orphaned sandboxes.
//   - identities:    each orphaned sandbox_id row is queued.
//   - slack-threads: sandbox_id is a NON-KEY attribute; rows without it are skipped.
//   - sandboxes:     only rows with status∈{failed,nocap} are queued; all other
//     statuses (including "starting" for in-flight creates) are skipped.
//
// Nil client → CheckSkipped.
// When dryRun||!deleteDDBRows → CheckWarn with per-table hint, no mutation.
// Otherwise: execute each DeleteItem, tally deleted/failed, return OK/WARN.
func checkOrphanedDDBRows(
	ctx context.Context,
	client DDBScanDeleteAPI,
	lister SandboxLister,
	dryRun bool,
	deleteDDBRows bool,
	budgetsTable string,
	identitiesTable string,
	slackThreadsTable string,
	sandboxesTable string,
) CheckResult {
	name := "Orphaned DDB Rows"

	if client == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "DynamoDB client not available"}
	}

	// --- Step 1: build active-sandbox set ---
	if lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "sandbox lister not available"}
	}
	records, err := lister.ListSandboxes(ctx, false)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list sandboxes: %v", err)}
	}
	active := make(map[string]bool, len(records))
	for _, r := range records {
		active[r.SandboxID] = true
	}

	// --- Step 2: scan each table and collect pending deletes ---
	var pending []ddbDeleteOp
	totalScanned := 0

	// budgets: PK="SANDBOX#{id}", SK="BUDGET#*"
	// ProjectionExpression: PK, SK
	budgetOps, budgetScanned, budgetErr := scanBudgetsTable(ctx, client, budgetsTable, active)
	if budgetErr != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("budgets scan error: %v", budgetErr)}
	}
	pending = append(pending, budgetOps...)
	totalScanned += budgetScanned

	// identities: sole hash key sandbox_id (S)
	identOps, identScanned, identErr := scanIdentitiesTable(ctx, client, identitiesTable, active)
	if identErr != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("identities scan error: %v", identErr)}
	}
	pending = append(pending, identOps...)
	totalScanned += identScanned

	// slack-threads: PK=channel_id, SK=thread_ts; sandbox_id is a NON-KEY attribute
	slackOps, slackScanned, slackErr := scanSlackThreadsTable(ctx, client, slackThreadsTable, active)
	if slackErr != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("slack-threads scan error: %v", slackErr)}
	}
	pending = append(pending, slackOps...)
	totalScanned += slackScanned

	// sandboxes: sole hash key sandbox_id (S); delete only status∈{failed,nocap}
	sbOps, sbScanned, sbErr := scanSandboxesTable(ctx, client, sandboxesTable, active)
	if sbErr != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("sandboxes scan error: %v", sbErr)}
	}
	pending = append(pending, sbOps...)
	totalScanned += sbScanned

	// --- Step 3: report or delete ---
	if len(pending) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d rows scanned, all active", totalScanned),
		}
	}

	// Count orphans per table for the hint message.
	perTable := countByTable(pending)
	summary := buildOrphanSummary(perTable)

	if dryRun || !deleteDDBRows {
		hint := "use --dry-run=false --delete-ddb-rows to delete"
		if !dryRun && !deleteDDBRows {
			hint = "use --delete-ddb-rows to delete"
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("found %d orphaned row(s) (%s): %s", len(pending), hint, summary),
			Remediation: hint,
		}
	}

	// Execute deletions.
	var deleted, failed int
	for _, op := range pending {
		_, delErr := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: awssdk.String(op.table),
			Key:       op.key,
		})
		if delErr != nil {
			failed++
		} else {
			deleted++
		}
	}

	msg := fmt.Sprintf("deleted %d orphaned row(s) (%s)", deleted, summary)
	if failed > 0 {
		msg += fmt.Sprintf("; %d failed", failed)
	}
	if failed == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: msg}
	}
	return CheckResult{Name: name, Status: CheckWarn, Message: msg}
}

// scanBudgetsTable paginates the budgets table collecting deletable ops.
// BUDGET#ai# rows are NEVER queued (AI spend history preservation).
func scanBudgetsTable(
	ctx context.Context,
	client DDBScanDeleteAPI,
	table string,
	active map[string]bool,
) (ops []ddbDeleteOp, scanned int, err error) {
	var startKey map[string]dynamodbtypes.AttributeValue
	for {
		out, scanErr := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:            awssdk.String(table),
			ProjectionExpression: awssdk.String("PK, SK"),
			ExclusiveStartKey:   startKey,
		})
		if scanErr != nil {
			return nil, scanned, scanErr
		}
		for _, item := range out.Items {
			scanned++
			pk := attrStr(item, "PK")
			sk := attrStr(item, "SK")
			// Only process SANDBOX#... rows.
			if !strings.HasPrefix(pk, "SANDBOX#") {
				continue
			}
			sandboxID := strings.TrimPrefix(pk, "SANDBOX#")
			// Preserve AI spend history unconditionally.
			if strings.HasPrefix(sk, "BUDGET#ai#") {
				continue
			}
			// Queue delete for orphaned sandboxes only.
			if !active[sandboxID] {
				ops = append(ops, ddbDeleteOp{
					table: table,
					key: map[string]dynamodbtypes.AttributeValue{
						"PK": &dynamodbtypes.AttributeValueMemberS{Value: pk},
						"SK": &dynamodbtypes.AttributeValueMemberS{Value: sk},
					},
				})
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return ops, scanned, nil
}

// scanIdentitiesTable paginates the identities table. Sole hash key: sandbox_id.
func scanIdentitiesTable(
	ctx context.Context,
	client DDBScanDeleteAPI,
	table string,
	active map[string]bool,
) (ops []ddbDeleteOp, scanned int, err error) {
	var startKey map[string]dynamodbtypes.AttributeValue
	for {
		out, scanErr := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:            awssdk.String(table),
			ProjectionExpression: awssdk.String("sandbox_id"),
			ExclusiveStartKey:   startKey,
		})
		if scanErr != nil {
			return nil, scanned, scanErr
		}
		for _, item := range out.Items {
			scanned++
			sandboxID := attrStr(item, "sandbox_id")
			if sandboxID == "" {
				continue
			}
			// The operator public-key row (sandbox_id="operator") is NOT a
			// sandbox and is never in the active set — it is platform identity.
			// Deleting it breaks every operator-signed action with
			// unknown_sender (it is what `km doctor --republish-operator-identity`
			// re-creates). Preserve it unconditionally so a `--with-deletes`
			// sweep does not eat it every run.
			if sandboxID == operatorIdentitySandboxID {
				continue
			}
			if !active[sandboxID] {
				ops = append(ops, ddbDeleteOp{
					table: table,
					key: map[string]dynamodbtypes.AttributeValue{
						"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
					},
				})
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return ops, scanned, nil
}

// scanSlackThreadsTable paginates the slack-threads table.
// PK=channel_id, SK=thread_ts; sandbox_id is a NON-KEY attribute.
// Rows without sandbox_id are skipped (legacy rows).
func scanSlackThreadsTable(
	ctx context.Context,
	client DDBScanDeleteAPI,
	table string,
	active map[string]bool,
) (ops []ddbDeleteOp, scanned int, err error) {
	var startKey map[string]dynamodbtypes.AttributeValue
	for {
		out, scanErr := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:            awssdk.String(table),
			ProjectionExpression: awssdk.String("channel_id, thread_ts, sandbox_id"),
			ExclusiveStartKey:   startKey,
		})
		if scanErr != nil {
			return nil, scanned, scanErr
		}
		for _, item := range out.Items {
			scanned++
			channelID := attrStr(item, "channel_id")
			threadTS := attrStr(item, "thread_ts")
			sandboxID := attrStr(item, "sandbox_id")
			// Skip rows with missing key or sandbox_id (legacy rows).
			if channelID == "" || threadTS == "" || sandboxID == "" {
				continue
			}
			if !active[sandboxID] {
				ops = append(ops, ddbDeleteOp{
					table: table,
					key: map[string]dynamodbtypes.AttributeValue{
						"channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
						"thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
					},
				})
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return ops, scanned, nil
}

// sandboxDeletableStatuses is the set of statuses for which a sandboxes-table row
// may be deleted. status∈{failed,nocap} indicates the sandbox never launched or
// hit a capacity error. All other statuses (running, stopped, paused, starting,
// destroyed) are preserved.
//
// Note: status="starting" denotes an in-flight create and MUST NOT be deleted.
var sandboxDeletableStatuses = map[string]bool{
	"failed": true,
	"nocap":  true,
}

// scanSandboxesTable paginates the sandboxes table.
// Sole hash key: sandbox_id. Queues delete only for status∈{failed,nocap} orphans.
//
// Multi-install note: the sandboxes table is {prefix}-sandboxes (scoped to the
// local install), so sibling-install rows are not present in this scan.
func scanSandboxesTable(
	ctx context.Context,
	client DDBScanDeleteAPI,
	table string,
	active map[string]bool,
) (ops []ddbDeleteOp, scanned int, err error) {
	var startKey map[string]dynamodbtypes.AttributeValue
	for {
		out, scanErr := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:            awssdk.String(table),
			ProjectionExpression: awssdk.String("sandbox_id, #s"),
			ExpressionAttributeNames: map[string]string{
				"#s": "status", // "status" is a DDB reserved word
			},
			ExclusiveStartKey: startKey,
		})
		if scanErr != nil {
			return nil, scanned, scanErr
		}
		for _, item := range out.Items {
			scanned++
			sandboxID := attrStr(item, "sandbox_id")
			status := attrStr(item, "status")
			if sandboxID == "" {
				continue
			}
			// Skip rows that are in the active set.
			if active[sandboxID] {
				continue
			}
			// Only delete rows with terminal error statuses.
			if !sandboxDeletableStatuses[status] {
				continue
			}
			ops = append(ops, ddbDeleteOp{
				table: table,
				key: map[string]dynamodbtypes.AttributeValue{
					"sandbox_id": &dynamodbtypes.AttributeValueMemberS{Value: sandboxID},
				},
			})
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}
	return ops, scanned, nil
}

// countByTable returns a map of table → count of pending ops.
func countByTable(ops []ddbDeleteOp) map[string]int {
	m := make(map[string]int)
	for _, op := range ops {
		m[op.table]++
	}
	return m
}

// buildOrphanSummary renders per-table orphan counts as "table:N, table:N".
func buildOrphanSummary(perTable map[string]int) string {
	var parts []string
	for tbl, n := range perTable {
		parts = append(parts, fmt.Sprintf("%s:%d", tbl, n))
	}
	return strings.Join(parts, ", ")
}

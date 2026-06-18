// Package cmd — doctor_checks.go
// km doctor check group for the Phase 116 km check serverless runner.
//
// The group reports four things:
//   1. {prefix}-checks DynamoDB table existence (OK/FAIL).
//   2. Orphan {prefix}-check-* Lambda functions not in the DDB table (WARN).
//   3. EventBridge Scheduler entries targeting a {prefix}-check-* Lambda that
//      no longer exists in the DDB table (WARN).
//   4. Per-check KM_CHECK_TRIGGER drift: re-bake the trigger from current
//      km-config.yaml checks.triggers and compare sourceHash vs. the hash
//      stored in the DDB row (WARN nudging "km check sync").
//
// The entire group is SKIPPED SILENTLY when:
//   - The {prefix}-checks DDB table is absent (dormant install), OR
//   - The LambdaCleanup + SchedulerClient clients are nil.
//
// This mirrors the GitHub bridge doctor group pattern: when no checks are
// configured and the table does not exist, all sub-checks skip silently.
package cmd

import (
	"context"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	appcfg "github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/check"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// checkChecksTableExists reports whether the {prefix}-checks DynamoDB table
// exists. Returns CheckOK on success, CheckError on absence/access error.
// Mirrors checkDynamoTable but with a WARN-level demotion so a missing table
// on a dormant install is advisory rather than fatal (same as the Slack channels
// table pattern in buildChecks).
func checkChecksTableExists(ctx context.Context, client DynamoDescribeAPI, tableName string) CheckResult {
	if client == nil {
		return CheckResult{
			Name:    "Checks Table (" + tableName + ")",
			Status:  CheckSkipped,
			Message: "DynamoDB client not available",
		}
	}
	_, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: awssdk.String(tableName),
	})
	if err != nil {
		return CheckResult{
			Name:        "Checks Table (" + tableName + ")",
			Status:      CheckError,
			Message:     fmt.Sprintf("table %q not found or not accessible: %v", tableName, err),
			Remediation: "Run 'km init --dry-run=false' to create the checks DynamoDB table",
		}
	}
	return CheckResult{
		Name:    "Checks Table (" + tableName + ")",
		Status:  CheckOK,
		Message: fmt.Sprintf("table %q exists", tableName),
	}
}

// checkOrphanCheckLambdas lists all Lambda functions with names matching
// {prefix}-check-* and warns about any not registered in the DDB checks table.
//
// Orphan checks arise when:
//   - km check deploy succeeded but the DDB write failed, OR
//   - a Lambda was created manually outside km, OR
//   - km check rm failed mid-way (Lambda deleted but DDB row survives — the
//     inverse case is handled by checkChecksTriggersInTable below).
func checkOrphanCheckLambdas(
	ctx context.Context,
	lambdaClient LambdaCleanupAPI,
	ddbClient check.ChecksDDBAPI,
	tableName string,
	resourcePrefix string,
) CheckResult {
	name := "Orphan Check Lambdas"
	if lambdaClient == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "Lambda client not available"}
	}
	if ddbClient == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "DynamoDB client not available"}
	}

	// Fetch the DDB table rows to build the known-check set.
	rows, err := check.ListCheckRows(ctx, ddbClient, tableName)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list DDB check rows: %v", err)}
	}
	known := make(map[string]bool, len(rows))
	for _, r := range rows {
		known[r.Name] = true
	}

	// Enumerate Lambdas matching {prefix}-check-* (paginated; no name filter param).
	checkPrefix := resourcePrefix + "-check-"
	var orphans []string
	var nextMarker *string
	for {
		out, err := lambdaClient.ListFunctions(ctx, &lambda.ListFunctionsInput{
			Marker: nextMarker,
		})
		if err != nil {
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list Lambdas: %v", err)}
		}
		for _, fn := range out.Functions {
			fnName := awssdk.ToString(fn.FunctionName)
			if !strings.HasPrefix(fnName, checkPrefix) {
				continue
			}
			// Extract the check name: everything after "{prefix}-check-"
			checkName := strings.TrimPrefix(fnName, checkPrefix)
			if checkName == "" {
				continue
			}
			if !known[checkName] {
				orphans = append(orphans, fnName)
			}
		}
		if out.NextMarker == nil {
			break
		}
		nextMarker = out.NextMarker
	}

	if len(orphans) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("no orphan %s-check-* Lambdas found", resourcePrefix),
		}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     fmt.Sprintf("%d orphan check Lambda(s) not in %s DDB table: %s", len(orphans), tableName, strings.Join(orphans, ", ")),
		Remediation: "Run 'km check deploy' to register missing rows, or 'km check rm <name>' to remove the Lambda",
		Details:     orphans,
	}
}

// checkOrphanCheckSchedules lists EventBridge Scheduler entries whose target
// ARN matches a {prefix}-check-* Lambda function that is NOT registered in the
// DDB checks table. These are stale schedule entries left after a km check rm
// that failed to clean up the schedule, or a manual Lambda deletion.
func checkOrphanCheckSchedules(
	ctx context.Context,
	schedulerClient kmaws.SchedulerAPI,
	ddbClient check.ChecksDDBAPI,
	tableName string,
	resourcePrefix string,
) CheckResult {
	name := "Orphan Check Schedules"
	if schedulerClient == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "Scheduler client not available"}
	}
	if ddbClient == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "DynamoDB client not available"}
	}

	// Fetch the DDB table rows to build the known-check set.
	rows, err := check.ListCheckRows(ctx, ddbClient, tableName)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list DDB check rows: %v", err)}
	}
	known := make(map[string]bool, len(rows))
	for _, r := range rows {
		known[r.Name] = true
	}

	// Enumerate all schedules in the {prefix}-checks group.
	groupName := resourcePrefix + "-checks"
	checkFnPrefix := "function:" + resourcePrefix + "-check-"
	var orphanSchedules []string
	var nextToken *string
	for {
		out, err := schedulerClient.ListSchedules(ctx, &scheduler.ListSchedulesInput{
			GroupName:  awssdk.String(groupName),
			NextToken:  nextToken,
		})
		if err != nil {
			// Group may not exist yet on dormant installs — treat as OK.
			if strings.Contains(err.Error(), "ResourceNotFoundException") ||
				strings.Contains(err.Error(), "does not exist") {
				return CheckResult{
					Name:    name,
					Status:  CheckOK,
					Message: fmt.Sprintf("schedule group %q not found (dormant)", groupName),
				}
			}
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list schedules: %v", err)}
		}
		for _, s := range out.Schedules {
			// The Target ARN for a check Lambda contains the function name.
			// We check whether the ARN suffix (after "function:") is a known check.
			targetARN := awssdk.ToString(s.Target.Arn)
			if !strings.Contains(targetARN, checkFnPrefix) {
				continue
			}
			// Extract check name from ARN: arn:aws:lambda:region:account:function:{prefix}-check-{name}
			fnPart := targetARN[strings.LastIndex(targetARN, "function:")+len("function:"):]
			checkName := strings.TrimPrefix(fnPart, resourcePrefix+"-check-")
			if checkName == "" || checkName == fnPart {
				continue
			}
			if !known[checkName] {
				orphanSchedules = append(orphanSchedules, awssdk.ToString(s.Name))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	if len(orphanSchedules) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("no orphan check schedules in group %q", groupName),
		}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     fmt.Sprintf("%d orphan check schedule(s) referencing absent DDB rows: %s", len(orphanSchedules), strings.Join(orphanSchedules, ", ")),
		Remediation: "Run 'km check schedule <name> --off' to remove stale schedules, or 'km check deploy' to re-register missing rows",
		Details:     orphanSchedules,
	}
}

// checkChecksTriggerDrift compares the sourceHash baked into each DDB check row
// (at km check deploy / km check sync time) against the hash of the current
// km-config.yaml checks.triggers entry for the same check name.
//
// A mismatch means the operator edited km-config.yaml (inline when_py / prompt,
// or a referenced @file changed) without running km check sync to re-bake
// KM_CHECK_TRIGGER into the Lambda environment. The Lambda will fire on the OLD
// predicate + prompt until synced.
func checkChecksTriggerDrift(
	ctx context.Context,
	ddbClient check.ChecksDDBAPI,
	tableName string,
	triggers []appcfg.CheckTrigger,
) CheckResult {
	name := "Check Trigger Drift"
	if ddbClient == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "DynamoDB client not available"}
	}
	if len(triggers) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: "no checks.triggers configured (capture-only mode for all checks)",
		}
	}

	// Fetch all DDB rows to compare hashes.
	rows, err := check.ListCheckRows(ctx, ddbClient, tableName)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list DDB check rows: %v", err)}
	}
	rowByName := make(map[string]check.CheckRow, len(rows))
	for _, r := range rows {
		rowByName[r.Name] = r
	}

	var drifted []string
	for _, t := range triggers {
		if t.Check == "" {
			continue
		}
		row, ok := rowByName[t.Check]
		if !ok {
			// No DDB row yet — check not deployed; skip (checkOrphanCheckLambdas covers the inverse).
			continue
		}
		if row.SourceHash == "" {
			// Row has no hash (pre-Phase-116 or manually inserted) — skip.
			continue
		}
		// Re-bake from current config to get the expected hash.
		_, expectedHash, bakErr := check.BakeTrigger(t)
		if bakErr != nil {
			// @file reference doesn't exist or can't be read — report as drift.
			drifted = append(drifted, fmt.Sprintf("%s (bake error: %v)", t.Check, bakErr))
			continue
		}
		if row.SourceHash != expectedHash {
			drifted = append(drifted, t.Check)
		}
	}

	if len(drifted) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d trigger(s) in sync with km-config.yaml", len(triggers)),
		}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     fmt.Sprintf("%d check(s) have stale KM_CHECK_TRIGGER (config drifted from deployed hash): %s", len(drifted), strings.Join(drifted, ", ")),
		Remediation: "Run 'km check sync' to re-bake KM_CHECK_TRIGGER from current km-config.yaml",
		Details:     drifted,
	}
}

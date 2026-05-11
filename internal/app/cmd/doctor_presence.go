// Package cmd — doctor_presence.go
// Plan 79-00 stub upgraded to full implementation by Plan 79-04.
//
//   checkPresenceDaemonHealthy: for each running sandbox, queries CloudWatch
//     Logs for a recent source:"presence" heartbeat event in the audit log
//     group; returns OK if all sandboxes emitted within 5 minutes, WARN
//     otherwise. The check is skipped if either the CW client or the sandbox
//     lister is nil (WARN-not-ERROR per Phase 79 PRD — a stale presence daemon
//     is a warning, not a platform failure, matching the doctor_slack.go
//     "demote ERROR to WARN" pattern).
//
// Log group convention: "/{resource_prefix}/sandboxes/{sandbox_id}/"
// This matches the audit-log sidecar (CW_LOG_GROUP env default) and
// pkg/aws.DeleteSandboxLogGroup / ExportSandboxLogs — confirmed at
// sidecars/audit-log/cmd/main.go line 50 and pkg/aws/cloudwatch.go line 69.
//
// Registration in runDoctor/buildChecks is done in Plan 79-04 (this file).
package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// CWLogsFilterAPI is the narrow CloudWatch Logs interface needed by the
// presence daemon doctor check. Only FilterLogEvents is required; the real
// *cloudwatchlogs.Client satisfies this interface directly.
type CWLogsFilterAPI interface {
	FilterLogEvents(ctx context.Context, input *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

// runningSandboxLister abstracts listing running sandbox IDs so tests can
// inject a fake without needing a real DynamoDB client.
type runningSandboxLister interface {
	ListRunningSandboxIDs(ctx context.Context) ([]string, error)
}

// runningSandboxListerFunc is a func-type alias satisfying runningSandboxLister.
// Used by initRealDepsWithExisting to wrap a closure without defining a new struct.
type runningSandboxListerFunc func(ctx context.Context) ([]string, error)

func (f runningSandboxListerFunc) ListRunningSandboxIDs(ctx context.Context) ([]string, error) {
	return f(ctx)
}

// checkPresenceDaemonHealthy returns OK if every running sandbox has emitted a
// source:"presence" CloudWatch event in the last 5 minutes; WARN otherwise.
// Skipped if either dependency is nil. WARN-not-ERROR per Phase 79 PRD —
// a stale presence daemon is a warning, not a platform failure (matches
// doctor_slack.go's "demote ERROR to WARN" pattern).
//
// logGroupPrefix is the CW log group prefix to which the sandbox ID is
// appended. Convention: "/{resource_prefix}/sandboxes/" (e.g. "/km/sandboxes/").
// The trailing slash is required — it matches the audit-log sidecar default.
func checkPresenceDaemonHealthy(
	ctx context.Context,
	cw CWLogsFilterAPI,
	lister runningSandboxLister,
	logGroupPrefix string,
) CheckResult {
	name := "Presence daemon healthy"

	if cw == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "CloudWatch Logs client not configured",
		}
	}
	if lister == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "sandbox lister not configured",
		}
	}

	sandboxIDs, err := lister.ListRunningSandboxIDs(ctx)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("failed to list running sandboxes: %v", err),
		}
	}
	if len(sandboxIDs) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: "no running sandboxes to check",
		}
	}

	// 5-minute staleness threshold per CONTEXT.md and Phase 79 PRD.
	startMs := time.Now().Add(-5 * time.Minute).UnixMilli()
	// CloudWatch Logs JSON metric filter syntax — text-style "source":"presence"
	// is rejected by the API as InvalidParameterException.
	filterPattern := `{ $.source = "presence" }`

	var stale []string
	for _, id := range sandboxIDs {
		// Log group convention: "/{prefix}/sandboxes/{sandbox_id}/"
		// Matches audit-log sidecar CW_LOG_GROUP default and pkg/aws helpers.
		// Trailing slash is significant — CW treats "/km/sandboxes/X" and
		// "/km/sandboxes/X/" as different groups.
		logGroup := logGroupPrefix + id + "/"

		out, cwErr := cw.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:  awssdk.String(logGroup),
			FilterPattern: awssdk.String(filterPattern),
			StartTime:     awssdk.Int64(startMs),
			Limit:         awssdk.Int32(1),
		})
		if cwErr != nil {
			// ResourceNotFoundException = log group doesn't exist yet (pre-Phase-79
			// sandbox or daemon never started). Treat as stale — the operator should
			// recreate the sandbox to pick up km-presence.
			stale = append(stale, id)
			continue
		}
		if out == nil || len(out.Events) == 0 {
			stale = append(stale, id)
		}
	}

	if len(stale) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("all %d running sandbox(es) have recent presence events (<=5min)", len(sandboxIDs)),
		}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     fmt.Sprintf("%d sandbox(es) have no recent presence events: %s", len(stale), strings.Join(stale, ", ")),
		Remediation: "Pre-Phase-79 sandboxes do not run km-presence — recreate via 'km destroy && km create'. For Phase-79+ sandboxes, check 'systemctl status km-presence' on the sandbox.",
	}
}

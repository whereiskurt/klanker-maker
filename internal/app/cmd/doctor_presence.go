// Package cmd — doctor_presence.go
// Plan 79-00 stub — km doctor check for Phase 79 presence daemon health.
//
//   checkPresenceDaemonHealthy: for each running sandbox, queries CloudWatch
//     Logs for a recent source:"presence" heartbeat event in the audit log
//     group; returns OK if all sandboxes emitted within 5 minutes, WARN
//     otherwise. The check is skipped if either the CW client or the sandbox
//     lister is nil (WARN-not-ERROR per Phase 79 PRD — a stale presence daemon
//     is a warning, not a platform failure, matching the doctor_slack.go
//     "demote ERROR to WARN" pattern).
//
// Registration in runDoctor/buildChecks is deferred to Plan 79-04; this file
// only makes the function exist so Plans 79-01 through 79-03 can compile
// against the final call site.
package cmd

import (
	"context"

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

// checkPresenceDaemonHealthy returns OK if every running sandbox has emitted a
// source:"presence" CloudWatch event in the last 5 minutes; WARN otherwise.
// Skipped if either dependency is nil. WARN-not-ERROR per Phase 79 PRD —
// a stale presence daemon is a warning, not a platform failure (matches
// doctor_slack.go's "demote ERROR to WARN" pattern).
func checkPresenceDaemonHealthy(
	ctx context.Context,
	cw CWLogsFilterAPI,
	lister runningSandboxLister,
	logGroupPrefix string,
) CheckResult {
	return CheckResult{
		Name:    "Presence daemon healthy",
		Status:  CheckWarn,
		Message: "not implemented (Plan 79-04 stub)",
	}
}

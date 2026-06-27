// Package httpproxy_test — quota_classify_test.go
// Wave 0 test stubs for PRX-01..03 (action classification at the proxy chokepoint).
// TestClassifyGitHub and TestClassifySES bodies are guarded with t.Skip("plan 05")
// because the ClassifyAction function is implemented in plan 05.
// TestNoDoubleCount is also guarded because the lambda-url exclusion logic lands
// in plan 05 alongside the classifier.
package httpproxy_test

import (
	"testing"
)

// TestClassifyGitHub (PRX-01) — proxy classifies GitHub API write requests to
// the correct action names:
//   - POST api.github.com /repos/{owner}/{repo}/pulls        → "github_pr"
//   - POST api.github.com /repos/{owner}/{repo}/issues/{n}/comments → "github_comment"
//   - POST api.github.com /repos/{owner}/{repo}/pulls/{n}/reviews  → "github_review"
func TestClassifyGitHub(t *testing.T) {
	t.Skip("plan 05: ClassifyAction function not yet implemented")

	// When implemented, assert via a table-driven test:
	// host="api.github.com", method=POST, path cases → expected action string.
	// Non-POST (GET) or non-repo paths → "" (no action, pass-through).
}

// TestClassifySES (PRX-02) — proxy classifies SES outbound email actions:
//   - POST email.*.amazonaws.com Action=SendEmail    → "email_send"
//   - POST email.*.amazonaws.com Action=SendRawEmail → "email_send"
//
// SES endpoint traffic is already MITM'd (Phase 88 budget metering for SES).
func TestClassifySES(t *testing.T) {
	t.Skip("plan 05: ClassifyAction function not yet implemented")

	// When implemented, assert via a table-driven test:
	// host matches email.*.amazonaws.com, body contains Action=SendEmail* → "email_send".
	// A GET (non-write) or DescribeActiveReceiptRuleSet → "" (no action).
}

// TestNoDoubleCount (PRX-03) — the proxy must NOT classify a POST to a
// Lambda Function URL (*.lambda-url.*.on.aws) as "slack_post".
//
// Rationale (Risk 1, CONTEXT.md §3): when km-slack posts a Slack message it
// sends a signed Ed25519 envelope to the Slack bridge Function URL. That HTTP
// egress transits the proxy. Counting it here would double-count it against
// the slack_post quota (the bridge already counts it). The exclusion is a
// deliberate chokepoint carve-out.
func TestNoDoubleCount(t *testing.T) {
	t.Skip("plan 05: ClassifyAction function not yet implemented")

	// When implemented, assert:
	// host="<id>.lambda-url.us-east-1.on.aws", method=POST → "" (no action classified).
	// The classification must be host-first — a POST to the lambda URL must return ""
	// regardless of the path or body content.
}

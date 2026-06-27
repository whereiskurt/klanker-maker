// Package httpproxy_test — quota_classify_test.go
// Tests for PRX-01..03 (action classification at the proxy chokepoint, Phase 121).
package httpproxy_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/quota"
	"github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

// TestClassifyGitHub (PRX-01) — proxy classifies GitHub API write requests to
// the correct action names.
func TestClassifyGitHub(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		host     string
		path     string
		expected quota.Action
	}{
		// POST /repos/{owner}/{repo}/pulls → github_pr
		{
			name: "create PR",
			method: "POST", host: "api.github.com",
			path:     "/repos/myorg/myrepo/pulls",
			expected: quota.ActionGithubPR,
		},
		// POST /repos/{owner}/{repo}/issues/{n}/comments → github_comment
		{
			name: "create issue comment",
			method: "POST", host: "api.github.com",
			path:     "/repos/myorg/myrepo/issues/42/comments",
			expected: quota.ActionGithubComment,
		},
		// POST /repos/{owner}/{repo}/pulls/{n}/reviews → github_review
		{
			name: "create PR review",
			method: "POST", host: "api.github.com",
			path:     "/repos/myorg/myrepo/pulls/7/reviews",
			expected: quota.ActionGithubReview,
		},
		// GET should not be classified (reads are fine).
		{
			name: "GET pulls not counted",
			method: "GET", host: "api.github.com",
			path:     "/repos/myorg/myrepo/pulls",
			expected: "",
		},
		// Non-repo path → no action.
		{
			name: "rate_limit endpoint",
			method: "POST", host: "api.github.com",
			path:     "/rate_limit",
			expected: "",
		},
		// Root path → no action.
		{
			name: "root path",
			method: "POST", host: "api.github.com",
			path:     "/",
			expected: "",
		},
		// POST /repos/{o}/{r}/pulls/{n}/files → not a review → no action.
		{
			name: "get PR files not review",
			method: "GET", host: "api.github.com",
			path:     "/repos/myorg/myrepo/pulls/7/files",
			expected: "",
		},
		// Nested path with trailing segments — PR create is top-level /pulls only.
		{
			name: "pulls subresource not PR",
			method: "POST", host: "api.github.com",
			path:     "/repos/myorg/myrepo/pulls/7/requested_reviewers",
			expected: "",
		},
		// Host with port.
		{
			name: "api.github.com with port",
			method: "POST", host: "api.github.com:443",
			path:     "/repos/myorg/myrepo/pulls",
			expected: quota.ActionGithubPR,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := httpproxy.ClassifyAction(tc.method, tc.host, tc.path)
			if got != tc.expected {
				t.Errorf("ClassifyAction(%q, %q, %q) = %q; want %q",
					tc.method, tc.host, tc.path, got, tc.expected)
			}
		})
	}
}

// TestClassifySES (PRX-02) — proxy classifies SES outbound email actions.
func TestClassifySES(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		host     string
		path     string
		expected quota.Action
	}{
		// POST email.{region}.amazonaws.com /v2/email/outbound-emails → email_send
		{
			name: "SES us-east-1 SendEmail",
			method: "POST", host: "email.us-east-1.amazonaws.com",
			path:     "/v2/email/outbound-emails",
			expected: quota.ActionEmailSend,
		},
		{
			name: "SES eu-west-1 SendEmail",
			method: "POST", host: "email.eu-west-1.amazonaws.com",
			path:     "/v2/email/outbound-emails",
			expected: quota.ActionEmailSend,
		},
		{
			name: "SES with trailing path",
			method: "POST", host: "email.us-east-1.amazonaws.com",
			path:     "/v2/email/outbound-emails/batch",
			expected: quota.ActionEmailSend,
		},
		// GET should not be classified.
		{
			name: "GET not email_send",
			method: "GET", host: "email.us-east-1.amazonaws.com",
			path:     "/v2/email/identities",
			expected: "",
		},
		// Non-send path.
		{
			name: "POST describe rules not email_send",
			method: "POST", host: "email.us-east-1.amazonaws.com",
			path:     "/v2/email/configuration-sets",
			expected: "",
		},
		// Bedrock-runtime is not SES.
		{
			name: "bedrock endpoint not SES",
			method: "POST", host: "bedrock-runtime.us-east-1.amazonaws.com",
			path:     "/v2/email/outbound-emails",
			expected: "",
		},
		// SES with port.
		{
			name: "SES host with port",
			method: "POST", host: "email.us-east-1.amazonaws.com:443",
			path:     "/v2/email/outbound-emails",
			expected: quota.ActionEmailSend,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := httpproxy.ClassifyAction(tc.method, tc.host, tc.path)
			if got != tc.expected {
				t.Errorf("ClassifyAction(%q, %q, %q) = %q; want %q",
					tc.method, tc.host, tc.path, got, tc.expected)
			}
		})
	}
}

// TestNoDoubleCount (PRX-03) — the proxy must NOT classify a POST to a
// Lambda Function URL (*.lambda-url.*.on.aws) as any action.
func TestNoDoubleCount(t *testing.T) {
	tests := []struct {
		name   string
		method string
		host   string
		path   string
	}{
		{
			name:   "lambda-url us-east-1",
			method: "POST",
			host:   "abc123def456.lambda-url.us-east-1.on.aws",
			path:   "/",
		},
		{
			name:   "lambda-url eu-west-1",
			method: "POST",
			host:   "xyz789.lambda-url.eu-west-1.on.aws",
			path:   "/events",
		},
		{
			name:   "lambda-url with port",
			method: "POST",
			host:   "abc123.lambda-url.us-east-1.on.aws:443",
			path:   "/",
		},
		{
			name:   "lambda-url deep path",
			method: "POST",
			host:   "fnurl.lambda-url.ap-southeast-1.on.aws",
			path:   "/v2/email/outbound-emails",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := httpproxy.ClassifyAction(tc.method, tc.host, tc.path)
			if got != "" {
				t.Errorf("ClassifyAction(%q, %q, %q) = %q; want \"\" (no double-count)",
					tc.method, tc.host, tc.path, got)
			}
		})
	}
}

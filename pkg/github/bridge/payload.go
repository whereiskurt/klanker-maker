// Package bridge implements the km-github-bridge Lambda handler.
// It is the GitHub-shaped twin of pkg/slack/bridge: HMAC-SHA256 webhook verify,
// loop guard, X-GitHub-Delivery GUID dedupe, @-mention detection, deny-by-default
// allowlist, config-driven repo→{alias,profile} resolution, warm-enqueue /
// cold-SandboxCreate dispatch, and a synchronous 👀 ACK reaction.
package bridge

import "encoding/json"

// IssueCommentPayload is the JSON body GitHub sends for issue_comment events.
// Only the fields in the locked field-mapping (CONTEXT.md) are captured:
// action, issue.number, issue.pull_request (presence = PR), comment.body,
// comment.user.{login,type}, comment.id, comment.html_url,
// installation.id, repository.full_name, repository.default_branch.
type IssueCommentPayload struct {
	Action  string       `json:"action"`
	Issue   IssueField   `json:"issue"`
	Comment CommentField `json:"comment"`
	// Sender mirrors the comment user for convenience (some webhook shapes).
	Sender         *UserField     `json:"sender,omitempty"`
	Installation   InstallField   `json:"installation"`
	Repository     RepositoryField `json:"repository"`
}

// IssueField is the subset of the issue object we need.
type IssueField struct {
	Number      int                `json:"number"`
	PullRequest *json.RawMessage   `json:"pull_request,omitempty"` // nil = plain issue; non-nil = PR comment
}

// CommentField is the subset of the comment object we need.
type CommentField struct {
	ID      int64     `json:"id"`
	Body    string    `json:"body"`
	HTMLURL string    `json:"html_url"`
	User    UserField `json:"user"`
}

// UserField captures the comment author.
type UserField struct {
	Login string `json:"login"`
	// Type is "User", "Bot", or "Organization".
	Type string `json:"type"`
}

// InstallField carries the GitHub App installation ID (int64 in GitHub's API
// but we store it as int64 for marshal convenience).
type InstallField struct {
	// ID is the GitHub App installation ID as returned in the webhook payload.
	ID int64 `json:"id"`
}

// RepositoryField is the subset of the repository object we need.
type RepositoryField struct {
	FullName      string `json:"full_name"`       // "owner/repo"
	DefaultBranch string `json:"default_branch"`  // "main", "master", etc.
}

// GitHubEnvelope is the message enqueued to the per-sandbox github-inbound
// FIFO queue (warm path) or carried in SandboxCreateDetail.GithubEnvelope
// (cold path, JSON-serialized). It mirrors the Slack SQS message shape
// but with GitHub-specific fields.
//
// Fields:
//   - Source: always "github" (source-aware poller discriminator).
//   - Repo: "owner/repo" full name.
//   - Number: issue/PR number.
//   - Kind: "issue_comment" (the webhook event type).
//   - CommentID: comment identifier for the 👀 reaction endpoint.
//   - HTMLURL: permalink to the comment (for logging/debug).
//   - Sender: comment author login.
//   - Body: free-form text after the @-mention (the agent prompt).
//   - InstallID: GitHub App installation ID (string for JSON; the token exchange API takes string).
//   - DefaultBranch: repository default branch ("main", "master").
type GitHubEnvelope struct {
	Source        string `json:"source"`
	Repo          string `json:"repo"`
	Number        int    `json:"number"`
	Kind          string `json:"kind"`
	CommentID     int64  `json:"comment_id"`
	HTMLURL       string `json:"html_url"`
	Sender        string `json:"sender"`
	Body          string `json:"body"`
	InstallID     string `json:"install_id"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

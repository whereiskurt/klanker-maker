package bridge_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// verifyGitHubSignature tests (RESEARCH Pattern 1)
// ============================================================

func makeSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyGitHubSignature(t *testing.T) {
	secret := "test-webhook-secret"
	body := []byte(`{"action":"created"}`)
	validSig := makeSignature(secret, body)

	tests := []struct {
		name      string
		secret    string
		sigHeader string
		body      []byte
		wantErr   bool
	}{
		{
			name:      "valid signature passes",
			secret:    secret,
			sigHeader: validSig,
			body:      body,
			wantErr:   false,
		},
		{
			name:      "wrong secret fails",
			secret:    "wrong-secret",
			sigHeader: validSig,
			body:      body,
			wantErr:   true,
		},
		{
			name:      "tampered body fails",
			secret:    secret,
			sigHeader: validSig,
			body:      []byte(`{"action":"deleted"}`),
			wantErr:   true,
		},
		{
			name:      "missing prefix fails",
			secret:    secret,
			sigHeader: hex.EncodeToString([]byte("no-prefix")),
			body:      body,
			wantErr:   true,
		},
		{
			name:      "empty sig header fails",
			secret:    secret,
			sigHeader: "",
			body:      body,
			wantErr:   true,
		},
		{
			name:      "wrong prefix (sha1= instead of sha256=) fails",
			secret:    secret,
			sigHeader: "sha1=" + hex.EncodeToString([]byte("fake")),
			body:      body,
			wantErr:   true,
		},
		{
			name:      "empty body with valid sig passes",
			secret:    secret,
			sigHeader: makeSignature(secret, []byte{}),
			body:      []byte{},
			wantErr:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := bridge.VerifyGitHubSignature(tc.secret, tc.sigHeader, tc.body)
			if (err != nil) != tc.wantErr {
				t.Errorf("VerifyGitHubSignature() err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// ============================================================
// Payload parsing tests
// ============================================================

func TestParseIssueCommentPayload(t *testing.T) {
	raw := `{
		"action": "created",
		"issue": {
			"number": 42,
			"pull_request": {"url": "https://github.com/owner/repo/pull/42"}
		},
		"comment": {
			"id": 99,
			"body": "@mybot please review this",
			"html_url": "https://github.com/owner/repo/issues/42#issuecomment-99",
			"user": {
				"login": "alice",
				"type": "User"
			}
		},
		"installation": {
			"id": 12345
		},
		"repository": {
			"full_name": "owner/repo",
			"default_branch": "main"
		}
	}`

	var p bridge.IssueCommentPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if p.Action != "created" {
		t.Errorf("Action=%q want created", p.Action)
	}
	if p.Issue.Number != 42 {
		t.Errorf("Issue.Number=%d want 42", p.Issue.Number)
	}
	if p.Issue.PullRequest == nil {
		t.Error("Issue.PullRequest should not be nil")
	}
	if p.Comment.ID != 99 {
		t.Errorf("Comment.ID=%d want 99", p.Comment.ID)
	}
	if p.Comment.Body != "@mybot please review this" {
		t.Errorf("Comment.Body=%q", p.Comment.Body)
	}
	if p.Comment.User.Login != "alice" {
		t.Errorf("Comment.User.Login=%q want alice", p.Comment.User.Login)
	}
	if p.Comment.User.Type != "User" {
		t.Errorf("Comment.User.Type=%q want User", p.Comment.User.Type)
	}
	if p.Installation.ID != 12345 {
		t.Errorf("Installation.ID=%d want 12345", p.Installation.ID)
	}
	if p.Repository.FullName != "owner/repo" {
		t.Errorf("Repository.FullName=%q want owner/repo", p.Repository.FullName)
	}
}

func TestParseIssueCommentPayload_NoPR(t *testing.T) {
	raw := `{
		"action": "created",
		"issue": {"number": 1},
		"comment": {"id": 2, "body": "hi", "user": {"login": "alice", "type": "User"}},
		"installation": {"id": 1},
		"repository": {"full_name": "owner/repo"}
	}`
	var p bridge.IssueCommentPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Issue.PullRequest != nil {
		t.Error("Issue.PullRequest should be nil for a plain issue")
	}
}

// ============================================================
// GitHubEnvelope serialization tests
// ============================================================

func TestGitHubEnvelope_RoundTrip(t *testing.T) {
	env := bridge.GitHubEnvelope{
		Source:        "github",
		Repo:          "owner/repo",
		Number:        42,
		Kind:          "issue_comment",
		CommentID:     99,
		HTMLURL:       "https://github.com/owner/repo/issues/42#issuecomment-99",
		Sender:        "alice",
		Body:          "please review",
		InstallID:     "12345",
		DefaultBranch: "main",
	}

	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got bridge.GitHubEnvelope
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.Source != "github" {
		t.Errorf("Source=%q want github", got.Source)
	}
	if got.Repo != "owner/repo" {
		t.Errorf("Repo=%q want owner/repo", got.Repo)
	}
	if got.Number != 42 {
		t.Errorf("Number=%d want 42", got.Number)
	}
	if got.CommentID != 99 {
		t.Errorf("CommentID=%d want 99", got.CommentID)
	}
	if got.Sender != "alice" {
		t.Errorf("Sender=%q want alice", got.Sender)
	}
	if got.Body != "please review" {
		t.Errorf("Body=%q want 'please review'", got.Body)
	}
}

// ============================================================
// Mention detection tests
// ============================================================

func TestContainsMention(t *testing.T) {
	tests := []struct {
		body     string
		botLogin string
		want     bool
	}{
		{"@mybot please review", "mybot", true},
		{"hello @mybot can you help", "mybot", true},
		{"no mention here", "mybot", false},
		{"@otherbot help", "mybot", false},
		{"@mybot", "mybot", true},
		// case-insensitive
		{"@MyBot please", "mybot", true},
		{"@MYBOT please", "mybot", true},
	}
	for _, tc := range tests {
		got := bridge.ContainsMention(tc.body, tc.botLogin)
		if got != tc.want {
			t.Errorf("ContainsMention(%q, %q)=%v want %v", tc.body, tc.botLogin, got, tc.want)
		}
	}
}

// ============================================================
// ExtractMentionBody tests (strip the @mention prefix)
// ============================================================

func TestExtractMentionBody(t *testing.T) {
	tests := []struct {
		body     string
		botLogin string
		want     string
	}{
		{"@mybot please review this PR", "mybot", "please review this PR"},
		{"hello @mybot can you help", "mybot", "can you help"},
		{"  @mybot   do the thing  ", "mybot", "do the thing"},
		{"@mybot", "mybot", ""},
	}
	for _, tc := range tests {
		got := bridge.ExtractMentionBody(tc.body, tc.botLogin)
		if got != strings.TrimSpace(tc.want) {
			t.Errorf("ExtractMentionBody(%q, %q)=%q want %q", tc.body, tc.botLogin, got, tc.want)
		}
	}
}

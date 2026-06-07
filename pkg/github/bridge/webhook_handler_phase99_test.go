// webhook_handler_phase99_test.go — Phase 99 handler tests: command pass reply paths + dormancy invariant.
//
// Tests: TestHandle_CommandsDormant (byte-identity guard), TestHandle_DefaultCommand,
// TestHandle_MultiCommand, TestHandle_CommandNotAuthorized, TestHandle_Help,
// TestHandle_UnknownToken, TestHandle_KnownCommand (happy path).
//
// Reuses test doubles from handle_test.go (mockSecretFetcher, mockBotLoginFetcher,
// mockNonceStore, mockResolver, mockPublisher, mockSQS, mockReactor) and
// webhook_handler_phase98_test.go (mockGitHubThreadStore, buildPayloadJSON,
// buildRequest, defaultOpts).
package bridge_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// mockCommenter — records PostComment calls for testing.
// Mirrors mockReactor but for the CommentPoster interface.
// ============================================================

type mockCommenter struct {
	called         bool
	installationID string
	owner          string
	repo           string
	issueNumber    int
	body           string
	err            error
}

func (m *mockCommenter) PostComment(_ context.Context, installationID, owner, repo string, issueNumber int, body string) error {
	m.called = true
	m.installationID = installationID
	m.owner = owner
	m.repo = repo
	m.issueNumber = issueNumber
	m.body = body
	return m.err
}

// Compile-time check: mockCommenter must satisfy bridge.CommentPoster.
var _ bridge.CommentPoster = &mockCommenter{}

// ============================================================
// Helpers for Phase 99 handler tests
// ============================================================

// testCommands returns a minimal command map for testing.
func testCommands() map[string]bridge.CommandEntry {
	return map[string]bridge.CommandEntry{
		"review": {
			Description: "Review the PR and provide inline comments",
			Alias:       "gh-review-alias",
			Profile:     "gh-review-profile",
			Allow:       []string{"alice", "bob"},
			Prompt:      "Please review the PR: {{args}}",
		},
		"patch": {
			Description: "Apply the smallest correct fix",
			Allow:       []string{"alice"},
			Prompt:      "Apply a minimal fix: {{args}}",
			// No Alias/Profile override — falls back to repo alias/profile
		},
		"deploy": {
			Description: "Deploy to staging",
			Allow:       []string{"admin"},
			Prompt:      "Deploy to staging: {{args}}",
		},
	}
}

// buildHandlerWithCommands builds a WebhookHandler with command pass wired.
// Uses the base buildHandler helper and layers in Phase 99 fields.
func buildHandlerWithCommands(
	commands map[string]bridge.CommandEntry,
	defaultCommand string,
	commenter bridge.CommentPoster,
) *bridge.WebhookHandler {
	entries := []bridge.RepoEntry{
		{
			Match:          "myorg/myrepo",
			Alias:          "myrepo-alias",
			Profile:        "myrepo-profile",
			Allow:          []string{"alice", "bob"},
			DefaultCommand: "",
		},
	}
	return &bridge.WebhookHandler{
		Secret:         &mockSecretFetcher{secret: testSecret},
		BotLogin:       &mockBotLoginFetcher{login: testBotLogin},
		Nonces:         newMockNonceStore(),
		Resolver:       &mockResolver{resolveErr: nil, sandboxID: "sb-cmd-123", queueURL: "https://sqs.example.com/q"},
		Publisher:      &mockPublisher{},
		SQS:            &mockSQS{},
		Reactor:        &mockReactor{},
		Entries:        entries,
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
		Commands:       commands,
		DefaultCommand: defaultCommand,
		Commenter:      commenter,
	}
}

// ============================================================
// Phase 99 Tests
// ============================================================

// TestHandle_CommandsDormant is the critical byte-identity guard.
// When h.Commands is nil/empty AND h.DefaultCommand is "", Handle() must behave
// EXACTLY as Phase 98: free-form ExtractMentionBody dispatch, no PostComment call.
func TestHandle_CommandsDormant(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "@mybot[bot] please review this PR"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	// Commands nil, DefaultCommand "" — dormant.
	h := buildHandlerWithCommands(nil, "", commenter)
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// CRITICAL: Commenter must NOT be called in dormant mode.
	if commenter.called {
		t.Error("Commenter must NOT be called when commands are unconfigured (dormant invariant)")
	}

	// Dispatch (SQS warm path) MUST be called — free-form passthrough.
	if !sqsSender.called {
		t.Error("SQS.Send must be called on dormant path (free-form dispatch)")
	}

	// Verify the envelope body is the ExtractMentionBody result (free-form).
	var env bridge.GitHubEnvelope
	if err := json.Unmarshal([]byte(sqsSender.body), &env); err != nil {
		t.Fatalf("SQS body not valid GitHubEnvelope JSON: %v", err)
	}
	wantBody := "please review this PR"
	if env.Body != wantBody {
		t.Errorf("envelope.Body=%q want %q (ExtractMentionBody result, not command-expanded)", env.Body, wantBody)
	}
}

// TestHandle_CommandsDormant_EmptyMap verifies that an empty (non-nil) command map
// also triggers dormant behavior (same as nil — the len()==0 gate).
func TestHandle_CommandsDormant_EmptyMap(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "@mybot[bot] lgtm"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}

	h := buildHandlerWithCommands(map[string]bridge.CommandEntry{}, "", commenter)
	h.SQS = sqsSender

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if commenter.called {
		t.Error("Commenter must NOT be called with empty Commands map (dormant invariant)")
	}
	if !sqsSender.called {
		t.Error("SQS.Send must be called on empty-commands dormant path")
	}
}

// TestHandle_MultiCommand verifies that `@bot /patch /review` results in a
// one-at-a-time error PostComment reply and NO dispatch.
func TestHandle_MultiCommand(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "@mybot[bot] /patch /review please look at both"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	h := buildHandlerWithCommands(testCommands(), "", commenter)
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter MUST be called with the multi-command error.
	if !commenter.called {
		t.Error("Commenter must be called for multi-command error")
	}
	// Error reply must mention both command names.
	if !strings.Contains(commenter.body, "patch") || !strings.Contains(commenter.body, "review") {
		t.Errorf("multi-command error reply does not mention both commands: %q", commenter.body)
	}

	// Dispatch must NOT happen.
	if sqsSender.called {
		t.Error("SQS.Send must NOT be called on multi-command error path")
	}
	if publisher.called {
		t.Error("Publisher must NOT be called on multi-command error path")
	}
}

// TestHandle_CommandNotAuthorized verifies that a sender in repo.allow but NOT in
// command.allow gets a polite "not authorized" PostComment reply and NO dispatch.
func TestHandle_CommandNotAuthorized(t *testing.T) {
	// "bob" is in repo.allow but NOT in patch.Allow (which is ["alice"]).
	opts := defaultOpts()
	opts.userLogin = "bob"
	opts.commentBody = "@mybot[bot] /patch fix the login bug"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	h := buildHandlerWithCommands(testCommands(), "", commenter)
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter MUST be called with the deny reply.
	if !commenter.called {
		t.Error("Commenter must be called for command-not-authorized denial")
	}
	if !strings.Contains(commenter.body, "patch") {
		t.Errorf("deny reply does not mention the command name: %q", commenter.body)
	}

	// Dispatch must NOT happen.
	if sqsSender.called {
		t.Error("SQS.Send must NOT be called on command-not-authorized path")
	}
	if publisher.called {
		t.Error("Publisher must NOT be called on command-not-authorized path")
	}
}

// TestHandle_Help verifies that `@bot /help` results in a PostComment listing
// commands (name + description) and the effective default for the repo; no dispatch.
func TestHandle_Help(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "@mybot[bot] /help"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	h := buildHandlerWithCommands(testCommands(), "review", commenter)
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter MUST be called with the help listing.
	if !commenter.called {
		t.Error("Commenter must be called for /help")
	}

	// Help reply must list at least some of the commands.
	if !strings.Contains(commenter.body, "review") {
		t.Errorf("/help reply does not mention 'review' command: %q", commenter.body)
	}
	if !strings.Contains(commenter.body, "patch") {
		t.Errorf("/help reply does not mention 'patch' command: %q", commenter.body)
	}

	// Dispatch must NOT happen.
	if sqsSender.called {
		t.Error("SQS.Send must NOT be called on /help path")
	}
	if publisher.called {
		t.Error("Publisher must NOT be called on /help path")
	}
}

// TestHandle_UnknownToken verifies that an unknown /token dispatches free-form
// (via ExtractMentionBody) with NO PostComment call.
func TestHandle_UnknownToken(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "@mybot[bot] can you check the /api endpoint"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}

	h := buildHandlerWithCommands(testCommands(), "", commenter)
	h.SQS = sqsSender

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter must NOT be called for unknown tokens (lenient-unknown rule D6).
	if commenter.called {
		t.Error("Commenter must NOT be called for unknown /token (lenient-unknown rule)")
	}

	// Dispatch (SQS) MUST be called — free-form dispatch.
	if !sqsSender.called {
		t.Error("SQS.Send must be called for unknown-token free-form dispatch")
	}
}

// TestHandle_DefaultCommand verifies that a command-less comment dispatches via the
// effective default command when one is configured: template expanded with {{args}}
// being the comment minus the mention.
func TestHandle_DefaultCommand(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "@mybot[bot] fix the flaky test in auth_test.go"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	// DefaultCommand = "review" (install-wide). No explicit /review in comment.
	h := buildHandlerWithCommands(testCommands(), "review", commenter)
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter must NOT be called (no reply — just dispatch).
	if commenter.called {
		t.Error("Commenter must NOT be called on default-command dispatch path")
	}

	// Dispatch MUST happen.
	if !sqsSender.called {
		t.Error("SQS.Send must be called on default-command dispatch path")
	}

	// Verify the envelope contains the expanded template (not raw mention body).
	var env bridge.GitHubEnvelope
	if err := json.Unmarshal([]byte(sqsSender.body), &env); err != nil {
		t.Fatalf("SQS body not valid GitHubEnvelope JSON: %v", err)
	}

	// The "review" command template is "Please review the PR: {{args}}"
	// args = "fix the flaky test in auth_test.go" (body minus mention).
	if !strings.HasPrefix(env.Body, "Please review the PR:") {
		t.Errorf("envelope.Body does not start with expanded template: %q", env.Body)
	}
	if !strings.Contains(env.Body, "fix the flaky test in auth_test.go") {
		t.Errorf("envelope.Body does not contain args: %q", env.Body)
	}
}

// TestHandle_KnownCommand_HappyPath verifies that a single known /review command
// dispatches with the command's alias/profile overrides and the expanded template.
func TestHandle_KnownCommand_HappyPath(t *testing.T) {
	opts := defaultOpts()
	opts.userLogin = "alice"
	opts.commentBody = "@mybot[bot] /review fix login timeout"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	h := buildHandlerWithCommands(testCommands(), "", commenter)
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter must NOT be called (happy path = dispatch).
	if commenter.called {
		t.Error("Commenter must NOT be called on known-command happy path")
	}

	// Dispatch MUST happen.
	if !sqsSender.called {
		t.Error("SQS.Send must be called on known-command happy path")
	}

	// Verify envelope: alias/profile from command override, body = expanded template.
	var env bridge.GitHubEnvelope
	if err := json.Unmarshal([]byte(sqsSender.body), &env); err != nil {
		t.Fatalf("SQS body not valid GitHubEnvelope JSON: %v", err)
	}

	// "review" command has Alias="gh-review-alias", Profile="gh-review-profile".
	// The envelope does NOT carry alias/profile directly (they're in the Publisher path
	// for cold create). For warm path (SQS), the dispatch uses the resolved alias for
	// queue lookup; the envelope Body should contain the expanded template.
	if !strings.HasPrefix(env.Body, "Please review the PR:") {
		t.Errorf("envelope.Body does not start with review template prefix: %q", env.Body)
	}
	// args = "fix login timeout" (mention + /review stripped).
	if !strings.Contains(env.Body, "fix login timeout") {
		t.Errorf("envelope.Body does not contain args: %q", env.Body)
	}
}

// TestHandle_KnownCommand_ColdPath verifies that a known command on the cold path
// (sandbox absent) publishes a SandboxCreate with the command's alias and profile.
func TestHandle_KnownCommand_ColdPath(t *testing.T) {
	opts := defaultOpts()
	opts.userLogin = "alice"
	opts.commentBody = "@mybot[bot] /review check the edge cases"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	commenter := &mockCommenter{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}

	// Cold path: resolver returns error (sandbox not found).
	h := buildHandlerWithCommands(testCommands(), "", commenter)
	h.Resolver = &mockResolver{resolveErr: &mockResolveError{}}
	h.SQS = sqsSender
	h.Publisher = publisher

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Commenter must NOT be called.
	if commenter.called {
		t.Error("Commenter must NOT be called on command cold path")
	}

	// Publisher MUST be called with command's alias and profile.
	if !publisher.called {
		t.Error("Publisher must be called on command cold path")
	}
	// "review" command overrides alias = "gh-review-alias", profile = "gh-review-profile".
	if publisher.alias != "gh-review-alias" {
		t.Errorf("Publisher alias=%q want gh-review-alias (command override)", publisher.alias)
	}
	if publisher.profile != "gh-review-profile" {
		t.Errorf("Publisher profile=%q want gh-review-profile (command override)", publisher.profile)
	}
}

// mockResolveError is a simple error type for cold-path testing.
type mockResolveError struct{}

func (e *mockResolveError) Error() string { return "alias not found" }

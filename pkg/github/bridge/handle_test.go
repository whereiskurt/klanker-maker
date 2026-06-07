package bridge_test

// handle_test.go — table-driven tests for WebhookHandler.Handle() branch coverage.
// Tests mock all AWS interfaces so no live calls are made.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// ============================================================
// Test doubles
// ============================================================

type mockSecretFetcher struct {
	secret string
	err    error
}

func (m *mockSecretFetcher) Fetch(_ context.Context) (string, error) {
	return m.secret, m.err
}

type mockBotLoginFetcher struct {
	login string
	err   error
}

func (m *mockBotLoginFetcher) Fetch(_ context.Context) (string, error) {
	return m.login, m.err
}

type mockNonceStore struct {
	// seen is the set of keys that have been stored.
	seen map[string]bool
	err  error
}

func newMockNonceStore() *mockNonceStore { return &mockNonceStore{seen: map[string]bool{}} }

func (m *mockNonceStore) CheckAndStore(_ context.Context, key string, _ int) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	if m.seen[key] {
		return true, nil
	}
	m.seen[key] = true
	return false, nil
}

type mockResolver struct {
	sandboxID string
	queueURL  string
	resolveErr error
	queueErr   error
	calls      []string
}

func (m *mockResolver) ResolveByAlias(_ context.Context, alias string) (string, error) {
	m.calls = append(m.calls, "resolve:"+alias)
	return m.sandboxID, m.resolveErr
}

func (m *mockResolver) GitHubQueueURL(_ context.Context, sandboxID string) (string, error) {
	m.calls = append(m.calls, "queue:"+sandboxID)
	return m.queueURL, m.queueErr
}

type mockPublisher struct {
	called bool
	alias  string
	profile string
	envelope string
	err    error
}

func (m *mockPublisher) PutSandboxCreate(_ context.Context, alias, profile, envJSON string) error {
	m.called = true
	m.alias = alias
	m.profile = profile
	m.envelope = envJSON
	return m.err
}

type mockSQS struct {
	called        bool
	queueURL      string
	body          string
	groupID       string
	deduplicationID string
	err           error
}

func (m *mockSQS) Send(_ context.Context, queueURL, body, groupID, dedupID string) error {
	m.called = true
	m.queueURL = queueURL
	m.body = body
	m.groupID = groupID
	m.deduplicationID = dedupID
	return m.err
}

type mockReactor struct {
	called        bool
	installID     string
	owner         string
	repo          string
	commentID     int64
	content       string
	err           error
}

func (m *mockReactor) AddReaction(_ context.Context, installID, owner, repo string, commentID int64, content string) error {
	m.called = true
	m.installID = installID
	m.owner = owner
	m.repo = repo
	m.commentID = commentID
	m.content = content
	return m.err
}

// ============================================================
// Test helpers
// ============================================================

const testSecret = "test-secret"
const testBotLogin = "mybot[bot]"
const testDeliveryGUID = "abc123"

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// buildPayload builds a minimal valid IssueCommentPayload JSON with the given overrides.
type payloadOpts struct {
	action      string
	userLogin   string
	userType    string
	commentBody string
	hasPR       bool
	installID   int64
	repo        string
}

func defaultOpts() payloadOpts {
	return payloadOpts{
		action:      "created",
		userLogin:   "alice",
		userType:    "User",
		commentBody: "@mybot[bot] please review",
		hasPR:       true,
		installID:   12345,
		repo:        "myorg/myrepo",
	}
}

func buildPayloadJSON(opts payloadOpts) []byte {
	type innerPR struct{}
	type user struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	type comment struct {
		ID      int64  `json:"id"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		User    user   `json:"user"`
	}
	type install struct {
		ID int64 `json:"id"`
	}
	type repo struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}

	p := map[string]any{
		"action": opts.action,
		"issue": map[string]any{
			"number": 42,
		},
		"comment": comment{
			ID:      99,
			Body:    opts.commentBody,
			HTMLURL: "https://github.com/owner/repo/issues/42#issuecomment-99",
			User:    user{Login: opts.userLogin, Type: opts.userType},
		},
		"installation": install{ID: opts.installID},
		"repository":   repo{FullName: opts.repo, DefaultBranch: "main"},
	}
	if opts.hasPR {
		issue := p["issue"].(map[string]any)
		issue["pull_request"] = map[string]string{"url": "https://github.com/myorg/myrepo/pull/42"}
	}
	b, _ := json.Marshal(p)
	return b
}

// buildHandler builds a WebhookHandler with the given mocks and a default valid config.
func buildHandler(secret *mockSecretFetcher, botLogin *mockBotLoginFetcher,
	nonces *mockNonceStore, resolver *mockResolver,
	publisher *mockPublisher, sqsSender *mockSQS, reactor bridge.GitHubReactor) *bridge.WebhookHandler {
	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "myrepo-alias", Profile: "myrepo-profile", Allow: []string{"alice"}},
	}
	return &bridge.WebhookHandler{
		Secret:         secret,
		BotLogin:       botLogin,
		Nonces:         nonces,
		Resolver:       resolver,
		Publisher:      publisher,
		SQS:            sqsSender,
		Reactor:        reactor,
		Entries:        entries,
		DefaultProfile: "default-profile",
		ResourcePrefix: "km",
		SandboxesTable: "km-sandboxes",
	}
}

func buildRequest(body []byte, opts ...func(h map[string]string)) bridge.WebhookRequest {
	headers := map[string]string{
		"x-hub-signature-256": signBody(testSecret, body),
		"x-github-event":      "issue_comment",
		"x-github-delivery":   testDeliveryGUID,
	}
	for _, fn := range opts {
		fn(headers)
	}
	return bridge.WebhookRequest{
		Headers: headers,
		RawBody: body,
		Body:    string(body),
	}
}

// ============================================================
// Handle() branch tests
// ============================================================

func TestHandle_BadSignature_Returns401(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body, func(h map[string]string) {
		h["x-hub-signature-256"] = "sha256=badbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadb"
	})

	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(),
		&mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/q"},
		&mockPublisher{},
		&mockSQS{},
		&mockReactor{},
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 401 {
		t.Errorf("StatusCode=%d want 401", resp.StatusCode)
	}
}

func TestHandle_SecretFetchError_Returns200(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	h := buildHandler(
		&mockSecretFetcher{err: errors.New("ssm unavailable")},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(),
		&mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/q"},
		&mockPublisher{},
		&mockSQS{},
		&mockReactor{},
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200 (secret fetch error must not 5xx)", resp.StatusCode)
	}
}

func TestHandle_ActionNotCreated_Returns200(t *testing.T) {
	opts := defaultOpts()
	opts.action = "deleted"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	nonces := newMockNonceStore()
	reactor := &mockReactor{}
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		nonces, &mockResolver{resolveErr: errors.New("not found")},
		&mockPublisher{}, &mockSQS{}, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	// Reactor must NOT be called (it's a drop, not a dispatch)
	if reactor.called {
		t.Error("Reactor should not be called on action-not-created drop")
	}
}

func TestHandle_BotComment_Returns200Drop(t *testing.T) {
	opts := defaultOpts()
	opts.userType = "Bot"
	opts.userLogin = "some-other-bot"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	reactor := &mockReactor{}
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), &mockResolver{resolveErr: errors.New("not found")},
		&mockPublisher{}, &mockSQS{}, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if reactor.called {
		t.Error("Reactor must not be called on bot-loop drop")
	}
}

func TestHandle_SelfComment_Returns200Drop(t *testing.T) {
	opts := defaultOpts()
	opts.userLogin = testBotLogin
	opts.userType = "Bot"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	reactor := &mockReactor{}
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), &mockResolver{resolveErr: errors.New("not found")},
		&mockPublisher{}, &mockSQS{}, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if reactor.called {
		t.Error("Reactor must not be called on self-comment drop")
	}
}

func TestHandle_NoPR_Returns200Drop(t *testing.T) {
	opts := defaultOpts()
	opts.hasPR = false
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	reactor := &mockReactor{}
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), &mockResolver{resolveErr: errors.New("not found")},
		&mockPublisher{}, &mockSQS{}, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200 (non-PR issue comment must drop)", resp.StatusCode)
	}
	if reactor.called {
		t.Error("Reactor must not be called for non-PR issue drop")
	}
}

func TestHandle_NoMention_Returns200Drop(t *testing.T) {
	opts := defaultOpts()
	opts.commentBody = "just a comment with no mention"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	reactor := &mockReactor{}
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), &mockResolver{resolveErr: errors.New("not found")},
		&mockPublisher{}, &mockSQS{}, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if reactor.called {
		t.Error("Reactor must not be called for no-mention drop")
	}
}

func TestHandle_SenderNotAllowlisted_SilentDrop(t *testing.T) {
	opts := defaultOpts()
	opts.userLogin = "unauthorized-user"
	body := buildPayloadJSON(opts)
	req := buildRequest(body)

	reactor := &mockReactor{}
	sqsSender := &mockSQS{}
	publisher := &mockPublisher{}
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), &mockResolver{resolveErr: errors.New("not found")},
		publisher, sqsSender, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200 (silent)", resp.StatusCode)
	}
	// CRITICAL: reactor MUST NOT be called for unauthorized sender (silent drop).
	if reactor.called {
		t.Error("Reactor MUST NOT be called for non-allowlisted sender (silent drop)")
	}
	if sqsSender.called {
		t.Error("SQS MUST NOT be called for non-allowlisted sender")
	}
	if publisher.called {
		t.Error("Publisher MUST NOT be called for non-allowlisted sender")
	}
}

func TestHandle_ReplayedDelivery_Returns200(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	nonces := newMockNonceStore()
	// Pre-seed the nonce so the second check returns replayed=true.
	_, _ = nonces.CheckAndStore(context.Background(), bridge.GitHubDeliveryNoncePrefix+testDeliveryGUID, 86400)

	reactor := &mockReactor{}
	sqsSender := &mockSQS{}
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		nonces, &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/q"},
		&mockPublisher{}, sqsSender, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if sqsSender.called {
		t.Error("SQS must not be called for replayed delivery")
	}
	if reactor.called {
		t.Error("Reactor must not be called for replayed delivery")
	}
}

func TestHandle_WarmPath_EnqueuesSQS_AndReacts(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/github-queue"}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}
	publisher := &mockPublisher{}

	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), resolver, publisher, sqsSender, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// SQS must be called with the github-inbound queue URL.
	if !sqsSender.called {
		t.Error("SQS Send must be called on warm path")
	}
	if sqsSender.queueURL != "https://sqs.example.com/github-queue" {
		t.Errorf("SQS queueURL=%q", sqsSender.queueURL)
	}

	// Verify envelope JSON is valid.
	var env bridge.GitHubEnvelope
	if err := json.Unmarshal([]byte(sqsSender.body), &env); err != nil {
		t.Fatalf("SQS body is not valid GitHubEnvelope JSON: %v", err)
	}
	if env.Source != "github" {
		t.Errorf("envelope.Source=%q want github", env.Source)
	}
	if env.Sender != "alice" {
		t.Errorf("envelope.Sender=%q want alice", env.Sender)
	}

	// Publisher must NOT be called on warm path.
	if publisher.called {
		t.Error("Publisher (cold create) must NOT be called on warm path")
	}

	// Reactor MUST be called (synchronous 👀 ACK).
	if !reactor.called {
		t.Error("Reactor must be called synchronously on warm path")
	}
	if reactor.content != "eyes" {
		t.Errorf("Reactor emoji=%q want eyes", reactor.content)
	}
}

func TestHandle_ColdPath_PublishesCreate_AndReacts(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolver{resolveErr: errors.New("alias not found")}
	publisher := &mockPublisher{}
	sqsSender := &mockSQS{}
	reactor := &mockReactor{}

	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), resolver, publisher, sqsSender, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}

	// Publisher must be called.
	if !publisher.called {
		t.Error("Publisher (SandboxCreate) must be called on cold path")
	}
	if publisher.alias != "myrepo-alias" {
		t.Errorf("Publisher alias=%q want myrepo-alias", publisher.alias)
	}
	if publisher.profile != "myrepo-profile" {
		t.Errorf("Publisher profile=%q want myrepo-profile", publisher.profile)
	}
	// Envelope JSON must be valid.
	var env bridge.GitHubEnvelope
	if err := json.Unmarshal([]byte(publisher.envelope), &env); err != nil {
		t.Fatalf("Publisher envelope not valid JSON: %v", err)
	}
	if env.Source != "github" {
		t.Errorf("envelope.Source=%q want github", env.Source)
	}

	// SQS must NOT be called on cold path.
	if sqsSender.called {
		t.Error("SQS must NOT be called on cold path")
	}

	// Reactor MUST be called (synchronous 👀 ACK).
	if !reactor.called {
		t.Error("Reactor must be called synchronously on cold path")
	}
}

func TestHandle_SQSError_Returns200(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/q"}
	sqsSender := &mockSQS{err: errors.New("sqs unavailable")}
	reactor := &mockReactor{}

	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), resolver, &mockPublisher{}, sqsSender, reactor,
	)
	resp := h.Handle(context.Background(), req)
	// MUST return 200 even on SQS error (GitHub redelivers 5xx with new GUID).
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200 (SQS error must not 5xx)", resp.StatusCode)
	}
}

func TestHandle_ReactorError_Returns200(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/q"}
	reactor := &mockReactor{err: errors.New("reaction failed")}

	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), resolver, &mockPublisher{}, &mockSQS{}, reactor,
	)
	resp := h.Handle(context.Background(), req)
	// Reactor error is non-fatal; must still return 200.
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200 (reactor error must not 5xx)", resp.StatusCode)
	}
}

func TestHandle_NonIssueCommentEvent_Returns200(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body, func(h map[string]string) {
		h["x-github-event"] = "push"
	})

	reactor := &mockReactor{}
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), &mockResolver{}, &mockPublisher{}, &mockSQS{}, reactor,
	)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
	if reactor.called {
		t.Error("Reactor must not be called for non-issue_comment events")
	}
}

// TestHandle_NilReactor_Warm verifies nil Reactor doesn't panic on warm path.
func TestHandle_NilReactor_Warm(t *testing.T) {
	body := buildPayloadJSON(defaultOpts())
	req := buildRequest(body)

	resolver := &mockResolver{sandboxID: "sb-123", queueURL: "https://sqs.example.com/q"}
	// Pass a nil interface (not typed nil) to test the nil-guard in Handle.
	h := buildHandler(
		&mockSecretFetcher{secret: testSecret},
		&mockBotLoginFetcher{login: testBotLogin},
		newMockNonceStore(), resolver, &mockPublisher{}, &mockSQS{},
		bridge.GitHubReactor(nil), // explicit nil interface
	)
	// Must not panic.
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode=%d want 200", resp.StatusCode)
	}
}

package bridge_test

// webhook_handler_phase116_test.go — Unit tests for the Phase 116 check: pre-filter.
//
// Tests:
//   GH-CHECK-PREFILTER-1: rule with check:, invoker returns triggered=true  → dispatch proceeds.
//   GH-CHECK-PREFILTER-2: rule with check:, invoker returns triggered=false → dispatch suppressed.
//   GH-CHECK-PREFILTER-3: rule with check:, invoker returns error           → dispatch fail-CLOSED.
//   GH-CHECK-PREFILTER-4: rule WITHOUT check: field                         → byte-identical to Phase 115.
//   GH-CHECK-PREFILTER-5: rule with check:, CheckInvoker is nil             → dispatch dropped (safe degradation).
//
// Mocks: reuse mockNonceStore, mockPublisher, mockSQS, mockSecretFetcher,
// mockBotLoginFetcher, mockResolver from handle_test.go (same package bridge_test).

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// =============================================================================
// Mock: LambdaInvoker
// =============================================================================

type mockLambdaInvoker struct {
	triggered bool
	err       error
	calls     []string // tracks which check names were invoked
}

func (m *mockLambdaInvoker) InvokeCheck(_ context.Context, name string, _ []byte) (bool, error) {
	m.calls = append(m.calls, name)
	return m.triggered, m.err
}

// =============================================================================
// Helpers
// =============================================================================

// newCheckPrefilterHandler builds a WebhookHandler pre-configured for event-route
// tests with the check pre-filter. It reuses the mockPublisher, mockNonceStore,
// mockSQS, and mockResolver from handle_test.go (same test package).
func newCheckPrefilterHandler(rules []bridge.EventRule, invoker bridge.LambdaInvoker) *bridge.WebhookHandler {
	return &bridge.WebhookHandler{
		Secret:   &mockSecretFetcher{secret: testSecret},
		BotLogin: &mockBotLoginFetcher{login: "km-bot[bot]"},
		Nonces:   newMockNonceStore(),
		Resolver: &mockResolver{},
		Publisher: &mockPublisher{},
		SQS:      &mockSQS{},
		EventRules:   rules,
		CheckInvoker: invoker,
	}
}

// repositoryCreatedPayloadForCheck builds a minimal "repository/created" payload
// for check pre-filter tests. Reuses repositoryCreatedPayload from
// webhook_handler_phase115_test.go (same test package).
func repositoryCreatedPayloadForCheck(repo string) []byte {
	type repoField struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		HTMLURL       string `json:"html_url"`
	}
	type userField struct {
		Login string `json:"login"`
	}
	type installField struct {
		ID int64 `json:"id"`
	}
	p := map[string]any{
		"action":       "created",
		"repository":   repoField{FullName: repo, DefaultBranch: "main", HTMLURL: "https://github.com/" + repo},
		"sender":       userField{Login: "octocat"},
		"installation": installField{ID: 123},
	}
	b, _ := json.Marshal(p)
	return b
}

// =============================================================================
// GH-CHECK-PREFILTER-1: triggered=true → dispatch proceeds
// =============================================================================

func TestCheckPrefilter_Triggered_DispatchProceeds(t *testing.T) {
	rule := bridge.EventRule{
		On:    "repository",
		Match: "owner/repo",
		Check: "wiz-intel",
		Alias: "", // cold-create path to simplify verification
		Prompt: "New repo created: {{repo}}",
	}
	invoker := &mockLambdaInvoker{triggered: true}
	pub := &mockPublisher{}
	h := &bridge.WebhookHandler{
		Secret:       &mockSecretFetcher{secret: testSecret},
		BotLogin:     &mockBotLoginFetcher{login: "km-bot[bot]"},
		Nonces:       newMockNonceStore(),
		Resolver:     &mockResolver{},
		Publisher:    pub,
		SQS:          &mockSQS{},
		EventRules:   []bridge.EventRule{rule},
		CheckInvoker: invoker,
	}
	body := repositoryCreatedPayloadForCheck("owner/repo")
	req := buildEventRequest("repository", "delivery-1", body)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(invoker.calls) != 1 || invoker.calls[0] != "wiz-intel" {
		t.Errorf("expected InvokeCheck called once with 'wiz-intel', got calls: %v", invoker.calls)
	}
	// Publisher.PutSandboxCreate must have been called (dispatch proceeded).
	if !pub.called {
		t.Error("expected PutSandboxCreate to be called when check triggers, but it was not")
	}
}

// =============================================================================
// GH-CHECK-PREFILTER-2: triggered=false → dispatch suppressed
// =============================================================================

func TestCheckPrefilter_NotTriggered_DispatchSuppressed(t *testing.T) {
	rule := bridge.EventRule{
		On:    "repository",
		Match: "owner/repo",
		Check: "wiz-intel",
		Alias: "",
		Prompt: "New repo: {{repo}}",
	}
	invoker := &mockLambdaInvoker{triggered: false}
	pub := &mockPublisher{}
	h := &bridge.WebhookHandler{
		Secret:       &mockSecretFetcher{secret: testSecret},
		BotLogin:     &mockBotLoginFetcher{login: "km-bot[bot]"},
		Nonces:       newMockNonceStore(),
		Resolver:     &mockResolver{},
		Publisher:    pub,
		SQS:          &mockSQS{},
		EventRules:   []bridge.EventRule{rule},
		CheckInvoker: invoker,
	}
	body := repositoryCreatedPayloadForCheck("owner/repo")
	req := buildEventRequest("repository", "delivery-2", body)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(invoker.calls) != 1 {
		t.Errorf("expected InvokeCheck called once, got: %v", invoker.calls)
	}
	// Publisher must NOT have been called (dispatch was suppressed).
	if pub.called {
		t.Error("expected PutSandboxCreate NOT to be called when check does not trigger, but it was")
	}
}

// =============================================================================
// GH-CHECK-PREFILTER-3: invoke error → fail-CLOSED (no dispatch)
// =============================================================================

func TestCheckPrefilter_InvokeError_FailClosed(t *testing.T) {
	rule := bridge.EventRule{
		On:    "repository",
		Match: "owner/repo",
		Check: "wiz-intel",
		Alias: "",
		Prompt: "New repo: {{repo}}",
	}
	invoker := &mockLambdaInvoker{triggered: false, err: errors.New("Lambda timeout")}
	pub := &mockPublisher{}
	h := &bridge.WebhookHandler{
		Secret:       &mockSecretFetcher{secret: testSecret},
		BotLogin:     &mockBotLoginFetcher{login: "km-bot[bot]"},
		Nonces:       newMockNonceStore(),
		Resolver:     &mockResolver{},
		Publisher:    pub,
		SQS:          &mockSQS{},
		EventRules:   []bridge.EventRule{rule},
		CheckInvoker: invoker,
	}
	body := repositoryCreatedPayloadForCheck("owner/repo")
	req := buildEventRequest("repository", "delivery-3", body)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 (not 500) even on check error, got %d", resp.StatusCode)
	}
	// Fail-CLOSED: no dispatch on error.
	if pub.called {
		t.Error("expected NO dispatch on invoke error (fail-CLOSED), but PutSandboxCreate was called")
	}
}

// =============================================================================
// GH-CHECK-PREFILTER-4: rule without Check → byte-identical to Phase 115
// =============================================================================

func TestCheckPrefilter_NoCheck_DispatchProceeds(t *testing.T) {
	// A rule without check: should dispatch exactly as in Phase 115.
	rule := bridge.EventRule{
		On:    "repository",
		Match: "owner/repo",
		// Check field absent (empty) → pre-filter disabled for this rule.
		Prompt: "New repo: {{repo}}",
	}
	invoker := &mockLambdaInvoker{triggered: true} // should never be called
	pub := &mockPublisher{}
	h := &bridge.WebhookHandler{
		Secret:       &mockSecretFetcher{secret: testSecret},
		BotLogin:     &mockBotLoginFetcher{login: "km-bot[bot]"},
		Nonces:       newMockNonceStore(),
		Resolver:     &mockResolver{},
		Publisher:    pub,
		SQS:          &mockSQS{},
		EventRules:   []bridge.EventRule{rule},
		CheckInvoker: invoker,
	}
	body := repositoryCreatedPayloadForCheck("owner/repo")
	req := buildEventRequest("repository", "delivery-4", body)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	// InvokeCheck must NOT have been called (no check configured for this rule).
	if len(invoker.calls) != 0 {
		t.Errorf("expected InvokeCheck NOT called when rule has no Check, got calls: %v", invoker.calls)
	}
	// Dispatch must still proceed.
	if !pub.called {
		t.Error("expected PutSandboxCreate to be called when no check pre-filter, but it was not")
	}
}

// =============================================================================
// GH-CHECK-PREFILTER-5: CheckInvoker nil with check: set → safe degradation (no dispatch)
// =============================================================================

func TestCheckPrefilter_CheckSet_InvokerNil_Dropped(t *testing.T) {
	// A rule with check: but nil CheckInvoker → warn + drop (safe degradation).
	rule := bridge.EventRule{
		On:    "repository",
		Match: "owner/repo",
		Check: "wiz-intel",
		Prompt: "New repo: {{repo}}",
	}
	pub := &mockPublisher{}
	h := &bridge.WebhookHandler{
		Secret:       &mockSecretFetcher{secret: testSecret},
		BotLogin:     &mockBotLoginFetcher{login: "km-bot[bot]"},
		Nonces:       newMockNonceStore(),
		Resolver:     &mockResolver{},
		Publisher:    pub,
		SQS:          &mockSQS{},
		EventRules:   []bridge.EventRule{rule},
		CheckInvoker: nil, // no invoker wired
	}
	body := repositoryCreatedPayloadForCheck("owner/repo")
	req := buildEventRequest("repository", "delivery-5", body)
	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	// When CheckInvoker is nil, dispatch is dropped (safer than proceeding blindly).
	if pub.called {
		t.Error("expected NO dispatch when CheckInvoker is nil, but PutSandboxCreate was called")
	}
}

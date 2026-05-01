package cmd_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

// ──────────────────────────────────────────────
// Fakes
// ──────────────────────────────────────────────

type fakeSlackInitAPI struct {
	authErr     error
	createID    string
	createErr   error
	inviteErr   error
	authCalls   int
	createCalls int
	inviteCalls int
}

func (f *fakeSlackInitAPI) AuthTest(_ context.Context) error {
	f.authCalls++
	return f.authErr
}

func (f *fakeSlackInitAPI) CreateChannel(_ context.Context, _ string) (string, error) {
	f.createCalls++
	return f.createID, f.createErr
}

func (f *fakeSlackInitAPI) InviteShared(_ context.Context, _, _ string) error {
	f.inviteCalls++
	return f.inviteErr
}

type fakeSSM struct {
	store map[string]string
	puts  []string
}

func newFakeSSM(initial map[string]string) *fakeSSM {
	m := make(map[string]string)
	for k, v := range initial {
		m[k] = v
	}
	return &fakeSSM{store: m}
}

func (f *fakeSSM) Get(_ context.Context, name string, _ bool) (string, error) {
	return f.store[name], nil
}

func (f *fakeSSM) Put(_ context.Context, name, value string, _ bool) error {
	if f.store == nil {
		f.store = make(map[string]string)
	}
	f.store[name] = value
	f.puts = append(f.puts, name)
	return nil
}

type fakeTerragrunt struct {
	applied []string
	outputs map[string]interface{}
	applyErr error
}

func (f *fakeTerragrunt) Apply(_ context.Context, m string) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applied = append(f.applied, m)
	return nil
}

func (f *fakeTerragrunt) Output(_ context.Context, dir string) (map[string]interface{}, error) {
	if f.outputs != nil {
		return f.outputs, nil
	}
	return map[string]interface{}{
		"function_url": map[string]interface{}{"value": "https://bridge.lambda.example.com/"},
	}, nil
}

type fakePrompter struct {
	responses map[string]string
	calls     int
	// stubResponses in order (used when key matching isn't needed)
	ordered []string
	idx     int
}

func (f *fakePrompter) PromptString(label string) (string, error) {
	f.calls++
	if f.responses != nil {
		if v, ok := f.responses[label]; ok {
			return v, nil
		}
	}
	if f.idx < len(f.ordered) {
		v := f.ordered[f.idx]
		f.idx++
		return v, nil
	}
	return "", nil
}

func (f *fakePrompter) PromptSecret(label string) (string, error) {
	f.calls++
	if f.responses != nil {
		if v, ok := f.responses[label]; ok {
			return v, nil
		}
	}
	if f.idx < len(f.ordered) {
		v := f.ordered[f.idx]
		f.idx++
		return v, nil
	}
	return "", nil
}

type fakeBridgePoster struct {
	resp     *slack.PostResponse
	err      error
	called   int
}

func (f *fakeBridgePoster) Post(_ context.Context, _ string, _ *slack.SlackEnvelope, _ []byte) (*slack.PostResponse, error) {
	f.called++
	if f.resp != nil {
		return f.resp, f.err
	}
	return &slack.PostResponse{OK: true, TS: "1234567890.000100"}, f.err
}

// buildSlackTestDeps returns a SlackCmdDeps wired with fakes ready for the happy path.
func buildSlackTestDeps(api *fakeSlackInitAPI, ssm *fakeSSM, tg *fakeTerragrunt, prompter *fakePrompter, poster *fakeBridgePoster) *cmd.SlackCmdDeps {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &cmd.SlackCmdDeps{
		NewSlackAPI: func(token string) cmd.SlackInitAPI {
			return api
		},
		SSM:        ssm,
		Terragrunt: tg,
		Prompter:   prompter,
		OperatorKeyLoader: func(ctx context.Context, region string) (ed25519.PrivateKey, error) {
			return priv, nil
		},
		BridgePoster: poster.Post,
		Region:       "use1",
		RepoRoot:     "/repo",
	}
}

// ──────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────

// 1. Happy path — empty SSM state: all params written, channel created, invite sent, Terraform applied.
func TestSlackInit_HappyPath_EmptyState(t *testing.T) {
	api := &fakeSlackInitAPI{createID: "C0123ABC"}
	ssm := newFakeSSM(nil)
	tg := &fakeTerragrunt{}
	prompter := &fakePrompter{ordered: []string{"xoxb-test-token", "ops@example.com"}}
	poster := &fakeBridgePoster{}

	deps := buildSlackTestDeps(api, ssm, tg, prompter, poster)
	opts := cmd.SlackInitOpts{SharedChannel: "km-notifications"}

	if err := cmd.RunSlackInit(context.Background(), deps, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Token stored
	if ssm.store["/km/slack/bot-token"] != "xoxb-test-token" {
		t.Errorf("bot-token not stored; got %q", ssm.store["/km/slack/bot-token"])
	}
	// Channel created and stored
	if api.createCalls != 1 {
		t.Errorf("CreateChannel calls = %d; want 1", api.createCalls)
	}
	if ssm.store["/km/slack/shared-channel-id"] != "C0123ABC" {
		t.Errorf("shared-channel-id not stored; got %q", ssm.store["/km/slack/shared-channel-id"])
	}
	// Invite sent
	if api.inviteCalls != 1 {
		t.Errorf("InviteShared calls = %d; want 1", api.inviteCalls)
	}
	// Terraform applied for both modules
	if len(tg.applied) != 2 {
		t.Errorf("Terragrunt.Apply calls = %d; want 2 (nonces + bridge)", len(tg.applied))
	}
	// bridge-url stored
	if ssm.store["/km/slack/bridge-url"] == "" {
		t.Errorf("bridge-url not stored")
	}
}

// 2. Idempotent — shared-channel-id and bridge-url already populated → skip create + invite + apply.
func TestSlackInit_Idempotent_SkipsChannelAndApply(t *testing.T) {
	api := &fakeSlackInitAPI{createID: "C0123ABC"}
	ssm := newFakeSSM(map[string]string{
		"/km/slack/shared-channel-id": "C0EXISTING",
		"/km/slack/bridge-url":        "https://existing.lambda.url/",
		"/km/slack/invite-email":      "already@example.com",
	})
	tg := &fakeTerragrunt{}
	prompter := &fakePrompter{ordered: []string{"xoxb-test-token"}}
	poster := &fakeBridgePoster{}

	deps := buildSlackTestDeps(api, ssm, tg, prompter, poster)
	opts := cmd.SlackInitOpts{SharedChannel: "km-notifications", Force: false}

	if err := cmd.RunSlackInit(context.Background(), deps, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if api.createCalls != 0 {
		t.Errorf("CreateChannel should not be called when channel already exists; got %d calls", api.createCalls)
	}
	if api.inviteCalls != 0 {
		t.Errorf("InviteShared should not be called when channel already exists; got %d calls", api.inviteCalls)
	}
	if len(tg.applied) != 0 {
		t.Errorf("Terragrunt.Apply should not be called when bridge-url exists; got %d calls", len(tg.applied))
	}
}

// 3. Force — pre-populated state + --force → CreateChannel called again.
func TestSlackInit_Force_RecreatesChannel(t *testing.T) {
	api := &fakeSlackInitAPI{createID: "CNEW"}
	ssm := newFakeSSM(map[string]string{
		"/km/slack/shared-channel-id": "COLD",
		"/km/slack/bridge-url":        "https://old.lambda.url/",
		"/km/slack/invite-email":      "old@example.com",
	})
	tg := &fakeTerragrunt{}
	prompter := &fakePrompter{ordered: []string{"xoxb-test-token"}}
	poster := &fakeBridgePoster{}

	deps := buildSlackTestDeps(api, ssm, tg, prompter, poster)
	opts := cmd.SlackInitOpts{SharedChannel: "km-notifications", Force: true, InviteEmail: "new@example.com"}

	if err := cmd.RunSlackInit(context.Background(), deps, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if api.createCalls != 1 {
		t.Errorf("CreateChannel should be called with --force; got %d calls", api.createCalls)
	}
	if ssm.store["/km/slack/shared-channel-id"] != "CNEW" {
		t.Errorf("shared-channel-id should be updated to CNEW; got %q", ssm.store["/km/slack/shared-channel-id"])
	}
}

// 4. Invalid token — AuthTest returns error → exit with "invalid Slack bot token".
func TestSlackInit_InvalidToken_Exits1(t *testing.T) {
	api := &fakeSlackInitAPI{authErr: &slack.SlackAPIError{Method: "auth.test", Code: "invalid_auth"}}
	ssm := newFakeSSM(nil)
	tg := &fakeTerragrunt{}
	prompter := &fakePrompter{ordered: []string{"xoxb-bad-token", "email@x.com"}}
	poster := &fakeBridgePoster{}

	deps := buildSlackTestDeps(api, ssm, tg, prompter, poster)
	opts := cmd.SlackInitOpts{SharedChannel: "km-notifications"}

	err := cmd.RunSlackInit(context.Background(), deps, opts)
	if err == nil {
		t.Fatal("expected error for invalid token, got nil")
	}
	if !strings.Contains(err.Error(), "invalid Slack bot token") {
		t.Errorf("error %q should contain 'invalid Slack bot token'", err.Error())
	}
}

// 5. Non-interactive — all flags provided → prompter never called.
func TestSlackInit_NonInteractive_FlagsBypass(t *testing.T) {
	api := &fakeSlackInitAPI{createID: "CFLAG"}
	ssm := newFakeSSM(nil)
	tg := &fakeTerragrunt{}
	prompter := &fakePrompter{}
	poster := &fakeBridgePoster{}

	deps := buildSlackTestDeps(api, ssm, tg, prompter, poster)
	opts := cmd.SlackInitOpts{
		BotToken:      "xoxb-flag-token",
		InviteEmail:   "flag@example.com",
		SharedChannel: "km-notifications",
	}

	if err := cmd.RunSlackInit(context.Background(), deps, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if prompter.calls != 0 {
		t.Errorf("prompter called %d times; want 0 (all flags provided)", prompter.calls)
	}
}

// 6. InviteShared returns not_allowed_token_type → error contains "Pro workspace".
func TestSlackInit_InviteShared_NotAllowed_ClearError(t *testing.T) {
	api := &fakeSlackInitAPI{
		createID:  "CNOTPRO",
		inviteErr: &slack.SlackAPIError{Method: "conversations.inviteShared", Code: "not_allowed_token_type"},
	}
	ssm := newFakeSSM(nil)
	tg := &fakeTerragrunt{}
	prompter := &fakePrompter{ordered: []string{"xoxb-not-pro", "email@x.com"}}
	poster := &fakeBridgePoster{}

	deps := buildSlackTestDeps(api, ssm, tg, prompter, poster)
	opts := cmd.SlackInitOpts{SharedChannel: "km-notifications"}

	err := cmd.RunSlackInit(context.Background(), deps, opts)
	if err == nil {
		t.Fatal("expected error for not_allowed_token_type, got nil")
	}
	if !strings.Contains(err.Error(), "Pro") {
		t.Errorf("error %q should mention 'Pro workspace'", err.Error())
	}
}

// 7. km slack test happy path — mock returns 200, last-test-timestamp written.
func TestSlackTest_HappyPath(t *testing.T) {
	ssm := newFakeSSM(map[string]string{
		"/km/slack/bridge-url":        "https://bridge.example.com/",
		"/km/slack/shared-channel-id": "C0TEST",
	})
	poster := &fakeBridgePoster{resp: &slack.PostResponse{OK: true, TS: "111.222"}}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	deps := &cmd.SlackCmdDeps{
		SSM:    ssm,
		OperatorKeyLoader: func(_ context.Context, _ string) (ed25519.PrivateKey, error) {
			return priv, nil
		},
		BridgePoster: poster.Post,
		Region:       "use1",
		RepoRoot:     "/repo",
	}

	var buf bytes.Buffer
	if err := cmd.RunSlackTest(context.Background(), deps, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "111.222") {
		t.Errorf("output %q should contain ts 111.222", buf.String())
	}
	if ssm.store["/km/slack/last-test-timestamp"] == "" {
		t.Error("last-test-timestamp not written to SSM")
	}
}

// 8. km slack test — bridge-url missing → exit 1 with "run km slack init first".
func TestSlackTest_NoBridgeURL_Exits1(t *testing.T) {
	ssm := newFakeSSM(nil)
	poster := &fakeBridgePoster{}
	deps := &cmd.SlackCmdDeps{
		SSM:    ssm,
		Region: "use1",
		BridgePoster: poster.Post,
	}

	var buf bytes.Buffer
	err := cmd.RunSlackTest(context.Background(), deps, &buf)
	if err == nil {
		t.Fatal("expected error for missing bridge-url, got nil")
	}
	if !strings.Contains(err.Error(), "km slack init") {
		t.Errorf("error %q should mention 'km slack init'", err.Error())
	}
}

// 9. km slack status — all keys populated → output contains workspace, channel, invite, bridge, timestamp.
func TestSlackStatus_PrintsSummary(t *testing.T) {
	ssm := newFakeSSM(map[string]string{
		"/km/slack/workspace":           `{"team_id":"T123","team_name":"AcmeCorp"}`,
		"/km/slack/shared-channel-id":   "CSTATUS",
		"/km/slack/invite-email":        "status@example.com",
		"/km/slack/bridge-url":          "https://bridge.example.com/",
		"/km/slack/last-test-timestamp": "2026-04-29T12:00:00Z",
	})
	deps := &cmd.SlackCmdDeps{
		SSM:    ssm,
		Region: "use1",
	}

	var buf bytes.Buffer
	if err := cmd.RunSlackStatus(context.Background(), deps, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"CSTATUS", "status@example.com", "https://bridge.example.com/", "2026-04-29T12:00:00Z"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; full output:\n%s", want, out)
		}
	}
}

// ──────────────────────────────────────────────
// Cobra wiring smoke test — km slack --help registers init/test/status/rotate-token
// ──────────────────────────────────────────────

func TestSlackCmd_Registered(t *testing.T) {
	cfg := &config.Config{}
	slackCmd := cmd.NewSlackCmd(cfg)
	var names []string
	for _, sub := range slackCmd.Commands() {
		names = append(names, sub.Name())
	}
	for _, want := range []string{"init", "test", "status", "rotate-token"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("subcommand %q not registered; found: %v", want, names)
		}
	}
}

// ──────────────────────────────────────────────
// km slack rotate-token tests (SLCK-13)
// ──────────────────────────────────────────────

// fakeRotateAPI wraps fakeSlackInitAPI and tracks AuthTest call count separately.
type fakeRotateAPI struct {
	authErr           error
	authTestCallCount int
}

func (f *fakeRotateAPI) AuthTest(_ context.Context) error {
	f.authTestCallCount++
	return f.authErr
}

func (f *fakeRotateAPI) CreateChannel(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (f *fakeRotateAPI) InviteShared(_ context.Context, _, _ string) error {
	return nil
}

// fakeBridgeColdStartCounter counts BridgeColdStart invocations.
type fakeBridgeColdStartCounter struct {
	callCount int
	err       error
}

func (f *fakeBridgeColdStartCounter) call(ctx context.Context) error {
	f.callCount++
	return f.err
}

// buildRotateTokenDeps wires rotate-token deps with caller-supplied fakes.
func buildRotateTokenDeps(
	api *fakeRotateAPI,
	ssmStore *fakeSSM,
	cs *fakeBridgeColdStartCounter,
	poster *fakeBridgePoster,
) *cmd.SlackCmdDeps {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &cmd.SlackCmdDeps{
		SSM: ssmStore,
		NewSlackAPI: func(token string) cmd.SlackInitAPI {
			return api
		},
		BridgeColdStart: cs.call,
		OperatorKeyLoader: func(_ context.Context, _ string) (ed25519.PrivateKey, error) {
			return priv, nil
		},
		BridgePoster: poster.Post,
		Region:       "use1",
		RepoRoot:     "/repo",
	}
}

// TestSlackRotateToken_HappyPath verifies the full 5-step flow succeeds:
// AuthTest called once (validation only, NOT for the smoke test), SSM.Put writes the
// new token, BridgeColdStart fires once, and the smoke test posts via BridgePoster.
func TestSlackRotateToken_HappyPath(t *testing.T) {
	ssmStore := newFakeSSM(map[string]string{
		"/km/slack/bridge-url":        "https://bridge.example.com/",
		"/km/slack/shared-channel-id": "C-SHARED",
	})
	api := &fakeRotateAPI{}
	cs := &fakeBridgeColdStartCounter{}
	poster := &fakeBridgePoster{resp: &slack.PostResponse{OK: true, TS: "ts-rotate-happy"}}

	deps := buildRotateTokenDeps(api, ssmStore, cs, poster)

	if err := cmd.RunSlackRotateToken(context.Background(), deps, cmd.SlackRotateTokenOpts{BotToken: "xoxb-new"}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// SSM must have the new token.
	if got := ssmStore.store["/km/slack/bot-token"]; got != "xoxb-new" {
		t.Errorf("SSM /km/slack/bot-token = %q; want xoxb-new", got)
	}
	// BridgeColdStart fires exactly once.
	if cs.callCount != 1 {
		t.Errorf("BridgeColdStart calls = %d; want 1", cs.callCount)
	}
	// AuthTest fires exactly once (validation only — smoke test goes through BridgePoster).
	if api.authTestCallCount != 1 {
		t.Errorf("AuthTest call count = %d; want 1 (validation only)", api.authTestCallCount)
	}
	// Smoke test (BridgePoster) fires exactly once.
	if poster.called != 1 {
		t.Errorf("BridgePoster calls = %d; want 1", poster.called)
	}
}

// TestSlackRotateToken_AuthTestFails verifies that an invalid token aborts before
// touching SSM, the cold-start, or the bridge poster.
func TestSlackRotateToken_AuthTestFails(t *testing.T) {
	ssmStore := newFakeSSM(nil)
	api := &fakeRotateAPI{authErr: &slack.SlackAPIError{Method: "auth.test", Code: "invalid_auth"}}
	cs := &fakeBridgeColdStartCounter{}
	poster := &fakeBridgePoster{}

	deps := buildRotateTokenDeps(api, ssmStore, cs, poster)

	err := cmd.RunSlackRotateToken(context.Background(), deps, cmd.SlackRotateTokenOpts{BotToken: "xoxb-revoked"})
	if err == nil {
		t.Fatal("expected error from auth.test failure, got nil")
	}
	// SSM must NOT have been written.
	if _, ok := ssmStore.store["/km/slack/bot-token"]; ok {
		t.Error("SSM /km/slack/bot-token should NOT be written on auth.test failure")
	}
	// Cold-start must NOT be called.
	if cs.callCount != 0 {
		t.Errorf("BridgeColdStart should not be called; got %d calls", cs.callCount)
	}
	// Smoke test (BridgePoster) must NOT be called.
	if poster.called != 0 {
		t.Errorf("BridgePoster should not be called; got %d calls", poster.called)
	}
}

// TestSlackRotateToken_ColdStartFailsButSmokePasses verifies that a cold-start
// failure is non-fatal: the token is still persisted, a warning is printed, and
// the smoke test continues (and succeeds), returning nil.
func TestSlackRotateToken_ColdStartFailsButSmokePasses(t *testing.T) {
	ssmStore := newFakeSSM(map[string]string{
		"/km/slack/bridge-url":        "https://bridge.example.com/",
		"/km/slack/shared-channel-id": "C-SHARED",
	})
	api := &fakeRotateAPI{}
	cs := &fakeBridgeColdStartCounter{err: errors.New("AccessDeniedException")}
	poster := &fakeBridgePoster{resp: &slack.PostResponse{OK: true, TS: "ts-coldfail"}}

	deps := buildRotateTokenDeps(api, ssmStore, cs, poster)

	if err := cmd.RunSlackRotateToken(context.Background(), deps, cmd.SlackRotateTokenOpts{BotToken: "xoxb-new"}); err != nil {
		t.Errorf("expected nil error (cold-start failure is non-fatal), got %v", err)
	}
	// Token must still be persisted even when cold-start fails.
	if got := ssmStore.store["/km/slack/bot-token"]; got != "xoxb-new" {
		t.Errorf("SSM /km/slack/bot-token must still be persisted; got %q", got)
	}
	// Cold-start was attempted (and failed).
	if cs.callCount != 1 {
		t.Errorf("BridgeColdStart should be called once; got %d calls", cs.callCount)
	}
	// Smoke test runs despite cold-start failure.
	if poster.called != 1 {
		t.Errorf("BridgePoster should still be called after cold-start failure; got %d calls", poster.called)
	}
}

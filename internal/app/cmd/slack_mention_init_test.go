package cmd_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// ────────────────────────────────────────────────────────────────────────────
// Phase 91 Plan 04 tests: bot-user-id SSM caching (POL-07, POL-08)
// ────────────────────────────────────────────────────────────────────────────

// fakeSSMCapturingSecure extends fakeSSM to record the secure flag per Put call.
// Used to assert that bot-user-id is stored as plain string (secure=false).
type fakeSSMCapturingSecure struct {
	*fakeSSM
	putSecure map[string]bool // key → secure flag of most-recent Put
}

func newFakeSSMCapturing(initial map[string]string) *fakeSSMCapturingSecure {
	return &fakeSSMCapturingSecure{
		fakeSSM:   newFakeSSM(initial),
		putSecure: make(map[string]bool),
	}
}

func (f *fakeSSMCapturingSecure) Put(ctx context.Context, name, value string, secure bool) error {
	f.putSecure[name] = secure
	return f.fakeSSM.Put(ctx, name, value, secure)
}

// fakeSSMWithBotUserIDError is an SSM that returns an error when Put is called
// for a key containing "bot-user-id", to test the non-fatal error path.
type fakeSSMWithBotUserIDError struct {
	*fakeSSM
}

func (f *fakeSSMWithBotUserIDError) Put(ctx context.Context, name, value string, secure bool) error {
	if strings.Contains(name, "bot-user-id") {
		return errors.New("simulated SSM Put error for bot-user-id")
	}
	return f.fakeSSM.Put(ctx, name, value, secure)
}

// TestRunSlackInit_BotUserIDCached verifies POL-07:
//   - RunSlackInit calls AuthTestWithUserID instead of AuthTest
//   - The returned UID is written to SSM at {prefix}slack/bot-user-id
//   - The SSM Put uses secure=false (plain string, not SecureString)
//   - RunSlackInit returns nil
func TestRunSlackInit_BotUserIDCached(t *testing.T) {
	api := &fakeSlackInitAPI{
		createID: "C-BOTUID",
		userID:   "UBOT_FAKE",
	}
	ssmCapturing := newFakeSSMCapturing(nil)
	tg := &fakeTerragrunt{}
	prompter := &fakePrompter{}
	poster := &fakeBridgePoster{}

	deps := buildSlackTestDepsWithCapturingSSM(api, ssmCapturing, tg, prompter, poster)
	opts := cmd.SlackInitOpts{
		BotToken:      "xoxb-test-token",
		InviteEmail:   "ops@example.com",
		SharedChannel: "km-notifications",
	}

	if err := cmd.RunSlackInit(context.Background(), deps, opts); err != nil {
		t.Fatalf("RunSlackInit returned error: %v", err)
	}

	// SSM must have bot-user-id with the sentinel UID.
	got := ssmCapturing.store["/km/slack/bot-user-id"]
	if got != "UBOT_FAKE" {
		t.Errorf("SSM /km/slack/bot-user-id = %q; want %q", got, "UBOT_FAKE")
	}

	// Must be plain string (not SecureString).
	if secure, ok := ssmCapturing.putSecure["/km/slack/bot-user-id"]; !ok {
		t.Error("SSM Put for /km/slack/bot-user-id was never called")
	} else if secure {
		t.Error("SSM Put for /km/slack/bot-user-id should use secure=false (plain string), got secure=true")
	}
}

// TestRunSlackInit_BotUserIDCached_PutErrorIsNonFatal verifies the WARN path:
// if the SSM Put for bot-user-id fails, RunSlackInit still returns nil.
// The bot-token persistence is the primary success criterion.
func TestRunSlackInit_BotUserIDCached_PutErrorIsNonFatal(t *testing.T) {
	api := &fakeSlackInitAPI{
		createID: "C-PUTER",
		userID:   "UBOT_ERR",
	}
	errSSM := &fakeSSMWithBotUserIDError{fakeSSM: newFakeSSM(nil)}
	tg := &fakeTerragrunt{}
	prompter := &fakePrompter{}
	poster := &fakeBridgePoster{}

	deps := buildSlackTestDepsWithCapturingSSM(api, errSSM, tg, prompter, poster)
	opts := cmd.SlackInitOpts{
		BotToken:      "xoxb-test-token",
		InviteEmail:   "ops@example.com",
		SharedChannel: "km-notifications",
	}

	// Must return nil even when bot-user-id Put fails.
	if err := cmd.RunSlackInit(context.Background(), deps, opts); err != nil {
		t.Errorf("RunSlackInit should return nil even when bot-user-id Put fails; got: %v", err)
	}
}

// TestRotateToken_BotUserIDCached verifies POL-08:
//   - RunSlackRotateToken calls AuthTestWithUserID after validating the new token
//   - The returned UID is written to SSM at {prefix}slack/bot-user-id
//   - The SSM Put uses secure=false (plain string)
//   - RunSlackRotateToken returns nil
func TestRotateToken_BotUserIDCached(t *testing.T) {
	api := &fakeRotateAPI{
		userID: "UBOT_ROTATED",
	}
	ssmCapturing := newFakeSSMCapturing(map[string]string{
		"/km/slack/bridge-url":        "https://bridge.example.com/",
		"/km/slack/shared-channel-id": "C-SHARED",
	})
	cs := &fakeBridgeColdStartCounter{}
	poster := &fakeBridgePoster{}

	deps := buildRotateTokenDepsCapturing(api, ssmCapturing, cs, poster)

	if err := cmd.RunSlackRotateToken(context.Background(), deps, cmd.SlackRotateTokenOpts{BotToken: "xoxb-new"}); err != nil {
		t.Fatalf("RunSlackRotateToken returned error: %v", err)
	}

	// SSM must have the bot-user-id written with the sentinel UID.
	got := ssmCapturing.store["/km/slack/bot-user-id"]
	if got != "UBOT_ROTATED" {
		t.Errorf("SSM /km/slack/bot-user-id = %q; want %q", got, "UBOT_ROTATED")
	}

	// Must be plain string (not SecureString).
	if secure, ok := ssmCapturing.putSecure["/km/slack/bot-user-id"]; !ok {
		t.Error("SSM Put for /km/slack/bot-user-id was never called")
	} else if secure {
		t.Error("SSM Put for /km/slack/bot-user-id should use secure=false (plain string), got secure=true")
	}
}

// TestRotateToken_BotUserIDCached_PutErrorIsNonFatal verifies the WARN path:
// if the SSM Put for bot-user-id fails during rotate-token, the command still
// returns nil (token persistence is the primary success criterion).
func TestRotateToken_BotUserIDCached_PutErrorIsNonFatal(t *testing.T) {
	api := &fakeRotateAPI{userID: "UBOT_ERR"}
	errSSM := &fakeSSMWithBotUserIDError{
		fakeSSM: newFakeSSM(map[string]string{
			"/km/slack/bridge-url":        "https://bridge.example.com/",
			"/km/slack/shared-channel-id": "C-SHARED",
		}),
	}
	cs := &fakeBridgeColdStartCounter{}
	poster := &fakeBridgePoster{}

	deps := buildRotateTokenDepsCapturing(api, errSSM, cs, poster)

	// Must return nil even when bot-user-id Put fails.
	if err := cmd.RunSlackRotateToken(context.Background(), deps, cmd.SlackRotateTokenOpts{BotToken: "xoxb-new"}); err != nil {
		t.Errorf("RunSlackRotateToken should return nil even when bot-user-id Put fails; got: %v", err)
	}
}

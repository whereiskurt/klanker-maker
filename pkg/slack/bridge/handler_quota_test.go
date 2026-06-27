// Package bridge_test — handler_quota_test.go
// Tests for BRG-01: Slack bridge calls quota.Record for ActionPost/ActionUpload.
// BLOCK trip → 429 + no user post; WARN trip → post + notice; FREEZE → latch + deny.
package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// quotaTrackSlack extends the existing fakeSlack with a notices slice so we can
// assert that control-plane trip notices are posted without counting toward user posts.
// Satisfies bridge.SlackPoster.
type quotaTrackSlack struct {
	posted  []string // user message calls
	notices []string // control-plane notice calls (ThreadTS != "" for post/upload)
	// The quota handler distinguishes notice posts (always with a non-empty threadTS
	// to reply in-thread) from user posts (which may have empty threadTS for top-level).
	// The handler calls PostMessage for both but with different threadTS conventions.
	// Here we capture ALL calls and separate them.
	allCalls []struct{ ch, subj, body, threadTS string }
}

func (f *quotaTrackSlack) PostMessage(_ context.Context, ch, subj, body, threadTS string) (string, error) {
	f.allCalls = append(f.allCalls, struct{ ch, subj, body, threadTS string }{ch, subj, body, threadTS})
	// Heuristic: notice messages start with 🛑 or ⚠️
	if strings.HasPrefix(body, "🛑") || strings.HasPrefix(body, "⚠") {
		f.notices = append(f.notices, body)
	} else {
		f.posted = append(f.posted, body)
	}
	return "1234.567", nil
}

func (f *quotaTrackSlack) ArchiveChannel(_ context.Context, ch string) error       { return nil }
func (f *quotaTrackSlack) GetPermalink(_ context.Context, ch, ts string) (string, error) {
	return "https://slack.com/archives/" + ch + "/p" + ts, nil
}
func (f *quotaTrackSlack) UpdateMessage(_ context.Context, _, ts, _ string) (string, error) {
	return ts, nil
}

// fakeActionLimits is a test double for bridge.ActionLimitsFetcher.
type fakeActionLimits struct {
	limitsJSON string
	err        error
}

func (f *fakeActionLimits) FetchLimits(ctx context.Context, sandboxID string) (string, error) {
	return f.limitsJSON, f.err
}

// TestQuotaRecord_BlockTrip (BRG-01) — when quota.Record returns a BLOCK decision,
// the handler must:
//  1. Return 429.
//  2. NOT call h.Slack.PostMessage for the user's message.
//  3. Post an in-thread control-plane trip notice ("🛑 Quota exceeded") via the
//     bridge's bot token (control-plane, NOT counted by quota.Record).
func TestQuotaRecord_BlockTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &quotaTrackSlack{}

	// Limits JSON: slack_post perHour=1 onBreach=block.
	limitsJSON := `{"slack_post":{"perHour":1,"onBreach":"block"}}`

	// Use a quota client that simulates a count of 2 (exceeds the limit of 1).
	quotaClient := bridge.NewFakeQuotaClient(2) // count=2 > limit=1 → exceeded → BLOCK

	h := &bridge.Handler{
		Now:        func() time.Time { return time.Unix(1714280400, 0) },
		Keys:       &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:     &fakeNonces{},
		Channels:   &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:      &fakeToken{tok: "xoxb-test"},
		Slack:      fs,
		Quota:      quotaClient,
		QuotaTable: "km-action-quota",
		Limits:     &fakeActionLimits{limitsJSON: limitsJSON},
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	// 1. 429 response for BLOCK.
	if resp.StatusCode != 429 {
		t.Errorf("BLOCK trip: want 429, got %d (body: %s)", resp.StatusCode, resp.Body)
	}

	// 2. User message NOT posted (only control-plane notice).
	if len(fs.posted) != 0 {
		t.Errorf("BLOCK trip: user message must not be posted; got %d posts: %v", len(fs.posted), fs.posted)
	}

	// 3. Control-plane notice posted.
	if len(fs.notices) == 0 {
		t.Errorf("BLOCK trip: expected a control-plane trip notice posted; allCalls=%v", fs.allCalls)
	}
}

// TestQuotaRecord_WarnTrip (BRG-01) — WARN trip → message still posted + notice posted.
func TestQuotaRecord_WarnTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &quotaTrackSlack{}

	limitsJSON := `{"slack_post":{"perHour":1,"onBreach":"warn"}}`
	quotaClient := bridge.NewFakeQuotaClient(2) // count=2 > limit=1 → exceeded → WARN

	h := &bridge.Handler{
		Now:        func() time.Time { return time.Unix(1714280400, 0) },
		Keys:       &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:     &fakeNonces{},
		Channels:   &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:      &fakeToken{tok: "xoxb-test"},
		Slack:      fs,
		Quota:      quotaClient,
		QuotaTable: "km-action-quota",
		Limits:     &fakeActionLimits{limitsJSON: limitsJSON},
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	// 200: message still flows in WARN mode.
	if resp.StatusCode != 200 {
		t.Errorf("WARN trip: want 200, got %d", resp.StatusCode)
	}
	// User message WAS posted.
	if len(fs.posted) != 1 {
		t.Errorf("WARN trip: user message must be posted; got %d", len(fs.posted))
	}
	// Control-plane notice also posted.
	if len(fs.notices) == 0 {
		t.Errorf("WARN trip: expected a control-plane notice; allCalls=%v", fs.allCalls)
	}
}

// fakeFreezer records FreezeSandbox calls for assertion.
type fakeFreezer struct {
	calls []struct{ sandboxID, reason, by string }
}

func (f *fakeFreezer) FreezeSandbox(_ context.Context, sandboxID, reason, by string) error {
	f.calls = append(f.calls, struct{ sandboxID, reason, by string }{sandboxID, reason, by})
	return nil
}

// TestQuotaRecord_FreezeTrip_AutoLatches (GAP-2) — when quota.Record returns a FREEZE decision,
// the handler must:
//  1. Return 429 (block the action).
//  2. Call h.Freezer.FreezeSandbox exactly once with by="auto:slack_post:hour".
//  3. NOT post the user message.
func TestQuotaRecord_FreezeTrip_AutoLatches(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &quotaTrackSlack{}
	ff := &fakeFreezer{}

	limitsJSON := `{"slack_post":{"perHour":1,"onBreach":"freeze"}}`
	quotaClient := bridge.NewFakeQuotaClient(2) // count=2 > limit=1 → exceeded → FREEZE

	h := &bridge.Handler{
		Now:        func() time.Time { return time.Unix(1714280400, 0) },
		Keys:       &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:     &fakeNonces{},
		Channels:   &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:      &fakeToken{tok: "xoxb-test"},
		Slack:      fs,
		Quota:      quotaClient,
		QuotaTable: "km-action-quota",
		Limits:     &fakeActionLimits{limitsJSON: limitsJSON},
		Freezer:    ff,
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	// 1. 429 response for FREEZE (block + latch).
	if resp.StatusCode != 429 {
		t.Errorf("FREEZE trip: want 429, got %d (body: %s)", resp.StatusCode, resp.Body)
	}

	// 2. FreezeSandbox called exactly once.
	if len(ff.calls) != 1 {
		t.Errorf("FREEZE trip: want exactly 1 FreezeSandbox call, got %d", len(ff.calls))
	} else {
		call := ff.calls[0]
		if call.sandboxID != "sb-abc123" {
			t.Errorf("FREEZE trip: FreezeSandbox sandboxID=%q, want %q", call.sandboxID, "sb-abc123")
		}
		if call.by != "auto:slack_post:hour" {
			t.Errorf("FREEZE trip: FreezeSandbox by=%q, want %q", call.by, "auto:slack_post:hour")
		}
		if call.reason == "" {
			t.Error("FREEZE trip: FreezeSandbox reason must be non-empty")
		}
	}

	// 3. User message NOT posted.
	if len(fs.posted) != 0 {
		t.Errorf("FREEZE trip: user message must not be posted; got %d posts: %v", len(fs.posted), fs.posted)
	}
}

// TestQuotaRecord_BlockTrip_NoFreeze — BLOCK trip must NOT call FreezeSandbox.
func TestQuotaRecord_BlockTrip_NoFreeze(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &quotaTrackSlack{}
	ff := &fakeFreezer{}

	limitsJSON := `{"slack_post":{"perHour":1,"onBreach":"block"}}`
	quotaClient := bridge.NewFakeQuotaClient(2) // count=2 > limit=1 → exceeded → BLOCK

	h := &bridge.Handler{
		Now:        func() time.Time { return time.Unix(1714280400, 0) },
		Keys:       &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:     &fakeNonces{},
		Channels:   &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:      &fakeToken{tok: "xoxb-test"},
		Slack:      fs,
		Quota:      quotaClient,
		QuotaTable: "km-action-quota",
		Limits:     &fakeActionLimits{limitsJSON: limitsJSON},
		Freezer:    ff,
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	// 429 (block).
	if resp.StatusCode != 429 {
		t.Errorf("BLOCK trip: want 429, got %d", resp.StatusCode)
	}

	// FreezeSandbox must NOT be called for BLOCK.
	if len(ff.calls) != 0 {
		t.Errorf("BLOCK trip: FreezeSandbox must NOT be called; got %d calls", len(ff.calls))
	}
}

// TestQuotaRecord_WarnTrip_NoFreeze — WARN trip must NOT call FreezeSandbox.
func TestQuotaRecord_WarnTrip_NoFreeze(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &quotaTrackSlack{}
	ff := &fakeFreezer{}

	limitsJSON := `{"slack_post":{"perHour":1,"onBreach":"warn"}}`
	quotaClient := bridge.NewFakeQuotaClient(2) // count=2 > limit=1 → exceeded → WARN

	h := &bridge.Handler{
		Now:        func() time.Time { return time.Unix(1714280400, 0) },
		Keys:       &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:     &fakeNonces{},
		Channels:   &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:      &fakeToken{tok: "xoxb-test"},
		Slack:      fs,
		Quota:      quotaClient,
		QuotaTable: "km-action-quota",
		Limits:     &fakeActionLimits{limitsJSON: limitsJSON},
		Freezer:    ff,
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	// 200 (warn — action still flows).
	if resp.StatusCode != 200 {
		t.Errorf("WARN trip: want 200, got %d", resp.StatusCode)
	}

	// FreezeSandbox must NOT be called for WARN.
	if len(ff.calls) != 0 {
		t.Errorf("WARN trip: FreezeSandbox must NOT be called; got %d calls", len(ff.calls))
	}
}

// TestQuotaRecord_NoLimits (BRG-01) — no configured limits → byte-identical to today (dormant).
func TestQuotaRecord_NoLimits(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &quotaTrackSlack{}

	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb-test"},
		Slack:    fs,
		// No Quota/Limits wired → dormant.
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Errorf("no limits: want 200, got %d", resp.StatusCode)
	}
	if len(fs.posted) != 1 {
		t.Errorf("no limits: user message must be posted; got %d", len(fs.posted))
	}
	// No notices.
	if len(fs.notices) != 0 {
		t.Errorf("no limits: no notices expected; got %d", len(fs.notices))
	}
	// Decode body to verify it contains ok:true and ts.
	var body map[string]interface{}
	_ = json.Unmarshal([]byte(resp.Body), &body)
	if body["ok"] != true {
		t.Errorf("no limits: response ok should be true")
	}
}

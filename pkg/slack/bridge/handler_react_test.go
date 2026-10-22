package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// fakeReactor is a SlackPoster that also implements ReactionPoster. It records
// every add/remove in order and can fail a given (verb, emoji) with a Slack
// error code, so the handler's idempotency mapping is tested against the error
// shape the real adapter produces ("bridge: reactions.add: already_reacted").
type fakeReactor struct {
	fakeSlack
	calls   []string          // "add:<emoji>" / "remove:<emoji>"
	fail    map[string]string // key "add:<emoji>" → Slack error code
	reactTS string
}

func (f *fakeReactor) AddReaction(_ context.Context, ch, ts, emoji string) error {
	f.reactTS = ts
	f.calls = append(f.calls, "add:"+emoji)
	if code, ok := f.fail["add:"+emoji]; ok {
		return errors.New("bridge: reactions.add: " + code)
	}
	return nil
}

func (f *fakeReactor) RemoveReaction(_ context.Context, ch, ts, emoji string) error {
	f.reactTS = ts
	f.calls = append(f.calls, "remove:"+emoji)
	if code, ok := f.fail["remove:"+emoji]; ok {
		return errors.New("bridge: reactions.remove: " + code)
	}
	return nil
}

func reactHandler(pub ed25519.PublicKey, fr *fakeReactor) *bridge.Handler {
	return &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    fr,
	}
}

func reactEnv(channel, ts, emoji string) *slack.SlackEnvelope {
	env := makeEnv(slack.ActionReact, "sb-abc123", channel)
	env.Body, env.Subject = "", ""
	env.MessageTS = ts
	env.Emoji = emoji
	return env
}

func TestHandleReact_Add_OwnChannel(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := &fakeReactor{}
	h := reactHandler(pub, fr)

	resp := h.Handle(context.Background(), signRequest(t, reactEnv("C0123ABC", "1714280400.000100", "white_check_mark"), priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s; want 200", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["ok"] != true || body["ts"] != "1714280400.000100" {
		t.Errorf("body = %v; want ok + the target ts", body)
	}
	if strings.Join(fr.calls, ",") != "add:white_check_mark" {
		t.Errorf("calls = %v; want exactly one add", fr.calls)
	}
	if fr.reactTS != "1714280400.000100" {
		t.Errorf("reaction targeted ts %q; want the envelope's message_ts", fr.reactTS)
	}
}

// Exclusive: the new state is added FIRST, then every other name in the set is
// removed, and the state being set is never removed even when it is listed.
func TestHandleReact_Exclusive_AddsThenRemovesOthers(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := &fakeReactor{fail: map[string]string{"remove:rotating_light": "no_reaction"}}
	h := reactHandler(pub, fr)

	env := reactEnv("C0123ABC", "1.1", "white_check_mark")
	env.Exclusive = []string{"hourglass_flowing_sand", "white_check_mark", "rotating_light"}
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s; want 200 (no_reaction on an exclusive remove is success)", resp.StatusCode, resp.Body)
	}
	want := "add:white_check_mark,remove:hourglass_flowing_sand,remove:rotating_light"
	if got := strings.Join(fr.calls, ","); got != want {
		t.Errorf("calls = %s; want %s", got, want)
	}
}

func TestHandleReact_Remove(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := &fakeReactor{}
	h := reactHandler(pub, fr)

	env := reactEnv("C0123ABC", "1.1", "hourglass_flowing_sand")
	env.Remove = true
	env.Exclusive = []string{"white_check_mark"} // ignored with --remove: one call only
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if got := strings.Join(fr.calls, ","); got != "remove:hourglass_flowing_sand" {
		t.Errorf("calls = %s; want a single remove", got)
	}
}

// Idempotency: a retry must never fail on its own earlier success.
func TestHandleReact_IdempotentCodesAreSuccess(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := &fakeReactor{fail: map[string]string{
		"add:white_check_mark":          "already_reacted",
		"remove:hourglass_flowing_sand": "no_reaction",
	}}
	h := reactHandler(pub, fr)

	resp := h.Handle(context.Background(), signRequest(t, reactEnv("C0123ABC", "1.1", "white_check_mark"), priv))
	if resp.StatusCode != 200 {
		t.Errorf("already_reacted: status = %d body=%s; want 200", resp.StatusCode, resp.Body)
	}
	env := reactEnv("C0123ABC", "1.1", "hourglass_flowing_sand")
	env.Remove = true
	env.Nonce = "ffeeddccbbaa99887766554433221100"
	resp = h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Errorf("no_reaction: status = %d body=%s; want 200", resp.StatusCode, resp.Body)
	}
}

// Any other Slack error surfaces with its code, as a 502, and stops the fan-out.
func TestHandleReact_OtherSlackErrorSurfacesCode(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := &fakeReactor{fail: map[string]string{"add:white_check_mark": "message_not_found"}}
	h := reactHandler(pub, fr)

	env := reactEnv("C0123ABC", "1.1", "white_check_mark")
	env.Exclusive = []string{"rotating_light"}
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 502 {
		t.Fatalf("status = %d body=%s; want 502", resp.StatusCode, resp.Body)
	}
	if responseBody(t, resp)["error"] != "message_not_found" {
		t.Errorf("error = %v; want the Slack code", responseBody(t, resp)["error"])
	}
	if len(fr.calls) != 1 {
		t.Errorf("calls = %v; a failed add must not go on to the exclusive removes", fr.calls)
	}
	// missing_scope is the operator-actionable one the design names explicitly.
	fr2 := &fakeReactor{fail: map[string]string{"add:white_check_mark": "missing_scope"}}
	h2 := reactHandler(pub, fr2)
	resp = h2.Handle(context.Background(), signRequest(t, reactEnv("C0123ABC", "1.1", "white_check_mark"), priv))
	if responseBody(t, resp)["error"] != "missing_scope" {
		t.Errorf("error = %v; want missing_scope, not a generic code", responseBody(t, resp)["error"])
	}
}

// A sandbox can only react in its own channel. A thread-anchored envelope does
// not help: react is not in the routed-thread carve-out (post/upload only).
func TestHandleReact_ForeignChannelRefused(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := &fakeReactor{}
	h := reactHandler(pub, fr)
	h.Threads = &routedThreads{rows: map[string]string{"C0OTHER|1.1": "sb-abc123"}}

	env := reactEnv("C0OTHER", "1.1", "white_check_mark")
	env.ThreadTS = "1.1"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "channel_mismatch" {
		t.Fatalf("status = %d body=%s; want 403 channel_mismatch", resp.StatusCode, resp.Body)
	}
	if len(fr.calls) != 0 {
		t.Errorf("Slack was called %v on a refused envelope", fr.calls)
	}
}

// Server-side validation holds even when the client is bypassed.
func TestHandleReact_BadInputs(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	cases := []struct {
		name     string
		mutate   func(*slack.SlackEnvelope)
		wantCode string
	}{
		{"missing ts", func(e *slack.SlackEnvelope) { e.MessageTS = "" }, "missing_message_ts"},
		{"space in name", func(e *slack.SlackEnvelope) { e.Emoji = "rotating light" }, "bad_emoji"},
		{"too long", func(e *slack.SlackEnvelope) { e.Emoji = strings.Repeat("a", 101) }, "bad_emoji"},
		{"empty name", func(e *slack.SlackEnvelope) { e.Emoji = "" }, "bad_emoji"},
		{"bad exclusive name", func(e *slack.SlackEnvelope) { e.Exclusive = []string{"ok_name", "<@U1>"} }, "bad_emoji"},
		{"too many exclusive", func(e *slack.SlackEnvelope) {
			for i := 0; i <= slack.MaxExclusiveReactions; i++ {
				e.Exclusive = append(e.Exclusive, strings.Repeat("x", i+1))
			}
		}, "too_many_exclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeReactor{}
			h := reactHandler(pub, fr)
			env := reactEnv("C0123ABC", "1.1", "white_check_mark")
			tc.mutate(env)
			resp := h.Handle(context.Background(), signRequest(t, env, priv))
			if resp.StatusCode != 400 {
				t.Fatalf("status = %d body=%s; want 400", resp.StatusCode, resp.Body)
			}
			if got := responseBody(t, resp)["error"]; got != tc.wantCode {
				t.Errorf("error = %v; want %s", got, tc.wantCode)
			}
			if len(fr.calls) != 0 {
				t.Errorf("Slack was called %v on invalid input", fr.calls)
			}
		})
	}
}

// Colons are stripped server-side too, so `:white_check_mark:` reacts as the bare name.
func TestHandleReact_ColonsStripped(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fr := &fakeReactor{}
	h := reactHandler(pub, fr)
	env := reactEnv("C0123ABC", "1.1", ":white_check_mark:")
	env.Exclusive = []string{":hourglass_flowing_sand:"}
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if got := strings.Join(fr.calls, ","); got != "add:white_check_mark,remove:hourglass_flowing_sand" {
		t.Errorf("calls = %s", got)
	}
}

// A poster without ReactionPoster (an older adapter) answers 500 react_unsupported,
// never a silent 200.
func TestHandleReact_PosterWithoutReactions(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub) // fakeSlack: no AddReaction/RemoveReaction
	resp := h.Handle(context.Background(), signRequest(t, reactEnv("C0123ABC", "1.1", "white_check_mark"), priv))
	if resp.StatusCode != 500 || responseBody(t, resp)["error"] != "react_unsupported" {
		t.Fatalf("status = %d body=%s; want 500 react_unsupported", resp.StatusCode, resp.Body)
	}
}

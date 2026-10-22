package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// fakeBroadcaster is a block-capable poster that also implements BroadcastPoster
// and records the broadcast calls separately from the plain ones.
type fakeBroadcaster struct {
	fakeBlockPoster
	broadcastCalls []fakePostMessageBlocksCall
}

func (f *fakeBroadcaster) PostMessageBroadcast(_ context.Context, ch, subj, body, blocksJSON, threadTS string) (string, error) {
	f.broadcastCalls = append(f.broadcastCalls, fakePostMessageBlocksCall{ch, subj, body, blocksJSON, threadTS})
	return "bcast.ts", nil
}

func broadcastHandler(pub ed25519.PublicKey, poster bridge.SlackPoster) *bridge.Handler {
	return &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    poster,
	}
}

// A threaded post with broadcast set goes through PostMessageBroadcast, carrying
// the blocks when present.
func TestHandler_Post_Broadcast_RoutesToBroadcastPoster(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fb := &fakeBroadcaster{}
	h := broadcastHandler(pub, fb)

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	env.ThreadTS = "1.1"
	env.Broadcast = true
	env.Blocks = `[{"type":"divider"}]`
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if responseBody(t, resp)["ts"] != "bcast.ts" {
		t.Errorf("ts = %v; want the broadcast poster's ts", responseBody(t, resp)["ts"])
	}
	if len(fb.broadcastCalls) != 1 || len(fb.postMessageCalls) != 0 || len(fb.postMessageBlocksCalls) != 0 {
		t.Fatalf("broadcast=%d plain=%d blocks=%d; want exactly one broadcast call", len(fb.broadcastCalls), len(fb.postMessageCalls), len(fb.postMessageBlocksCalls))
	}
	c := fb.broadcastCalls[0]
	if c.threadTS != "1.1" || c.blocksJSON != env.Blocks || c.body != env.Body {
		t.Errorf("broadcast call = %+v", c)
	}
}

// Without a thread the flag means nothing: the ordinary post path runs.
func TestHandler_Post_Broadcast_IgnoredWithoutThread(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fb := &fakeBroadcaster{}
	h := broadcastHandler(pub, fb)

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	env.Broadcast = true
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if len(fb.broadcastCalls) != 0 || len(fb.postMessageCalls) != 1 {
		t.Errorf("broadcast=%d plain=%d; want the plain path", len(fb.broadcastCalls), len(fb.postMessageCalls))
	}
}

// A poster without BroadcastPoster still posts (the reply lands in the thread);
// the broadcast is degraded, not fatal, because the message itself is the point.
func TestHandler_Post_Broadcast_DegradesWithoutBroadcastPoster(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fp := &fakeBlockPoster{returnTS: "plain.ts"}
	h := broadcastHandler(pub, fp)

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	env.ThreadTS = "1.1"
	env.Broadcast = true
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 || responseBody(t, resp)["ts"] != "plain.ts" {
		t.Fatalf("status = %d body=%s; want 200 via the plain path", resp.StatusCode, resp.Body)
	}
	if len(fp.postMessageCalls) != 1 {
		t.Errorf("plain calls = %d; want 1", len(fp.postMessageCalls))
	}
}

// Broadcast never widens authorization: a routed foreign thread still admits the
// post (post is in the carve-out) and the broadcast flag rides along unchanged.
func TestHandler_Post_Broadcast_FalseLeavesPlainPathUntouched(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fb := &fakeBroadcaster{}
	h := broadcastHandler(pub, fb)

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	env.ThreadTS = "1.1"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if len(fb.broadcastCalls) != 0 || len(fb.postMessageCalls) != 1 {
		t.Errorf("broadcast=%d plain=%d; a threaded post without the flag must not broadcast", len(fb.broadcastCalls), len(fb.postMessageCalls))
	}
}

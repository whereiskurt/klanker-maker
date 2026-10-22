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

type fakeBlockUpdater struct {
	fakeBlockPoster
	textUpdates  []string // channel|ts|text
	blockUpdates []string // channel|ts|text|blocks
}

func (f *fakeBlockUpdater) UpdateMessage(_ context.Context, ch, ts, text string) (string, error) {
	f.textUpdates = append(f.textUpdates, ch+"|"+ts+"|"+text)
	return ts, nil
}

func (f *fakeBlockUpdater) UpdateMessageBlocks(_ context.Context, ch, ts, text, blocksJSON string) (string, error) {
	f.blockUpdates = append(f.blockUpdates, ch+"|"+ts+"|"+text+"|"+blocksJSON)
	return ts, nil
}

func updateHandler(pub ed25519.PublicKey, poster bridge.SlackPoster) *bridge.Handler {
	return &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    poster,
	}
}

func updateEnv(text, blocks, subject string) *slack.SlackEnvelope {
	env := makeEnv(slack.ActionUpdate, "sb-abc123", "C0123ABC")
	env.Body = ""
	env.Subject = subject
	env.MessageTS = "1.1"
	env.Text = text
	env.Blocks = blocks
	return env
}

func TestHandler_Update_WithBlocks_RoutesToBlockUpdater(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fu := &fakeBlockUpdater{}
	h := updateHandler(pub, fu)
	resp := h.Handle(context.Background(), signRequest(t, updateEnv("fallback", `[{"type":"divider"}]`, "ignored"), priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if len(fu.blockUpdates) != 1 || len(fu.textUpdates) != 0 {
		t.Fatalf("block=%d text=%d; want exactly one block update", len(fu.blockUpdates), len(fu.textUpdates))
	}
	// Subject is ignored with blocks (a header block expresses it), as for post.
	if fu.blockUpdates[0] != `C0123ABC|1.1|fallback|[{"type":"divider"}]` {
		t.Errorf("block update = %s", fu.blockUpdates[0])
	}
}

// Text mode folds the subject into a bold header, the same shape post uses.
func TestHandler_Update_TextWithSubject_FoldsHeader(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fu := &fakeBlockUpdater{}
	h := updateHandler(pub, fu)
	resp := h.Handle(context.Background(), signRequest(t, updateEnv("body", "", "Subj"), priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if len(fu.textUpdates) != 1 || fu.textUpdates[0] != "C0123ABC|1.1|*Subj*\n\nbody" {
		t.Errorf("text updates = %v; want the folded bold header", fu.textUpdates)
	}
}

// No subject, no blocks: byte-identical to the pre-change update.
func TestHandler_Update_TextOnly_Unchanged(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fu := &fakeBlockUpdater{}
	h := updateHandler(pub, fu)
	resp := h.Handle(context.Background(), signRequest(t, updateEnv("body", "", ""), priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, resp.Body)
	}
	if len(fu.textUpdates) != 1 || fu.textUpdates[0] != "C0123ABC|1.1|body" {
		t.Errorf("text updates = %v", fu.textUpdates)
	}
}

// A poster without BlockUpdater degrades to the text update (the fallback text
// is what gets shown), never a 500.
func TestHandler_Update_WithBlocks_DegradesWithoutBlockUpdater(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub) // fakeSlack: UpdateMessage only
	resp := h.Handle(context.Background(), signRequest(t, updateEnv("fallback", `[{"type":"divider"}]`, ""), priv))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s; want 200 via UpdateMessage", resp.StatusCode, resp.Body)
	}
}

func TestSlackPosterAdapter_ImplementsBlockUpdater(t *testing.T) {
	var _ bridge.BlockUpdater = (*bridge.SlackPosterAdapter)(nil)
}

// chat.update wire format: blocks ride beside text when present and are absent
// otherwise (Slack removes existing blocks on a text-only update by itself).
func TestSlackPosterAdapter_UpdateMessageBlocks_WireFormat(t *testing.T) {
	ad, paths, bodies := reactTestAdapter(t, map[string]string{"/chat.update": `{"ok":true,"ts":"1.1"}`})
	if _, err := ad.UpdateMessageBlocks(context.Background(), "C01", "1.1", "fb", `[{"type":"divider"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := ad.UpdateMessage(context.Background(), "C01", "1.1", "plain"); err != nil {
		t.Fatal(err)
	}
	if (*paths)[0] != "/chat.update" || (*paths)[1] != "/chat.update" {
		t.Fatalf("paths = %v", *paths)
	}
	b0, b1 := (*bodies)[0], (*bodies)[1]
	if b0["channel"] != "C01" || b0["ts"] != "1.1" || b0["text"] != "fb" || b0["blocks"] == nil {
		t.Errorf("blocks update body = %v", b0)
	}
	if _, has := b1["blocks"]; has || b1["text"] != "plain" {
		t.Errorf("text update body = %v; must not carry blocks", b1)
	}
}

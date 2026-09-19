package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// captureBridge returns a stub bridge that records the envelope it received.
func captureBridge(t *testing.T) (*httptest.Server, *slack.SlackEnvelope) {
	t.Helper()
	got := &slack.SlackEnvelope{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("decode envelope: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"1.2"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// Every render mode must carry the mention in the plain `text` — Slack builds
// the push notification from `text` when blocks are present, so a mention that
// lives only in a block is visible but silent.
func TestRunWith_MentionInTextForEveryRenderMode(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "# Done\n\nfixed the tests")
	for _, mode := range []string{"plain", "mrkdwn", "blocks", "blocks-rich"} {
		t.Run(mode, func(t *testing.T) {
			srv, env := captureBridge(t)
			if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", mode, "<@U0KURT>", "<!here>"); err != nil {
				t.Fatalf("runWith: %v", err)
			}
			if !strings.HasPrefix(env.Body, "<@U0KURT> <!here>\n") {
				t.Errorf("[%s] body does not start with the mention line: %q", mode, env.Body)
			}
			if strings.Contains(env.Body, "&lt;@") {
				t.Errorf("[%s] mention was HTML-escaped in body: %q", mode, env.Body)
			}
		})
	}
}

// In block modes the mention also leads the block array as a section/mrkdwn
// element — the one Block Kit element documented to resolve <@U…>.
func TestRunWith_MentionLeadsBlocks(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "# Done\n\nfixed the tests")
	for _, mode := range []string{"blocks", "blocks-rich"} {
		t.Run(mode, func(t *testing.T) {
			srv, env := captureBridge(t)
			if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", mode, "<@U0KURT>"); err != nil {
				t.Fatalf("runWith: %v", err)
			}
			if env.Blocks == "" {
				t.Fatalf("[%s] no blocks sent", mode)
			}
			var blocks []map[string]any
			if err := json.Unmarshal([]byte(env.Blocks), &blocks); err != nil {
				t.Fatalf("blocks not JSON: %v", err)
			}
			first := blocks[0]
			text, _ := first["text"].(map[string]any)
			if first["type"] != "section" || text["type"] != "mrkdwn" || text["text"] != "<@U0KURT>" {
				t.Errorf("[%s] first block = %v; want section/mrkdwn <@U0KURT>", mode, first)
			}
		})
	}
}

// No --mention ⇒ byte-identical to before: no preamble, no extra block.
func TestRunWith_NoMentionIsUnchanged(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "plain body")
	srv, env := captureBridge(t)
	if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", "plain"); err != nil {
		t.Fatalf("runWith: %v", err)
	}
	if env.Body != "plain body" {
		t.Errorf("body altered without --mention: %q", env.Body)
	}
}

// runPost rejects a name at parse time (exit 2) — never posts the literal "@Ron".
func TestRunPost_MentionNameRejected(t *testing.T) {
	bodyPath := writeTmpBody(t, "x")
	var stderr bytes.Buffer
	code := runPost([]string{"--channel", "C123", "--body", bodyPath, "--mention", "Ron"}, &stderr)
	if code != 2 {
		t.Fatalf("exit code = %d; want 2 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Ron") {
		t.Errorf("stderr does not name the bad entry: %s", stderr.String())
	}
}

// --mention may be repeated and comma-joined; both forms reach the same list.
func TestMentionFlag_RepeatAndComma(t *testing.T) {
	var m mentionFlag
	for _, v := range []string{"U1,U2", "here"} {
		if err := m.Set(v); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(m, "|"); got != "U1,U2|here" {
		t.Errorf("mentionFlag = %q", got)
	}
	if m.String() == "" {
		t.Error("String() empty")
	}
}

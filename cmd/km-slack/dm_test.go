package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

func TestRunDM_RejectsNamesAndMissingText(t *testing.T) {
	var errb bytes.Buffer
	for _, bad := range []string{"kurt", "here", "channel", "<@U0KURT>", "@U0KURT"} {
		errb.Reset()
		if rc := runDM([]string{"--to", bad, "--text", "hi"}, &errb); rc != 2 || !strings.Contains(errb.String(), "user id") {
			t.Fatalf("--to %q must be rejected at parse time: rc=%d %s", bad, rc, errb.String())
		}
	}
	errb.Reset()
	if rc := runDM([]string{"--to", "U0KURT"}, &errb); rc != 2 {
		t.Fatalf("missing --text/--body must be rc 2, got %d %s", rc, errb.String())
	}
	errb.Reset()
	t.Setenv("KM_SLACK_CHANNEL_ID", "")
	t.Setenv("KM_SLACK_THREAD_TS", "")
	if rc := runDM([]string{"--to", "U0KURT", "--text", "hi", "--from-thread"}, &errb); rc != 2 || !strings.Contains(errb.String(), "KM_SLACK_THREAD_TS") {
		t.Fatalf("--from-thread without the turn env must be rc 2 naming the var: rc=%d %s", rc, errb.String())
	}
}

func dmBridge(t *testing.T, got *slack.SlackEnvelope, ts string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		w.Write([]byte(`{"ok":true,"ts":"` + ts + `","channel_id":"D0KURT"}`))
	}))
}

func TestRunDMWith_EnvelopeShapeAndNoMarker(t *testing.T) {
	var got slack.SlackEnvelope
	srv := dmBridge(t, &got, "1.5")
	defer srv.Close()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	markers := t.TempDir()
	t.Setenv("KM_SLACK_POSTED_DIR", markers)

	ch, ts, err := runDMWith(context.Background(), priv, "sb-1", srv.URL, dmOptions{to: "U0KURT", text: "hello", render: "plain"})
	if err != nil || ch != "D0KURT" || ts != "1.5" {
		t.Fatalf("got %q %q %v", ch, ts, err)
	}
	if got.Action != slack.ActionDM || got.TargetUser != "U0KURT" || got.Body != "hello" || got.Channel != "" || got.ThreadTS != "" {
		t.Fatalf("envelope %+v", got)
	}
	if entries, _ := os.ReadDir(markers); len(entries) != 0 {
		t.Fatalf("a DM is top-level and must write no posted marker: %v", entries)
	}

	_, _, err = runDMWith(context.Background(), priv, "sb-1", srv.URL, dmOptions{to: "U0KURT", text: "hello", render: "plain", fromChannel: "C1", fromThread: "1.0"})
	if err != nil || got.Channel != "C1" || got.ThreadTS != "1.0" {
		t.Fatalf("--from-thread must carry channel+thread: %+v %v", got, err)
	}
}

func TestRunDMWith_BodyFileAndBlocks(t *testing.T) {
	var got slack.SlackEnvelope
	srv := dmBridge(t, &got, "1.6")
	defer srv.Close()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := filepath.Join(t.TempDir(), "body.md")
	os.WriteFile(f, []byte("# Summary\n\n- one\n- two\n"), 0o644)
	if _, _, err := runDMWith(context.Background(), priv, "sb-1", srv.URL, dmOptions{to: "U0KURT", bodyPath: f, render: "blocks-rich"}); err != nil {
		t.Fatal(err)
	}
	if got.Blocks == "" {
		t.Fatalf("blocks-rich must populate Blocks: %+v", got)
	}
}

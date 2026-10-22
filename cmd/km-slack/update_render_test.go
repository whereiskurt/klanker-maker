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

func updateBridge(t *testing.T, rejectBlocks bool) (*httptest.Server, *[]slack.SlackEnvelope) {
	t.Helper()
	var envs []slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		_ = json.NewDecoder(r.Body).Decode(&env)
		envs = append(envs, env)
		w.Header().Set("Content-Type", "application/json")
		if rejectBlocks && env.Blocks != "" {
			w.WriteHeader(502)
			fmt.Fprintln(w, `{"ok":false,"error":"invalid_blocks"}`)
			return
		}
		fmt.Fprintln(w, `{"ok":true,"ts":"1.1"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &envs
}

// --render blocks-rich on update carries the Block Kit array and the rendered
// fallback text, exactly as post does, so an edit can reproduce a rich post.
func TestUpdate_RenderRich_CarriesBlocksAndFallback(t *testing.T) {
	_, priv := genKey(t)
	srv, envs := updateBridge(t, false)
	err := runUpdateOpts(context.Background(), priv, "sb-test", srv.URL, updateOptions{
		channel: "C1", ts: "1.1", body: "# Verdict\n\nNot **vulnerable**.", render: "blocks-rich", subject: "TIC",
	})
	if err != nil {
		t.Fatal(err)
	}
	e := (*envs)[0]
	if e.Action != slack.ActionUpdate || e.MessageTS != "1.1" || e.Channel != "C1" {
		t.Errorf("envelope = %+v", e)
	}
	if e.Blocks == "" || !strings.Contains(e.Blocks, `"type"`) {
		t.Errorf("Blocks empty; want a Block Kit array: %+v", e)
	}
	if e.Text == "" || strings.Contains(e.Text, "**") {
		t.Errorf("Text = %q; want the rendered fallback (mrkdwn, not raw markdown)", e.Text)
	}
	if e.Subject != "TIC" {
		t.Errorf("Subject = %q; want TIC", e.Subject)
	}
}

// plain is byte-for-byte what update sent before: raw text, no blocks, no subject.
func TestUpdate_Plain_IsLegacyEnvelope(t *testing.T) {
	_, priv := genKey(t)
	srv, envs := updateBridge(t, false)
	if err := runUpdateWith(context.Background(), priv, "sb-test", srv.URL, "C1", "1.1", "raw **text**"); err != nil {
		t.Fatal(err)
	}
	e := (*envs)[0]
	if e.Text != "raw **text**" || e.Blocks != "" || e.Subject != "" {
		t.Errorf("legacy envelope changed: %+v", e)
	}
}

func TestUpdate_Mrkdwn_RendersText(t *testing.T) {
	_, priv := genKey(t)
	srv, envs := updateBridge(t, false)
	if err := runUpdateOpts(context.Background(), priv, "sb-test", srv.URL, updateOptions{channel: "C1", ts: "1.1", body: "**bold**", render: "mrkdwn"}); err != nil {
		t.Fatal(err)
	}
	if e := (*envs)[0]; e.Text != "*bold*" || e.Blocks != "" {
		t.Errorf("mrkdwn envelope = %+v; want *bold* and no blocks", e)
	}
}

// invalid_blocks: re-send once with a fresh nonce and no blocks, as post does.
func TestUpdate_InvalidBlocksFallsBackOnce(t *testing.T) {
	_, priv := genKey(t)
	srv, envs := updateBridge(t, true)
	if err := runUpdateOpts(context.Background(), priv, "sb-test", srv.URL, updateOptions{channel: "C1", ts: "1.1", body: "# T\n\nbody", render: "blocks-rich"}); err != nil {
		t.Fatalf("fallback should succeed: %v", err)
	}
	if len(*envs) != 2 {
		t.Fatalf("envelopes = %d; want blocks attempt + text fallback", len(*envs))
	}
	a, b := (*envs)[0], (*envs)[1]
	if a.Blocks == "" || b.Blocks != "" {
		t.Errorf("first should carry blocks, second none: %q / %q", a.Blocks, b.Blocks)
	}
	if a.Nonce == b.Nonce {
		t.Errorf("fallback must use a fresh nonce (the bridge reserved the first)")
	}
	if b.Text == "" || b.MessageTS != "1.1" {
		t.Errorf("fallback envelope = %+v", b)
	}
}

// Flags: --render precedence is flag > KM_SLACK_RENDER > plain; unknown warns and uses plain.
func TestRunUpdate_RenderFlagParsing(t *testing.T) {
	t.Setenv("KM_SANDBOX_ID", "")
	t.Setenv("KM_SLACK_BRIDGE_URL", "")
	var stderr bytes.Buffer
	code := runUpdate([]string{"--channel", "C1", "--ts", "1.1", "--text", "x", "--render", "bogus"}, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "unknown --render") {
		t.Errorf("exit=%d stderr=%q; want the unknown-render warning before the env check (exit 2)", code, stderr.String())
	}
}

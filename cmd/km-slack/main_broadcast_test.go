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

func broadcastBridge(t *testing.T) (*httptest.Server, *[]slack.SlackEnvelope) {
	t.Helper()
	var envs []slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		_ = json.NewDecoder(r.Body).Decode(&env)
		envs = append(envs, env)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"1.2"}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &envs
}

// --broadcast on a threaded post sets Broadcast on the envelope; the default
// leaves it unset so older bridges keep verifying the signature.
func TestPost_Broadcast_SetsEnvelopeFlag(t *testing.T) {
	_, priv := genKey(t)
	srv, envs := broadcastBridge(t)
	t.Setenv("KM_SLACK_POSTED_MARKER", "0")

	_, err := runWithOpts(context.Background(), priv, "sb-test", srv.URL, postOptions{
		channel: "C1", bodyPath: writeTmpBody(t, "hi"), thread: "1.1", renderMode: "plain", broadcast: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !(*envs)[0].Broadcast || (*envs)[0].ThreadTS != "1.1" {
		t.Errorf("envelope = %+v; want Broadcast with the thread", (*envs)[0])
	}

	if _, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C1", "", writeTmpBody(t, "hi"), "1.1", "plain"); err != nil {
		t.Fatal(err)
	}
	if (*envs)[1].Broadcast {
		t.Errorf("runWith must not set Broadcast")
	}
}

// The invalid_blocks fallback envelope carries the flag too, or a rich broadcast
// that Slack rejects would silently lose its channel visibility.
func TestPost_Broadcast_SurvivesInvalidBlocksFallback(t *testing.T) {
	_, priv := genKey(t)
	t.Setenv("KM_SLACK_POSTED_MARKER", "0")
	var envs []slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		_ = json.NewDecoder(r.Body).Decode(&env)
		envs = append(envs, env)
		w.Header().Set("Content-Type", "application/json")
		if env.Blocks != "" {
			w.WriteHeader(502)
			fmt.Fprintln(w, `{"ok":false,"error":"invalid_blocks"}`)
			return
		}
		fmt.Fprintln(w, `{"ok":true,"ts":"1.2"}`)
	}))
	defer srv.Close()

	_, err := runWithOpts(context.Background(), priv, "sb-test", srv.URL, postOptions{
		channel: "C1", bodyPath: writeTmpBody(t, "# Title\n\nbody"), thread: "1.1", renderMode: "blocks-rich", broadcast: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(envs) != 2 {
		t.Fatalf("envelopes = %d; want the blocks attempt and the fallback", len(envs))
	}
	if !envs[0].Broadcast || !envs[1].Broadcast {
		t.Errorf("broadcast lost across the fallback: first=%v second=%v", envs[0].Broadcast, envs[1].Broadcast)
	}
}

// post: --broadcast needs --thread and refuses --new-message, before any env or key.
func TestRunPost_BroadcastUsage(t *testing.T) {
	body := writeTmpBody(t, "x")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no thread", []string{"--channel", "C1", "--body", body, "--broadcast"}},
		{"new-message", []string{"--channel", "C1", "--body", body, "--thread", "1.1", "--new-message", "--broadcast"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := runPost(tc.args, &stderr); code != 2 || !strings.Contains(stderr.String(), "--broadcast") {
				t.Errorf("exit=%d stderr=%q; want 2 naming --broadcast", code, stderr.String())
			}
		})
	}
}

// reply: broadcast rides to the resolved thread; the channel-root fallback is an
// error because there is no thread to broadcast from.
func TestReply_Broadcast(t *testing.T) {
	_, priv := genKey(t)
	srv, envs := broadcastBridge(t)
	t.Setenv("KM_SLACK_POSTED_MARKER", "0")
	t.Setenv("KM_SLACK_CHANNEL_ID", "C1")
	t.Setenv("KM_SLACK_THREAD_TS", "")

	if err := runReplyWith(context.Background(), priv, "sb-test", srv.URL, runReplyOptions{
		thread: "1.1", bodyPath: writeTmpBody(t, "hi"), render: "plain", broadcast: true,
	}); err != nil {
		t.Fatal(err)
	}
	if !(*envs)[0].Broadcast || (*envs)[0].ThreadTS != "1.1" {
		t.Errorf("envelope = %+v", (*envs)[0])
	}

	err := runReplyWith(context.Background(), priv, "sb-test", srv.URL, runReplyOptions{
		session: "no-such-session", bodyPath: writeTmpBody(t, "hi"), render: "plain", broadcast: true,
	})
	if err == nil || !strings.Contains(err.Error(), "--broadcast") {
		t.Errorf("channel-root fallback with --broadcast: err = %v; want a refusal naming the flag", err)
	}
	posts := 0
	for _, e := range *envs {
		if e.Action == slack.ActionPost {
			posts++
		}
	}
	if posts != 1 {
		t.Errorf("a refused broadcast must not post: %d post envelopes (the lookup-thread call is expected)", posts)
	}
}

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

func reactBridge(t *testing.T, answer string) (*httptest.Server, *[]slack.SlackEnvelope) {
	t.Helper()
	var envs []slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		envs = append(envs, env)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(answer, `"ok":false`) {
			w.WriteHeader(502)
		}
		fmt.Fprintln(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv, &envs
}

// The verb signs ONE react envelope carrying channel, ts, bare emoji, the
// exclusive set, and remove; the bridge does the fan-out.
func TestRunReactWith_EnvelopeShape(t *testing.T) {
	_, priv := genKey(t)
	srv, envs := reactBridge(t, `{"ok":true,"ts":"1714280400.000100"}`)

	var out bytes.Buffer
	err := runReactWith(context.Background(), priv, "sb-test", srv.URL, &out, reactOptions{
		channel:   "C123",
		ts:        "1714280400.000100",
		emoji:     ":white_check_mark:",
		exclusive: []string{"hourglass_flowing_sand", ":rotating_light:", "white_check_mark"},
	})
	if err != nil {
		t.Fatalf("runReactWith: %v", err)
	}
	if len(*envs) != 1 {
		t.Fatalf("bridge received %d envelopes; want 1 (the bridge fans out, not the client)", len(*envs))
	}
	e := (*envs)[0]
	if e.Action != slack.ActionReact || e.Channel != "C123" || e.MessageTS != "1714280400.000100" {
		t.Errorf("envelope = %+v", e)
	}
	if e.Emoji != "white_check_mark" {
		t.Errorf("Emoji = %q; want colons stripped client-side", e.Emoji)
	}
	if strings.Join(e.Exclusive, ",") != "hourglass_flowing_sand,rotating_light,white_check_mark" {
		t.Errorf("Exclusive = %v; want normalised names in order", e.Exclusive)
	}
	if e.Remove {
		t.Errorf("Remove must be false without --remove")
	}
	if got := out.String(); got != "km-slack: reacted ts=1714280400.000100 emoji=white_check_mark\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestRunReactWith_Remove(t *testing.T) {
	_, priv := genKey(t)
	srv, envs := reactBridge(t, `{"ok":true,"ts":"1.1"}`)
	var out bytes.Buffer
	if err := runReactWith(context.Background(), priv, "sb-test", srv.URL, &out, reactOptions{channel: "C1", ts: "1.1", emoji: "eyes", remove: true}); err != nil {
		t.Fatal(err)
	}
	if !(*envs)[0].Remove {
		t.Errorf("Remove not set on the envelope")
	}
	if !strings.Contains(out.String(), "removed ts=1.1 emoji=eyes") {
		t.Errorf("stdout = %q; want the removed line", out.String())
	}
}

// Bridge or Slack errors surface with the code; nothing is retried locally.
func TestRunReactWith_BridgeErrorSurfacesCode(t *testing.T) {
	_, priv := genKey(t)
	srv, _ := reactBridge(t, `{"ok":false,"error":"message_not_found"}`)
	var out bytes.Buffer
	err := runReactWith(context.Background(), priv, "sb-test", srv.URL, &out, reactOptions{channel: "C1", ts: "9.9", emoji: "eyes"})
	if err == nil || !strings.Contains(err.Error(), "message_not_found") {
		t.Errorf("err = %v; want the Slack code", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout must stay empty on failure: %q", out.String())
	}
}

// Client-side validation runs before any key load or network call.
func TestRunReact_UsageErrors(t *testing.T) {
	t.Setenv("KM_SLACK_CHANNEL_ID", "")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no emoji", []string{"--channel", "C1", "--ts", "1.1"}, "--emoji"},
		{"no ts", []string{"--channel", "C1", "--emoji", "eyes"}, "--ts"},
		{"no channel", []string{"--ts", "1.1", "--emoji", "eyes"}, "--channel"},
		{"bad name", []string{"--channel", "C1", "--ts", "1.1", "--emoji", ":rotating light:"}, "bad emoji"},
		{"too long", []string{"--channel", "C1", "--ts", "1.1", "--emoji", strings.Repeat("a", 101)}, "bad emoji"},
		{"bad exclusive", []string{"--channel", "C1", "--ts", "1.1", "--emoji", "eyes", "--exclusive", "ok,<@U1>"}, "bad emoji"},
		{"too many exclusive", []string{"--channel", "C1", "--ts", "1.1", "--emoji", "eyes", "--exclusive", "a,b,c,d,e,f,g,h,i,j,k"}, "at most 10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := runReact(tc.args, &stderr); code != 2 {
				t.Fatalf("exit = %d stderr=%q; want 2", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr = %q; want mention of %q", stderr.String(), tc.want)
			}
		})
	}
}

// --channel defaults to $KM_SLACK_CHANNEL_ID, like post. The verb then stops at the
// missing bridge env (exit 2) rather than at the channel check.
func TestRunReact_ChannelDefaultsFromEnv(t *testing.T) {
	t.Setenv("KM_SLACK_CHANNEL_ID", "CENV")
	t.Setenv("KM_SANDBOX_ID", "")
	t.Setenv("KM_SLACK_BRIDGE_URL", "")
	var stderr bytes.Buffer
	code := runReact([]string{"--ts", "1.1", "--emoji", "eyes"}, &stderr)
	if code != 2 || strings.Contains(stderr.String(), "--channel") {
		t.Fatalf("exit=%d stderr=%q; the channel should have come from the env", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "KM_SANDBOX_ID") {
		t.Errorf("stderr = %q; want the missing-env message", stderr.String())
	}
}

func TestDispatch_ReactIsASubcommand(t *testing.T) {
	var stderr bytes.Buffer
	usage(&stderr)
	if !strings.Contains(stderr.String(), "react") {
		t.Errorf("usage does not list react")
	}
	stderr.Reset()
	if code := dispatch([]string{"react"}, &stderr); code != 2 {
		t.Errorf("dispatch react with no flags: exit %d stderr=%q; want 2 (usage), not unknown subcommand", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("react is routed as unknown: %q", stderr.String())
	}
}

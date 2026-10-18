package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// The session action carries its own ownership check, so the pre-switch
// channel authz (owned channel vs routed thread) and the channel-required
// parse guard must both let it through. These tests drive the full Handle.

type sessionGH struct {
	owner string
	calls int
}

func (s *sessionGH) LookupSandbox(_ context.Context, _ bridge.ThreadKey) (string, error) {
	return s.owner, nil
}
func (s *sessionGH) SetSession(_ context.Context, _ bridge.ThreadKey, _, _, _ string) error {
	s.calls++
	return nil
}

func sessionWireEnv(sender string) *slack.SlackEnvelope {
	env := makeEnv(slack.ActionSession, sender, "")
	env.Subject, env.Body = "", ""
	env.SessionID, env.AgentType = "sess-1", "claude"
	return env
}

func TestHandle_Session_GitHub_NoChannel_PassesParseAndAuthz(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	gh := &sessionGH{owner: "sb-abc123"}
	h.GitHubSessions = gh
	env := sessionWireEnv("sb-abc123")
	env.Source, env.Repo, env.Number = "github", "o/r", 7
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 || gh.calls != 1 {
		t.Fatalf("want 200 + one write, got %d %s (calls=%d)", resp.StatusCode, resp.Body, gh.calls)
	}
}

func TestHandle_Session_Slack_ForeignChannelOwnedRow_Allowed(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	th := &recordingThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-abc123"}}
	h.Threads = th
	env := sessionWireEnv("sb-abc123")
	env.Source, env.Channel, env.ThreadTS = "slack", "CFOREIGN", "1.0"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 || len(th.sessions) != 1 {
		t.Fatalf("want 200 + one write, got %d %s (sessions=%v)", resp.StatusCode, resp.Body, th.sessions)
	}
}

func TestHandle_Session_Slack_ForeignRow_403NotChannelMismatch(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	th := &recordingThreads{rows: map[string]string{"C0123ABC|1.0": "sb-OTHER"}}
	h.Threads = th
	env := sessionWireEnv("sb-abc123")
	env.Source, env.Channel, env.ThreadTS = "slack", "C0123ABC", "1.0"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 403 || len(th.sessions) != 0 {
		t.Fatalf("want 403 and no write, got %d %s (sessions=%v)", resp.StatusCode, resp.Body, th.sessions)
	}
	if body := responseBody(t, resp); body["error"] != "thread_not_owned" {
		t.Fatalf("error = %v (owning the channel must not authorize a write to another box's row)", body["error"])
	}
}

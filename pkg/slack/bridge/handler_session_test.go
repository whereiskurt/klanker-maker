package bridge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// fakeSessionStore is a ThreadSessionStore double for the GitHub/H1 tables.
type fakeSessionStore struct {
	owner   map[string]string // sessionKeyString(k) -> sandbox_id
	calls   []string          // "<key>|sess|agent|owner"
	lookErr error
	setErr  error
}

func (f *fakeSessionStore) LookupSandbox(_ context.Context, k ThreadKey) (string, error) {
	if f.lookErr != nil {
		return "", f.lookErr
	}
	return f.owner[sessionKeyString(k)], nil
}

func (f *fakeSessionStore) SetSession(_ context.Context, k ThreadKey, sid, agent, owner string) error {
	f.calls = append(f.calls, sessionKeyString(k)+"|"+sid+"|"+agent+"|"+owner)
	return f.setErr
}

func sessionKeyString(k ThreadKey) string {
	if k.Repo != "" {
		return k.Repo + "#" + strconv.Itoa(k.Number)
	}
	return k.ReportID + "/" + k.Target
}

func sessionEnv(source, sender string) slack.SlackEnvelope {
	return slack.SlackEnvelope{Action: slack.ActionSession, SenderID: sender, Source: source, SessionID: "sess-1", AgentType: "claude", Nonce: "n", Timestamp: 1, Version: slack.EnvelopeVersion}
}

func ownedThreads(rows map[string]string) *fakeThreads {
	return &fakeThreads{sandboxByThread: rows}
}

func TestHandleSession_Slack_OwnedRowIsUpdated(t *testing.T) {
	threads := ownedThreads(map[string]string{"C1|1.1": "sb-1"})
	h := &Handler{Threads: threads}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS = "C1", "1.1"
	resp := h.handleSession(context.Background(), env)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, resp.Body)
	}
	if got := threads.setSessionCalls; len(got) != 1 || got[0] != "C1|1.1|sess-1|claude|sb-1" {
		t.Fatalf("SetSession calls = %v", got)
	}
}

func TestHandleSession_Slack_MissingRowIs404_NeverCreates(t *testing.T) {
	threads := ownedThreads(nil)
	h := &Handler{Threads: threads}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS = "C1", "9.9"
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 404 || !strings.Contains(resp.Body, "thread_unknown") {
		t.Fatalf("want 404 thread_unknown, got %d %s", resp.StatusCode, resp.Body)
	}
	if len(threads.setSessionCalls)+len(threads.upserts)+len(threads.rebinds) != 0 {
		t.Fatal("a missing row must never be written or created")
	}
}

func TestHandleSession_Slack_ForeignRowIs403(t *testing.T) {
	threads := ownedThreads(map[string]string{"C1|1.1": "sb-OTHER"})
	h := &Handler{Threads: threads}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS = "C1", "1.1"
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 403 || !strings.Contains(resp.Body, "thread_not_owned") {
		t.Fatalf("want 403 thread_not_owned, got %d %s", resp.StatusCode, resp.Body)
	}
	if len(threads.setSessionCalls) != 0 {
		t.Fatal("a foreign row must never be written")
	}
}

func TestHandleSession_Slack_NilThreadsIs503(t *testing.T) {
	h := &Handler{}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS = "C1", "1.1"
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 503 || !strings.Contains(resp.Body, "source_unavailable") {
		t.Fatalf("want 503 source_unavailable, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandleSession_GitHub_UsesItsStore(t *testing.T) {
	gh := &fakeSessionStore{owner: map[string]string{"o/r#7": "sb-1"}}
	threads := ownedThreads(nil)
	h := &Handler{Threads: threads, GitHubSessions: gh}
	env := sessionEnv("github", "sb-1")
	env.Repo, env.Number = "o/r", 7
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, resp.Body)
	}
	if len(gh.calls) != 1 || gh.calls[0] != "o/r#7|sess-1|claude|sb-1" {
		t.Fatalf("calls = %v", gh.calls)
	}
	if len(threads.setSessionCalls) != 0 {
		t.Fatal("github source must not touch the slack threads table")
	}
}

func TestHandleSession_H1_UsesItsStore(t *testing.T) {
	h1 := &fakeSessionStore{owner: map[string]string{"r1/ir-bot": "sb-1"}}
	h := &Handler{Threads: ownedThreads(nil), H1Sessions: h1}
	env := sessionEnv("h1", "sb-1")
	env.ReportID, env.Target, env.AgentType = "r1", "ir-bot", "codex"
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, resp.Body)
	}
	if len(h1.calls) != 1 || h1.calls[0] != "r1/ir-bot|sess-1|codex|sb-1" {
		t.Fatalf("calls = %v", h1.calls)
	}
}

func TestHandleSession_GitHub_ForeignRowIs403(t *testing.T) {
	gh := &fakeSessionStore{owner: map[string]string{"o/r#7": "sb-OTHER"}}
	h := &Handler{GitHubSessions: gh}
	env := sessionEnv("github", "sb-1")
	env.Repo, env.Number = "o/r", 7
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 403 || !strings.Contains(resp.Body, "thread_not_owned") {
		t.Fatalf("want 403, got %d %s", resp.StatusCode, resp.Body)
	}
	if len(gh.calls) != 0 {
		t.Fatal("foreign row written")
	}
}

func TestHandleSession_H1_UnsetStoreIs503(t *testing.T) {
	h := &Handler{Threads: ownedThreads(nil)}
	env := sessionEnv("h1", "sb-1")
	env.ReportID, env.Target = "r1", "ir-bot"
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 503 || !strings.Contains(resp.Body, "source_unavailable") {
		t.Fatalf("want 503 source_unavailable, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandleSession_GitHub_UnsetStoreIs503(t *testing.T) {
	h := &Handler{Threads: ownedThreads(nil)}
	env := sessionEnv("github", "sb-1")
	env.Repo, env.Number = "o/r", 7
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 503 || !strings.Contains(resp.Body, "source_unavailable") {
		t.Fatalf("want 503 source_unavailable, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandleSession_LookupErrorIs500(t *testing.T) {
	gh := &fakeSessionStore{lookErr: errors.New("ddb down")}
	h := &Handler{GitHubSessions: gh}
	env := sessionEnv("github", "sb-1")
	env.Repo, env.Number = "o/r", 7
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 500 || !strings.Contains(resp.Body, "thread_lookup_failed") {
		t.Fatalf("want 500 thread_lookup_failed, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandleSession_WriteErrorIs500(t *testing.T) {
	gh := &fakeSessionStore{owner: map[string]string{"o/r#7": "sb-1"}, setErr: errors.New("ddb down")}
	h := &Handler{GitHubSessions: gh}
	env := sessionEnv("github", "sb-1")
	env.Repo, env.Number = "o/r", 7
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 500 || !strings.Contains(resp.Body, "session_write_failed") {
		t.Fatalf("want 500 session_write_failed, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandleSession_MissingKeyIs400(t *testing.T) {
	h := &Handler{Threads: ownedThreads(nil), GitHubSessions: &fakeSessionStore{}, H1Sessions: &fakeSessionStore{}}
	for _, env := range []slack.SlackEnvelope{
		func() slack.SlackEnvelope { e := sessionEnv("slack", "sb-1"); e.Channel = "C1"; return e }(), // no thread_ts
		func() slack.SlackEnvelope { e := sessionEnv("github", "sb-1"); e.Repo = "o/r"; return e }(),  // no number
		func() slack.SlackEnvelope { e := sessionEnv("h1", "sb-1"); e.ReportID = "r1"; return e }(),   // no target
		func() slack.SlackEnvelope { e := sessionEnv("bogus", "sb-1"); return e }(),
		func() slack.SlackEnvelope {
			e := sessionEnv("slack", "sb-1")
			e.Channel, e.ThreadTS, e.AgentType = "C1", "1.1", "gemini"
			return e
		}(),
	} {
		resp := h.handleSession(context.Background(), env)
		if resp.StatusCode != 400 || !strings.Contains(resp.Body, "missing_fields") {
			t.Fatalf("source=%s: want 400 missing_fields, got %d %s", env.Source, resp.StatusCode, resp.Body)
		}
	}
}

func TestHandleSession_EmptySessionClears(t *testing.T) {
	threads := ownedThreads(map[string]string{"C1|1.1": "sb-1"})
	h := &Handler{Threads: threads}
	env := sessionEnv("slack", "sb-1")
	env.Channel, env.ThreadTS, env.SessionID = "C1", "1.1", ""
	if resp := h.handleSession(context.Background(), env); resp.StatusCode != 200 {
		t.Fatalf("status %d %s", resp.StatusCode, resp.Body)
	}
	if got := threads.setSessionCalls; len(got) != 1 || got[0] != "C1|1.1||claude|sb-1" {
		t.Fatalf("SetSession calls = %v (empty session id must pass through as the clear signal)", got)
	}
}

// The lookup said the row was the sender's, but the conditioned write found it
// gone or rebound (TTL delete, Rebind): 403, not 500, and no created row.
func TestHandleSession_ConditionFailedAtWriteIs403(t *testing.T) {
	ccf := &dynamodbtypes.ConditionalCheckFailedException{Message: strPtr("The conditional request failed")}
	for name, h := range map[string]*Handler{
		"slack":  {Threads: &fakeThreads{sandboxByThread: map[string]string{"C1|1.1": "sb-1"}, setSessionErr: fmt.Errorf("threads set-session: %w", ccf)}},
		"github": {GitHubSessions: &fakeSessionStore{owner: map[string]string{"o/r#7": "sb-1"}, setErr: fmt.Errorf("km-github-threads set-session: %w", ccf)}},
	} {
		env := sessionEnv(name, "sb-1")
		env.Channel, env.ThreadTS, env.Repo, env.Number = "C1", "1.1", "o/r", 7
		resp := h.handleSession(context.Background(), env)
		if resp.StatusCode != 403 || !strings.Contains(resp.Body, "thread_not_owned") {
			t.Fatalf("%s: want 403 thread_not_owned, got %d %s", name, resp.StatusCode, resp.Body)
		}
	}
}

func strPtr(s string) *string { return &s }

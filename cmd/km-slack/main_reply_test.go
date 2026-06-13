package main

// Phase 110 Plan 03 Task 1: TDD tests for km-slack reply subcommand.
//
// Resolution chain (first-hit-wins):
//  1. --thread + --channel (or KM_SLACK_CHANNEL_ID)
//  2. $KM_SLACK_THREAD_TS env var
//  3. session id (--session or auto-detect) → bridge lookup-thread
//  4. fallback: top-level post to $KM_SLACK_CHANNEL_ID
//
// Tests use a fake bridge HTTP server and temp dirs so no AWS calls are made.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// genReplyKey returns a fresh ephemeral Ed25519 key pair.
func genReplyKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return pub, priv
}

// writeTmpReplyBody writes content to a temp file and returns the path.
func writeTmpReplyBody(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "km-slack-reply-body-*.txt")
	if err != nil {
		t.Fatalf("create temp body: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write body: %v", err)
	}
	f.Close()
	return f.Name()
}

// newFakeReplyBridge returns a server that captures all request envelopes and returns
// responses from the provided handler func. The capturedEnvs slice holds all decoded
// envelopes in order.
func newFakeReplyBridge(t *testing.T, handler func(w http.ResponseWriter, env slack.SlackEnvelope)) (*httptest.Server, *[]slack.SlackEnvelope) {
	t.Helper()
	var captured []slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		captured = append(captured, env)
		handler(w, env)
	}))
	return srv, &captured
}

// TestRunReply exercises the first two runReplyWith resolution steps:
//  1. --thread + --channel → posts to that exact thread.
//  2. $KM_SLACK_THREAD_TS (no --thread) → posts to (KM_SLACK_CHANNEL_ID, env-ts).
//  3. resolvable session via bridge lookup-thread → posts to (channel_id, thread_ts) from bridge.
func TestRunReply(t *testing.T) {
	_, priv := genReplyKey(t)

	t.Run("explicit-thread-and-channel", func(t *testing.T) {
		var capturedEnv slack.SlackEnvelope
		srv, _ := newFakeReplyBridge(t, func(w http.ResponseWriter, env slack.SlackEnvelope) {
			capturedEnv = env
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"ts":"999.111"}`)
		})
		defer srv.Close()

		bodyPath := writeTmpReplyBody(t, "explicit thread reply")
		t.Setenv("KM_SLACK_CHANNEL_ID", "C_DEFAULT")
		t.Setenv("KM_SLACK_THREAD_TS", "")

		err := runReplyWith(context.Background(), priv, "sb-test", srv.URL, runReplyOptions{
			channel:  "C_EXPLICIT",
			thread:   "111.222",
			bodyPath: bodyPath,
			render:   "plain",
		})
		if err != nil {
			t.Fatalf("runReplyWith explicit thread: %v", err)
		}
		if capturedEnv.Action != slack.ActionPost {
			t.Errorf("expected ActionPost, got %q", capturedEnv.Action)
		}
		if capturedEnv.Channel != "C_EXPLICIT" {
			t.Errorf("expected channel=C_EXPLICIT, got %q", capturedEnv.Channel)
		}
		if capturedEnv.ThreadTS != "111.222" {
			t.Errorf("expected thread=111.222, got %q", capturedEnv.ThreadTS)
		}
	})

	t.Run("env-thread-ts", func(t *testing.T) {
		var capturedEnv slack.SlackEnvelope
		srv, _ := newFakeReplyBridge(t, func(w http.ResponseWriter, env slack.SlackEnvelope) {
			capturedEnv = env
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"ts":"888.001"}`)
		})
		defer srv.Close()

		bodyPath := writeTmpReplyBody(t, "env thread reply")
		t.Setenv("KM_SLACK_CHANNEL_ID", "C_FROM_ENV")
		t.Setenv("KM_SLACK_THREAD_TS", "777.888")

		err := runReplyWith(context.Background(), priv, "sb-test", srv.URL, runReplyOptions{
			bodyPath: bodyPath,
			render:   "plain",
		})
		if err != nil {
			t.Fatalf("runReplyWith env thread: %v", err)
		}
		if capturedEnv.Channel != "C_FROM_ENV" {
			t.Errorf("expected channel=C_FROM_ENV, got %q", capturedEnv.Channel)
		}
		if capturedEnv.ThreadTS != "777.888" {
			t.Errorf("expected thread=777.888, got %q", capturedEnv.ThreadTS)
		}
	})

	t.Run("session-lookup-found", func(t *testing.T) {
		// The bridge must see: first a lookup-thread request, then a post request.
		callCount := 0
		var capturedPost slack.SlackEnvelope
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var env slack.SlackEnvelope
			if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
				http.Error(w, "bad body", 400)
				return
			}
			callCount++
			w.Header().Set("Content-Type", "application/json")
			if env.Action == slack.ActionLookupThread {
				// Return a found result.
				fmt.Fprintln(w, `{"ok":true,"found":true,"channel_id":"C_SESSION","thread_ts":"555.666","agent_type":"claude"}`)
				return
			}
			// ActionPost
			capturedPost = env
			fmt.Fprintln(w, `{"ok":true,"ts":"555.667"}`)
		}))
		defer srv.Close()

		bodyPath := writeTmpReplyBody(t, "session lookup reply")
		t.Setenv("KM_SLACK_CHANNEL_ID", "C_DEFAULT")
		t.Setenv("KM_SLACK_THREAD_TS", "")
		t.Setenv("KM_AGENT", "")

		err := runReplyWith(context.Background(), priv, "sb-test", srv.URL, runReplyOptions{
			session:  "test-session-id-uuid",
			bodyPath: bodyPath,
			render:   "plain",
		})
		if err != nil {
			t.Fatalf("runReplyWith session lookup: %v", err)
		}
		if callCount != 2 {
			t.Errorf("expected 2 bridge calls (lookup + post), got %d", callCount)
		}
		if capturedPost.Channel != "C_SESSION" {
			t.Errorf("expected channel=C_SESSION, got %q", capturedPost.Channel)
		}
		if capturedPost.ThreadTS != "555.666" {
			t.Errorf("expected thread=555.666, got %q", capturedPost.ThreadTS)
		}
	})
}

// TestAutoDetectClaudeSession verifies that, given a temp ~/.claude/projects tree
// with multiple *.jsonl files at different mtimes, autoDetectSession returns the
// UUID stem of the newest-by-mtime file.
func TestAutoDetectClaudeSession(t *testing.T) {
	// Create a temp dir to act as ~/.claude
	claudeHome := t.TempDir()

	// Create subdirectory structure: ~/.claude/projects/<encoded-path>/<uuid>.jsonl
	proj1 := filepath.Join(claudeHome, "projects", "project-alpha")
	proj2 := filepath.Join(claudeHome, "projects", "project-beta")
	if err := os.MkdirAll(proj1, 0755); err != nil {
		t.Fatalf("mkdir proj1: %v", err)
	}
	if err := os.MkdirAll(proj2, 0755); err != nil {
		t.Fatalf("mkdir proj2: %v", err)
	}

	// Write older file first.
	olderPath := filepath.Join(proj1, "aaaaaaaa-0000-0000-0000-000000000001.jsonl")
	if err := os.WriteFile(olderPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("write older: %v", err)
	}
	// Ensure mtime ordering.
	time.Sleep(5 * time.Millisecond)

	// Write newer file.
	newerPath := filepath.Join(proj2, "bbbbbbbb-1111-1111-1111-000000000002.jsonl")
	if err := os.WriteFile(newerPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("write newer: %v", err)
	}

	// Override the claude home root via the test override var.
	origClaudeHome := claudeProjectsRoot
	claudeProjectsRoot = filepath.Join(claudeHome, "projects")
	defer func() { claudeProjectsRoot = origClaudeHome }()

	got := autoDetectClaudeSession()
	want := "bbbbbbbb-1111-1111-1111-000000000002"
	if got != want {
		t.Errorf("autoDetectClaudeSession = %q, want %q", got, want)
	}
}

// TestRunReply_FallbackToChannelRoot verifies that when no --thread, no
// $KM_SLACK_THREAD_TS, and the bridge returns found:false for the session,
// runReplyWith falls through to a top-level post to $KM_SLACK_CHANNEL_ID with
// empty thread_ts (channel-root).
func TestRunReply_FallbackToChannelRoot(t *testing.T) {
	_, priv := genReplyKey(t)

	callCount := 0
	var capturedPost slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if env.Action == slack.ActionLookupThread {
			// Not found.
			fmt.Fprintln(w, `{"ok":true,"found":false}`)
			return
		}
		// ActionPost fallback.
		capturedPost = env
		fmt.Fprintln(w, `{"ok":true,"ts":"111.222"}`)
	}))
	defer srv.Close()

	bodyPath := writeTmpReplyBody(t, "fallback to channel root")
	t.Setenv("KM_SLACK_CHANNEL_ID", "C_ROOT_FALLBACK")
	t.Setenv("KM_SLACK_THREAD_TS", "")
	t.Setenv("KM_AGENT", "")

	err := runReplyWith(context.Background(), priv, "sb-test", srv.URL, runReplyOptions{
		session:  "nonexistent-session-id",
		bodyPath: bodyPath,
		render:   "plain",
	})
	if err != nil {
		t.Fatalf("runReplyWith fallback: %v", err)
	}
	// lookup-thread + post = 2 calls.
	if callCount != 2 {
		t.Errorf("expected 2 bridge calls (lookup + fallback post), got %d", callCount)
	}
	if capturedPost.Channel != "C_ROOT_FALLBACK" {
		t.Errorf("expected channel=C_ROOT_FALLBACK, got %q", capturedPost.Channel)
	}
	if capturedPost.ThreadTS != "" {
		t.Errorf("expected empty thread_ts for channel-root fallback, got %q", capturedPost.ThreadTS)
	}
}

// TestRunReply_NoBridgeCallsWhenEnvTSSet verifies step 2: when KM_SLACK_THREAD_TS
// is set, no lookup-thread bridge call is made (fast path).
func TestRunReply_NoBridgeCallsWhenEnvTSSet(t *testing.T) {
	_, priv := genReplyKey(t)

	lookupCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		json.NewDecoder(r.Body).Decode(&env)
		if env.Action == slack.ActionLookupThread {
			lookupCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"123.456"}`)
	}))
	defer srv.Close()

	bodyPath := writeTmpReplyBody(t, "env ts set — no lookup")
	t.Setenv("KM_SLACK_CHANNEL_ID", "C_CHAN")
	t.Setenv("KM_SLACK_THREAD_TS", "444.555")

	err := runReplyWith(context.Background(), priv, "sb-test", srv.URL, runReplyOptions{
		bodyPath: bodyPath,
		render:   "plain",
	})
	if err != nil {
		t.Fatalf("runReplyWith env ts: %v", err)
	}
	if lookupCalled {
		t.Error("expected no lookup-thread call when KM_SLACK_THREAD_TS is set")
	}
}

// TestRunReplyWith_SlackNotConfigured verifies that when KM_SLACK_CHANNEL_ID is
// empty and no --channel/--thread flags are provided, runReplyWith exits with
// an error containing the "Slack not configured" message.
func TestRunReplyWith_SlackNotConfigured(t *testing.T) {
	_, priv := genReplyKey(t)
	bodyPath := writeTmpReplyBody(t, "test")

	t.Setenv("KM_SLACK_CHANNEL_ID", "")
	t.Setenv("KM_SLACK_THREAD_TS", "")

	err := runReplyWith(context.Background(), priv, "sb-test", "http://localhost/noop", runReplyOptions{
		bodyPath: bodyPath,
		render:   "plain",
	})
	if err == nil {
		t.Fatal("expected error for unconfigured Slack sandbox")
	}
	if !strings.Contains(err.Error(), "Slack not configured") {
		t.Errorf("expected 'Slack not configured' in error, got: %v", err)
	}
}

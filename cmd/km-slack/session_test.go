package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunSession_FlagValidation(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want int
		msg  string
	}{
		{"slack ok shape", []string{"--source", "slack", "--channel", "C1", "--thread", "1.1", "--session-id", "s", "--agent", "claude"}, 1, "KM_SANDBOX_ID"}, // passes validation, fails on env (exit 1)
		{"slack missing thread", []string{"--source", "slack", "--channel", "C1", "--session-id", "s", "--agent", "claude"}, 2, "--thread"},
		{"github missing number", []string{"--source", "github", "--repo", "o/r", "--session-id", "s", "--agent", "claude"}, 2, "--number"},
		{"h1 missing target", []string{"--source", "h1", "--report", "r", "--session-id", "s", "--agent", "claude"}, 2, "--target"},
		{"github number zero", []string{"--source", "github", "--repo", "o/r", "--number", "0", "--session-id", "s", "--agent", "claude"}, 2, "--number"},
		{"github number negative", []string{"--source", "github", "--repo", "o/r", "--number", "-1", "--session-id", "s", "--agent", "claude"}, 2, "--number"},
		{"bad source", []string{"--source", "jira", "--session-id", "s", "--agent", "claude"}, 2, "--source"},
		{"bad agent", []string{"--source", "slack", "--channel", "C1", "--thread", "1.1", "--session-id", "s", "--agent", "gemini"}, 2, "--agent"},
		{"session-id and clear", []string{"--source", "slack", "--channel", "C1", "--thread", "1.1", "--session-id", "s", "--clear", "--agent", "claude"}, 2, "--clear"},
		{"neither session-id nor clear", []string{"--source", "slack", "--channel", "C1", "--thread", "1.1", "--agent", "claude"}, 2, "--session-id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("KM_SANDBOX_ID", "")
			var stderr bytes.Buffer
			if got := runSession(c.args, &stderr); got != c.want {
				t.Fatalf("exit %d, want %d (stderr: %s)", got, c.want, stderr.String())
			}
			if !strings.Contains(stderr.String(), c.msg) {
				t.Fatalf("stderr %q should mention %s", stderr.String(), c.msg)
			}
		})
	}
}

func TestBuildSessionEnvelope_Shapes(t *testing.T) {
	env, err := buildSessionEnvelope("sb-1", sessionOptions{source: "github", repo: "o/r", number: 7, sessionID: "s1", agent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if env.Action != "session" || env.Source != "github" || env.Repo != "o/r" || env.Number != 7 || env.SessionID != "s1" || env.AgentType != "codex" || env.SenderID != "sb-1" {
		t.Fatalf("envelope = %+v", env)
	}
	env2, _ := buildSessionEnvelope("sb-1", sessionOptions{source: "slack", channel: "C1", thread: "1.1", clear: true, agent: "claude"})
	if env2.SessionID != "" || env2.Channel != "C1" || env2.ThreadTS != "1.1" {
		t.Fatalf("clear envelope = %+v", env2)
	}
	env3, _ := buildSessionEnvelope("sb-1", sessionOptions{source: "h1", report: "r1", target: "ir-bot", sessionID: "s", agent: "claude"})
	if env3.ReportID != "r1" || env3.Target != "ir-bot" {
		t.Fatalf("h1 envelope = %+v", env3)
	}
}

func TestRunSessionWith_BridgeResponses(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	o := sessionOptions{source: "slack", channel: "C1", thread: "1.1", sessionID: "s", agent: "claude"}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"ok":false,"error":"thread_unknown"}`))
	}))
	defer bad.Close()
	if err := runSessionWith(context.Background(), priv, "sb-1", bad.URL, o); err == nil || !strings.Contains(err.Error(), "thread_unknown") {
		t.Fatalf("want thread_unknown error, got %v", err)
	}

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer good.Close()
	if err := runSessionWith(context.Background(), priv, "sb-1", good.URL, o); err != nil {
		t.Fatalf("200 ok must return nil, got %v", err)
	}
}

func TestBuildSessionEnvelope_ClearGithubAndH1(t *testing.T) {
	g, err := buildSessionEnvelope("sb-1", sessionOptions{source: "github", repo: "o/r", number: 7, clear: true, agent: "claude"})
	if err != nil || g.SessionID != "" || g.Repo != "o/r" || g.Number != 7 || g.Source != "github" {
		t.Fatalf("github clear envelope = %+v, %v", g, err)
	}
	h, err := buildSessionEnvelope("sb-1", sessionOptions{source: "h1", report: "r1", target: "ir-bot", clear: true, agent: "codex"})
	if err != nil || h.SessionID != "" || h.ReportID != "r1" || h.Target != "ir-bot" || h.Source != "h1" {
		t.Fatalf("h1 clear envelope = %+v, %v", h, err)
	}
}

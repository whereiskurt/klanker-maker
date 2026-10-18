package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

type routedThreads struct {
	rows map[string]string // "chan|ts" → sandbox_id
}

func (r *routedThreads) Get(_ context.Context, _, _ string) (string, error) { return "", nil }
func (r *routedThreads) Upsert(_ context.Context, _, _, _ string) error     { return nil }
func (r *routedThreads) Rebind(_ context.Context, _, _, _ string) error     { return nil }
func (r *routedThreads) SetSession(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (r *routedThreads) MarkDispatched(_ context.Context, _, _, _ string) error {
	return nil
}
func (r *routedThreads) DispatchBoundary(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (r *routedThreads) LookupSandbox(_ context.Context, ch, ts string) (string, error) {
	return r.rows[ch+"|"+ts], nil
}
func (r *routedThreads) LookupBySession(_ context.Context, _, _ string) (string, string, string, error) {
	return "", "", "", nil
}

func TestHandler_ForeignChannel_RoutedThread_Allowed(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Threads = &routedThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-abc123"}}
	env := makeEnv(slack.ActionPost, "sb-abc123", "CFOREIGN")
	env.ThreadTS = "1.0"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 200 {
		t.Fatalf("want 200, got %d body=%s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_ForeignChannel_WrongSandboxRow_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Threads = &routedThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-someone-else"}}
	env := makeEnv(slack.ActionPost, "sb-abc123", "CFOREIGN")
	env.ThreadTS = "1.0"
	resp := h.Handle(context.Background(), signRequest(t, env, priv))
	if resp.StatusCode != 403 || responseBody(t, resp)["error"] != "channel_mismatch" {
		t.Fatalf("want 403 channel_mismatch, got %d %s", resp.StatusCode, resp.Body)
	}
}

func TestHandler_ForeignChannel_NoThread_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Threads = &routedThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-abc123"}}
	for i, action := range []string{slack.ActionPost, slack.ActionPermalink, slack.ActionUpdate} {
		env := makeEnv(action, "sb-abc123", "CFOREIGN")
		env.Nonce = fmt.Sprintf("%032x", i+1) // fresh nonce: the fake store rejects replays
		env.MessageTS = "1.0"                 // permalink/update carry a message ts, never a thread
		resp := h.Handle(context.Background(), signRequest(t, env, priv))
		if resp.StatusCode != 403 {
			t.Fatalf("%s without thread_ts in a foreign channel must be 403, got %d", action, resp.StatusCode)
		}
	}
}

func TestHandler_ForeignChannel_NilThreads_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub) // Threads nil
	env := makeEnv(slack.ActionPost, "sb-abc123", "CFOREIGN")
	env.ThreadTS = "1.0"
	if resp := h.Handle(context.Background(), signRequest(t, env, priv)); resp.StatusCode != 403 {
		t.Fatalf("nil thread store must fail closed, got %d", resp.StatusCode)
	}
}

func TestHandler_ForeignChannel_RoutedThread_OnlyPostAndUpload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Threads = &routedThreads{rows: map[string]string{"CFOREIGN|1.0": "sb-abc123"}}
	for i, action := range []string{slack.ActionPermalink, slack.ActionUpdate, slack.ActionUpload} {
		env := makeEnv(action, "sb-abc123", "CFOREIGN")
		env.Nonce = fmt.Sprintf("%032x", i+100)
		env.ThreadTS = "1.0"
		resp := h.Handle(context.Background(), signRequest(t, env, priv))
		mismatch := resp.StatusCode == 403 && responseBody(t, resp)["error"] == "channel_mismatch"
		if action == slack.ActionUpload && mismatch {
			t.Fatalf("upload in a routed thread must pass authz, got %d %s", resp.StatusCode, resp.Body)
		}
		if action != slack.ActionUpload && !mismatch {
			t.Fatalf("%s must stay 403 channel_mismatch even with a routed thread, got %d %s", action, resp.StatusCode, resp.Body)
		}
	}
}

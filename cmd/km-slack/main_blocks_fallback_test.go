package main

// Layer-2 defense-in-depth test for the 2026-06-24 invalid_blocks incident.
//
// When the bridge rejects a block payload (502 invalid_blocks) — e.g. a
// schema-invalid table block that passed the local size caps — runWith must
// re-post the same reply WITHOUT blocks (mrkdwn fallback) so the agent's reply
// is never silently dropped. The re-post MUST use a FRESH envelope (new nonce):
// the bridge already reserved the first envelope's nonce, so reusing it returns
// replayed_nonce 401.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// TestRunWith_InvalidBlocksFallback verifies the single mrkdwn re-post on
// invalid_blocks: first call (with blocks) → 502 invalid_blocks; second call
// (no blocks) → 200. Asserts success, the second request carries no blocks, and
// it used a different nonce.
func TestRunWith_InvalidBlocksFallback(t *testing.T) {
	_, priv := genKey(t)

	var mu sync.Mutex
	var captured []slack.SlackEnvelope

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		mu.Lock()
		captured = append(captured, env)
		n := len(captured)
		mu.Unlock()

		if n == 1 {
			// First attempt carries blocks → reject with invalid_blocks.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway) // 502
			fmt.Fprintln(w, `{"error":"invalid_blocks","ok":false}`)
			return
		}
		// Fallback attempt → succeed.
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"9.9"}`)
	}))
	defer srv.Close()

	// A table body that RenderRich will turn into blocks (so the first attempt
	// carries a non-empty Blocks field).
	body := "# Title\n\n| A | B |\n|---|---|\n| 1 | 2 |\n"
	bodyPath := writeTmpBody(t, body)

	ts, err := runWith(context.Background(), priv, "sb-fallback", srv.URL, "C123", "", bodyPath, "", "blocks-rich")
	if err != nil {
		t.Fatalf("runWith: expected success after mrkdwn fallback, got error: %v", err)
	}
	if ts != "9.9" {
		t.Errorf("runWith: got ts=%q, want %q (fallback ts)", ts, "9.9")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("expected exactly 2 bridge calls (blocks attempt + mrkdwn fallback), got %d", len(captured))
	}
	if captured[0].Blocks == "" {
		t.Error("first attempt: expected non-empty Blocks (blocks-rich render)")
	}
	if captured[1].Blocks != "" {
		t.Errorf("fallback attempt: expected empty Blocks, got %q", captured[1].Blocks)
	}
	if captured[0].Nonce == captured[1].Nonce {
		t.Errorf("fallback attempt reused nonce %q; must build a fresh envelope (replayed_nonce guard)", captured[0].Nonce)
	}
	if captured[1].Body == "" {
		t.Error("fallback attempt: Body must carry the mrkdwn/plain fallback text")
	}
}

// TestRunWith_NoFallbackWithoutBlocks verifies that a non-blocks post (plain)
// that fails is NOT retried — the fallback only triggers when blocks were sent.
func TestRunWith_NoFallbackWithoutBlocks(t *testing.T) {
	_, priv := genKey(t)

	var mu sync.Mutex
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintln(w, `{"error":"invalid_blocks","ok":false}`)
	}))
	defer srv.Close()

	body := "just plain text, no blocks\n"
	bodyPath := writeTmpBody(t, body)

	_, err := runWith(context.Background(), priv, "sb-plain", srv.URL, "C123", "", bodyPath, "", "plain")
	if err == nil {
		t.Fatal("runWith plain: expected error to propagate (no blocks → no fallback)")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("plain post failure: expected exactly 1 call (no fallback), got %d", calls)
	}
}

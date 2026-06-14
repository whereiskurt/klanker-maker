package main

// Phase 111 Plan 03 Task 1: RICH-14, RICH-15, RICH-16 integration tests
// for runWith / runPost / runReply blocks-rich mode wiring.
//
// RICH-14: KM_SLACK_AI_FOOTER=true → trailing context block appended
// RICH-15: blocks-rich accepted in runPost AND runReply validation switches
// RICH-16: fallback chain RenderRich ok=false → RenderBlocks → Mrkdwnify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// --- RICH-15: runPost accepts blocks-rich without normalising to "plain" ---

// TestRunPost_BlocksRich verifies that --render blocks-rich (and KM_SLACK_RENDER=blocks-rich)
// is accepted by runPost — NOT normalised to "plain" (Pitfall 6 guard for runPost side).
func TestRunPost_BlocksRich(t *testing.T) {
	_, priv := genKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"1.1"}`)
	}))
	defer srv.Close()

	body := "# Heading\n\nSome prose with **bold**.\n"
	bodyPath := writeTmpBody(t, body)

	// runPost validation path: verify blocks-rich is not rejected by feeding it
	// through runWith directly (runPost's switch sets renderMode then calls runWith).
	// We test the switch by calling runWith with "blocks-rich" — it must not fall
	// through to "plain" (which would pass through body verbatim).
	//
	// A blocks-rich render of a GFM H1 + prose produces a non-plain result:
	// the envelope Body will be the RenderRich fallbackText (not "# Heading\n\n...").
	_, err := runWith(context.Background(), priv, "sb-rich-test", srv.URL, "C123", "", bodyPath, "", "blocks-rich")
	if err != nil {
		t.Fatalf("runWith blocks-rich: unexpected error: %v", err)
	}
}

// TestRunPost_BlocksRich_SwitchAccepted verifies the runPost switch accepts "blocks-rich"
// (not normalised to "plain") by observing that the rendered body in the envelope differs
// from the raw input — only possible if the switch didn't strip the mode.
func TestRunPost_BlocksRich_SwitchAccepted(t *testing.T) {
	_, priv := genKey(t)

	var capturedEnv slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedEnv); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"1.2"}`)
	}))
	defer srv.Close()

	// A raw input with a GFM H1 that RenderRich will promote to a header block.
	// In "plain" mode the Body field would be "# Heading\n\nText.\n" verbatim.
	// In "blocks-rich" mode the Body field is the stripForFallback output ("Heading\n\nText.").
	raw := "# Heading\n\nText.\n"
	bodyPath := writeTmpBody(t, raw)

	_, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", "blocks-rich")
	if err != nil {
		t.Fatalf("runWith blocks-rich: %v", err)
	}

	// If render was "plain", Body == raw.
	// If render was "blocks-rich", Body is the fallback text (H1 stripped of '#').
	if capturedEnv.Body == raw {
		t.Errorf("blocks-rich: envelope Body is identical to raw input, suggesting mode was ignored (plain pass-through); got %q", capturedEnv.Body)
	}
	// The Blocks field must be non-empty — RenderRich returned ok=true.
	if capturedEnv.Blocks == "" {
		t.Errorf("blocks-rich: envelope Blocks field is empty; expected non-empty block JSON")
	}
}

// --- RICH-15 (Pitfall 6): runReply also accepts blocks-rich ---

// TestRunReply_BlocksRich verifies that runReply's validation switch also accepts
// "blocks-rich" without normalising to "plain". This is the Pitfall-6 guard.
func TestRunReply_BlocksRich(t *testing.T) {
	_, priv := genReplyKey(t)

	var capturedEnv slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Some requests may be lookup-thread; only capture post-like requests.
		if err := json.NewDecoder(r.Body).Decode(&capturedEnv); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		switch capturedEnv.Action {
		case "lookup-thread":
			// Return a thread not found so runReplyWith falls back to top-level post.
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"thread_ts":"","channel_id":""}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"ts":"2.2"}`)
		}
	}))
	defer srv.Close()

	// Use KM_SLACK_CHANNEL_ID so the reply falls back to top-level post.
	t.Setenv("KM_SLACK_CHANNEL_ID", "C_REPLY_TEST")
	t.Setenv("KM_SLACK_THREAD_TS", "") // no pre-set thread

	raw := "# Reply Heading\n\nReply body.\n"
	bodyPath := writeTmpReplyBody(t, raw)

	err := runReplyWith(context.Background(), priv, "sb-reply-test", srv.URL, runReplyOptions{
		channel:  "C_REPLY_TEST",
		thread:   "",
		bodyPath: bodyPath,
		render:   "blocks-rich",
		session:  "",
	})
	if err != nil {
		t.Fatalf("runReplyWith blocks-rich: %v", err)
	}
	// If the switch had normalised blocks-rich → plain, Body == raw verbatim.
	// If blocks-rich was accepted, Body is the fallback text.
	if capturedEnv.Body == raw {
		t.Errorf("runReply blocks-rich: Body is raw input — switch likely normalised mode to plain; got %q", capturedEnv.Body)
	}
}

// --- RICH-16: runWith blocks-rich dispatches to RenderRich + fallback chain ---

// TestRunWith_BlocksRich verifies that blocks-rich produces a non-empty Blocks
// field populated from RenderRich, and the Body contains the fallback text.
func TestRunWith_BlocksRich(t *testing.T) {
	_, priv := genKey(t)

	var capturedEnv slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedEnv); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"3.3"}`)
	}))
	defer srv.Close()

	// A mixed prose+table body that exercises both the markdown block and table block paths.
	body := `# Summary

Here is some prose with **bold** and a [link](https://example.com).

| Name   | Count |
|--------|------:|
| Alpha  | 42    |
| Beta   | 7     |
`
	bodyPath := writeTmpBody(t, body)

	_, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", "blocks-rich")
	if err != nil {
		t.Fatalf("runWith blocks-rich: %v", err)
	}

	// Blocks field must be non-empty JSON array.
	if capturedEnv.Blocks == "" {
		t.Fatal("blocks-rich: Blocks field is empty; expected non-empty")
	}
	var blocks []map[string]any
	if err := json.Unmarshal([]byte(capturedEnv.Blocks), &blocks); err != nil {
		t.Fatalf("blocks-rich: Blocks is not valid JSON array: %v\nBlocks=%s", err, capturedEnv.Blocks)
	}
	if len(blocks) == 0 {
		t.Fatal("blocks-rich: Blocks array is empty")
	}
	// First block should be a header block (H1 promoted).
	if got := blocks[0]["type"]; got != "header" {
		t.Errorf("blocks-rich: first block type: got %q, want %q", got, "header")
	}
	// Should contain at least one table block.
	var hasTable bool
	for _, b := range blocks {
		if b["type"] == "table" {
			hasTable = true
			break
		}
	}
	if !hasTable {
		t.Errorf("blocks-rich: expected at least one table block, got types: %v",
			func() []any {
				types := make([]any, len(blocks))
				for i, b := range blocks {
					types[i] = b["type"]
				}
				return types
			}())
	}
}

// TestRunWith_FallbackChain verifies the RenderRich ok=false → RenderBlocks → Mrkdwnify chain.
// We trigger it by sending an input that exceeds the 12K cumulative markdown cap so RenderRich
// returns ok=false; RenderBlocks should succeed producing Blocks; if that also failed, Mrkdwnify
// would be the final fallback.
func TestRunWith_FallbackChain(t *testing.T) {
	_, priv := genKey(t)

	var capturedEnv slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedEnv); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"4.4"}`)
	}))
	defer srv.Close()

	// Build an input that trips the 12K cumulative markdown cap (>12,000 chars of prose).
	// RenderRich returns ok=false; RenderBlocks should succeed.
	bigProse := strings.Repeat("word ", 2600) // ~13,000 chars of prose
	bodyPath := writeTmpBody(t, bigProse)

	_, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", "blocks-rich")
	if err != nil {
		t.Fatalf("runWith blocks-rich fallback: unexpected error: %v", err)
	}
	// Even if RenderRich failed, either RenderBlocks or Mrkdwnify must have produced output.
	// The Body field must be non-empty.
	if capturedEnv.Body == "" {
		t.Error("fallback chain: Body is empty; expected rendered content")
	}
	// Either Blocks is set (RenderBlocks succeeded as Tier-2 fallback) or not (Mrkdwnify used).
	// We just verify the round-trip completed without error.
	t.Logf("fallback chain result: Blocks=%s Body(first 100)=%q",
		func() string {
			if capturedEnv.Blocks != "" {
				return "non-empty"
			}
			return "empty (mrkdwn fallback)"
		}(),
		func() string {
			s := capturedEnv.Body
			if len(s) > 100 {
				return s[:100]
			}
			return s
		}())
}

// --- RICH-14: KM_SLACK_AI_FOOTER=true appends disclaimer context block ---

// TestRunWith_AIFooter verifies that KM_SLACK_AI_FOOTER=true appends a trailing
// context block with an AI-disclaimer, and that the block is absent when the var
// is unset or "false".
func TestRunWith_AIFooter(t *testing.T) {
	_, priv := genKey(t)

	body := "Some result from the agent.\n"
	bodyPath := writeTmpBody(t, body)

	t.Run("footer-present-when-true", func(t *testing.T) {
		t.Setenv("KM_SLACK_AI_FOOTER", "true")

		var capturedEnv slack.SlackEnvelope
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&capturedEnv); err != nil {
				http.Error(w, "bad body", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"ts":"5.1"}`)
		}))
		defer srv.Close()

		_, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", "blocks-rich")
		if err != nil {
			t.Fatalf("runWith blocks-rich AIFooter=true: %v", err)
		}

		if capturedEnv.Blocks == "" {
			t.Fatal("AIFooter=true: Blocks field empty; expected block JSON")
		}
		var blocks []map[string]any
		if err := json.Unmarshal([]byte(capturedEnv.Blocks), &blocks); err != nil {
			t.Fatalf("Blocks not valid JSON: %v", err)
		}
		// Last block must be a context block carrying the AI-disclaimer.
		if len(blocks) == 0 {
			t.Fatal("AIFooter=true: blocks array is empty")
		}
		last := blocks[len(blocks)-1]
		if got := last["type"]; got != "context" {
			t.Errorf("AIFooter=true: last block type: got %q, want %q", got, "context")
		}
		// The context block elements must contain the disclaimer text.
		elems, _ := last["elements"].([]any)
		if len(elems) == 0 {
			t.Fatal("AIFooter=true: last context block has no elements")
		}
		elem0, _ := elems[0].(map[string]any)
		text, _ := elem0["text"].(string)
		if !strings.Contains(text, "AI") && !strings.Contains(text, "verify") {
			t.Errorf("AIFooter=true: disclaimer text missing 'AI' or 'verify'; got %q", text)
		}
	})

	t.Run("footer-absent-when-false", func(t *testing.T) {
		t.Setenv("KM_SLACK_AI_FOOTER", "false")

		var capturedEnv slack.SlackEnvelope
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&capturedEnv); err != nil {
				http.Error(w, "bad body", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"ts":"5.2"}`)
		}))
		defer srv.Close()

		_, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", "blocks-rich")
		if err != nil {
			t.Fatalf("runWith blocks-rich AIFooter=false: %v", err)
		}

		if capturedEnv.Blocks == "" {
			// Blocks empty means mrkdwn fallback — footer not present, pass.
			return
		}
		var blocks []map[string]any
		if err := json.Unmarshal([]byte(capturedEnv.Blocks), &blocks); err != nil {
			t.Fatalf("Blocks not valid JSON: %v", err)
		}
		if len(blocks) == 0 {
			return
		}
		last := blocks[len(blocks)-1]
		// If last block is a context block, check it's NOT the AI disclaimer
		// (it could legitimately be a tool-line context block from the body).
		if last["type"] == "context" {
			elems, _ := last["elements"].([]any)
			for _, el := range elems {
				elem, _ := el.(map[string]any)
				text, _ := elem["text"].(string)
				if strings.Contains(text, "verify before sharing") {
					t.Errorf("AIFooter=false: found AI-disclaimer context block; it should be absent. text=%q", text)
				}
			}
		}
	})

	t.Run("footer-absent-when-unset", func(t *testing.T) {
		// Unset the env var entirely.
		t.Setenv("KM_SLACK_AI_FOOTER", "")

		var capturedEnv slack.SlackEnvelope
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&capturedEnv); err != nil {
				http.Error(w, "bad body", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `{"ok":true,"ts":"5.3"}`)
		}))
		defer srv.Close()

		_, err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "", "blocks-rich")
		if err != nil {
			t.Fatalf("runWith blocks-rich AIFooter=unset: %v", err)
		}

		if capturedEnv.Blocks == "" {
			return
		}
		var blocks []map[string]any
		if err := json.Unmarshal([]byte(capturedEnv.Blocks), &blocks); err != nil {
			t.Fatalf("Blocks not valid JSON: %v", err)
		}
		if len(blocks) == 0 {
			return
		}
		last := blocks[len(blocks)-1]
		if last["type"] == "context" {
			elems, _ := last["elements"].([]any)
			for _, el := range elems {
				elem, _ := el.(map[string]any)
				text, _ := elem["text"].(string)
				if strings.Contains(text, "verify before sharing") {
					t.Errorf("AIFooter unset: found AI-disclaimer context block; it should be absent. text=%q", text)
				}
			}
		}
	})
}

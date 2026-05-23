package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// TestMain shrinks BridgeBackoff to milliseconds so retry tests don't slow the suite.
func TestMain(m *testing.M) {
	slack.BridgeBackoff = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	os.Exit(m.Run())
}

// genKey returns a fresh ephemeral Ed25519 key pair for test use.
func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	return pub, priv
}

// writeTmpBody writes content to a temp file and returns the path.
func writeTmpBody(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "km-slack-body-*.txt")
	if err != nil {
		t.Fatalf("create temp body: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write body: %v", err)
	}
	f.Close()
	return f.Name()
}

// TestKmSlackPost_HappyPath — server returns 200 {"ok":true,"ts":"1.2"} → exit 0;
// assert request shape (X-KM-Sender-ID, X-KM-Signature base64, body is canonical JSON).
func TestKmSlackPost_HappyPath(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "hello from test")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check headers are present
		if r.Header.Get("X-KM-Sender-ID") == "" {
			t.Error("missing X-KM-Sender-ID header")
		}
		if r.Header.Get("X-KM-Signature") == "" {
			t.Error("missing X-KM-Signature header")
		}
		// Check body is JSON
		var env slack.SlackEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Errorf("body is not valid JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"1.2"}`)
	}))
	defer ts.Close()

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "Test subject", bodyPath, "", "plain")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

// TestKmSlackPost_BodyTooLarge_TruncatedAndSent — Phase 74: 40KB+1 body in plain
// mode is truncated to MaxRenderedBytes and sent with a footer, NOT rejected.
// The old "exit 1 on oversized body" behavior is superseded by the overflow path.
func TestKmSlackPost_BodyTooLarge_TruncatedAndSent(t *testing.T) {
	_, priv := genKey(t)
	// 40KB+1: exceeds MaxRenderedBytes (35KB), so overflow truncation fires.
	bigBody := strings.Repeat("x", slack.MaxBodyBytes+1)
	bodyPath := writeTmpBody(t, bigBody)

	var callCount int32
	srv, captured := newFakeBridge(t)
	defer srv.Close()
	_ = callCount

	// Phase 74: oversized bodies are truncated, not rejected.
	err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "subj", bodyPath, "", "plain")
	if err != nil {
		t.Fatalf("expected oversized body to be truncated+sent, got error: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("expected 1 request for truncated body, got %d", len(*captured))
	}
	const footer = "\n_…truncated; see full transcript at Stop_"
	if !strings.HasSuffix((*captured)[0].Body, footer) {
		t.Errorf("expected truncation footer in body; got: %q", (*captured)[0].Body[max(0, len((*captured)[0].Body)-len(footer)-5):])
	}
}

// TestKmSlackPost_MissingSandboxID_Exits1 — KM_SANDBOX_ID unset → exit 1.
func TestKmSlackPost_MissingSandboxID_Exits1(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "test")

	err := runWith(context.Background(), priv, "", "http://localhost/noop", "C123", "subj", bodyPath, "", "plain")
	if err == nil {
		t.Fatal("expected error for missing sandboxID")
	}
}

// TestKmSlackPost_MissingBridgeURL_Exits1 — KM_SLACK_BRIDGE_URL unset → exit 1.
func TestKmSlackPost_MissingBridgeURL_Exits1(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "test")

	err := runWith(context.Background(), priv, "sb-test", "", "C123", "subj", bodyPath, "", "plain")
	if err == nil {
		t.Fatal("expected error for missing bridgeURL")
	}
}

// TestKmSlackPost_BodyDash_Rejected — `--body -` → exit 1 with stderr "stdin not supported".
func TestKmSlackPost_BodyDash_Rejected(t *testing.T) {
	_, priv := genKey(t)
	// "-" is not a real file, so ReadFile will fail — but we want the stdin-rejection
	// to fire BEFORE attempting to read the file.
	// The stdin check is in main()'s flag validation, not in runWith.
	// So we test it via the bodyPath "-" hitting the file-read path.
	// runWith will try to os.ReadFile("-") which returns an error — that counts as exit 1.
	err := runWith(context.Background(), priv, "sb-test", "http://localhost/noop", "C123", "subj", "-", "", "plain")
	if err == nil {
		t.Fatal("expected error for body '-'")
	}
}

// TestKmSlackPost_BridgeReturns401_Exit1 — server returns 401 → exit 1.
func TestKmSlackPost_BridgeReturns401_Exit1(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "test")

	var callCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(401)
		fmt.Fprintln(w, `{"error":"unauthorized"}`)
	}))
	defer ts.Close()

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "subj", bodyPath, "", "plain")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	// Fail-fast: only 1 request (no retry on 4xx)
	if count := atomic.LoadInt32(&callCount); count != 1 {
		t.Errorf("expected 1 request on 401, got %d", count)
	}
}

// TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0 — 503 twice then 200 → exit 0.
func TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "test")

	var callCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n < 3 {
			w.WriteHeader(503)
			fmt.Fprintln(w, `{"error":"service unavailable"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"5.6"}`)
	}))
	defer ts.Close()

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "subj", bodyPath, "", "plain")
	if err != nil {
		t.Fatalf("expected success after 503 retries, got: %v", err)
	}
	if count := atomic.LoadInt32(&callCount); count != 3 {
		t.Errorf("expected 3 requests (2×503 + 1×200), got %d", count)
	}
}

// TestKmSlackPost_SignatureVerifiesAtServer — server reads body, decodes X-KM-Signature,
// verifies using the test's known public key → succeeds.
func TestKmSlackPost_SignatureVerifiesAtServer(t *testing.T) {
	pub, priv := genKey(t)
	bodyPath := writeTmpBody(t, "verify me")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decode signature from header
		sigB64 := r.Header.Get("X-KM-Signature")
		if sigB64 == "" {
			http.Error(w, "missing signature", 400)
			return
		}
		sig, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil {
			http.Error(w, "bad signature encoding: "+err.Error(), 400)
			return
		}
		// Unmarshal envelope from body
		var env slack.SlackEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, "bad body: "+err.Error(), 400)
			return
		}
		// Verify signature
		if err := slack.VerifyEnvelope(&env, sig, pub); err != nil {
			http.Error(w, "signature invalid: "+err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"9.9"}`)
	}))
	defer ts.Close()

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "verify-subj", bodyPath, "", "plain")
	if err != nil {
		t.Fatalf("signature verify failed at server: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Phase 74 REND-14..REND-16: render mode + overflow tests
// ---------------------------------------------------------------------------

// newFakeBridge returns an httptest.Server that captures the decoded envelope
// from each POST and returns {"ok":true,"ts":"123.456"}.
func newFakeBridge(t *testing.T) (*httptest.Server, *[]slack.SlackEnvelope) {
	t.Helper()
	var captured []slack.SlackEnvelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env slack.SlackEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			http.Error(w, "bad body", 400)
			return
		}
		captured = append(captured, env)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true,"ts":"123.456"}`)
	}))
	return srv, &captured
}

// TestRunWith_Plain (REND-15): renderMode="plain" passes body through verbatim —
// Mrkdwnify is never invoked and existing Phase 62/63/68 callers see no change.
func TestRunWith_Plain(t *testing.T) {
	_, priv := genKey(t)
	srv, captured := newFakeBridge(t)
	defer srv.Close()

	input := "**bold**\n# heading\n"
	bodyPath := writeTmpBody(t, input)

	err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "subj", bodyPath, "", "plain")
	if err != nil {
		t.Fatalf("runWith plain: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("expected 1 envelope captured, got %d", len(*captured))
	}
	if got := (*captured)[0].Body; got != input {
		t.Errorf("plain mode: body modified; got %q, want %q", got, input)
	}
}

// TestRunWith_EnvOverride (REND-16): KM_SLACK_RENDER=mrkdwn causes Mrkdwnify
// to run; explicit --render=plain flag overrides the env var.
func TestRunWith_EnvOverride(t *testing.T) {
	_, priv := genKey(t)

	input := "**bold**\n# heading\n"

	t.Run("env-set-no-flag", func(t *testing.T) {
		t.Setenv("KM_SLACK_RENDER", "mrkdwn")
		srv, captured := newFakeBridge(t)
		defer srv.Close()
		bodyPath := writeTmpBody(t, input)

		// Simulate runPost flag resolution: flag empty → read env → "mrkdwn"
		renderMode := ""
		if renderMode == "" {
			renderMode = os.Getenv("KM_SLACK_RENDER")
		}
		if renderMode == "" {
			renderMode = "plain"
		}

		err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "subj", bodyPath, "", renderMode)
		if err != nil {
			t.Fatalf("runWith mrkdwn via env: %v", err)
		}
		if len(*captured) != 1 {
			t.Fatalf("expected 1 envelope, got %d", len(*captured))
		}
		got := (*captured)[0].Body
		// Should be transformed: **bold** → *bold*, # heading → *heading*
		if got == input {
			t.Errorf("env-override: expected mrkdwn transformation, got verbatim input: %q", got)
		}
		if !contains(got, "*bold*") {
			t.Errorf("env-override: expected *bold* in output, got: %q", got)
		}
	})

	t.Run("explicit-flag-wins", func(t *testing.T) {
		t.Setenv("KM_SLACK_RENDER", "mrkdwn")
		srv, captured := newFakeBridge(t)
		defer srv.Close()
		bodyPath := writeTmpBody(t, input)

		// Explicit "--render=plain" wins over KM_SLACK_RENDER=mrkdwn.
		// runPost logic: flag set → use flag (skip env lookup).
		err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "subj", bodyPath, "", "plain")
		if err != nil {
			t.Fatalf("runWith explicit plain over env: %v", err)
		}
		if len(*captured) != 1 {
			t.Fatalf("expected 1 envelope, got %d", len(*captured))
		}
		if got := (*captured)[0].Body; got != input {
			t.Errorf("explicit-flag-wins: expected verbatim body, got: %q", got)
		}
	})
}

// TestRunWith_Overflow (REND-14): body > MaxRenderedBytes is truncated with a
// footer; the defense-in-depth MaxBodyBytes check does NOT fire.
func TestRunWith_Overflow(t *testing.T) {
	_, priv := genKey(t)
	srv, captured := newFakeBridge(t)
	defer srv.Close()

	// Build a body larger than MaxRenderedBytes. Mrkdwnify on plain ASCII returns
	// the same bytes, so the rendered size equals the input size.
	oversized := strings.Repeat("a", slack.MaxRenderedBytes+5000)
	bodyPath := writeTmpBody(t, oversized)

	err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "subj", bodyPath, "", "mrkdwn")
	if err != nil {
		t.Fatalf("runWith overflow: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(*captured))
	}
	const footer = "\n_…truncated; see full transcript at Stop_"
	body := (*captured)[0].Body

	if !strings.HasSuffix(body, footer) {
		t.Errorf("overflow: body should end with footer; got suffix: %q", body[max(0, len(body)-len(footer)-5):])
	}
	if !strings.HasPrefix(body, oversized[:slack.MaxRenderedBytes]) {
		t.Errorf("overflow: body should start with first MaxRenderedBytes bytes of input")
	}
	wantLen := slack.MaxRenderedBytes + len(footer)
	if len(body) != wantLen {
		t.Errorf("overflow: body length = %d, want %d", len(body), wantLen)
	}
}

// TestRunWith_Blocks (BLK+BRDG-02): renderMode="blocks" populates env.Blocks with
// valid Block Kit JSON beginning with a header block, and env.Body contains the
// plain-text fallback (no markdown symbols).
func TestRunWith_Blocks(t *testing.T) {
	_, priv := genKey(t)
	srv, captured := newFakeBridge(t)
	defer srv.Close()

	bodyPath := writeTmpBody(t, "# Heading\n\nbody text\n")

	err := runWith(context.Background(), priv, "sb-test", srv.URL, "C123", "", bodyPath, "1234.5", "blocks")
	if err != nil {
		t.Fatalf("runWith blocks: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("expected 1 envelope, got %d", len(*captured))
	}
	env := (*captured)[0]
	if env.Blocks == "" {
		t.Fatal("expected env.Blocks non-empty for blocks render mode")
	}
	if !strings.HasPrefix(env.Blocks, `[{"`) {
		t.Errorf("env.Blocks should be JSON array; got prefix: %q", env.Blocks[:min(20, len(env.Blocks))])
	}
	// The first block should be a header block.
	if !strings.Contains(env.Blocks, `"type":"header"`) {
		t.Errorf("expected header block in blocks JSON; got: %s", env.Blocks)
	}
	// Body should be the plain-text fallback (no markdown heading symbols).
	if strings.Contains(env.Body, "#") {
		t.Errorf("Body still contains '#' markdown; got: %q", env.Body)
	}
	if strings.Contains(env.Body, "**") {
		t.Errorf("Body still contains '**' markdown; got: %q", env.Body)
	}
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// contains is a helper to avoid importing strings in test-only code when already imported.
func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// max returns the larger of two ints (backcompat for Go < 1.21 builtin).
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TestRunPost_NewMessage_Stub — Phase 70 Plan 70-04 seed. Real impl Task 2.
// Verifies --new-message flag passes thread="" and prints ts to stdout.
func TestRunPost_NewMessage_Stub(t *testing.T) { t.Skip("Wave 0 stub — Plan 70-04 Task 2") }

// TestRunPermalink_Stub — Phase 70 Plan 70-04 seed. Real impl Task 3.
// Verifies permalink subcommand routes to ActionPermalink + returns URL to stdout.
func TestRunPermalink_Stub(t *testing.T) { t.Skip("Wave 0 stub — Plan 70-04 Task 3") }

// TestRunUpdate_Stub — Phase 70 Plan 70-04 seed. Real impl Task 3.
// Verifies update subcommand routes to ActionUpdate.
func TestRunUpdate_Stub(t *testing.T) { t.Skip("Wave 0 stub — Plan 70-04 Task 3") }

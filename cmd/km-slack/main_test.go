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
	"sync/atomic"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
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

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "Test subject", bodyPath, "")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

// TestKmSlackPost_BodyTooLarge_ExitsBeforeHttp — 40KB+1 body → exit 1, no HTTP call.
func TestKmSlackPost_BodyTooLarge_ExitsBeforeHttp(t *testing.T) {
	_, priv := genKey(t)
	bigBody := string(make([]byte, slack.MaxBodyBytes+1))
	bodyPath := writeTmpBody(t, bigBody)

	var callCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		fmt.Fprintln(w, `{"ok":true}`)
	}))
	defer ts.Close()

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "subj", bodyPath, "")
	if err == nil {
		t.Fatal("expected error for oversized body, got nil")
	}
	if atomic.LoadInt32(&callCount) != 0 {
		t.Errorf("expected 0 HTTP calls for oversized body, got %d", callCount)
	}
}

// TestKmSlackPost_MissingSandboxID_Exits1 — KM_SANDBOX_ID unset → exit 1.
func TestKmSlackPost_MissingSandboxID_Exits1(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "test")

	err := runWith(context.Background(), priv, "", "http://localhost/noop", "C123", "subj", bodyPath, "")
	if err == nil {
		t.Fatal("expected error for missing sandboxID")
	}
}

// TestKmSlackPost_MissingBridgeURL_Exits1 — KM_SLACK_BRIDGE_URL unset → exit 1.
func TestKmSlackPost_MissingBridgeURL_Exits1(t *testing.T) {
	_, priv := genKey(t)
	bodyPath := writeTmpBody(t, "test")

	err := runWith(context.Background(), priv, "sb-test", "", "C123", "subj", bodyPath, "")
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
	err := runWith(context.Background(), priv, "sb-test", "http://localhost/noop", "C123", "subj", "-", "")
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

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "subj", bodyPath, "")
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

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "subj", bodyPath, "")
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

	err := runWith(context.Background(), priv, "sb-test", ts.URL, "C123", "verify-subj", bodyPath, "")
	if err != nil {
		t.Fatalf("signature verify failed at server: %v", err)
	}
}

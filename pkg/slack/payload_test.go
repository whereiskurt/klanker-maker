package slack_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// makeFixedEnvelope returns a deterministic SlackEnvelope for signing/canonical tests.
func makeFixedEnvelope() *slack.SlackEnvelope {
	return &slack.SlackEnvelope{
		Action:    slack.ActionPost,
		Body:      "hello",
		Channel:   "C0123ABC",
		Nonce:     "00000000000000000000000000000000",
		SenderID:  "sb-abc123",
		Subject:   "[sb-abc123] needs permission",
		ThreadTS:  "",
		Timestamp: 1714280400,
		Version:   1,
	}
}

func TestBuildEnvelope_HappyPath(t *testing.T) {
	env, err := slack.BuildEnvelope(slack.ActionPost, "sb-abc123", "C0123ABC", "test subject", "test body", "")
	if err != nil {
		t.Fatalf("BuildEnvelope returned error: %v", err)
	}
	if env.Action != slack.ActionPost {
		t.Errorf("Action = %q; want %q", env.Action, slack.ActionPost)
	}
	if env.SenderID != "sb-abc123" {
		t.Errorf("SenderID = %q; want %q", env.SenderID, "sb-abc123")
	}
	if env.Channel != "C0123ABC" {
		t.Errorf("Channel = %q; want %q", env.Channel, "C0123ABC")
	}
	if env.Subject != "test subject" {
		t.Errorf("Subject = %q; want %q", env.Subject, "test subject")
	}
	if env.Body != "test body" {
		t.Errorf("Body = %q; want %q", env.Body, "test body")
	}
	if env.ThreadTS != "" {
		t.Errorf("ThreadTS = %q; want empty", env.ThreadTS)
	}
	if env.Version != slack.EnvelopeVersion {
		t.Errorf("Version = %d; want %d", env.Version, slack.EnvelopeVersion)
	}
	if len(env.Nonce) != 32 {
		t.Errorf("Nonce length = %d; want 32 hex chars", len(env.Nonce))
	}
	// Nonce should be hex
	for _, c := range env.Nonce {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("Nonce %q contains non-hex character %c", env.Nonce, c)
			break
		}
	}
	// Timestamp should be approximately now
	now := time.Now().Unix()
	if env.Timestamp < now-5 || env.Timestamp > now+5 {
		t.Errorf("Timestamp %d is too far from now %d", env.Timestamp, now)
	}
}

func TestBuildEnvelope_BodyTooLarge_ReturnsErr(t *testing.T) {
	body := strings.Repeat("a", slack.MaxBodyBytes+1)
	_, err := slack.BuildEnvelope(slack.ActionPost, "x", "C", "s", body, "")
	if err == nil {
		t.Fatal("expected ErrBodyTooLarge, got nil")
	}
	if err != slack.ErrBodyTooLarge {
		t.Errorf("error = %v; want ErrBodyTooLarge", err)
	}
}

func TestBuildEnvelope_BodyAtBoundary_OK(t *testing.T) {
	body := strings.Repeat("a", slack.MaxBodyBytes)
	env, err := slack.BuildEnvelope(slack.ActionPost, "x", "C", "s", body, "")
	if err != nil {
		t.Fatalf("expected success at boundary, got error: %v", err)
	}
	if len(env.Body) != slack.MaxBodyBytes {
		t.Errorf("Body length = %d; want %d", len(env.Body), slack.MaxBodyBytes)
	}
}

func TestCanonicalJSON_Deterministic(t *testing.T) {
	env := makeFixedEnvelope()
	b1, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON first call: %v", err)
	}
	b2, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON second call: %v", err)
	}
	if string(b1) != string(b2) {
		t.Errorf("CanonicalJSON not deterministic:\n  first:  %s\n  second: %s", b1, b2)
	}
}

func TestCanonicalJSON_FieldOrderAlphabetical(t *testing.T) {
	env := makeFixedEnvelope()
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	s := string(b)

	// Fields in alphabetical-by-tag order. Phase 68 added four upload-only
	// fields (content_type, filename, s3_key, size_bytes); they serialize
	// as zero values for non-upload actions so canonical signing remains
	// deterministic across all action types. Phase 70 added message_ts and text.
	// Phase 110 added session_id (alphabetical: after s3_key, before sender_id).
	fields := []string{
		`"action"`, `"blocks"`, `"body"`, `"channel"`, `"content_type"`, `"filename"`,
		`"message_ts"`, `"nonce"`, `"s3_key"`, `"session_id"`, `"sender_id"`, `"size_bytes"`,
		`"subject"`, `"text"`, `"thread_ts"`, `"timestamp"`, `"version"`,
	}
	last := 0
	for _, field := range fields {
		pos := strings.Index(s, field)
		if pos < 0 {
			t.Errorf("field %s not found in JSON: %s", field, s)
			continue
		}
		if pos < last {
			t.Errorf("field %s appears before previous field in JSON: %s", field, s)
		}
		last = pos
	}

	// Also check against a golden constant. The exact output should match
	// this (no trailing newline, fields alphabetical). The four Phase 68
	// upload fields appear at their alphabetical positions with zero values.
	// Phase 74 PR2 added "blocks" between "action" and "body" (alphabetical).
	// Phase 70 added "message_ts" (after filename, before nonce) and "text"
	// (after subject, before thread_ts).
	golden := `{"action":"post","blocks":"","body":"hello","channel":"C0123ABC","content_type":"","filename":"","message_ts":"","nonce":"00000000000000000000000000000000","s3_key":"","session_id":"","sender_id":"sb-abc123","size_bytes":0,"subject":"[sb-abc123] needs permission","text":"","thread_ts":"","timestamp":1714280400,"version":1}`
	if s != golden {
		t.Errorf("canonical JSON mismatch:\n  got:  %s\n  want: %s", s, golden)
	}
}

func TestCanonicalJSON_DifferentNonce_DifferentBytes(t *testing.T) {
	env1 := makeFixedEnvelope()
	env2 := makeFixedEnvelope()
	env2.Nonce = "ffffffffffffffffffffffffffffffff"

	b1, err := slack.CanonicalJSON(env1)
	if err != nil {
		t.Fatalf("CanonicalJSON env1: %v", err)
	}
	b2, err := slack.CanonicalJSON(env2)
	if err != nil {
		t.Fatalf("CanonicalJSON env2: %v", err)
	}
	if string(b1) == string(b2) {
		t.Error("expected different bytes for different nonces, got identical output")
	}
}

func TestSignVerifyEnvelope_RoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	env := makeFixedEnvelope()
	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		t.Fatalf("SignEnvelope: %v", err)
	}

	if err := slack.VerifyEnvelope(env, sig, pub); err != nil {
		t.Errorf("VerifyEnvelope failed: %v", err)
	}
}

func TestVerifyEnvelope_WrongKey_Fails(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey priv: %v", err)
	}
	wrongPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey wrongPub: %v", err)
	}

	env := makeFixedEnvelope()
	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		t.Fatalf("SignEnvelope: %v", err)
	}

	if err := slack.VerifyEnvelope(env, sig, wrongPub); err == nil {
		t.Error("expected verification to fail with wrong key, got nil")
	}
}

func TestVerifyEnvelope_MutatedBody_Fails(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	env := makeFixedEnvelope()
	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		t.Fatalf("SignEnvelope: %v", err)
	}

	// Mutate the body after signing
	env.Body = "mutated body"

	if err := slack.VerifyEnvelope(env, sig, pub); err == nil {
		t.Error("expected verification to fail after body mutation, got nil")
	}
}

// TestPayload_PermalinkAction asserts the ActionPermalink constant value and that a
// permalink envelope carries the MessageTS field correctly. Phase 70 Plan 70-04.
func TestPayload_PermalinkAction(t *testing.T) {
	if slack.ActionPermalink != "permalink" {
		t.Errorf("ActionPermalink = %q; want %q", slack.ActionPermalink, "permalink")
	}
	// Build a permalink envelope and confirm MessageTS round-trips through canonical JSON.
	env, err := slack.BuildEnvelope(slack.ActionPermalink, "sb-test", "C123", "", "", "")
	if err != nil {
		t.Fatalf("BuildEnvelope(ActionPermalink): %v", err)
	}
	env.MessageTS = "1701000000.001"
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if !strings.Contains(string(b), `"message_ts":"1701000000.001"`) {
		t.Errorf("canonical JSON missing message_ts; got: %s", string(b))
	}
}

// TestCanonicalJSON_SessionID asserts that a non-empty SessionID serializes with
// "session_id" appearing AFTER "s3_key" and BEFORE "sender_id" in the canonical
// JSON bytes. Phase 110 Plan 02: the field must sit in alphabetical-by-tag position
// so sign/verify produce byte-identical output on both sender and verifier sides.
func TestCanonicalJSON_SessionID(t *testing.T) {
	env := makeFixedEnvelope()
	env.SessionID = "01JXXXXXXXXXXXXXXXXXXXXXXXXX"

	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	s := string(b)

	s3KeyPos := strings.Index(s, `"s3_key"`)
	sessionPos := strings.Index(s, `"session_id"`)
	senderPos := strings.Index(s, `"sender_id"`)

	if s3KeyPos < 0 {
		t.Fatalf("s3_key not found in canonical JSON: %s", s)
	}
	if sessionPos < 0 {
		t.Fatalf("session_id not found in canonical JSON: %s", s)
	}
	if senderPos < 0 {
		t.Fatalf("sender_id not found in canonical JSON: %s", s)
	}

	if sessionPos <= s3KeyPos {
		t.Errorf("session_id (pos %d) must appear AFTER s3_key (pos %d); got: %s", sessionPos, s3KeyPos, s)
	}
	if sessionPos >= senderPos {
		t.Errorf("session_id (pos %d) must appear BEFORE sender_id (pos %d); got: %s", sessionPos, senderPos, s)
	}

	// Also confirm the value round-trips.
	if !strings.Contains(s, `"session_id":"01JXXXXXXXXXXXXXXXXXXXXXXXXX"`) {
		t.Errorf("session_id value not found in canonical JSON: %s", s)
	}
}

// TestCanonicalJSON_ZeroSessionID asserts that a zero-valued SessionID still
// serializes the key (back-compat with existing signers that don't set it).
func TestCanonicalJSON_ZeroSessionID(t *testing.T) {
	env := makeFixedEnvelope()
	// SessionID is zero-valued (empty string).

	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	s := string(b)

	if !strings.Contains(s, `"session_id":""`) {
		t.Errorf("zero session_id key not found in canonical JSON: %s", s)
	}
}

// TestPayload_UpdateAction asserts the ActionUpdate constant value and that an
// update envelope carries the MessageTS + Text fields correctly. Phase 70 Plan 70-04.
func TestPayload_UpdateAction(t *testing.T) {
	if slack.ActionUpdate != "update" {
		t.Errorf("ActionUpdate = %q; want %q", slack.ActionUpdate, "update")
	}
	// Build an update envelope and confirm MessageTS + Text round-trip.
	env, err := slack.BuildEnvelope(slack.ActionUpdate, "sb-test", "C123", "", "", "")
	if err != nil {
		t.Fatalf("BuildEnvelope(ActionUpdate): %v", err)
	}
	env.MessageTS = "1701000000.001"
	env.Text = "edited body"
	b, err := slack.CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"message_ts":"1701000000.001"`) {
		t.Errorf("canonical JSON missing message_ts; got: %s", s)
	}
	if !strings.Contains(s, `"text":"edited body"`) {
		t.Errorf("canonical JSON missing text; got: %s", s)
	}
}

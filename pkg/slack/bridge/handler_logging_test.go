package bridge_test

// handler_logging_test.go — Part A tests for structured logging in Handle.
// Each test captures log output via a bytes.Buffer slog handler and asserts
// that the expected log key=value pairs appear.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
	"github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
)

// captureLogger installs a bytes.Buffer slog handler and returns the buffer.
// The caller MUST call restore() in a defer to reinstate the previous logger.
func captureLogger(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	bridge.SetLogger(slog.New(h))
	return &buf, func() {
		// Reset to discard logger (avoids stderr noise from other tests).
		bridge.SetLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	}
}

// assertLog checks that a log buffer contains ALL the expected substrings.
func assertLog(t *testing.T, buf *bytes.Buffer, want ...string) {
	t.Helper()
	out := buf.String()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("log output missing %q\nfull log:\n%s", w, out)
		}
	}
}

// ──────────────────────────────────────────────
// Logging tests
// ──────────────────────────────────────────────

// TestHandlerLog_HappyPath_EmitsInfoRequest confirms that a successful sandbox post
// emits an INFO request line and an INFO ok line.
func TestHandlerLog_HappyPath_EmitsInfoRequest(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &fakeSlack{returnTS: "ts.log.ok"}
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb-test"},
		Slack:    fs,
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200. Body: %s", resp.StatusCode, resp.Body)
	}

	assertLog(t, buf,
		"bridge: request",
		`action=post`,
		`sender_id=sb-abc123`,
		`channel=C0123ABC`,
		"bridge: ok",
	)
}

// TestHandlerLog_BadEnvelope_EmitsWarn confirms that a parse failure logs WARN bad_envelope.
func TestHandlerLog_BadEnvelope_EmitsWarn(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{},
	}

	req := &bridge.Request{Body: "{not json", Headers: map[string]string{}}
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400", resp.StatusCode)
	}
	assertLog(t, buf, "bridge: bad_envelope", "step=parse")
}

// TestHandlerLog_StaleTimestamp_EmitsWarnWithSkew confirms stale_timestamp logs skew_seconds.
func TestHandlerLog_StaleTimestamp_EmitsWarnWithSkew(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	env.Timestamp = 1714280400 - 600
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 401 {
		t.Fatalf("StatusCode = %d; want 401", resp.StatusCode)
	}
	assertLog(t, buf, "bridge: stale_timestamp", "skew_seconds=600", "step=timestamp")
}

// TestHandlerLog_ReplayedNonce_EmitsWarnWithNoncePrefix confirms replayed_nonce logs nonce_prefix.
func TestHandlerLog_ReplayedNonce_EmitsWarnWithNoncePrefix(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fn := &fakeNonces{}
	fs := &fakeSlack{returnTS: "ts1"}
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   fn,
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    fs,
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)

	// First call — succeeds; second call — replayed nonce.
	h.Handle(context.Background(), req)
	buf.Reset() // clear first-call logs

	resp := h.Handle(context.Background(), req)
	if resp.StatusCode != 401 {
		t.Fatalf("StatusCode = %d; want 401", resp.StatusCode)
	}
	assertLog(t, buf, "bridge: replayed_nonce", "nonce_prefix=aabbccdd", "step=nonce")
}

// TestHandlerLog_NonceDynamoUnavailable_EmitsError confirms DynamoDB failure logs ERROR.
func TestHandlerLog_NonceDynamoUnavailable_EmitsError(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Nonces = &fakeNonces{fail: true}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 500 {
		t.Fatalf("StatusCode = %d; want 500", resp.StatusCode)
	}
	assertLog(t, buf, "bridge: nonce_unavailable", "step=nonce", "error=")
}

// TestHandlerLog_BotTokenUnavailable_EmitsError confirms bot_token_unavailable logs ERROR
// with the underlying SSM error string.
func TestHandlerLog_BotTokenUnavailable_EmitsError(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{err: errors.New("ssm unavailable")},
		Slack:    &fakeSlack{},
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 500 {
		t.Fatalf("StatusCode = %d; want 500", resp.StatusCode)
	}
	assertLog(t, buf, "bridge: bot_token_unavailable", "ssm unavailable", "step=token_fetch")
}

// TestHandlerLog_SlackUpstreamError_EmitsErrorWithSlackCode confirms that when the Slack
// API returns an error (e.g. not_in_channel → 502 from bridge), the handler logs the full
// slack_error string and HTTP status — the key UAT bug this task fixes.
func TestHandlerLog_SlackUpstreamError_EmitsErrorWithSlackCode(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Slack = &fakeSlack{
		postErr: errors.New("slack chat.postMessage: not_in_channel"),
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 502 {
		t.Fatalf("StatusCode = %d; want 502. Body: %s", resp.StatusCode, resp.Body)
	}
	// The log MUST include the full Slack error so CloudWatch shows the real failure.
	assertLog(t, buf,
		"bridge: slack_call_failed",
		"not_in_channel",
		"step=dispatch",
		"status=502",
	)
}

// TestHandlerLog_ChannelMismatch_EmitsWarnWithOwnedChannel confirms channel_mismatch logs
// the owned_channel and requested channel for quick diagnosis.
func TestHandlerLog_ChannelMismatch_EmitsWarnWithOwnedChannel(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Slack = &fakeSlack{}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C9999") // wrong channel
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 403 {
		t.Fatalf("StatusCode = %d; want 403", resp.StatusCode)
	}
	assertLog(t, buf,
		"bridge: channel_mismatch",
		"channel=C9999",
		"owned_channel=C0123ABC",
		"step=authz",
	)
}

// TestHandlerLog_BadSignature_EmitsWarn confirms bad_signature logs sender_id + error.
func TestHandlerLog_BadSignature_EmitsWarn(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	pubA, _, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)

	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pubA}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{},
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, privB) // signed with wrong key
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 401 {
		t.Fatalf("StatusCode = %d; want 401", resp.StatusCode)
	}
	assertLog(t, buf, "bridge: bad_signature", "sender_id=sb-abc123", "step=signature")
}

// TestHandlerLog_UnknownSender_EmitsWarn confirms unknown_sender logs sender_id.
func TestHandlerLog_UnknownSender_EmitsWarn(t *testing.T) {
	buf, restore := captureLogger(t)
	defer restore()

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{},
	}

	// Sign with a key that is NOT registered.
	env := makeEnv(slack.ActionPost, "sb-unknown", "C0123ABC")
	canonical, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		t.Fatalf("SignEnvelope: %v", err)
	}
	req := &bridge.Request{
		Body: string(canonical),
		Headers: map[string]string{
			"X-KM-Sender-ID": env.SenderID,
			"X-KM-Signature": base64.StdEncoding.EncodeToString(sig),
		},
	}
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 404 {
		t.Fatalf("StatusCode = %d; want 404", resp.StatusCode)
	}
	assertLog(t, buf, "bridge: unknown_sender", "sender_id=sb-unknown", "step=key_fetch")
}

package bridge_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
	"github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
)

// ---------------------------------------------------------------------------
// In-memory fakes
// ---------------------------------------------------------------------------

type fakeKeys struct {
	keys map[string]ed25519.PublicKey
}

func (f *fakeKeys) Fetch(_ context.Context, id string) (ed25519.PublicKey, error) {
	if k, ok := f.keys[id]; ok {
		return k, nil
	}
	return nil, bridge.ErrSenderNotFound
}

type fakeNonces struct {
	seen map[string]bool
	fail bool
}

func (f *fakeNonces) Reserve(_ context.Context, n string, _ int) error {
	if f.fail {
		return errors.New("ddb down")
	}
	if f.seen[n] {
		return bridge.ErrNonceReplayed
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	f.seen[n] = true
	return nil
}

type fakeChannels struct {
	owned map[string]string
}

func (f *fakeChannels) OwnedChannel(_ context.Context, id string) (string, error) {
	return f.owned[id], nil
}

type fakeToken struct {
	tok string
	err error
}

func (f *fakeToken) Fetch(_ context.Context) (string, error) { return f.tok, f.err }

type fakeSlack struct {
	posted     []string
	archived   []string
	postErr    error
	archiveErr error
	returnTS   string
}

func (f *fakeSlack) PostMessage(_ context.Context, ch, subj, body, _ string) (string, error) {
	if f.postErr != nil {
		return "", f.postErr
	}
	f.posted = append(f.posted, ch+"|"+subj+"|"+body)
	return f.returnTS, nil
}

func (f *fakeSlack) ArchiveChannel(_ context.Context, ch string) error {
	if f.archiveErr != nil {
		return f.archiveErr
	}
	f.archived = append(f.archived, ch)
	return nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// signRequest builds a signed Request for the given envelope using the given
// private key. The canonical JSON is used as the body (reflects what was
// signed), and X-KM-Sender-ID + X-KM-Signature headers are set.
func signRequest(t *testing.T, env *slack.SlackEnvelope, priv ed25519.PrivateKey) *bridge.Request {
	t.Helper()
	canonical, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		t.Fatalf("SignEnvelope: %v", err)
	}
	return &bridge.Request{
		Body: string(canonical),
		Headers: map[string]string{
			"X-KM-Sender-ID": env.SenderID,
			"X-KM-Signature": base64.StdEncoding.EncodeToString(sig),
		},
	}
}

// defaultHandler returns a fully-wired Handler for happy-path sandbox tests.
// sandboxID is "sb-abc123" by default with channel "C0123ABC".
func defaultHandler(pub ed25519.PublicKey) *bridge.Handler {
	return &bridge.Handler{
		Now: func() time.Time { return time.Unix(1714280400, 0) },
		Keys: &fakeKeys{keys: map[string]ed25519.PublicKey{
			"sb-abc123": pub,
		}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb-test"},
		Slack:    &fakeSlack{returnTS: "1714280400.000001"},
	}
}

// makeEnv builds a SlackEnvelope with a fixed timestamp matching defaultHandler.Now().
func makeEnv(action, senderID, channel string) *slack.SlackEnvelope {
	return &slack.SlackEnvelope{
		Action:    action,
		Body:      "test body",
		Channel:   channel,
		Nonce:     "aabbccddeeff00112233445566778899",
		SenderID:  senderID,
		Subject:   "[" + senderID + "] subject",
		ThreadTS:  "",
		Timestamp: 1714280400,
		Version:   slack.EnvelopeVersion,
	}
}

// responseBody decodes the JSON body of a Response into a map.
func responseBody(t *testing.T, resp *bridge.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &m); err != nil {
		t.Fatalf("unmarshal response body %q: %v", resp.Body, err)
	}
	return m
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestHandler_ValidSandboxPost_ChatPostMessage: sandbox sb-abc123 posts to its
// own channel C0123ABC → 200, fakeSlack.posted has exactly one entry.
func TestHandler_ValidSandboxPost_ChatPostMessage(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &fakeSlack{returnTS: "111.222"}
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    fs,
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200. Body: %s", resp.StatusCode, resp.Body)
	}
	if len(fs.posted) != 1 {
		t.Errorf("fakeSlack.posted len = %d; want 1", len(fs.posted))
	}
	body := responseBody(t, resp)
	if body["ok"] != true {
		t.Errorf("ok = %v; want true", body["ok"])
	}
	if body["ts"] != "111.222" {
		t.Errorf("ts = %v; want 111.222", body["ts"])
	}
}

// TestHandler_ChannelMismatch_403: sandbox posts to C9999 but owned channel is C0123ABC.
func TestHandler_ChannelMismatch_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Slack = &fakeSlack{}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C9999")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 403 {
		t.Fatalf("StatusCode = %d; want 403. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "channel_mismatch" {
		t.Errorf("error = %v; want channel_mismatch", body["error"])
	}
}

// TestHandler_SandboxArchive_403: sandbox sends action=archive → 403.
func TestHandler_SandboxArchive_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)

	env := makeEnv(slack.ActionArchive, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 403 {
		t.Fatalf("StatusCode = %d; want 403. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "sandbox_action_forbidden" {
		t.Errorf("error = %v; want sandbox_action_forbidden", body["error"])
	}
}

// TestHandler_SandboxTest_403: sandbox sends action=test → 403.
func TestHandler_SandboxTest_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)

	env := makeEnv(slack.ActionTest, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 403 {
		t.Fatalf("StatusCode = %d; want 403. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "sandbox_action_forbidden" {
		t.Errorf("error = %v; want sandbox_action_forbidden", body["error"])
	}
}

// TestHandler_OperatorArchive_OK: operator sends action=archive → 200, archived.
func TestHandler_OperatorArchive_OK(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &fakeSlack{}
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{slack.SenderOperator: pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    fs,
	}

	env := makeEnv(slack.ActionArchive, slack.SenderOperator, "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200. Body: %s", resp.StatusCode, resp.Body)
	}
	if len(fs.archived) != 1 || fs.archived[0] != "C0123ABC" {
		t.Errorf("archived = %v; want [C0123ABC]", fs.archived)
	}
}

// TestHandler_OperatorTest_PostsToSharedChannel: operator action=test → 200,
// fakeSlack.posted contains the channel.
func TestHandler_OperatorTest_PostsToSharedChannel(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &fakeSlack{returnTS: "ts.test"}
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{slack.SenderOperator: pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    fs,
	}

	env := makeEnv(slack.ActionTest, slack.SenderOperator, "C-SHARED")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200. Body: %s", resp.StatusCode, resp.Body)
	}
	if len(fs.posted) != 1 {
		t.Errorf("posted len = %d; want 1", len(fs.posted))
	}
}

// TestHandler_StaleTimestamp_401: env.Timestamp = now - 600s → 401.
func TestHandler_StaleTimestamp_401(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	env.Timestamp = 1714280400 - 600 // 600s in the past; handler.Now = 1714280400
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 401 {
		t.Fatalf("StatusCode = %d; want 401. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "stale_timestamp" {
		t.Errorf("error = %v; want stale_timestamp", body["error"])
	}
}

// TestHandler_FutureTimestamp_401: env.Timestamp = now + 600s → 401.
func TestHandler_FutureTimestamp_401(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	env.Timestamp = 1714280400 + 600 // 600s in the future
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 401 {
		t.Fatalf("StatusCode = %d; want 401. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "stale_timestamp" {
		t.Errorf("error = %v; want stale_timestamp", body["error"])
	}
}

// TestHandler_ReplayedNonce_401: first call → 200, second identical call → 401.
func TestHandler_ReplayedNonce_401(t *testing.T) {
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

	// First call should succeed.
	resp1 := h.Handle(context.Background(), req)
	if resp1.StatusCode != 200 {
		t.Fatalf("first call StatusCode = %d; want 200. Body: %s", resp1.StatusCode, resp1.Body)
	}

	// Second call with same envelope (same nonce) should fail.
	resp2 := h.Handle(context.Background(), req)
	if resp2.StatusCode != 401 {
		t.Fatalf("second call StatusCode = %d; want 401. Body: %s", resp2.StatusCode, resp2.Body)
	}
	body := responseBody(t, resp2)
	if body["error"] != "replayed_nonce" {
		t.Errorf("error = %v; want replayed_nonce", body["error"])
	}
}

// TestHandler_BadSignature_401: sign with key A but register public key B.
func TestHandler_BadSignature_401(t *testing.T) {
	pubA, _, _ := ed25519.GenerateKey(rand.Reader)
	_, privB, _ := ed25519.GenerateKey(rand.Reader)

	// Register pubA but sign with privB — mismatch.
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
		t.Fatalf("StatusCode = %d; want 401. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "bad_signature" {
		t.Errorf("error = %v; want bad_signature", body["error"])
	}
}

// TestHandler_UnknownSender_404: sender_id not in fakeKeys → 404.
func TestHandler_UnknownSender_404(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{}}, // empty
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{},
	}

	env := makeEnv(slack.ActionPost, "sb-unknown", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 404 {
		t.Fatalf("StatusCode = %d; want 404. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "unknown_sender" {
		t.Errorf("error = %v; want unknown_sender", body["error"])
	}
}

// TestHandler_BotTokenMissing_500: fakeToken returns error → 500.
func TestHandler_BotTokenMissing_500(t *testing.T) {
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
		t.Fatalf("StatusCode = %d; want 500. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "bot_token_unavailable" {
		t.Errorf("error = %v; want bot_token_unavailable", body["error"])
	}
}

// TestHandler_SlackRateLimited_503_RetryAfter: postErr = ErrSlackRateLimited{10} →
// 503 with Retry-After: "10".
func TestHandler_SlackRateLimited_503_RetryAfter(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Slack = &fakeSlack{
		postErr: &bridge.ErrSlackRateLimited{RetryAfterSeconds: 10, Method: "chat.postMessage"},
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 503 {
		t.Fatalf("StatusCode = %d; want 503. Body: %s", resp.StatusCode, resp.Body)
	}
	if resp.Headers["Retry-After"] != "10" {
		t.Errorf("Retry-After header = %q; want 10", resp.Headers["Retry-After"])
	}
	body := responseBody(t, resp)
	if body["error"] != "rate_limited" {
		t.Errorf("error = %v; want rate_limited", body["error"])
	}
}

// TestHandler_Slack5xx_502_CodePropagated: postErr message contains colon-separated
// code → 502 with that code.
func TestHandler_Slack5xx_502_CodePropagated(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Slack = &fakeSlack{
		postErr: errors.New("slack chat.postMessage: channel_not_found"),
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 502 {
		t.Fatalf("StatusCode = %d; want 502. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "channel_not_found" {
		t.Errorf("error = %v; want channel_not_found", body["error"])
	}
}

// TestHandler_DynamoDBNonceUnavailable_500: fakeNonces.fail=true → 500.
func TestHandler_DynamoDBNonceUnavailable_500(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := defaultHandler(pub)
	h.Nonces = &fakeNonces{fail: true}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 500 {
		t.Fatalf("StatusCode = %d; want 500. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "nonce_unavailable" {
		t.Errorf("error = %v; want nonce_unavailable", body["error"])
	}
}

// TestHandler_BadEnvelope_400: req.Body is not valid JSON → 400.
func TestHandler_BadEnvelope_400(t *testing.T) {
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{},
	}

	req := &bridge.Request{
		Body:    "{not json",
		Headers: map[string]string{},
	}
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "bad_envelope" {
		t.Errorf("error = %v; want bad_envelope", body["error"])
	}
}

// TestHandler_MissingFields_400: Action="" → 400 missing_fields.
func TestHandler_MissingFields_400(t *testing.T) {
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{},
	}

	env := &slack.SlackEnvelope{
		// Action intentionally empty
		Body:      "body",
		Channel:   "C0123ABC",
		Nonce:     "aabb",
		SenderID:  "sb-abc123",
		Subject:   "subject",
		Timestamp: 1714280400,
		Version:   1,
	}
	b, _ := json.Marshal(env)
	req := &bridge.Request{
		Body:    string(b),
		Headers: map[string]string{},
	}
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "missing_fields" {
		t.Errorf("error = %v; want missing_fields", body["error"])
	}
}

// TestHandler_HeaderSenderMismatch_401: env.SenderID="sb-a" but X-KM-Sender-ID="sb-b".
func TestHandler_HeaderSenderMismatch_401(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-a": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-a": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{},
	}

	env := makeEnv(slack.ActionPost, "sb-a", "C0123ABC")
	req := signRequest(t, env, priv)
	// Override the sender ID header to a different value.
	req.Headers["X-KM-Sender-ID"] = "sb-b"
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 401 {
		t.Fatalf("StatusCode = %d; want 401. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "sender_header_mismatch" {
		t.Errorf("error = %v; want sender_header_mismatch", body["error"])
	}
}

// TestHandler_HappyPath_NoHeaderSender_StillVerifies: omit X-KM-Sender-ID header
// (only signature header) → still works.
func TestHandler_HappyPath_NoHeaderSender_StillVerifies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	fs := &fakeSlack{returnTS: "ts.ok"}
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    fs,
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	canonical, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		t.Fatalf("SignEnvelope: %v", err)
	}
	// No X-KM-Sender-ID header — only signature.
	req := &bridge.Request{
		Body: string(canonical),
		Headers: map[string]string{
			"X-KM-Signature": base64.StdEncoding.EncodeToString(sig),
		},
	}
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200. Body: %s", resp.StatusCode, resp.Body)
	}
}

// TestHandler_ArchiveChannel_SlackError_502: archiveErr is a plain error → 502.
func TestHandler_ArchiveChannel_SlackError_502(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{slack.SenderOperator: pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{}},
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{archiveErr: errors.New("slack conversations.archive: channel_not_found")},
	}

	env := makeEnv(slack.ActionArchive, slack.SenderOperator, "C9999")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 502 {
		t.Fatalf("StatusCode = %d; want 502. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "channel_not_found" {
		t.Errorf("error = %v; want channel_not_found", body["error"])
	}
}

// TestHandler_SandboxPost_NoOwnedChannel_403: sandbox has no channel configured → 403.
func TestHandler_SandboxPost_NoOwnedChannel_403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	h := &bridge.Handler{
		Now:      func() time.Time { return time.Unix(1714280400, 0) },
		Keys:     &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:   &fakeNonces{},
		Channels: &fakeChannels{owned: map[string]string{}}, // no channel for sb-abc123
		Token:    &fakeToken{tok: "xoxb"},
		Slack:    &fakeSlack{},
	}

	env := makeEnv(slack.ActionPost, "sb-abc123", "C0123ABC")
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 403 {
		t.Fatalf("StatusCode = %d; want 403. Body: %s", resp.StatusCode, resp.Body)
	}
	body := responseBody(t, resp)
	if body["error"] != "channel_mismatch" {
		t.Errorf("error = %v; want channel_mismatch", body["error"])
	}
}

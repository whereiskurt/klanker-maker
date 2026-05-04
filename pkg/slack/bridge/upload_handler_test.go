package bridge_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
	"github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
)

// ---------------------------------------------------------------------------
// Mocks — Phase 68 ActionUpload
// ---------------------------------------------------------------------------

// mockS3Getter records whether GetObject was called and returns either
// the configured body or an error.
type mockS3Getter struct {
	body   []byte
	err    error
	called bool
}

func (m *mockS3Getter) GetObject(_ context.Context, _ string) (io.ReadCloser, int64, error) {
	m.called = true
	if m.err != nil {
		return nil, 0, m.err
	}
	return io.NopCloser(bytes.NewReader(m.body)), int64(len(m.body)), nil
}

// mockUploader records the body it received from the streaming pipe so tests
// can assert that S3 → Slack passthrough is verbatim.
type mockUploader struct {
	fileID, permalink string
	err               error
	called            bool
	receivedBody      []byte
}

func (m *mockUploader) UploadFile(_ context.Context, _, _, _, _ string, _ int64, body io.Reader) (string, string, error) {
	m.called = true
	if m.err != nil {
		return "", "", m.err
	}
	m.receivedBody, _ = io.ReadAll(body)
	return m.fileID, m.permalink, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// uploadHandler returns a fully-wired Handler for happy-path upload tests.
// sandboxID is "sb-abc123" by default with channel "C0123ABC".
func uploadHandler(pub ed25519.PublicKey, s3 *mockS3Getter, up *mockUploader) *bridge.Handler {
	return &bridge.Handler{
		Now:          func() time.Time { return time.Unix(1714280400, 0) },
		Keys:         &fakeKeys{keys: map[string]ed25519.PublicKey{"sb-abc123": pub}},
		Nonces:       &fakeNonces{},
		Channels:     &fakeChannels{owned: map[string]string{"sb-abc123": "C0123ABC"}},
		Token:        &fakeToken{tok: "xoxb-test"},
		Slack:        &fakeSlack{},
		S3Getter:     s3,
		FileUploader: up,
	}
}

// makeUploadEnv builds a valid ActionUpload envelope. Caller mutates fields
// to exercise specific failure paths.
func makeUploadEnv() *slack.SlackEnvelope {
	return &slack.SlackEnvelope{
		Action:      slack.ActionUpload,
		Body:        "",
		Channel:     "C0123ABC",
		ContentType: "application/gzip",
		Filename:    "claude-transcript-sess-x.jsonl.gz",
		Nonce:       "aabbccddeeff00112233445566778899",
		S3Key:       "transcripts/sb-abc123/sess-x.jsonl.gz",
		SenderID:    "sb-abc123",
		SizeBytes:   16,
		Subject:     "",
		ThreadTS:    "1700000000.000100",
		Timestamp:   1714280400,
		Version:     slack.EnvelopeVersion,
	}
}

// ---------------------------------------------------------------------------
// Tests — replace 9 Wave 0 stubs
// ---------------------------------------------------------------------------

// TestHandler_ActionUpload_HappyPath — valid envelope + S3 returns body +
// FileUploader returns ok → 200 with permalink + file_id in response body.
func TestHandler_ActionUpload_HappyPath(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte("compressed-jsonl-bytes")
	s3 := &mockS3Getter{body: body}
	up := &mockUploader{fileID: "F1ABC", permalink: "https://slack/x"}
	h := uploadHandler(pub, s3, up)

	env := makeUploadEnv()
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d; want 200. Body: %s", resp.StatusCode, resp.Body)
	}
	if !s3.called {
		t.Errorf("S3.GetObject not called; want called")
	}
	if !up.called {
		t.Errorf("FileUploader.UploadFile not called; want called")
	}
	if !bytes.Equal(up.receivedBody, body) {
		t.Errorf("UploadFile received %q; want passthrough %q", up.receivedBody, body)
	}
	rb := responseBody(t, resp)
	if rb["ok"] != true {
		t.Errorf("ok = %v; want true", rb["ok"])
	}
	if rb["file_id"] != "F1ABC" {
		t.Errorf("file_id = %v; want F1ABC", rb["file_id"])
	}
	if rb["permalink"] != "https://slack/x" {
		t.Errorf("permalink = %v; want https://slack/x", rb["permalink"])
	}
}

// TestHandler_ActionUpload_PrefixMismatch — S3Key prefix doesn't match
// sender_id; S3.GetObject MUST NOT be called.
func TestHandler_ActionUpload_PrefixMismatch(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s3 := &mockS3Getter{body: []byte("ignored")}
	up := &mockUploader{}
	h := uploadHandler(pub, s3, up)

	env := makeUploadEnv()
	env.S3Key = "transcripts/sb-other/x.gz"
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 403 {
		t.Fatalf("StatusCode = %d; want 403. Body: %s", resp.StatusCode, resp.Body)
	}
	if s3.called {
		t.Errorf("S3.GetObject called on prefix mismatch; must not be called before validation passes")
	}
	rb := responseBody(t, resp)
	if rb["error"] != "s3_key_prefix_mismatch" {
		t.Errorf("error = %v; want s3_key_prefix_mismatch", rb["error"])
	}
}

// TestHandler_ActionUpload_FilenameInvalid — filename containing "/" → 400.
func TestHandler_ActionUpload_FilenameInvalid(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s3 := &mockS3Getter{body: []byte("ignored")}
	up := &mockUploader{}
	h := uploadHandler(pub, s3, up)

	env := makeUploadEnv()
	env.Filename = "evil/path.gz"
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	if s3.called {
		t.Errorf("S3.GetObject called before filename validation")
	}
	rb := responseBody(t, resp)
	if rb["error"] != "filename_invalid" {
		t.Errorf("error = %v; want filename_invalid", rb["error"])
	}
}

// TestHandler_ActionUpload_ContentTypeNotAllowed — content_type
// "application/octet-stream" not in allow-list → 400.
func TestHandler_ActionUpload_ContentTypeNotAllowed(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s3 := &mockS3Getter{body: []byte("ignored")}
	up := &mockUploader{}
	h := uploadHandler(pub, s3, up)

	env := makeUploadEnv()
	env.ContentType = "application/octet-stream"
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	rb := responseBody(t, resp)
	if rb["error"] != "content_type_not_allowed" {
		t.Errorf("error = %v; want content_type_not_allowed", rb["error"])
	}
}

// TestHandler_ActionUpload_SizeCap — size_bytes = 200MB → 400.
func TestHandler_ActionUpload_SizeCap(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s3 := &mockS3Getter{}
	up := &mockUploader{}
	h := uploadHandler(pub, s3, up)

	env := makeUploadEnv()
	env.SizeBytes = 200 * 1024 * 1024
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	rb := responseBody(t, resp)
	if rb["error"] != "size_invalid" {
		t.Errorf("error = %v; want size_invalid", rb["error"])
	}
}

// TestHandler_ActionUpload_SizeZero — size_bytes = 0 → 400.
func TestHandler_ActionUpload_SizeZero(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s3 := &mockS3Getter{}
	up := &mockUploader{}
	h := uploadHandler(pub, s3, up)

	env := makeUploadEnv()
	env.SizeBytes = 0
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	rb := responseBody(t, resp)
	// size=0 fails the early missing_fields check (Timestamp/Channel/etc.) only
	// when individual fields are zero; SizeBytes=0 in an upload is caught later
	// by our validation block as "size_invalid".
	if rb["error"] != "size_invalid" {
		t.Errorf("error = %v; want size_invalid", rb["error"])
	}
}

// TestHandler_ActionUpload_ChannelEmpty — channel = "" → 400 missing_fields
// (caught at the early envelope check, before reaching the upload validators).
func TestHandler_ActionUpload_ChannelEmpty(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s3 := &mockS3Getter{}
	up := &mockUploader{}
	h := uploadHandler(pub, s3, up)

	env := makeUploadEnv()
	env.Channel = ""
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	rb := responseBody(t, resp)
	// The envelope-level check fires first because Channel is one of the
	// required fields. This is the expected, defensible behavior.
	if rb["error"] != "missing_fields" {
		t.Errorf("error = %v; want missing_fields", rb["error"])
	}
}

// TestHandler_ActionUpload_ScopeMissing — handler.MissingFilesWrite=true
// short-circuits the entire flow → 400 with files:write message.
func TestHandler_ActionUpload_ScopeMissing(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s3 := &mockS3Getter{body: []byte("ignored")}
	up := &mockUploader{}
	h := uploadHandler(pub, s3, up)
	h.MissingFilesWrite = true

	env := makeUploadEnv()
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 400 {
		t.Fatalf("StatusCode = %d; want 400. Body: %s", resp.StatusCode, resp.Body)
	}
	if s3.called {
		t.Errorf("S3.GetObject called despite scope_missing")
	}
	rb := responseBody(t, resp)
	gotErr, _ := rb["error"].(string)
	if gotErr == "" || !contains(gotErr, "files:write") {
		t.Errorf("error = %q; want substring 'files:write'", gotErr)
	}
}

// TestHandler_ActionUpload_S3GetFails — S3.GetObject returns error → 502
// s3_get_failed; FileUploader MUST NOT be called.
func TestHandler_ActionUpload_S3GetFails(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s3 := &mockS3Getter{err: errors.New("s3: NoSuchKey")}
	up := &mockUploader{}
	h := uploadHandler(pub, s3, up)

	env := makeUploadEnv()
	req := signRequest(t, env, priv)
	resp := h.Handle(context.Background(), req)

	if resp.StatusCode != 502 {
		t.Fatalf("StatusCode = %d; want 502. Body: %s", resp.StatusCode, resp.Body)
	}
	if !s3.called {
		t.Errorf("S3.GetObject was not called; expected to be called and to fail")
	}
	if up.called {
		t.Errorf("FileUploader.UploadFile called despite S3 failure")
	}
	rb := responseBody(t, resp)
	if rb["error"] != "s3_get_failed" {
		t.Errorf("error = %v; want s3_get_failed", rb["error"])
	}
}

// contains is a tiny strings.Contains-equivalent that avoids pulling strings
// into this test for one site. (handler_test.go imports keep the surface lean.)
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

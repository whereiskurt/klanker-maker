package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/whereiskurt/klankrmkr/pkg/slack"
	"github.com/whereiskurt/klankrmkr/pkg/slack/bridge"
)

// ---------------------------------------------------------------------------
// Stubs for routing test (separate from main_test.go's stubs to avoid
// cross-test mutation). The cold-start init() already wired a real handler;
// each test overrides the global with t.Cleanup-restoration so package-level
// state stays clean across the suite.
// ---------------------------------------------------------------------------

type uploadStubKeys struct {
	pub ed25519.PublicKey
}

func (s *uploadStubKeys) Fetch(_ context.Context, _ string) (ed25519.PublicKey, error) {
	return s.pub, nil
}

type uploadStubNonces struct{}

func (s *uploadStubNonces) Reserve(_ context.Context, _ string, _ int) error { return nil }

type uploadStubChannels struct{}

func (s *uploadStubChannels) OwnedChannel(_ context.Context, _ string) (string, error) {
	return "C0123ABC", nil
}

type uploadStubToken struct{}

func (s *uploadStubToken) Fetch(_ context.Context) (string, error) { return "xoxb-stub", nil }

type uploadStubSlack struct{}

func (s *uploadStubSlack) PostMessage(_ context.Context, _, _, _, _ string) (string, error) {
	return "ts.stub", nil
}
func (s *uploadStubSlack) ArchiveChannel(_ context.Context, _ string) error { return nil }

type uploadStubS3 struct {
	called bool
}

func (s *uploadStubS3) GetObject(_ context.Context, _ string) (io.ReadCloser, int64, error) {
	s.called = true
	return io.NopCloser(strings.NewReader("ignored")), 7, nil
}

type uploadStubFileUploader struct {
	called bool
}

func (u *uploadStubFileUploader) UploadFile(_ context.Context, _, _, _, _ string, _ int64, _ io.Reader) (string, string, error) {
	u.called = true
	return "F-NOPE", "https://slack/nope", nil
}

// ---------------------------------------------------------------------------
// TestActionUploadRouting — confirms the Lambda Function URL handler routes
// an envelope with action=upload to the bridge.Handler ActionUpload dispatch
// case (vs landing in the Phase 63 post/archive/test paths or the Phase 67
// /events branch).
//
// We set MissingFilesWrite=true so the dispatch returns 400 with a "files:write"
// error string, proving the envelope reached the new branch without needing
// real S3 or Slack. S3.GetObject MUST NOT be called.
// ---------------------------------------------------------------------------
func TestActionUploadRouting(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	s3Stub := &uploadStubS3{}
	upStub := &uploadStubFileUploader{}

	origHandler := handler
	handler = &bridge.Handler{
		Now:               func() time.Time { return time.Unix(1714280400, 0) },
		Keys:              &uploadStubKeys{pub: pub},
		Nonces:            &uploadStubNonces{},
		Channels:          &uploadStubChannels{},
		Token:             &uploadStubToken{},
		Slack:             &uploadStubSlack{},
		S3Getter:          s3Stub,
		FileUploader:      upStub,
		MissingFilesWrite: true, // routes to the new branch's scope-gate
	}
	t.Cleanup(func() { handler = origHandler })

	// Build a signed ActionUpload envelope.
	env := &slack.SlackEnvelope{
		Action:      slack.ActionUpload,
		Body:        "",
		Channel:     "C0123ABC",
		ContentType: "application/gzip",
		Filename:    "claude-transcript.jsonl.gz",
		Nonce:       "aabbccddeeff00112233445566778899",
		S3Key:       "transcripts/sb-abc123/sess.jsonl.gz",
		SenderID:    "sb-abc123",
		SizeBytes:   16,
		Subject:     "",
		ThreadTS:    "1700000000.000100",
		Timestamp:   1714280400,
		Version:     slack.EnvelopeVersion,
	}
	canonical, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		t.Fatalf("SignEnvelope: %v", err)
	}

	ev := events.LambdaFunctionURLRequest{
		Body: string(canonical),
		Headers: map[string]string{
			"X-KM-Sender-ID": env.SenderID,
			"X-KM-Signature": base64.StdEncoding.EncodeToString(sig),
		},
	}

	resp, err := handle(context.Background(), ev)
	if err != nil {
		t.Fatalf("handle returned error: %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for scope_missing routing, got %d (body: %s)", resp.StatusCode, resp.Body)
	}
	if s3Stub.called {
		t.Errorf("S3 GetObject called despite MissingFilesWrite=true; routing did not hit the scope-gate first")
	}
	if upStub.called {
		t.Errorf("FileUploader called despite MissingFilesWrite=true")
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	gotErr, _ := body["error"].(string)
	if !strings.Contains(gotErr, "files:write") {
		t.Errorf("error = %q; want substring 'files:write'", gotErr)
	}
}

// ---------------------------------------------------------------------------
// TestProbeFilesWriteScope — unit test for the probeFilesWriteScope helper.
// Uses an httptest.Server that returns the X-OAuth-Scopes header so we can
// exercise both the present-scope and missing-scope branches without hitting
// real Slack. Documents the fail-open behavior on absent header.
// ---------------------------------------------------------------------------
func TestProbeFilesWriteScope(t *testing.T) {
	tests := []struct {
		name        string
		scopeHeader string
		wantMissing bool
	}{
		{"scope_present", "chat:write,files:write,channels:read", false},
		{"scope_missing", "chat:write,channels:read", true},
		{"empty_header_fails_open", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.scopeHeader != "" {
					w.Header().Set("X-OAuth-Scopes", tc.scopeHeader)
				}
				w.WriteHeader(200)
				w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
			}))
			t.Cleanup(srv.Close)

			missing, err := probeFilesWriteScope(context.Background(), srv.URL, "xoxb-test")
			if err != nil {
				t.Fatalf("probeFilesWriteScope: unexpected error: %v", err)
			}
			if missing != tc.wantMissing {
				t.Errorf("missing = %v; want %v (scopes=%q)", missing, tc.wantMissing, tc.scopeHeader)
			}
		})
	}
}

package slack

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Plan 68-02 promotes the Wave 0 stubs into real canonical-JSON forward +
// backward compatibility checks for the upload-envelope ABI extension.
//
// Note: this file lives in package slack (not slack_test) so the tests can
// reach the unexported package internals directly. The Phase 63 tests in
// payload_test.go remain in slack_test (black-box) and exercise the same
// public API via slack.X qualifiers.

func TestCanonicalJSON_ActionUpload(t *testing.T) {
	env, err := BuildEnvelopeUpload(
		"sb-abc123",
		"C0123ABC",
		"1700000000.000100",
		"transcripts/sb-abc123/sess-x.jsonl.gz",
		"claude-transcript-sess-x.jsonl.gz",
		"application/gzip",
		12345,
	)
	if err != nil {
		t.Fatalf("BuildEnvelopeUpload: %v", err)
	}
	canon, err := CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	s := string(canon)

	// Confirm key ordering: the canonical document MUST list keys in
	// alphabetical order so signing/verification is deterministic across
	// sender (km-slack) and verifier (bridge) without sorting overhead.
	order := []string{
		`"action"`, `"body"`, `"channel"`, `"content_type"`, `"filename"`,
		`"nonce"`, `"s3_key"`, `"sender_id"`, `"size_bytes"`, `"subject"`,
		`"thread_ts"`, `"timestamp"`, `"version"`,
	}
	last := -1
	for _, k := range order {
		i := strings.Index(s, k)
		if i < 0 {
			t.Fatalf("expected key %s in canonical JSON; got: %s", k, s)
		}
		if i <= last {
			t.Fatalf("canonical JSON keys out of alphabetical order at %s: %s", k, s)
		}
		last = i
	}

	// Round-trip through encoding/json must recover the original values.
	var out SlackEnvelope
	if err := json.Unmarshal(canon, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Action != ActionUpload {
		t.Errorf("Action = %q; want %q", out.Action, ActionUpload)
	}
	if out.S3Key != env.S3Key {
		t.Errorf("S3Key = %q; want %q", out.S3Key, env.S3Key)
	}
	if out.Filename != env.Filename {
		t.Errorf("Filename = %q; want %q", out.Filename, env.Filename)
	}
	if out.ContentType != env.ContentType {
		t.Errorf("ContentType = %q; want %q", out.ContentType, env.ContentType)
	}
	if out.SizeBytes != env.SizeBytes {
		t.Errorf("SizeBytes = %d; want %d", out.SizeBytes, env.SizeBytes)
	}
	if out.Channel != env.Channel {
		t.Errorf("Channel = %q; want %q", out.Channel, env.Channel)
	}
	if out.ThreadTS != env.ThreadTS {
		t.Errorf("ThreadTS = %q; want %q", out.ThreadTS, env.ThreadTS)
	}
	if out.Body != "" {
		t.Errorf("Body = %q; want empty for upload", out.Body)
	}
	if out.Subject != "" {
		t.Errorf("Subject = %q; want empty for upload", out.Subject)
	}
	if out.Version != EnvelopeVersion {
		t.Errorf("Version = %d; want %d", out.Version, EnvelopeVersion)
	}
}

func TestCanonicalJSON_PostUnchangedAfterAdditiveFields(t *testing.T) {
	env, err := BuildEnvelope(ActionPost, "sb-abc123", "C0123ABC", "subject", "body", "")
	if err != nil {
		t.Fatalf("BuildEnvelope: %v", err)
	}
	canon, err := CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	s := string(canon)

	// New upload-only fields must serialize with their zero values for non-
	// upload actions. They are NOT omitempty: the canonical document must
	// contain every struct field so signing produces identical bytes on
	// sender and verifier sides regardless of action type.
	for _, mustContain := range []string{
		`"content_type":""`,
		`"filename":""`,
		`"s3_key":""`,
		`"size_bytes":0`,
	} {
		if !strings.Contains(s, mustContain) {
			t.Fatalf("expected %s in canonical post envelope; got: %s", mustContain, s)
		}
	}

	// Two consecutive marshals must produce identical bytes.
	canon2, err := CanonicalJSON(env)
	if err != nil {
		t.Fatalf("CanonicalJSON second call: %v", err)
	}
	if string(canon) != string(canon2) {
		t.Fatalf("post-envelope canonical not deterministic:\n  first:  %s\n  second: %s", canon, canon2)
	}
}

func TestBuildEnvelopeUpload_ValidatesRequired(t *testing.T) {
	cases := []struct {
		name    string
		channel string
		s3Key   string
		fname   string
		ctype   string
		sz      int64
		wantErr error
	}{
		{"empty_channel", "", "transcripts/x/y.gz", "y.gz", "application/gzip", 1, ErrUploadChannelEmpty},
		{"empty_s3key", "C1", "", "y.gz", "application/gzip", 1, ErrUploadS3KeyEmpty},
		{"bad_filename_slash", "C1", "transcripts/x/y.gz", "a/b.gz", "application/gzip", 1, ErrUploadFilenameInvalid},
		{"bad_filename_empty", "C1", "transcripts/x/y.gz", "", "application/gzip", 1, ErrUploadFilenameInvalid},
		{"bad_filename_nul", "C1", "transcripts/x/y.gz", "a\x00b", "application/gzip", 1, ErrUploadFilenameInvalid},
		{"bad_filename_too_long", "C1", "transcripts/x/y.gz", strings.Repeat("a", 256), "application/gzip", 1, ErrUploadFilenameInvalid},
		{"size_zero", "C1", "transcripts/x/y.gz", "y.gz", "application/gzip", 0, ErrUploadSizeInvalid},
		{"size_negative", "C1", "transcripts/x/y.gz", "y.gz", "application/gzip", -1, ErrUploadSizeInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := BuildEnvelopeUpload("sb-x", c.channel, "", c.s3Key, c.fname, c.ctype, c.sz)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("expected %v; got %v", c.wantErr, err)
			}
		})
	}
}

func TestBuildEnvelopeUpload_RoundTripCanonical(t *testing.T) {
	env, err := BuildEnvelopeUpload(
		"sb-abc",
		"C1",
		"1700000000.000100",
		"transcripts/sb-abc/sess.jsonl.gz",
		"f.gz",
		"application/gzip",
		100,
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	canon, err := CanonicalJSON(env)
	if err != nil {
		t.Fatalf("canon: %v", err)
	}

	var out SlackEnvelope
	if err := json.Unmarshal(canon, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	canon2, err := CanonicalJSON(&out)
	if err != nil {
		t.Fatalf("canon2: %v", err)
	}
	if string(canon) != string(canon2) {
		t.Fatalf("canonical not stable across roundtrip:\n%s\n!=\n%s", canon, canon2)
	}
}

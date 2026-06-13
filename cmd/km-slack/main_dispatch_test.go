package main

import (
	"bytes"
	"strings"
	"testing"
)

// Plan 68-05 Wave 2 Task 4: real bodies for the 5 dispatch tests seeded
// in Plan 68-00. These exercise the dispatcher itself plus each
// subcommand's flag-validation path. End-to-end signing/SDK paths for
// post/upload/record-mapping are covered by main_test.go (post) and
// will be covered by Plan 68-08 / Plan 68-12 integration tests for
// upload + record-mapping (which need real bridge + DDB).

func TestDispatch_NoArgs(t *testing.T) {
	var buf bytes.Buffer
	code := dispatch(nil, &buf)
	if code != 2 {
		t.Fatalf("expected 2; got %d", code)
	}
	if !strings.Contains(buf.String(), "usage") {
		t.Fatalf("expected usage in stderr; got %q", buf.String())
	}
}

func TestDispatch_UnknownSubcommand(t *testing.T) {
	var buf bytes.Buffer
	code := dispatch([]string{"bogus"}, &buf)
	if code != 2 {
		t.Fatalf("expected 2; got %d", code)
	}
	if !strings.Contains(buf.String(), "unknown subcommand") {
		t.Fatalf("expected 'unknown subcommand' in stderr; got %q", buf.String())
	}
}

// TestDispatch_Post — dispatcher routes "post" to runPost, which without
// --channel/--body returns exit 2 with a "required" message. We don't
// exercise the SSM/bridge path here (covered by main_test.go).
func TestDispatch_Post(t *testing.T) {
	var buf bytes.Buffer
	code := dispatch([]string{"post"}, &buf)
	if code == 0 {
		t.Fatalf("expected non-zero (post requires --channel --body); got %d", code)
	}
	if !strings.Contains(buf.String(), "--channel and --body are required") {
		t.Fatalf("expected channel/body required message; got %q", buf.String())
	}
}

// TestDispatch_Upload — dispatcher routes "upload" to runUpload, which without
// any flags returns exit 2 with the "missing required flags" message.
func TestDispatch_Upload(t *testing.T) {
	var buf bytes.Buffer
	code := dispatch([]string{"upload"}, &buf)
	if code == 0 {
		t.Fatalf("expected non-zero (upload requires flags); got %d", code)
	}
	if !strings.Contains(buf.String(), "missing required flags") {
		t.Fatalf("expected missing-required message; got %q", buf.String())
	}
}

// TestDispatch_RecordMapping — dispatcher routes "record-mapping" to
// runRecordMapping, which without flags returns exit 2 with the
// "missing required flags" message.
func TestDispatch_RecordMapping(t *testing.T) {
	var buf bytes.Buffer
	code := dispatch([]string{"record-mapping"}, &buf)
	if code == 0 {
		t.Fatalf("expected non-zero (record-mapping requires flags); got %d", code)
	}
	if !strings.Contains(buf.String(), "missing required flags") {
		t.Fatalf("expected missing-required message; got %q", buf.String())
	}
}

// TestDispatch_Reply — dispatcher routes "reply" to runReply, which without
// --body returns exit 2 with the "required" message. The route itself is
// confirmed by reaching runReply (not the "unknown subcommand" path).
func TestDispatch_Reply(t *testing.T) {
	var buf bytes.Buffer
	// Without --body, runReply exits 2 with "required" message.
	code := dispatch([]string{"reply"}, &buf)
	if code == 0 {
		t.Fatalf("expected non-zero (reply requires --body); got %d", code)
	}
	if strings.Contains(buf.String(), "unknown subcommand") {
		t.Fatalf("expected reply to be routed (not unknown subcommand); got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "--body is required") {
		t.Fatalf("expected '--body is required' in stderr; got %q", buf.String())
	}
}

// TestDispatch_Reply_SlackNotConfigured — when $KM_SLACK_CHANNEL_ID is empty
// and no --channel/--thread flags are provided but --body is given, reply
// must exit non-zero with the "Slack not configured" message instead of
// panicking or crashing. This covers the risk-7 guard path.
//
// Note: runReply requires KM_SANDBOX_ID and KM_SLACK_BRIDGE_URL before calling
// runReplyWith; to exercise the "Slack not configured" guard we'd need to set
// those env vars too and bypass SSM. The TestRunReplyWith_SlackNotConfigured
// test in main_reply_test.go covers this path directly via runReplyWith. This
// test confirms the dispatch table routes to runReply correctly.
func TestDispatch_Reply_Routes(t *testing.T) {
	var buf bytes.Buffer
	// Pass --body with a non-existent file; runReply will fail flag validation
	// for KM_SANDBOX_ID before file-not-found, proving dispatch reached runReply.
	t.Setenv("KM_SANDBOX_ID", "")
	t.Setenv("KM_SLACK_BRIDGE_URL", "")
	code := dispatch([]string{"reply", "--body", "/tmp/km-slack-reply-test-body.txt"}, &buf)
	if code == 0 {
		t.Fatalf("expected non-zero; got %d", code)
	}
	// Must NOT say "unknown subcommand".
	if strings.Contains(buf.String(), "unknown subcommand") {
		t.Fatalf("reply not dispatched: got unknown-subcommand; stderr=%q", buf.String())
	}
}

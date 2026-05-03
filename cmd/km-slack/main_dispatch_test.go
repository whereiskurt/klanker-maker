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

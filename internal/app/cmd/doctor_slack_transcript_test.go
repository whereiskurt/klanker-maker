// Package cmd — doctor_slack_transcript_test.go
// Plan 68-11 — tests for the three Slack transcript-streaming doctor checks:
//   - checkSlackTranscriptTableExists
//   - checkSlackFilesWriteScope
//   - checkSlackTranscriptStaleObjects
//
// All tests use lightweight local fakes (no real AWS calls). The HTTP probe
// path is exercised via the same `getScopes` closure dep used by the Phase 67
// checkSlackAppEventsScopes — production wiring lives in fetchSlackBotScopes
// (already covered by the Phase 67 doctor wiring).
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// =============================================================================
// Local fakes (S3 ListObjectsV2 only — DDB DescribeTable reuses
// mockDynamoClient already defined in doctor_test.go)
// =============================================================================

// fakeS3List is a minimal kmaws.S3ListAPI implementation used by
// checkSlackTranscriptStaleObjects tests. Returns the configured pages in
// sequence; nil page → empty single page.
type fakeS3List struct {
	pages   []*s3.ListObjectsV2Output
	listErr error
	calls   int
}

func (f *fakeS3List) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(f.pages) == 0 {
		return &s3.ListObjectsV2Output{}, nil
	}
	idx := f.calls
	f.calls++
	if idx >= len(f.pages) {
		return &s3.ListObjectsV2Output{}, nil
	}
	return f.pages[idx], nil
}

// GetObject is unused by Plan 68-11 checks but required to satisfy the
// kmaws.S3ListAPI interface contract (the same interface is shared with
// the sandbox-listing code path).
func (f *fakeS3List) GetObject(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return nil, errors.New("not implemented in fakeS3List")
}

// =============================================================================
// checkSlackTranscriptTableExists
// =============================================================================

// TestDoctor_SlackTranscriptTableExists: DDB returns TableStatus=ACTIVE → OK
// with the table name in the message.
func TestDoctor_SlackTranscriptTableExists(t *testing.T) {
	ddb := &mockDynamoClient{
		output: &dynamodb.DescribeTableOutput{
			Table: &ddbtypes.TableDescription{
				TableStatus: ddbtypes.TableStatusActive,
			},
		},
	}
	r := checkSlackTranscriptTableExists(context.Background(), ddb, "km-slack-stream-messages")
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "km-slack-stream-messages") {
		t.Errorf("expected message to mention table name, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "ACTIVE") {
		t.Errorf("expected message to mention ACTIVE, got: %s", r.Message)
	}
}

// TestDoctor_SlackTranscriptTableMissing: DDB DescribeTable returns
// ResourceNotFoundException → WARN with a remediation hint pointing at km init.
func TestDoctor_SlackTranscriptTableMissing(t *testing.T) {
	ddb := &mockDynamoClient{err: errors.New("ResourceNotFoundException: table not found")}
	r := checkSlackTranscriptTableExists(context.Background(), ddb, "km-slack-stream-messages")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on missing table, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "km-slack-stream-messages") {
		t.Errorf("expected message to mention table name, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Error("expected non-empty remediation hint pointing at km init")
	}
	if !strings.Contains(r.Remediation, "km init") {
		t.Errorf("expected remediation to mention 'km init', got: %s", r.Remediation)
	}
}

// =============================================================================
// checkSlackFilesWriteScope
// =============================================================================

// TestDoctor_SlackFilesWriteScope: getScopes returns scopes including
// files:write → OK with success message.
func TestDoctor_SlackFilesWriteScope(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:read", "files:write", "reactions:write"}, nil
	}
	r := checkSlackFilesWriteScope(context.Background(), getScopes)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "files:write") {
		t.Errorf("expected success message to mention files:write, got: %s", r.Message)
	}
}

// TestDoctor_SlackFilesWriteScopeMissing: getScopes returns scopes WITHOUT
// files:write → WARN with re-auth message and non-empty remediation.
func TestDoctor_SlackFilesWriteScopeMissing(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return []string{"chat:write", "channels:read", "reactions:write"}, nil // no files:write
	}
	r := checkSlackFilesWriteScope(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "files:write") {
		t.Errorf("expected message to mention files:write, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Error("expected non-empty remediation hint")
	}
	if !strings.Contains(r.Remediation, "OAuth") && !strings.Contains(r.Remediation, "reinstall") {
		t.Errorf("expected remediation to mention OAuth/reinstall, got: %s", r.Remediation)
	}
}

// TestDoctor_SlackFilesWriteScope_AuthTestError: getScopes returns an error
// → WARN (do not fail doctor on auth.test outage).
func TestDoctor_SlackFilesWriteScope_AuthTestError(t *testing.T) {
	getScopes := func(_ context.Context) ([]string, error) {
		return nil, errors.New("invalid_auth")
	}
	r := checkSlackFilesWriteScope(context.Background(), getScopes)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on auth.test error, got %s", r.Status)
	}
}

// TestDoctor_SlackFilesWriteScope_NilDeps: nil getScopes → SKIPPED.
func TestDoctor_SlackFilesWriteScope_NilDeps(t *testing.T) {
	r := checkSlackFilesWriteScope(context.Background(), nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// checkSlackTranscriptStaleObjects
// =============================================================================

// TestDoctor_SlackTranscriptStaleObjects: S3 returns three transcript prefixes
// (sb-a, sb-b, sb-c); DDB lists [sb-a, sb-b] live → WARN with sb-c surfaced.
func TestDoctor_SlackTranscriptStaleObjects(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("transcripts/sb-a/")},
					{Prefix: awssdk.String("transcripts/sb-b/")},
					{Prefix: awssdk.String("transcripts/sb-c/")},
				},
			},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) {
		return []string{"sb-a", "sb-b"}, nil
	}
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "transcripts/sb-c/") {
		t.Errorf("expected sb-c in message, got: %s", r.Message)
	}
	if strings.Contains(r.Message, "sb-a") || strings.Contains(r.Message, "sb-b") {
		t.Errorf("expected ONLY stale prefix in message, got: %s", r.Message)
	}
	if r.Remediation == "" {
		t.Error("expected non-empty remediation hint for cleanup")
	}
}

// TestDoctor_SlackTranscriptStaleObjects_AllLive: every S3 prefix matches a
// live sandbox → OK.
func TestDoctor_SlackTranscriptStaleObjects_AllLive(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("transcripts/sb-a/")},
					{Prefix: awssdk.String("transcripts/sb-b/")},
				},
			},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) {
		return []string{"sb-a", "sb-b", "sb-c"}, nil // sb-c has no transcripts yet, that's fine
	}
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "none stale") {
		t.Errorf("expected 'none stale' in message, got: %s", r.Message)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_NoPrefixes: S3 returns empty page →
// OK with "no transcript prefixes" message.
func TestDoctor_SlackTranscriptStaleObjects_NoPrefixes(t *testing.T) {
	s3Cli := &fakeS3List{pages: nil}
	listIDs := func(_ context.Context) ([]string, error) {
		return []string{"sb-a"}, nil
	}
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs)
	if r.Status != CheckOK {
		t.Fatalf("expected OK, got %s", r.Status)
	}
	if !strings.Contains(r.Message, "no transcript prefixes") {
		t.Errorf("expected 'no transcript prefixes' in message, got: %s", r.Message)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_S3Error: S3 ListObjectsV2 fails →
// WARN (cleanup advisory; never fails doctor).
func TestDoctor_SlackTranscriptStaleObjects_S3Error(t *testing.T) {
	s3Cli := &fakeS3List{listErr: errors.New("AccessDenied")}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on S3 error, got %s", r.Status)
	}
	if !strings.Contains(r.Message, "ListObjectsV2") {
		t.Errorf("expected message to mention ListObjectsV2, got: %s", r.Message)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_NilDeps: nil deps → SKIPPED.
func TestDoctor_SlackTranscriptStaleObjects_NilDeps(t *testing.T) {
	r := checkSlackTranscriptStaleObjects(context.Background(), nil, "bucket", nil)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_NoBucket: empty bucket name → SKIPPED.
func TestDoctor_SlackTranscriptStaleObjects_NoBucket(t *testing.T) {
	s3Cli := &fakeS3List{}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "", listIDs)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

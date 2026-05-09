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
	"fmt"
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

	// Plan quick-7: DeleteObjects mock state.
	deleteObjectsCalls int
	deleteObjectsKeys  []string
	deleteObjectsErr   error   // returned for every call when set
	deleteObjectsErrs  []error // returned per-call (overrides deleteObjectsErr) when index in range

	// MaxKeys=1 sample responses keyed by Input.Prefix (used by Bug 5 age
	// guard in checkOrphanedArtifacts). Lookup misses return an empty
	// response so the caller treats the prefix as stale-eligible —
	// matches the production code's "bias toward stale on uncertain age".
	samplesByPrefix map[string]*s3.ListObjectsV2Output
}

func (f *fakeS3List) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	// MaxKeys=1 sample-call branch (Bug 5 age guard). Doesn't consume f.pages
	// so existing pages[] scripts stay intact.
	if in != nil && in.MaxKeys != nil && *in.MaxKeys == 1 {
		if in.Prefix != nil {
			if out, ok := f.samplesByPrefix[*in.Prefix]; ok {
				return out, nil
			}
		}
		return &s3.ListObjectsV2Output{}, nil
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

// DeleteObjects records the keys passed in each call (Plan quick-7 cleanup
// path). When deleteObjectsErrs is set, errors are returned per-call (in
// order); otherwise deleteObjectsErr (single value) is returned for every
// call. Required so fakeS3List satisfies kmaws.S3CleanupAPI.
func (f *fakeS3List) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	idx := f.deleteObjectsCalls
	f.deleteObjectsCalls++
	if in != nil && in.Delete != nil {
		for _, o := range in.Delete.Objects {
			if o.Key != nil {
				f.deleteObjectsKeys = append(f.deleteObjectsKeys, *o.Key)
			}
		}
	}
	if idx < len(f.deleteObjectsErrs) {
		if e := f.deleteObjectsErrs[idx]; e != nil {
			return nil, e
		}
	} else if f.deleteObjectsErr != nil {
		return nil, f.deleteObjectsErr
	}
	return &s3.DeleteObjectsOutput{}, nil
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
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, true, false)
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
	// Plan quick-7: dry-run remediation must point operators at --dry-run=false,
	// not the legacy aws-cli command.
	if !strings.Contains(r.Remediation, "--dry-run=false") {
		t.Errorf("expected remediation to point at --dry-run=false, got: %s", r.Remediation)
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
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, true, false)
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
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, true, false)
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
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, true, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on S3 error, got %s", r.Status)
	}
	if !strings.Contains(r.Message, "ListObjectsV2") {
		t.Errorf("expected message to mention ListObjectsV2, got: %s", r.Message)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_NilDeps: nil deps → SKIPPED.
func TestDoctor_SlackTranscriptStaleObjects_NilDeps(t *testing.T) {
	r := checkSlackTranscriptStaleObjects(context.Background(), nil, "bucket", nil, true, false)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_NoBucket: empty bucket name → SKIPPED.
func TestDoctor_SlackTranscriptStaleObjects_NoBucket(t *testing.T) {
	s3Cli := &fakeS3List{}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "", listIDs, true, false)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED, got %s", r.Status)
	}
}

// =============================================================================
// Plan quick-7 — checkSlackTranscriptStaleObjects cleanup-path tests
// =============================================================================

// TestDoctor_SlackTranscriptStaleObjects_DryRunTrue_NoDestructiveCalls:
// stale prefix sb-c present, dryRun=true → WARN with sb-c surfaced, no
// DeleteObjects calls.
func TestDoctor_SlackTranscriptStaleObjects_DryRunTrue_NoDestructiveCalls(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("transcripts/sb-a/")},
					{Prefix: awssdk.String("transcripts/sb-c/")},
				},
			},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) {
		return []string{"sb-a"}, nil
	}
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, true, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "transcripts/sb-c/") {
		t.Errorf("expected sb-c in message, got: %s", r.Message)
	}
	if s3Cli.deleteObjectsCalls != 0 {
		t.Errorf("dryRun=true must not call DeleteObjects; saw %d calls", s3Cli.deleteObjectsCalls)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_HappyPath: 1 stale
// prefix (sb-c) with 3 objects → 1 DeleteObjects call carrying 3 keys,
// WARN with (1 deleted, 0 skipped, 3 objects total).
func TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_HappyPath(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			// Page 0: top-level CommonPrefixes scan.
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("transcripts/sb-a/")},
					{Prefix: awssdk.String("transcripts/sb-c/")},
				},
			},
			// Page 1: listAllKeysUnderPrefix("transcripts/sb-c/") — 3 keys, no truncation.
			{
				Contents: []s3types.Object{
					{Key: awssdk.String("transcripts/sb-c/sess-1.jsonl.gz")},
					{Key: awssdk.String("transcripts/sb-c/sess-2.jsonl.gz")},
					{Key: awssdk.String("transcripts/sb-c/sess-3.jsonl.gz")},
				},
			},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) {
		return []string{"sb-a"}, nil
	}
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if s3Cli.deleteObjectsCalls != 1 {
		t.Errorf("expected 1 DeleteObjects call, got %d", s3Cli.deleteObjectsCalls)
	}
	if len(s3Cli.deleteObjectsKeys) != 3 {
		t.Errorf("expected 3 keys passed to DeleteObjects, got %d", len(s3Cli.deleteObjectsKeys))
	}
	if !strings.Contains(r.Message, "(1 deleted, 0 skipped, 3 objects total)") {
		t.Errorf("expected '(1 deleted, 0 skipped, 3 objects total)', got: %s", r.Message)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_PartialFailure: 2 stale
// prefixes; first prefix's DeleteObjects succeeds, second prefix's fails →
// WARN with (1 deleted, 1 skipped, ...) — loop is not aborted on error.
func TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_PartialFailure(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			// Page 0: top-level CommonPrefixes — 2 stale (sb-c, sb-d).
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("transcripts/sb-c/")},
					{Prefix: awssdk.String("transcripts/sb-d/")},
				},
			},
			// Page 1: listAll for sb-c — 1 key, no truncation.
			{
				Contents: []s3types.Object{
					{Key: awssdk.String("transcripts/sb-c/sess-c.jsonl.gz")},
				},
			},
			// Page 2: listAll for sb-d — 2 keys, no truncation.
			{
				Contents: []s3types.Object{
					{Key: awssdk.String("transcripts/sb-d/sess-d1.jsonl.gz")},
					{Key: awssdk.String("transcripts/sb-d/sess-d2.jsonl.gz")},
				},
			},
		},
		// Per-call errors: call 0 (sb-c) succeeds, call 1 (sb-d) fails.
		deleteObjectsErrs: []error{nil, errors.New("AccessDenied")},
	}
	listIDs := func(_ context.Context) ([]string, error) {
		return []string{}, nil // no live sandboxes — both prefixes are stale
	}
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "(1 deleted, 1 skipped,") {
		t.Errorf("expected '(1 deleted, 1 skipped,...)', got: %s", r.Message)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_MultiPage: 1 stale prefix
// with 1500 objects across 2 ListObjectsV2 pages → 2 DeleteObjects batches
// (1000 + 500), 1500 objects total deleted.
func TestDoctor_SlackTranscriptStaleObjects_DryRunFalse_MultiPage(t *testing.T) {
	// Build 1500 keys split across two ListObjectsV2 pages.
	page1 := make([]s3types.Object, 1000)
	for i := 0; i < 1000; i++ {
		page1[i] = s3types.Object{Key: awssdk.String(fmt.Sprintf("transcripts/sb-c/k%04d.jsonl.gz", i))}
	}
	page2 := make([]s3types.Object, 500)
	for i := 0; i < 500; i++ {
		page2[i] = s3types.Object{Key: awssdk.String(fmt.Sprintf("transcripts/sb-c/k%04d.jsonl.gz", 1000+i))}
	}
	truthy := true
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			// Page 0: top-level scan.
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("transcripts/sb-c/")},
				},
			},
			// Page 1: listAll for sb-c — 1000 keys, IsTruncated=true.
			{
				Contents:              page1,
				IsTruncated:           &truthy,
				NextContinuationToken: awssdk.String("page2-token"),
			},
			// Page 2: listAll for sb-c continuation — 500 keys, IsTruncated=false.
			{
				Contents: page2,
			},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) {
		return []string{}, nil
	}
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s (msg=%s)", r.Status, r.Message)
	}
	// Two DeleteObjects batches: 1000 + 500.
	if s3Cli.deleteObjectsCalls != 2 {
		t.Errorf("expected 2 DeleteObjects batches, got %d", s3Cli.deleteObjectsCalls)
	}
	if len(s3Cli.deleteObjectsKeys) != 1500 {
		t.Errorf("expected 1500 keys total across both batches, got %d", len(s3Cli.deleteObjectsKeys))
	}
	if !strings.Contains(r.Message, "1500 objects total") {
		t.Errorf("expected '1500 objects total' in message, got: %s", r.Message)
	}
}

// TestDoctor_SlackTranscriptStaleObjects_DryRunFalseWithoutDeleteS3_NoDestructiveCalls
// verifies the explicit-opt-in property: --dry-run=false alone is NOT enough
// to delete transcript prefixes — the operator must also pass --delete-s3.
// Same gate as checkOrphanedArtifacts. Transcripts can hold conversation
// history operators may want to retain.
func TestDoctor_SlackTranscriptStaleObjects_DryRunFalseWithoutDeleteS3_NoDestructiveCalls(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			{CommonPrefixes: []s3types.CommonPrefix{{Prefix: awssdk.String("transcripts/sb-c/")}}},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkSlackTranscriptStaleObjects(context.Background(), s3Cli, "test-bucket", listIDs, false, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if s3Cli.deleteObjectsCalls != 0 {
		t.Errorf("--dry-run=false alone (without --delete-s3) must NOT call DeleteObjects; saw %d", s3Cli.deleteObjectsCalls)
	}
	if !strings.Contains(r.Remediation, "--delete-s3") {
		t.Errorf("expected remediation to mention --delete-s3, got: %s", r.Remediation)
	}
	if strings.Contains(r.Remediation, "--dry-run=false") {
		t.Errorf("remediation in --dry-run=false mode shouldn't repeat the flag, got: %s", r.Remediation)
	}
}

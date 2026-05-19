// Package cmd — doctor_state_digest_test.go
// Phase 84.1 Plan 03 — GAP-8: state-digest mismatch detection.
//
// Mocks the narrow S3StateReader and LockDigestReader interfaces declared in
// doctor.go and exercises checkStateLockDigest across consistent, mismatched,
// orphaned, missing-table, and nil-client scenarios. Also exercises the
// pagination requirement (H7) and the LockID parsing edge case (H8).
package cmd

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// -----------------------------------------------------------------------------
// Mocks
// -----------------------------------------------------------------------------

// mockS3StateReader implements S3StateReader. Returns bodies keyed by
// "<bucket>/<key>". A nil entry triggers a NoSuchKey error (orphan case).
type mockS3StateReader struct {
	bodies map[string][]byte
	// missing keys (presence in this set means NoSuchKey is returned)
	missing map[string]bool
	err     error
}

func (m *mockS3StateReader) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	full := awssdk.ToString(params.Bucket) + "/" + awssdk.ToString(params.Key)
	if m.missing[full] {
		return nil, &s3types.NoSuchKey{Message: awssdk.String("not found")}
	}
	body, ok := m.bodies[full]
	if !ok {
		return nil, &s3types.NoSuchKey{Message: awssdk.String("not found")}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(body))}, nil
}

// mockLockDigestReader implements LockDigestReader. Returns a sequence of
// ScanOutput pages — necessary to test the NewScanPaginator path (H7).
type mockLockDigestReader struct {
	pages    []*dynamodb.ScanOutput
	err      error
	callsLog *int // optional counter; tests may inspect to assert pagination behavior
}

func (m *mockLockDigestReader) Scan(_ context.Context, input *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.callsLog != nil {
		*m.callsLog++
	}
	// NewScanPaginator pages by ExclusiveStartKey from the previous response's
	// LastEvaluatedKey. We return the next page if input.ExclusiveStartKey is
	// non-nil; the first call has it nil.
	if input.ExclusiveStartKey == nil {
		if len(m.pages) == 0 {
			return &dynamodb.ScanOutput{}, nil
		}
		return m.pages[0], nil
	}
	// Find the page whose "key" matches the cursor token (we encode page index
	// as a string in LastEvaluatedKey for test simplicity).
	cur, ok := input.ExclusiveStartKey["page"].(*dynamodbtypes.AttributeValueMemberS)
	if !ok {
		return &dynamodb.ScanOutput{}, nil
	}
	for i, p := range m.pages {
		if v, exists := p.LastEvaluatedKey["page"].(*dynamodbtypes.AttributeValueMemberS); exists && v.Value == cur.Value {
			if i+1 < len(m.pages) {
				return m.pages[i+1], nil
			}
		}
	}
	return &dynamodb.ScanOutput{}, nil
}

// helper: build a Scan item for LockID + Digest
func lockItem(lockID, digest string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"LockID": &dynamodbtypes.AttributeValueMemberS{Value: lockID},
		"Digest": &dynamodbtypes.AttributeValueMemberS{Value: digest},
	}
}

// helper: md5 hex of a byte slice
func md5hex(b []byte) string {
	return fmt.Sprintf("%x", md5.Sum(b))
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

// Test 1: All consistent → CheckOK with "consistent (N items checked)".
func TestCheckStateLockDigest_AllConsistent(t *testing.T) {
	state := []byte("hello")
	bucket := "tf-km-12345"
	key := "use1/ses/terraform.tfstate"
	lockID := bucket + "/" + key + "-md5"

	s3mock := &mockS3StateReader{
		bodies: map[string][]byte{bucket + "/" + key: state},
	}
	ddbmock := &mockLockDigestReader{
		pages: []*dynamodb.ScanOutput{
			{Items: []map[string]dynamodbtypes.AttributeValue{lockItem(lockID, md5hex(state))}},
		},
	}

	result := checkStateLockDigest(context.Background(), s3mock, ddbmock, "tf-km-locks-use1")
	if result.Status != CheckOK {
		t.Fatalf("expected CheckOK, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "consistent") {
		t.Errorf("expected message to say 'consistent', got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "1") {
		t.Errorf("expected message to mention count, got: %s", result.Message)
	}
}

// Test 2: Mismatch → CheckWarn with copy-paste remediation.
func TestCheckStateLockDigest_Mismatch(t *testing.T) {
	state := []byte("hello")
	bucket := "tf-km-12345"
	key := "use1/ses/terraform.tfstate"
	lockID := bucket + "/" + key + "-md5"
	correctMD5 := md5hex(state)
	staleMD5 := "deadbeefdeadbeefdeadbeefdeadbeef"

	s3mock := &mockS3StateReader{
		bodies: map[string][]byte{bucket + "/" + key: state},
	}
	ddbmock := &mockLockDigestReader{
		pages: []*dynamodb.ScanOutput{
			{Items: []map[string]dynamodbtypes.AttributeValue{lockItem(lockID, staleMD5)}},
		},
	}

	result := checkStateLockDigest(context.Background(), s3mock, ddbmock, "tf-km-locks-use1")
	if result.Status != CheckWarn {
		t.Fatalf("expected CheckWarn for digest mismatch, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, lockID) {
		t.Errorf("expected message to mention LockID %q, got: %s", lockID, result.Message)
	}
	// Remediation must contain a literally-copy-pasteable command with table,
	// LockID, and correct MD5.
	if !strings.Contains(result.Remediation, "aws dynamodb update-item") {
		t.Errorf("expected remediation to contain 'aws dynamodb update-item', got: %s", result.Remediation)
	}
	if !strings.Contains(result.Remediation, "tf-km-locks-use1") {
		t.Errorf("expected remediation to contain table name, got: %s", result.Remediation)
	}
	if !strings.Contains(result.Remediation, lockID) {
		t.Errorf("expected remediation to contain LockID, got: %s", result.Remediation)
	}
	if !strings.Contains(result.Remediation, correctMD5) {
		t.Errorf("expected remediation to contain correct MD5 %q, got: %s", correctMD5, result.Remediation)
	}
}

// Test 3: S3 state missing but DDB digest present → CheckWarn (orphan).
func TestCheckStateLockDigest_S3StateMissing(t *testing.T) {
	bucket := "tf-km-12345"
	key := "use1/ghost-module/terraform.tfstate"
	lockID := bucket + "/" + key + "-md5"

	s3mock := &mockS3StateReader{
		missing: map[string]bool{bucket + "/" + key: true},
	}
	ddbmock := &mockLockDigestReader{
		pages: []*dynamodb.ScanOutput{
			{Items: []map[string]dynamodbtypes.AttributeValue{lockItem(lockID, "anyhash")}},
		},
	}

	result := checkStateLockDigest(context.Background(), s3mock, ddbmock, "tf-km-locks-use1")
	if result.Status != CheckWarn {
		t.Fatalf("expected CheckWarn for orphan (state object missing), got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "orphan") {
		t.Errorf("expected message to mention 'orphan', got: %s", result.Message)
	}
}

// Test 4: Lock table doesn't exist → CheckSkipped.
func TestCheckStateLockDigest_LockTableMissing(t *testing.T) {
	s3mock := &mockS3StateReader{}
	ddbmock := &mockLockDigestReader{
		err: &dynamodbtypes.ResourceNotFoundException{Message: awssdk.String("Requested resource not found")},
	}

	result := checkStateLockDigest(context.Background(), s3mock, ddbmock, "tf-km-locks-use1")
	if result.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped when lock table missing, got %s: %s", result.Status, result.Message)
	}
}

// Test 5: nil S3 client → CheckSkipped.
func TestCheckStateLockDigest_NilClient(t *testing.T) {
	ddbmock := &mockLockDigestReader{}
	result := checkStateLockDigest(context.Background(), nil, ddbmock, "tf-km-locks-use1")
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for nil S3 client, got %s", result.Status)
	}

	s3mock := &mockS3StateReader{}
	result = checkStateLockDigest(context.Background(), s3mock, nil, "tf-km-locks-use1")
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped for nil DDB client, got %s", result.Status)
	}
}

// Test 6: Both nil → CheckSkipped (no panic).
func TestCheckStateLockDigest_BothNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("checkStateLockDigest panicked with both clients nil: %v", r)
		}
	}()
	result := checkStateLockDigest(context.Background(), nil, nil, "tf-km-locks-use1")
	if result.Status != CheckSkipped {
		t.Errorf("expected CheckSkipped when both clients nil, got %s", result.Status)
	}
}

// Test 7 (H7): Paginates lock-table Scan — 3 pages × 5 items = 15 LockIDs all
// processed. DDB Scan does NOT auto-paginate in aws-sdk-go-v2; we must use
// NewScanPaginator explicitly. The test fails if any page is skipped.
func TestCheckStateLockDigest_PaginatesLockTableScan(t *testing.T) {
	state := []byte("payload")
	hash := md5hex(state)
	bucket := "tf-km-12345"

	// Build 3 pages of 5 items each — 15 total LockIDs.
	mkPage := func(start int, last bool, cursorVal string) *dynamodb.ScanOutput {
		items := make([]map[string]dynamodbtypes.AttributeValue, 5)
		for i := 0; i < 5; i++ {
			lockID := fmt.Sprintf("%s/use1/mod%02d/terraform.tfstate-md5", bucket, start+i)
			items[i] = lockItem(lockID, hash)
		}
		out := &dynamodb.ScanOutput{Items: items}
		if !last {
			out.LastEvaluatedKey = map[string]dynamodbtypes.AttributeValue{
				"page": &dynamodbtypes.AttributeValueMemberS{Value: cursorVal},
			}
		}
		return out
	}

	pages := []*dynamodb.ScanOutput{
		mkPage(0, false, "p1"),
		mkPage(5, false, "p2"),
		mkPage(10, true, ""),
	}

	// S3 returns the same payload for every key — exercises GetObject 15 times.
	bodies := map[string][]byte{}
	for i := 0; i < 15; i++ {
		bodies[fmt.Sprintf("%s/use1/mod%02d/terraform.tfstate", bucket, i)] = state
	}
	s3mock := &mockS3StateReader{bodies: bodies}

	var calls int
	ddbmock := &mockLockDigestReader{pages: pages, callsLog: &calls}

	result := checkStateLockDigest(context.Background(), s3mock, ddbmock, "tf-km-locks-use1")
	if result.Status != CheckOK {
		t.Fatalf("expected CheckOK (all consistent across pages), got %s: %s", result.Status, result.Message)
	}
	// Message should reflect that 15 items were checked.
	if !strings.Contains(result.Message, "15") {
		t.Errorf("expected 15 items processed across paginated Scan, got: %s", result.Message)
	}
	// Pagination must have made at least 3 Scan calls.
	if calls < 3 {
		t.Errorf("expected at least 3 Scan calls (one per page), got: %d", calls)
	}
}

// Test 8 (H8): parseLockID handles state keys that contain slashes correctly.
// S3 bucket names cannot contain "/", so the first-slash split is unambiguous.
func TestParseLockID_KeyWithSlashes(t *testing.T) {
	bucket, key, ok := parseLockID("tf-km-12345/use1/ses/terraform.tfstate-md5")
	if !ok {
		t.Fatal("parseLockID returned ok=false for valid input")
	}
	if bucket != "tf-km-12345" {
		t.Errorf("expected bucket=tf-km-12345, got: %q", bucket)
	}
	if key != "use1/ses/terraform.tfstate" {
		t.Errorf("expected key=use1/ses/terraform.tfstate, got: %q", key)
	}

	// Edge: no slash at all → ok=false.
	_, _, ok = parseLockID("noslash-md5")
	if ok {
		t.Error("parseLockID should return ok=false when input has no slash")
	}
}

// Compile-time assertion that the mocks satisfy the production interfaces.
// If the interfaces drift, these vars will refuse to compile.
var _ S3StateReader = (*mockS3StateReader)(nil)
var _ LockDigestReader = (*mockLockDigestReader)(nil)

// errSentinel is a convenience for tests that want a non-AWS error type.
var errSentinel = errors.New("sentinel")

var _ = errSentinel // referenced from future tests; keep linter happy

// =============================================================================
// Phase 85 — sweeper mocks (S3StateHeadAPI + LockDigestDeleterAPI)
// =============================================================================

// mockS3StateHead implements S3StateHeadAPI for sweeper tests.
//   - Keys in `missing` (bucket+"/"+key form) return *s3types.NotFound.
//   - If err is non-nil, ALL calls return that error (ambiguous-error case).
//   - Otherwise returns empty HeadObjectOutput (object exists).
type mockS3StateHead struct {
	missing map[string]bool
	err     error
}

func (m *mockS3StateHead) HeadObject(_ context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	full := awssdk.ToString(params.Bucket) + "/" + awssdk.ToString(params.Key)
	if m.missing[full] {
		return nil, &s3types.NotFound{Message: awssdk.String("not found")}
	}
	return &s3.HeadObjectOutput{}, nil
}

// mockLockDigestDeleter implements LockDigestDeleterAPI for sweeper tests.
// Records every BatchWriteItem call. On the FIRST call only (callCount==1),
// echoes unprocessedLocks back via UnprocessedItems to exercise the retry path.
type mockLockDigestDeleter struct {
	calls            [][]string // each entry = one call's lockIDs in PK order
	err              error
	unprocessedLocks []string // returned as UnprocessedItems on first call
	callCount        int
}

func (m *mockLockDigestDeleter) BatchWriteItem(_ context.Context, params *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	var batch []string
	var tableName string
	for tn, reqs := range params.RequestItems {
		tableName = tn
		for _, r := range reqs {
			if r.DeleteRequest != nil {
				if av, ok := r.DeleteRequest.Key["LockID"].(*dynamodbtypes.AttributeValueMemberS); ok {
					batch = append(batch, av.Value)
				}
			}
		}
	}
	m.calls = append(m.calls, batch)
	out := &dynamodb.BatchWriteItemOutput{}
	if m.callCount == 1 && len(m.unprocessedLocks) > 0 {
		unprocessed := make([]dynamodbtypes.WriteRequest, 0, len(m.unprocessedLocks))
		for _, id := range m.unprocessedLocks {
			unprocessed = append(unprocessed, dynamodbtypes.WriteRequest{
				DeleteRequest: &dynamodbtypes.DeleteRequest{
					Key: map[string]dynamodbtypes.AttributeValue{
						"LockID": &dynamodbtypes.AttributeValueMemberS{Value: id},
					},
				},
			})
		}
		out.UnprocessedItems = map[string][]dynamodbtypes.WriteRequest{tableName: unprocessed}
	}
	return out, nil
}

// =============================================================================
// Phase 85 — red-state test stubs (8 total)
//
// Plan 02 turns these GREEN by implementing checkStateLockDigestSweeper.
// DO NOT rename — Plans 02 / 03 and 85-VALIDATION.md reference them by name.
// =============================================================================

// TDD-1 — orphan + age passes → row deleted
func TestDigestSweeper_OrphanAgePassesDeleted(t *testing.T) {
	// ARRANGE: one orphan row whose embedded sandbox-id is NOT in the live lister
	//          (so age guard passes). dryRun=false, deleteStateDigests=true.
	//
	// ACT:     call checkStateLockDigestSweeper.
	//
	// ASSERT:  ddbWrite.calls has length 1 with the single orphan LockID.
	//          result.Status == CheckWarn.
	//          result.Message contains "1 orphan" and the LockID.
	//
	// RED:     not yet implemented.
	t.Fatalf("RED — TDD-1 not yet implemented in checkStateLockDigestSweeper")
}

// TDD-2 — orphan + age fails → row skipped (sandbox-id still resolves to live record)
func TestDigestSweeper_OrphanAgeFailsSkipped(t *testing.T) {
	ctx := context.Background()
	// Orphan row whose embedded sandbox-id IS in the live lister.
	lockID := "tf-km-state-use1/tf-km/sandboxes/sb-alive9999/budget-enforcer/terraform.tfstate-md5"
	bucket, key, _ := parseLockID(lockID)

	s3mock := &mockS3StateHead{missing: map[string]bool{bucket + "/" + key: true}}
	ddbScan := &mockLockDigestReader{pages: []*dynamodb.ScanOutput{{
		Items: []map[string]dynamodbtypes.AttributeValue{
			lockItem(lockID, "deadbeef"),
		},
	}}}
	ddbWrite := &mockLockDigestDeleter{}
	// Lister returns sb-alive9999 as live → age guard FAILS → row skipped.
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{{SandboxID: "sb-alive9999"}}}

	res := checkStateLockDigestSweeper(ctx, s3mock, ddbScan, ddbWrite, lister,
		"tf-km-locks-use1", false /*dryRun*/, true /*deleteStateDigests*/, 24*time.Hour)

	if len(ddbWrite.calls) != 0 {
		t.Fatalf("expected zero BatchWriteItem calls when age guard fails; got %d batches: %v", len(ddbWrite.calls), ddbWrite.calls)
	}
	if !strings.Contains(res.Message, "skipped") && !strings.Contains(res.Message, "sandbox still live") {
		t.Fatalf("expected message to indicate row skipped; got %q", res.Message)
	}
	if res.Status != CheckWarn {
		t.Fatalf("expected CheckWarn (orphan exists but skipped); got %v", res.Status)
	}
}

// TDD-3 — S3 HEAD returns network/5xx error → row skipped (NEVER delete on ambiguous error)
func TestDigestSweeper_S3HeadAmbiguousError_Skipped(t *testing.T) {
	ctx := context.Background()
	lockID := "tf-km-state-use1/tf-km/sandboxes/sb-aaaa1111/budget-enforcer/terraform.tfstate-md5"

	s3mock := &mockS3StateHead{err: fmt.Errorf("connection reset by peer")}
	ddbScan := &mockLockDigestReader{pages: []*dynamodb.ScanOutput{{
		Items: []map[string]dynamodbtypes.AttributeValue{
			lockItem(lockID, "deadbeef"),
		},
	}}}
	ddbWrite := &mockLockDigestDeleter{}
	lister := &fakeSandboxLister{}

	res := checkStateLockDigestSweeper(ctx, s3mock, ddbScan, ddbWrite, lister,
		"tf-km-locks-use1", false /*dryRun*/, true /*deleteStateDigests*/, 24*time.Hour)

	if len(ddbWrite.calls) != 0 {
		t.Fatalf("expected zero BatchWriteItem calls on ambiguous error; got %d batches: %v", len(ddbWrite.calls), ddbWrite.calls)
	}
	// On ambiguous error the row is classified as cant-verify, not orphan.
	// Message must indicate uncertain classification (0 orphan, 1 other) or
	// contain "could not verify".
	if !strings.Contains(res.Message, "could not verify") && !strings.Contains(res.Message, "0 orphan") {
		t.Fatalf("expected message to indicate uncertain classification (0 orphan or 'could not verify'); got %q", res.Message)
	}
}

// TDD-4 — non-orphan mismatch (object exists, MD5 stale) → NEVER deleted by sweeper
func TestDigestSweeper_LiveS3StaleDigest_NotDeletedBySweeper(t *testing.T) {
	ctx := context.Background()
	lockID := "tf-km-state-use1/tf-km/sandboxes/sb-livectx/budget-enforcer/terraform.tfstate-md5"

	// mockS3StateHead returns success (no `missing` entry, no `err`) → object
	// exists → sweeper must NOT classify as orphan.
	s3mock := &mockS3StateHead{}
	ddbScan := &mockLockDigestReader{pages: []*dynamodb.ScanOutput{{
		Items: []map[string]dynamodbtypes.AttributeValue{
			lockItem(lockID, "stale-md5-deadbeef"),
		},
	}}}
	ddbWrite := &mockLockDigestDeleter{}
	lister := &fakeSandboxLister{}

	res := checkStateLockDigestSweeper(ctx, s3mock, ddbScan, ddbWrite, lister,
		"tf-km-locks-use1", false /*dryRun*/, true /*deleteStateDigests*/, 24*time.Hour)

	if len(ddbWrite.calls) != 0 {
		t.Fatalf("expected zero BatchWriteItem calls when S3 object exists (sweeper out-of-scope for live+stale-MD5); got %d batches: %v", len(ddbWrite.calls), ddbWrite.calls)
	}
	// Result should report a clean state (live row not flagged as anything by
	// this sweeper — md5 mismatch detection is OUT OF SCOPE per BRIEF.md).
	if res.Status != CheckOK {
		t.Fatalf("expected CheckOK (sweeper has no mismatches to flag when S3 object exists); got %v: %q", res.Status, res.Message)
	}
}

// TDD-5 — batch of 26 items → splits into exactly 2 BatchWriteItem calls (25 + 1)
func TestDigestSweeper_26Items_TwoBatches(t *testing.T) {
	// ARRANGE: 26 orphan rows, all age-pass. dryRun=false, deleteStateDigests=true.
	// ACT/ASSERT: ddbWrite.callCount == 2; ddbWrite.calls[0] has 25 items; ddbWrite.calls[1] has 1 item.
	t.Fatalf("RED — TDD-5 not yet implemented")
}

// EXTRA — output format: summary + up to 10 inline + "… and N more (use --json for full list)"
func TestDigestSweeper_OutputFormat(t *testing.T) {
	ctx := context.Background()
	// 13 orphan rows — exercises the "10 inline + 'and N more'" continuation.
	items := make([]map[string]dynamodbtypes.AttributeValue, 13)
	missing := map[string]bool{}
	for i := 0; i < 13; i++ {
		lockID := fmt.Sprintf("tf-km-state-use1/tf-km/sandboxes/sb-fmt%04d/budget-enforcer/terraform.tfstate-md5", i)
		bucket, key, _ := parseLockID(lockID)
		missing[bucket+"/"+key] = true
		items[i] = lockItem(lockID, "abc")
	}
	s3mock := &mockS3StateHead{missing: missing}
	ddbScan := &mockLockDigestReader{pages: []*dynamodb.ScanOutput{{Items: items}}}
	ddbWrite := &mockLockDigestDeleter{}
	lister := &fakeSandboxLister{}

	// Dry-run — no deletes, just reporting.
	res := checkStateLockDigestSweeper(ctx, s3mock, ddbScan, ddbWrite, lister,
		"tf-km-locks-use1", true /*dryRun*/, false /*deleteStateDigests*/, 24*time.Hour)

	if !strings.Contains(res.Message, "state digest mismatch in 13 item(s)") {
		t.Fatalf("expected summary 'state digest mismatch in 13 item(s)' in message; got: %s", res.Message)
	}
	if !strings.Contains(res.Message, "… and 3 more (use --json for full list)") {
		t.Fatalf("expected '… and 3 more (use --json for full list)' continuation; got: %s", res.Message)
	}
	// ACCEPT-READ: the uncapped list MUST be in Details.
	if len(res.Details) != 13 {
		t.Fatalf("expected res.Details len 13 (full uncapped list for --json); got len %d: %v", len(res.Details), res.Details)
	}
	// Sanity: dry-run path should yield no BatchWriteItem calls.
	if len(ddbWrite.calls) != 0 {
		t.Fatalf("expected zero BatchWriteItem calls in dry-run; got %d", len(ddbWrite.calls))
	}
}

// EXTRA — UnprocessedItems retry path: BatchWriteItem returns some lockIDs unprocessed → reported as failed
func TestDigestSweeper_UnprocessedItems(t *testing.T) {
	// ARRANGE: 3 orphan rows; mockLockDigestDeleter.unprocessedLocks holds one of them.
	// ACT/ASSERT: result.Message reports 2 deleted, 1 failed (or equivalent); the unprocessed LockID is named.
	t.Fatalf("RED — TestDigestSweeper_UnprocessedItems not yet implemented")
}

// EXTRA — shared-module LockID fallback (checker warning #3): a LockID with NO
// "/sandboxes/" segment (e.g. ses, vpc, scp, management/* shared modules) goes
// through parseSandboxIDFromLockID's ok=false branch. When the delete gate is
// open AND S3 returns NotFound, the row IS deleted (no per-row owner to verify
// against — strict NotFound + open gate is sufficient).
func TestDigestSweeper_SharedModuleLockID_NoSandboxID(t *testing.T) {
	ctx := context.Background()
	// Shared-module LockID — NOTE: no "/sandboxes/" segment.
	lockID := "tf-km-state-use1/tf-km/use1/ses/terraform.tfstate-md5"
	bucket, key, _ := parseLockID(lockID)

	// Self-check: confirm parseSandboxIDFromLockID treats this as ok=false.
	if _, ok := parseSandboxIDFromLockID(lockID); ok {
		t.Fatalf("test setup error: expected parseSandboxIDFromLockID(%q) ok=false (shared-module), got ok=true", lockID)
	}

	s3mock := &mockS3StateHead{missing: map[string]bool{bucket + "/" + key: true}}
	ddbScan := &mockLockDigestReader{pages: []*dynamodb.ScanOutput{{
		Items: []map[string]dynamodbtypes.AttributeValue{
			lockItem(lockID, "cafebabe"),
		},
	}}}
	ddbWrite := &mockLockDigestDeleter{}
	lister := &fakeSandboxLister{} // empty — irrelevant; shared-module lockIDs bypass the lister cross-reference

	res := checkStateLockDigestSweeper(ctx, s3mock, ddbScan, ddbWrite, lister,
		"tf-km-locks-use1", false /*dryRun*/, true /*deleteStateDigests*/, 24*time.Hour)

	if len(ddbWrite.calls) != 1 {
		t.Fatalf("expected exactly 1 BatchWriteItem call for shared-module orphan; got %d calls: %v", len(ddbWrite.calls), ddbWrite.calls)
	}
	if len(ddbWrite.calls[0]) != 1 || ddbWrite.calls[0][0] != lockID {
		t.Fatalf("expected the shared-module LockID in batch; got %v", ddbWrite.calls[0])
	}
	if res.Status != CheckWarn {
		t.Fatalf("expected CheckWarn for orphan shared-module row; got %v", res.Status)
	}
	if !strings.Contains(res.Message, lockID) {
		t.Fatalf("expected message to reference the shared-module LockID; got: %s", res.Message)
	}
	// ACCEPT-READ: Details must contain the uncapped list.
	if len(res.Details) != 1 || !strings.Contains(res.Details[0], lockID) {
		t.Fatalf("expected res.Details to contain the LockID; got: %v", res.Details)
	}
}

// Phase 85 — sweeper interface assertions
var _ S3StateHeadAPI = (*mockS3StateHead)(nil)
var _ LockDigestDeleterAPI = (*mockLockDigestDeleter)(nil)

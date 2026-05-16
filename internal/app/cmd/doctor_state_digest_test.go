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

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
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

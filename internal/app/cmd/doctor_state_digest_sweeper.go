// Package cmd — doctor_state_digest_sweeper.go
//
// Phase 85 successor to checkStateLockDigest. Adds parallel HeadObject scan,
// age guard, and BatchWriteItem delete path for orphan state-lock DDB rows
// where the sibling S3 state object has been definitively deleted.
//
// This file holds only the new sweeper function, its narrow interface seams,
// and helper stubs. The Phase 84.1 checkStateLockDigest function (in doctor.go)
// remains untouched until Plan 03 swaps the buildChecks registration.
//
// See .planning/phases/85-doctor-orphan-state-lock-digest-sweeper-report-cleanup/BRIEF.md.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3StateHeadAPI is the narrow S3 surface for the Phase 85 orphan sweeper.
// HeadObject returns s3types.NotFound (HTTP 404) — distinct from GetObject's
// s3types.NoSuchKey — when the state object is absent. Kept separate from the
// existing S3StateReader (GetObject) interface in doctor.go so the Phase 84.1
// tests stay locked against their own seam.
type S3StateHeadAPI interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// LockDigestDeleterAPI is the narrow DynamoDB surface for BatchWriteItem deletes.
// BatchWriteItem accepts up to 25 WriteRequest items per call and can return a
// non-empty UnprocessedItems map on partial throttle; callers must handle both.
type LockDigestDeleterAPI interface {
	BatchWriteItem(ctx context.Context, params *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

// checkStateLockDigestSweeper is the Phase 85 successor to checkStateLockDigest.
//
// Behavior (to be implemented in Plan 02):
//   - Parallel HeadObject-based orphan scan (~10 worker goroutines).
//   - Age guard via SandboxLister cross-reference: lock rows whose embedded
//     sandbox-id still resolves to a live sandbox record are NEVER deleted.
//   - BatchWriteItem delete path, gated on dryRun==false && deleteStateDigests==true.
//   - Summary + 10-item inline preview + "use --json for full list" output format.
//
// Wave 0 stub: returns CheckSkipped so callers compile but assertions in the
// new Phase 85 tests fail with their planned t.Fatalf("RED — ...") messages.
func checkStateLockDigestSweeper(
	ctx context.Context,
	s3Head S3StateHeadAPI,
	ddbRead LockDigestReader,
	ddbWrite LockDigestDeleterAPI,
	lister SandboxLister,
	lockTableName string,
	dryRun bool,
	deleteStateDigests bool,
	minOrphanAge time.Duration,
) CheckResult {
	// Silence unused-import / unused-variable warnings until Plan 02 wires the
	// real implementation. These references compile but do nothing observable.
	_ = ctx
	_ = s3Head
	_ = ddbRead
	_ = ddbWrite
	_ = lister
	_ = lockTableName
	_ = dryRun
	_ = deleteStateDigests
	_ = minOrphanAge
	_ = errors.New
	_ = fmt.Sprintf
	_ = strings.Builder{}
	_ = sync.WaitGroup{}
	_ = awssdk.String
	_ = dynamodbtypes.WriteRequest{}
	_ = s3types.NotFound{}

	return CheckResult{
		Name:    "Terraform state lock digest",
		Status:  CheckSkipped,
		Message: "phase 85 sweeper not yet implemented (Wave 0 stub)",
	}
}

// renderSweeperMessage formats the WARN message: summary line + up to 10 inline
// items + "… and N more (use --json for full list)" continuation. The public
// test TestDigestSweeper_OutputFormat asserts on the output of this helper, so
// the signature is locked here even though the body is a Plan 02 stub.
//
// Returns (summary, fullList) where summary is the multi-line human-readable
// message and fullList is every item ID without inline cap (for --json output).
// Implementation: Plan 02.
func renderSweeperMessage(orphansDeleted, orphansSkipped, mismatchOther []string) (summary string, fullList []string) {
	_ = orphansDeleted
	_ = orphansSkipped
	_ = mismatchOther
	return "", nil // stub — Plan 02
}

// batchDeleteLockItems issues BatchWriteItem in 25-item batches against
// tableName and retries UnprocessedItems. Returns (deleted, failed, failures)
// where failures maps lockID → error for items that did not delete.
// Implementation: Plan 02.
func batchDeleteLockItems(
	ctx context.Context,
	client LockDigestDeleterAPI,
	tableName string,
	lockIDs []string,
) (deleted int, failed int, failures map[string]error) {
	_ = ctx
	_ = client
	_ = tableName
	_ = lockIDs
	return 0, 0, nil // stub — Plan 02
}

// Compile-time assertions: the real AWS SDK v2 clients satisfy the new narrow
// interfaces. If the SDK ever changes a method set, these refuse to compile
// and we catch the drift at build time rather than at the call site.
var _ S3StateHeadAPI = (*s3.Client)(nil)
var _ LockDigestDeleterAPI = (*dynamodb.Client)(nil)

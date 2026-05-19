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

// parseSandboxIDFromLockID extracts the sandbox-ID from a Terragrunt state key
// embedded in a LockID such as
// "tf-km-state-use1/tf-km/sandboxes/sb-12345678/budget-enforcer/terraform.tfstate-md5".
//
// Sandbox-scoped state keys always contain the segment "/sandboxes/<sandbox-id>/".
// Shared-module state keys (ses, vpc, scp, management/*, etc.) do NOT contain
// this segment; for those, the function returns ok=false.
//
// Caller MUST handle ok=false by either falling back to the strict NotFound-only
// guard (no per-row sandbox cross-reference is possible) or skipping the row.
// Exercised by TestDigestSweeper_SharedModuleLockID_NoSandboxID.
func parseSandboxIDFromLockID(lockID string) (sandboxID string, ok bool) {
	const marker = "/sandboxes/"
	idx := strings.Index(lockID, marker)
	if idx < 0 {
		return "", false
	}
	after := lockID[idx+len(marker):]
	end := strings.IndexByte(after, '/')
	if end < 0 {
		return "", false
	}
	sid := after[:end]
	if sid == "" {
		return "", false
	}
	return sid, true
}

// sweeperInlineCap is the maximum number of items rendered inline in the
// human-readable WARN message. Items beyond this cap are summarised as
// "… and N more (use --json for full list)"; the full uncapped list is
// always emitted via CheckResult.Details for --json consumers (ACCEPT-READ).
const sweeperInlineCap = 10

// checkStateLockDigestSweeper is the Phase 85 successor to checkStateLockDigest.
//
// Pipeline:
//  1. Paginated Scan of the lock table — collects every "*-md5" row.
//  2. Parallel HeadObject scan (10-worker semaphore) — classifies each row as
//     orphan (s3types.NotFound — HTTP 404), cant-verify (ambiguous error), or
//     live (object exists).
//  3. Age guard: rows whose LockID embeds a still-live sandbox-id are skipped
//     from deletion. Shared-module rows (no /sandboxes/ segment) bypass the
//     lister cross-reference and fall to the strict NotFound-only path.
//  4. Optional BatchWriteItem delete path, gated on dryRun==false AND
//     deleteStateDigests==true. UnprocessedItems are surfaced as failed.
//  5. Output: summary + 10-item inline preview + "use --json for full list"
//     plus CheckResult.Details populated with the FULL uncapped item list.
//
// minOrphanAge is accepted for forward compatibility but currently UNUSED — no
// per-row creation timestamp exists in the Terragrunt lock table schema; the
// age guard is implemented via SandboxLister cross-reference instead (see
// step 3). Reserved for a future time-based extension.
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
	const name = "Terraform state lock digest"
	if s3Head == nil || ddbRead == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "S3 or DynamoDB client unavailable"}
	}
	_ = minOrphanAge // reserved; current age guard uses sandbox-lister cross-reference

	// 1) Scan the lock table (paginated — H7 from Phase 84.1).
	type lockRow struct {
		lockID string
		bucket string
		key    string
	}
	var rows []lockRow
	paginator := dynamodb.NewScanPaginator(ddbRead, &dynamodb.ScanInput{TableName: awssdk.String(lockTableName)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			var rnf *dynamodbtypes.ResourceNotFoundException
			if errors.As(err, &rnf) {
				return CheckResult{Name: name, Status: CheckSkipped, Message: fmt.Sprintf("lock table %q does not exist", lockTableName)}
			}
			return CheckResult{Name: name, Status: CheckError, Message: fmt.Sprintf("scan lock table page: %v", err)}
		}
		for _, item := range page.Items {
			lockIDAttr, ok := item["LockID"].(*dynamodbtypes.AttributeValueMemberS)
			if !ok || !strings.HasSuffix(lockIDAttr.Value, "-md5") {
				continue
			}
			bucket, key, ok := parseLockID(lockIDAttr.Value)
			if !ok {
				continue
			}
			rows = append(rows, lockRow{lockID: lockIDAttr.Value, bucket: bucket, key: key})
		}
	}
	if len(rows) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: "state digest consistent (0 items checked)"}
	}

	// 2) Build the live-sandbox set for the age guard (best-effort; nil lister = no guard).
	liveSandboxes := map[string]bool{}
	if lister != nil {
		if records, err := lister.ListSandboxes(ctx, false); err == nil {
			for _, r := range records {
				liveSandboxes[r.SandboxID] = true
			}
		}
		// Lister errors are non-fatal — age guard simply allows nothing to be
		// skipped by sandbox-id mapping. Combined with the strict NotFound-only
		// delete gate, this is still safe: a stale list cannot cause a false
		// positive deletion of a row whose S3 object still exists.
	}

	// 3) Parallel HEAD scan with a 10-worker semaphore (matches BRIEF.md target).
	const headWorkers = 10
	type scanResult struct {
		lockID     string
		sandboxID  string
		hasSID     bool
		isOrphan   bool // definitive s3types.NotFound (HEAD 404)
		cantVerify bool // ambiguous error (network/5xx/throttle)
	}
	results := make([]scanResult, len(rows))
	sem := make(chan struct{}, headWorkers)
	var wg sync.WaitGroup
	for i := range rows {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Pitfall #4 — Respect context cancellation while waiting for a
			// semaphore slot. Without this select, a cancelled context leaks
			// the goroutine indefinitely.
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			r := rows[i]
			sid, hasSID := parseSandboxIDFromLockID(r.lockID)
			res := scanResult{lockID: r.lockID, sandboxID: sid, hasSID: hasSID}
			_, err := s3Head.HeadObject(ctx, &s3.HeadObjectInput{
				Bucket: awssdk.String(r.bucket),
				Key:    awssdk.String(r.key),
			})
			if err == nil {
				// Object exists → not an orphan. Could still be a stale-MD5
				// mismatch, but per BRIEF.md that is OUT OF SCOPE for the
				// sweeper.
				results[i] = res
				return
			}
			// Pitfall #1 — HeadObject 404 is *s3types.NotFound, NOT
			// *s3types.NoSuchKey. s3types.NoSuchKey is GetObject-only (Phase
			// 84.1 checkStateLockDigest uses it). If you change this, the
			// sweeper will NEVER classify anything as orphan.
			var notFound *s3types.NotFound
			if errors.As(err, &notFound) {
				res.isOrphan = true
			} else {
				res.cantVerify = true
			}
			results[i] = res
		}(i)
	}
	wg.Wait()

	// 4) Classify rows into orphan-deletable / orphan-skipped / cant-verify / other.
	var orphanDeletable []string // definitive NotFound + age guard passes (or no sandbox-id at all)
	var orphanSkipped []string   // definitive NotFound but age guard fails (live sandbox)
	var cantVerify []string      // ambiguous error
	for _, r := range results {
		switch {
		case r.isOrphan:
			// Age guard: if LockID embeds a sandbox-ID that is still LIVE → skip.
			// If LockID has NO sandbox-ID (shared-module lock like ses/vpc/scp)
			// → no per-row owner to consult; treat as deletable when the delete
			// gate is open. This matches BRIEF.md: "delete iff S3 HEAD returns
			// definitive NoSuchKey AND DDB row age exceeds threshold."
			// Exercised by TestDigestSweeper_SharedModuleLockID_NoSandboxID.
			if r.hasSID && liveSandboxes[r.sandboxID] {
				orphanSkipped = append(orphanSkipped, r.lockID)
			} else {
				orphanDeletable = append(orphanDeletable, r.lockID)
			}
		case r.cantVerify:
			cantVerify = append(cantVerify, r.lockID)
		}
		// else: object exists, no action.
	}

	// 5) Optional delete path — gated on dryRun=false AND deleteStateDigests=true.
	deleted, failed := 0, 0
	var deleteFailures map[string]error
	if !dryRun && deleteStateDigests && len(orphanDeletable) > 0 && ddbWrite != nil {
		deleted, failed, deleteFailures = batchDeleteLockItems(ctx, ddbWrite, lockTableName, orphanDeletable)
	}

	// 6) Render output (summary + 10-item preview + "and N more (use --json...)") AND
	//    capture the uncapped list for CheckResult.Details (ACCEPT-READ).
	summary, fullList := renderSweeperMessage(orphanDeletable, orphanSkipped, cantVerify)
	status := CheckWarn
	if len(orphanDeletable) == 0 && len(orphanSkipped) == 0 && len(cantVerify) == 0 {
		status = CheckOK
		summary = fmt.Sprintf("state digest consistent (%d items checked)", len(rows))
		fullList = nil // OK status → no detail list
	}
	remediation := ""
	if len(orphanDeletable) > 0 && (dryRun || !deleteStateDigests) {
		remediation = "Re-run with --dry-run=false --delete-state-digests to delete orphan rows."
	}

	// Surface delete outcomes in the WARN message when the destructive path ran.
	if !dryRun && deleteStateDigests && len(orphanDeletable) > 0 {
		var sb strings.Builder
		sb.WriteString(summary)
		fmt.Fprintf(&sb, "\n  → %d deleted, %d failed", deleted, failed)
		if failed > 0 {
			// Name the failed lockIDs in the message (up to 5 — the rest go in
			// --json / Details).
			i := 0
			for id, e := range deleteFailures {
				if i >= 5 {
					break
				}
				fmt.Fprintf(&sb, "\n    failed: %s (%v)", id, e)
				i++
			}
		}
		summary = sb.String()
		if failed > 0 {
			remediation = fmt.Sprintf("%d orphan row(s) failed to delete (typically throttling). Re-run after a short wait.", failed)
		}
	}

	return CheckResult{
		Name:        name,
		Status:      status,
		Message:     summary,
		Remediation: remediation,
		Details:     fullList, // ACCEPT-READ: --json consumers get the FULL uncapped list (omitempty drops it when nil)
	}
}

// renderSweeperMessage formats the WARN message: summary line + up to 10 inline
// items + "… and N more (use --json for full list)" continuation. Returns
// (summary, fullList) where summary is the multi-line human-readable message
// and fullList is every item ID without inline cap (for --json output via
// CheckResult.Details — ACCEPT-READ).
func renderSweeperMessage(orphanDeletable, orphanSkipped, otherMismatch []string) (summary string, fullList []string) {
	orphanCount := len(orphanDeletable) + len(orphanSkipped)
	otherCount := len(otherMismatch)
	total := orphanCount + otherCount

	var sb strings.Builder
	fmt.Fprintf(&sb, "state digest mismatch in %d item(s) (%d orphan: state object missing, %d other)",
		total, orphanCount, otherCount)

	// Build the full, uncapped list for --json consumers (CheckResult.Details).
	fullList = make([]string, 0, total)
	for _, id := range orphanDeletable {
		fullList = append(fullList, id+" (orphan: state object missing)")
	}
	for _, id := range orphanSkipped {
		fullList = append(fullList, id+" (orphan: state object missing — skipped, sandbox still live)")
	}
	for _, id := range otherMismatch {
		fullList = append(fullList, id+" (could not verify — S3 head error)")
	}

	// Inline cap at sweeperInlineCap items, with continuation line.
	for i, line := range fullList {
		if i >= sweeperInlineCap {
			fmt.Fprintf(&sb, "\n  … and %d more (use --json for full list)", total-sweeperInlineCap)
			break
		}
		fmt.Fprintf(&sb, "\n  %s", line)
	}
	return sb.String(), fullList
}

// batchDeleteLockItems issues DynamoDB BatchWriteItem calls in 25-item batches.
// BatchWriteItem is a partial-success API: a successful (nil-error) call can
// still return UnprocessedItems for throttled items (Pitfall #2). Those LockIDs
// are NOT retried automatically here — they are surfaced as failed so the
// operator re-runs km doctor (which will pick them up again).
//
// Returns (deleted, failed, failuresByLockID) where deleted+failed == len(lockIDs).
//
// Implementation: Plan 02 (Task 2).
func batchDeleteLockItems(
	ctx context.Context,
	client LockDigestDeleterAPI,
	tableName string,
	lockIDs []string,
) (deleted int, failed int, failures map[string]error) {
	const batchSize = 25
	failures = make(map[string]error)

	for i := 0; i < len(lockIDs); i += batchSize {
		end := i + batchSize
		if end > len(lockIDs) {
			end = len(lockIDs)
		}
		batch := lockIDs[i:end]

		requests := make([]dynamodbtypes.WriteRequest, len(batch))
		for j, id := range batch {
			requests[j] = dynamodbtypes.WriteRequest{
				DeleteRequest: &dynamodbtypes.DeleteRequest{
					Key: map[string]dynamodbtypes.AttributeValue{
						"LockID": &dynamodbtypes.AttributeValueMemberS{Value: id},
					},
				},
			}
		}

		out, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]dynamodbtypes.WriteRequest{
				tableName: requests,
			},
		})
		if err != nil {
			// Whole-batch failure: count every item as failed.
			for _, id := range batch {
				failed++
				failures[id] = err
			}
			continue
		}

		// Pitfall #2 — BatchWriteItem is partial-success: err==nil but
		// UnprocessedItems may be non-empty. Do NOT skip this check.
		unprocessedIDs := map[string]bool{}
		if unprocessed, ok := out.UnprocessedItems[tableName]; ok {
			for _, r := range unprocessed {
				if r.DeleteRequest != nil {
					if av, ok := r.DeleteRequest.Key["LockID"].(*dynamodbtypes.AttributeValueMemberS); ok {
						unprocessedIDs[av.Value] = true
					}
				}
			}
		}
		for _, id := range batch {
			if unprocessedIDs[id] {
				failed++
				failures[id] = fmt.Errorf("unprocessed by DynamoDB (throttled — re-run to retry)")
			} else {
				deleted++
			}
		}
	}
	return deleted, failed, failures
}

// Compile-time assertions: the real AWS SDK v2 clients satisfy the new narrow
// interfaces. If the SDK ever changes a method set, these refuse to compile
// and we catch the drift at build time rather than at the call site.
var _ S3StateHeadAPI = (*s3.Client)(nil)
var _ LockDigestDeleterAPI = (*dynamodb.Client)(nil)

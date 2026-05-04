// Package cmd — doctor_slack_transcript.go
// Plan 68-11 — km doctor checks for Phase 68 transcript streaming health.
//
//   checkSlackTranscriptTableExists: DescribeTable on the stream-messages DDB
//     table; OK if exists + ACTIVE, WARN otherwise. Catches operators who
//     deployed Phase 68 sandboxes before running km init to provision the table.
//
//   checkSlackFilesWriteScope: probes Slack auth.test directly with the
//     operator's bot token from SSM /km/slack/bot-token, captures
//     X-OAuth-Scopes response header, returns OK if files:write present,
//     WARN otherwise. Mirrors Plan 08's cold-start logic but operator-side
//     (not Lambda-side).
//
//   checkSlackTranscriptStaleObjects: S3 ListObjectsV2 on transcripts/ prefix,
//     scans km-sandboxes DDB for live sandbox IDs, computes the difference;
//     WARN listing transcripts/{sandbox_id}/ prefixes whose sandbox no longer
//     exists. Cleanup advisory, not a failure.
//
// All checks follow the Phase 67 doctor_slack.go pattern: closure-based deps,
// nil deps → SKIPPED, never FAIL (Phase 68 is opt-in; missing transcript
// table is not a hard failure for non-opted-in deployments).
package cmd

import (
	"context"
	"fmt"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// =============================================================================
// Plan 68-11 — Slack transcript-streaming diagnostic checks
// =============================================================================

// checkSlackTranscriptTableExists verifies the Phase 68 stream-messages
// DynamoDB table is provisioned and ACTIVE. Returns:
//
//   - SKIPPED: no DDB client configured.
//   - OK: table exists with status ACTIVE.
//   - WARN: DescribeTable failed (table missing or inaccessible) — likely the
//     operator hasn't run km init since upgrading to Phase 68.
//   - WARN: table exists but is not ACTIVE (CREATING/UPDATING/DELETING).
func checkSlackTranscriptTableExists(
	ctx context.Context,
	client DynamoDescribeAPI,
	tableName string,
) CheckResult {
	name := "Slack transcript table exists"
	if client == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "DynamoDB client not configured",
		}
	}
	if tableName == "" {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "stream-messages table name not configured",
		}
	}
	out, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: awssdk.String(tableName),
	})
	if err != nil {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("DescribeTable %s failed: %v", tableName, err),
			Remediation: "Run 'km init' to provision the Phase 68 transcript-streaming DynamoDB table",
		}
	}
	if out.Table == nil || out.Table.TableStatus != ddbtypes.TableStatusActive {
		gotStatus := "<unknown>"
		if out.Table != nil {
			gotStatus = string(out.Table.TableStatus)
		}
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("table %s status=%s (expected ACTIVE)", tableName, gotStatus),
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Message: fmt.Sprintf("table %s ACTIVE", tableName),
	}
}

// checkSlackFilesWriteScope probes the Slack auth.test endpoint via the
// injected getScopes callback (same callback used by checkSlackAppEventsScopes
// for Phase 67 inbound) and reports whether the bot token has files:write.
// Required by Phase 68 transcript upload (bridge ActionUpload calls
// files.upload + files.completeUploadExternal).
//
// Returns:
//   - SKIPPED: getScopes is nil (bot token not configured / Slack not set up).
//   - OK: files:write present in scopes.
//   - WARN: files:write missing (transcript upload would 400 from bridge).
//   - WARN: getScopes returned an error (do not fail doctor on auth.test outage).
func checkSlackFilesWriteScope(
	ctx context.Context,
	getScopes func(context.Context) ([]string, error),
) CheckResult {
	name := "Slack files:write scope"
	if getScopes == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "Slack auth-test scopes func not configured",
		}
	}
	scopes, err := getScopes(ctx)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("could not check Slack scopes: %v", err),
		}
	}
	for _, s := range scopes {
		if s == "files:write" {
			return CheckResult{
				Name:    name,
				Status:  CheckOK,
				Message: "Slack bot has files:write scope (transcript upload supported)",
			}
		}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     "Slack bot is missing files:write scope — transcript upload via bridge ActionUpload will fail",
		Remediation: "Add files:write via Slack App config → OAuth & Permissions → Bot Token Scopes, then reinstall the app to your workspace (bot token unchanged — no 'km slack rotate-token' needed). Run 'km doctor' again to verify.",
	}
}

// checkSlackTranscriptStaleObjects lists S3 objects under the transcripts/
// prefix, derives the unique sandbox-id sub-prefixes, intersects them with
// the live sandboxes from DDB, and warns about any orphan transcript prefix
// whose sandbox row no longer exists.
//
// Cleanup advisory — never fails the doctor run. Returns:
//
//   - SKIPPED: deps are nil (no S3 client / no listSandboxes func / no bucket).
//   - OK: no transcript prefixes, or every prefix matches a live sandbox.
//   - WARN: one or more orphan prefixes detected.
func checkSlackTranscriptStaleObjects(
	ctx context.Context,
	s3Client kmaws.S3CleanupAPI,
	bucket string,
	listSandboxIDs func(context.Context) ([]string, error),
	dryRun bool,
) CheckResult {
	name := "Slack transcript stale objects"
	if s3Client == nil || listSandboxIDs == nil {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "S3 client or sandbox-list func not configured",
		}
	}
	if bucket == "" {
		return CheckResult{
			Name:    name,
			Status:  CheckSkipped,
			Message: "artifacts bucket not configured",
		}
	}

	// List unique sandbox-id prefixes under transcripts/.
	var prefixes []string
	var continuationToken *string
	for {
		out, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            awssdk.String(bucket),
			Prefix:            awssdk.String("transcripts/"),
			Delimiter:         awssdk.String("/"),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return CheckResult{
				Name:    name,
				Status:  CheckWarn,
				Message: fmt.Sprintf("S3 ListObjectsV2 transcripts/: %v", err),
			}
		}
		for _, cp := range out.CommonPrefixes {
			if cp.Prefix != nil {
				prefixes = append(prefixes, *cp.Prefix)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		continuationToken = out.NextContinuationToken
	}

	if len(prefixes) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: "no transcript prefixes in S3",
		}
	}

	// Build set of live sandbox IDs.
	liveIDs, err := listSandboxIDs(ctx)
	if err != nil {
		return CheckResult{
			Name:    name,
			Status:  CheckWarn,
			Message: fmt.Sprintf("list sandboxes failed: %v", err),
		}
	}
	liveSet := make(map[string]struct{}, len(liveIDs))
	for _, id := range liveIDs {
		liveSet[id] = struct{}{}
	}

	var stale []string
	for _, p := range prefixes {
		// p is "transcripts/sb-abc/", extract the sandbox ID.
		trimmed := strings.TrimPrefix(p, "transcripts/")
		sid := strings.TrimSuffix(trimmed, "/")
		if sid == "" {
			continue
		}
		if _, alive := liveSet[sid]; !alive {
			stale = append(stale, p)
		}
	}
	if len(stale) == 0 {
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: fmt.Sprintf("%d transcript prefix(es); none stale", len(prefixes)),
		}
	}

	// Plan quick-7: dry-run path — detect-only; point operator at --dry-run=false.
	if dryRun {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%d stale transcript prefix(es): %s", len(stale), strings.Join(stale, ", ")),
			Remediation: "Re-run with --dry-run=false to delete the orphan transcript objects",
		}
	}

	// Plan quick-7: destructive cleanup path. Per stale prefix, paginate
	// ListObjectsV2 (no delimiter) to collect every object key, then batch
	// DeleteObjects in groups of 1000 (S3 API limit). Per-prefix failures
	// don't abort the loop.
	deleted, skipped, objectsDeleted := 0, 0, 0
	for _, p := range stale {
		keys, listErr := listAllKeysUnderPrefix(ctx, s3Client, bucket, p)
		if listErr != nil {
			skipped++
			continue
		}
		if len(keys) == 0 {
			// Empty prefix (no objects to delete) — count as cleaned.
			deleted++
			continue
		}
		prefixOK := true
		for batchStart := 0; batchStart < len(keys); batchStart += 1000 {
			end := batchStart + 1000
			if end > len(keys) {
				end = len(keys)
			}
			objs := make([]s3types.ObjectIdentifier, 0, end-batchStart)
			for _, k := range keys[batchStart:end] {
				objs = append(objs, s3types.ObjectIdentifier{Key: awssdk.String(k)})
			}
			_, delErr := s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: awssdk.String(bucket),
				Delete: &s3types.Delete{
					Objects: objs,
					Quiet:   awssdk.Bool(true),
				},
			})
			if delErr != nil {
				prefixOK = false
				break
			}
			objectsDeleted += end - batchStart
		}
		if prefixOK {
			deleted++
		} else {
			skipped++
		}
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: fmt.Sprintf("%d stale transcript prefix(es) (%d deleted, %d skipped, %d objects total)", len(stale), deleted, skipped, objectsDeleted),
	}
}

// listAllKeysUnderPrefix paginates ListObjectsV2 (no delimiter) and returns
// every object key under the given prefix. Used by the Plan quick-7 cleanup
// path to enumerate keys before batched DeleteObjects.
func listAllKeysUnderPrefix(ctx context.Context, c kmaws.S3CleanupAPI, bucket, prefix string) ([]string, error) {
	var keys []string
	var token *string
	for {
		out, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            awssdk.String(bucket),
			Prefix:            awssdk.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return keys, nil
}


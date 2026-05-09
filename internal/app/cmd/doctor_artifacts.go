// Package cmd — orphaned-S3-artifacts detection for `km doctor`.
//
// km create uploads userdata, the rendered profile, and runtime artifacts to
// s3://{ArtifactsBucket}/artifacts/{sandbox-id}/... at provisioning time.
// km destroy reads .km-profile.yaml from that prefix and exports CloudWatch
// logs INTO it — but it never sweeps the prefix itself, so every destroyed
// sandbox leaves behind one artifacts/{sandbox-id}/ folder in S3 forever.
// Over weeks/months these accumulate and the artifacts bucket grows
// unbounded.
//
// checkOrphanedArtifacts mirrors checkSlackTranscriptStaleObjects: list
// CommonPrefixes under "artifacts/", intersect with live sandbox IDs from
// DynamoDB, and warn (or delete, when --dry-run=false --delete-s3) on any
// prefix whose sandbox is gone.
package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// checkOrphanedArtifacts lists CommonPrefixes under artifacts/ in the
// configured artifacts bucket and warns about any whose sandbox-id has no
// matching DynamoDB record. When dryRun is false AND deleteS3 is true,
// every object under each stale prefix is deleted in batches of 1000.
//
// The deleteS3 gate is the same explicit-opt-in pattern as --delete-ebs and
// --delete-sqs: artifact prefixes can include the rendered profile, sandbox
// userdata, and exported logs, so we require the operator to commit
// explicitly before sweeping. Without --delete-s3, the check stays
// report-only even when --dry-run=false is set globally.
func checkOrphanedArtifacts(
	ctx context.Context,
	s3Client kmaws.S3CleanupAPI,
	bucket string,
	listSandboxIDs func(context.Context) ([]string, error),
	dryRun bool,
	deleteS3 bool,
) CheckResult {
	name := "Orphaned S3 artifacts"
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

	// Enumerate per-sandbox prefixes under artifacts/.
	var prefixes []string
	var continuationToken *string
	for {
		out, err := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            awssdk.String(bucket),
			Prefix:            awssdk.String("artifacts/"),
			Delimiter:         awssdk.String("/"),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return CheckResult{
				Name:    name,
				Status:  CheckWarn,
				Message: fmt.Sprintf("S3 ListObjectsV2 artifacts/: %v", err),
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
			Message: "no artifact prefixes in S3",
		}
	}

	// Intersect with the live-sandbox set.
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

	// 10-minute provisioning cutoff: skip prefixes whose oldest object was
	// created in the last 10 minutes — likely an in-flight km create. Sample
	// one object per stale candidate (MaxKeys=1) to keep the extra S3 traffic
	// proportional to the orphan list, not the full artifact set.
	provisioningCutoff := time.Now().Add(-10 * time.Minute)
	var stale []string
	skippedYoung := 0
	for _, p := range prefixes {
		// p is "artifacts/sb-abc/", extract the sandbox ID.
		trimmed := strings.TrimPrefix(p, "artifacts/")
		sid := strings.TrimSuffix(trimmed, "/")
		if sid == "" {
			continue
		}
		if _, alive := liveSet[sid]; alive {
			continue
		}
		// Sample one object's LastModified — if all objects under the prefix
		// are younger than the cutoff, skip; otherwise mark stale. Errors
		// (transient S3, prefix race) bias toward "stale" to avoid masking
		// real orphans.
		sample, sampleErr := s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:  awssdk.String(bucket),
			Prefix:  awssdk.String(p),
			MaxKeys: awssdk.Int32(1),
		})
		if sampleErr == nil && sample != nil && len(sample.Contents) > 0 && sample.Contents[0].LastModified != nil {
			if sample.Contents[0].LastModified.After(provisioningCutoff) {
				skippedYoung++
				continue
			}
		}
		stale = append(stale, p)
	}
	if len(stale) == 0 {
		msg := fmt.Sprintf("%d artifact prefix(es); none stale", len(prefixes))
		if skippedYoung > 0 {
			msg = fmt.Sprintf("%s (skipped %d prefix(es) <10min old; possible in-flight km create)", msg, skippedYoung)
		}
		return CheckResult{
			Name:    name,
			Status:  CheckOK,
			Message: msg,
		}
	}

	// Report-only path. dryRun OR opt-in missing. Two-flavored remediation:
	// dry-run users get the full pair; --dry-run=false-without-opt-in users
	// only get told to add --delete-s3 (avoids nagging them about a flag
	// they already passed).
	if dryRun || !deleteS3 {
		remediation := "Re-run with --dry-run=false --delete-s3 to delete the orphan artifact objects"
		if !dryRun && !deleteS3 {
			remediation = "Add --delete-s3 to also delete the orphan artifact objects"
		}
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%d stale artifact prefix(es): %s", len(stale), strings.Join(stale, ", ")),
			Remediation: remediation,
		}
	}

	// Destructive path: per stale prefix, paginate ListObjectsV2 and batch
	// DeleteObjects (1000-key S3 limit). Per-prefix failures don't abort.
	deleted, skipped, objectsDeleted := 0, 0, 0
	for _, p := range stale {
		keys, listErr := listAllKeysUnderPrefix(ctx, s3Client, bucket, p)
		if listErr != nil {
			skipped++
			continue
		}
		if len(keys) == 0 {
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
		Message: fmt.Sprintf("%d stale artifact prefix(es) (%d deleted, %d skipped, %d objects total)", len(stale), deleted, skipped, objectsDeleted),
	}
}

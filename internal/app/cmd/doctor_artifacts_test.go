// Tests for checkOrphanedArtifacts (orphan artifacts/{sandbox-id}/ S3 prefix sweep).
//
// fakeS3List is reused from doctor_slack_transcript_test.go since both checks
// share the same kmaws.S3CleanupAPI surface (ListObjectsV2 + DeleteObjects).
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// TestDoctor_OrphanedArtifacts_NilDeps_Skipped: nil S3 client OR nil
// listSandboxIDs func → SKIPPED. Same as the transcript check.
func TestDoctor_OrphanedArtifacts_NilDeps_Skipped(t *testing.T) {
	r := checkOrphanedArtifacts(context.Background(), nil, "bucket", nil, true, false)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for nil deps, got %s: %s", r.Status, r.Message)
	}
}

// TestDoctor_OrphanedArtifacts_EmptyBucket_Skipped: bucket name empty → SKIPPED.
func TestDoctor_OrphanedArtifacts_EmptyBucket_Skipped(t *testing.T) {
	s3Cli := &fakeS3List{}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkOrphanedArtifacts(context.Background(), s3Cli, "", listIDs, true, false)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for empty bucket, got %s: %s", r.Status, r.Message)
	}
}

// TestDoctor_OrphanedArtifacts_NoPrefixes_OK: S3 returns no CommonPrefixes →
// OK ("no artifact prefixes in S3"). Common when the cluster is brand new.
func TestDoctor_OrphanedArtifacts_NoPrefixes_OK(t *testing.T) {
	s3Cli := &fakeS3List{pages: nil}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkOrphanedArtifacts(context.Background(), s3Cli, "test-bucket", listIDs, true, false)
	if r.Status != CheckOK {
		t.Fatalf("expected OK with no prefixes, got %s: %s", r.Status, r.Message)
	}
}

// TestDoctor_OrphanedArtifacts_AllAlive_OK: every artifact prefix corresponds
// to a live sandbox in DDB → OK with the count.
func TestDoctor_OrphanedArtifacts_AllAlive_OK(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("artifacts/sb-a/")},
					{Prefix: awssdk.String("artifacts/sb-b/")},
				},
			},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) {
		return []string{"sb-a", "sb-b"}, nil
	}
	r := checkOrphanedArtifacts(context.Background(), s3Cli, "test-bucket", listIDs, true, false)
	if r.Status != CheckOK {
		t.Fatalf("expected OK when every prefix has a live sandbox, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "2 artifact prefix(es)") {
		t.Errorf("expected '2 artifact prefix(es)' in message, got: %s", r.Message)
	}
}

// TestDoctor_OrphanedArtifacts_DryRun_NoDestructiveCalls: stale prefix sb-c
// present, dryRun=true → WARN with sb-c surfaced, NO DeleteObjects calls.
// Even when --delete-s3 is set, dry-run wins.
func TestDoctor_OrphanedArtifacts_DryRun_NoDestructiveCalls(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("artifacts/sb-a/")},
					{Prefix: awssdk.String("artifacts/sb-c/")},
				},
			},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) { return []string{"sb-a"}, nil }
	r := checkOrphanedArtifacts(context.Background(), s3Cli, "test-bucket", listIDs, true, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "artifacts/sb-c/") {
		t.Errorf("expected sb-c surfaced in message, got: %s", r.Message)
	}
	if s3Cli.deleteObjectsCalls != 0 {
		t.Errorf("dryRun=true must NOT call DeleteObjects; saw %d calls", s3Cli.deleteObjectsCalls)
	}
	if !strings.Contains(r.Remediation, "--dry-run=false --delete-s3") {
		t.Errorf("dry-run remediation should point at full pair, got: %s", r.Remediation)
	}
}

// TestDoctor_OrphanedArtifacts_DryRunFalseWithoutDeleteS3_NoDestructiveCalls
// verifies the explicit-opt-in property: --dry-run=false alone is NOT enough
// to delete artifact prefixes — the operator must also pass --delete-s3.
// Same pattern as --delete-ebs and --delete-sqs.
func TestDoctor_OrphanedArtifacts_DryRunFalseWithoutDeleteS3_NoDestructiveCalls(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			{CommonPrefixes: []s3types.CommonPrefix{{Prefix: awssdk.String("artifacts/sb-c/")}}},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkOrphanedArtifacts(context.Background(), s3Cli, "test-bucket", listIDs, false, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if s3Cli.deleteObjectsCalls != 0 {
		t.Errorf("--dry-run=false alone must NOT delete; saw %d DeleteObjects calls", s3Cli.deleteObjectsCalls)
	}
	if !strings.Contains(r.Remediation, "--delete-s3") {
		t.Errorf("expected remediation to mention --delete-s3, got: %s", r.Remediation)
	}
	// The remediation should NOT also nag about --dry-run=false (already passed).
	if strings.Contains(r.Remediation, "--dry-run=false") {
		t.Errorf("remediation in --dry-run=false mode shouldn't repeat the flag, got: %s", r.Remediation)
	}
}

// TestDoctor_OrphanedArtifacts_DryRunFalse_HappyPath: 1 stale prefix (sb-c)
// with 3 objects, --dry-run=false --delete-s3 → 1 DeleteObjects call carrying
// 3 keys, WARN with (1 deleted, 0 skipped, 3 objects total).
func TestDoctor_OrphanedArtifacts_DryRunFalse_HappyPath(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			// Page 0: top-level CommonPrefixes scan.
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("artifacts/sb-a/")},
					{Prefix: awssdk.String("artifacts/sb-c/")},
				},
			},
			// Page 1: listAllKeysUnderPrefix("artifacts/sb-c/") — 3 keys, no truncation.
			{
				Contents: []s3types.Object{
					{Key: awssdk.String("artifacts/sb-c/.km-profile.yaml")},
					{Key: awssdk.String("artifacts/sb-c/km-userdata.sh")},
					{Key: awssdk.String("artifacts/sb-c/logs.tar.gz")},
				},
			},
		},
	}
	listIDs := func(_ context.Context) ([]string, error) { return []string{"sb-a"}, nil }
	r := checkOrphanedArtifacts(context.Background(), s3Cli, "test-bucket", listIDs, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
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

// TestDoctor_OrphanedArtifacts_DryRunFalse_PartialFailure: 2 stale prefixes;
// first DeleteObjects succeeds, second fails → WARN with (1 deleted, 1
// skipped, ...). Loop is not aborted on per-prefix error.
func TestDoctor_OrphanedArtifacts_DryRunFalse_PartialFailure(t *testing.T) {
	s3Cli := &fakeS3List{
		pages: []*s3.ListObjectsV2Output{
			{
				CommonPrefixes: []s3types.CommonPrefix{
					{Prefix: awssdk.String("artifacts/sb-c/")},
					{Prefix: awssdk.String("artifacts/sb-d/")},
				},
			},
			// listAll for sb-c — 1 key.
			{Contents: []s3types.Object{{Key: awssdk.String("artifacts/sb-c/k1")}}},
			// listAll for sb-d — 1 key.
			{Contents: []s3types.Object{{Key: awssdk.String("artifacts/sb-d/k2")}}},
		},
		// First DeleteObjects (sb-c) succeeds, second (sb-d) fails.
		deleteObjectsErrs: []error{nil, errors.New("AccessDenied")},
	}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkOrphanedArtifacts(context.Background(), s3Cli, "test-bucket", listIDs, false, true)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "(1 deleted, 1 skipped,") {
		t.Errorf("expected '(1 deleted, 1 skipped,...)', got: %s", r.Message)
	}
}

// TestDoctor_OrphanedArtifacts_ListError_Warn: top-level ListObjectsV2
// returns AccessDenied → WARN with the error surfaced. Doctor must not fail.
func TestDoctor_OrphanedArtifacts_ListError_Warn(t *testing.T) {
	s3Cli := &fakeS3List{listErr: errors.New("AccessDenied")}
	listIDs := func(_ context.Context) ([]string, error) { return nil, nil }
	r := checkOrphanedArtifacts(context.Background(), s3Cli, "test-bucket", listIDs, true, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on ListObjectsV2 error, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "AccessDenied") {
		t.Errorf("expected error text in message, got: %s", r.Message)
	}
}

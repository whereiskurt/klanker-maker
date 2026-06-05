// Tests for checkOrphanedArtifacts (orphan artifacts/{sandbox-id}/ S3 prefix sweep)
// and checkS3LifecyclePolicy (transient-prefix expiry guardrail).
//
// fakeS3List is reused from doctor_slack_transcript_test.go since both checks
// share the same kmaws.S3CleanupAPI surface (ListObjectsV2 + DeleteObjects).
// mockS3Lifecycle is defined in doctor_test.go (Wave 1 infrastructure).
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
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

// ---------------------------------------------------------------------------
// checkS3LifecyclePolicy tests (DBG-S3 / DBG-S3-SET)
// ---------------------------------------------------------------------------

// noSuchLifecycleErr simulates the AWS API error for GetBucketLifecycleConfiguration
// when no lifecycle policy exists on the bucket. Implements smithy.APIError so
// errors.As with *smithy.GenericAPIError works correctly in tests.
type noSuchLifecycleConfigErr struct {
	smithy.GenericAPIError
}

func newNoSuchLifecycleErr() *noSuchLifecycleConfigErr {
	return &noSuchLifecycleConfigErr{
		GenericAPIError: smithy.GenericAPIError{Code: "NoSuchLifecycleConfiguration"},
	}
}

// TestDoctor_S3LifecyclePolicy_NilClient_Skipped: nil client → SKIPPED.
func TestDoctor_S3LifecyclePolicy_NilClient_Skipped(t *testing.T) {
	r := checkS3LifecyclePolicy(context.Background(), nil, "bucket", 30, false, false)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for nil client, got %s: %s", r.Status, r.Message)
	}
}

// TestDoctor_S3LifecyclePolicy_EmptyBucket_Skipped: empty bucket → SKIPPED.
func TestDoctor_S3LifecyclePolicy_EmptyBucket_Skipped(t *testing.T) {
	m := &mockS3Lifecycle{}
	r := checkS3LifecyclePolicy(context.Background(), m, "", 30, false, false)
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for empty bucket, got %s: %s", r.Status, r.Message)
	}
}

// TestDoctor_S3LifecyclePolicy_Missing: GetBucketLifecycleConfiguration returns
// NoSuchLifecycleConfiguration → WARN listing all four uncovered transient
// prefixes; no Put call because dryRun=true.
func TestDoctor_S3LifecyclePolicy_Missing(t *testing.T) {
	putCalls := 0
	m := &mockS3Lifecycle{
		getLifecycleFn: func(_ context.Context, _ *s3.GetBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
			return nil, newNoSuchLifecycleErr()
		},
		putLifecycleFn: func(_ context.Context, _ *s3.PutBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error) {
			putCalls++
			return &s3.PutBucketLifecycleConfigurationOutput{}, nil
		},
	}
	r := checkS3LifecyclePolicy(context.Background(), m, "my-bucket", 30, true, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN for missing lifecycle, got %s: %s", r.Status, r.Message)
	}
	if putCalls != 0 {
		t.Errorf("dryRun=true must NOT call PutBucketLifecycleConfiguration; got %d calls", putCalls)
	}
	// All four transient prefixes must appear in the message or remediation.
	for _, prefix := range []string{"logs/", "remote-create/", "agent-runs/", "slack-inbound/"} {
		if !strings.Contains(r.Message, prefix) && !strings.Contains(r.Remediation, prefix) {
			t.Errorf("expected %q mentioned in WARN output, message=%q remediation=%q", prefix, r.Message, r.Remediation)
		}
	}
}

// TestDoctor_S3LifecyclePolicy_MissingNoSetFlag: --dry-run=false but
// --set-s3-lifecycle not passed → WARN, no Put.
func TestDoctor_S3LifecyclePolicy_MissingNoSetFlag(t *testing.T) {
	putCalls := 0
	m := &mockS3Lifecycle{
		getLifecycleFn: func(_ context.Context, _ *s3.GetBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
			return nil, newNoSuchLifecycleErr()
		},
		putLifecycleFn: func(_ context.Context, _ *s3.PutBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error) {
			putCalls++
			return &s3.PutBucketLifecycleConfigurationOutput{}, nil
		},
	}
	// dryRun=false but setLifecycle=false
	r := checkS3LifecyclePolicy(context.Background(), m, "my-bucket", 30, false, false)
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if putCalls != 0 {
		t.Errorf("setLifecycle=false must NOT Put; got %d calls", putCalls)
	}
}

// makeTransientRule builds a lifecycle rule for a transient prefix, using the same
// Filter shape as checkS3LifecyclePolicy.
func makeTransientRule(id, prefix string, days int32) s3types.LifecycleRule {
	return s3types.LifecycleRule{
		ID:     awssdk.String(id),
		Status: s3types.ExpirationStatusEnabled,
		Filter: &s3types.LifecycleRuleFilter{Prefix: awssdk.String(prefix)},
		Expiration: &s3types.LifecycleExpiration{
			Days: awssdk.Int32(days),
		},
	}
}

// TestDoctor_S3LifecyclePolicy_AlreadySet: all four transient prefixes covered
// by existing expiry rules → CheckOK, zero Put calls (idempotent).
func TestDoctor_S3LifecyclePolicy_AlreadySet(t *testing.T) {
	putCalls := 0
	existingRules := []s3types.LifecycleRule{
		makeTransientRule("km-doctor-expire-logs-", "logs/", 30),
		makeTransientRule("km-doctor-expire-remote-create-", "remote-create/", 30),
		makeTransientRule("km-doctor-expire-agent-runs-", "agent-runs/", 30),
		makeTransientRule("km-doctor-expire-slack-inbound-", "slack-inbound/", 30),
	}
	m := &mockS3Lifecycle{
		getLifecycleFn: func(_ context.Context, _ *s3.GetBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
			return &s3.GetBucketLifecycleConfigurationOutput{Rules: existingRules}, nil
		},
		putLifecycleFn: func(_ context.Context, _ *s3.PutBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error) {
			putCalls++
			return &s3.PutBucketLifecycleConfigurationOutput{}, nil
		},
	}
	r := checkS3LifecyclePolicy(context.Background(), m, "my-bucket", 30, false, true)
	if r.Status != CheckOK {
		t.Fatalf("expected OK when all prefixes already covered, got %s: %s", r.Status, r.Message)
	}
	if putCalls != 0 {
		t.Errorf("idempotent: expected zero Put calls when already set, got %d", putCalls)
	}
}

// TestDoctor_S3LifecyclePolicy_SetMergesPreservesExisting: an unrelated
// operator rule exists + three transient prefixes are uncovered. --set-s3-lifecycle
// must call Put exactly once; the put input must contain the unrelated rule
// unchanged plus three new transient-prefix rules; no build-artifact prefix rule.
func TestDoctor_S3LifecyclePolicy_SetMergesPreservesExisting(t *testing.T) {
	// Only logs/ is covered; the other three are missing.
	operatorRule := s3types.LifecycleRule{
		ID:     awssdk.String("operator-custom-rule"),
		Status: s3types.ExpirationStatusEnabled,
		Filter: &s3types.LifecycleRuleFilter{Prefix: awssdk.String("operator-data/")},
		Expiration: &s3types.LifecycleExpiration{
			Days: awssdk.Int32(365),
		},
	}
	coveredRule := makeTransientRule("km-doctor-expire-logs-", "logs/", 30)
	var capturedRules []s3types.LifecycleRule
	putCalls := 0
	m := &mockS3Lifecycle{
		getLifecycleFn: func(_ context.Context, _ *s3.GetBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
			return &s3.GetBucketLifecycleConfigurationOutput{
				Rules: []s3types.LifecycleRule{operatorRule, coveredRule},
			}, nil
		},
		putLifecycleFn: func(_ context.Context, input *s3.PutBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error) {
			putCalls++
			if input.LifecycleConfiguration != nil {
				capturedRules = input.LifecycleConfiguration.Rules
			}
			return &s3.PutBucketLifecycleConfigurationOutput{}, nil
		},
	}
	r := checkS3LifecyclePolicy(context.Background(), m, "my-bucket", 30, false, true)
	if r.Status != CheckOK {
		t.Fatalf("expected OK after successful set, got %s: %s", r.Status, r.Message)
	}
	if putCalls != 1 {
		t.Fatalf("expected exactly 1 Put call, got %d", putCalls)
	}

	// Total rules: 2 existing (operator + logs) + 3 new (remote-create, agent-runs, slack-inbound).
	if len(capturedRules) != 5 {
		t.Errorf("expected 5 rules in Put (2 existing + 3 new), got %d", len(capturedRules))
	}

	// Operator rule must be present unchanged.
	operatorFound := false
	for _, rule := range capturedRules {
		if rule.ID != nil && *rule.ID == "operator-custom-rule" {
			operatorFound = true
		}
	}
	if !operatorFound {
		t.Error("operator-custom-rule must be preserved in merged Put")
	}

	// Build-artifact prefixes (toolchain/, sidecars/, rsync/) must NOT appear.
	for _, rule := range capturedRules {
		var prefix string
		if rule.Filter != nil && rule.Filter.Prefix != nil {
			prefix = *rule.Filter.Prefix
		} else if rule.Prefix != nil {
			prefix = *rule.Prefix
		}
		for _, forbidden := range []string{"toolchain/", "sidecars/", "rsync/"} {
			if prefix == forbidden {
				t.Errorf("build-artifact prefix %q must NOT get a lifecycle rule", forbidden)
			}
		}
	}
}

// TestDoctor_S3LifecyclePolicy_Idempotent: simulate a second run after a
// successful --set-s3-lifecycle. The Get returns all four transient rules →
// OK with zero Put calls.
func TestDoctor_S3LifecyclePolicy_Idempotent(t *testing.T) {
	// After first run all four transient prefixes are set.
	transientPrefixes := []string{"logs/", "remote-create/", "agent-runs/", "slack-inbound/"}
	var rules []s3types.LifecycleRule
	for _, p := range transientPrefixes {
		rules = append(rules, makeTransientRule("km-doctor-expire-"+strings.ReplaceAll(p, "/", "-"), p, 30))
	}
	putCalls := 0
	m := &mockS3Lifecycle{
		getLifecycleFn: func(_ context.Context, _ *s3.GetBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
			return &s3.GetBucketLifecycleConfigurationOutput{Rules: rules}, nil
		},
		putLifecycleFn: func(_ context.Context, _ *s3.PutBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error) {
			putCalls++
			return &s3.PutBucketLifecycleConfigurationOutput{}, nil
		},
	}
	r := checkS3LifecyclePolicy(context.Background(), m, "my-bucket", 30, false, true)
	if r.Status != CheckOK {
		t.Fatalf("expected OK on second run (idempotent), got %s: %s", r.Status, r.Message)
	}
	if putCalls != 0 {
		t.Errorf("idempotent second run must not Put; got %d calls", putCalls)
	}
}

package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	smithy "github.com/aws/smithy-go"

	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
)

// fakeUnbootstrapSSM records GetParametersByPath / DeleteParameters calls and
// returns a configurable list of parameter names the caller should "find".
type fakeUnbootstrapSSM struct {
	stored        []string         // parameters that exist under the path
	deleted       []string         // names passed to DeleteParameters (flattened)
	deleteErr     error            // if non-nil, DeleteParameters returns this
	enumerateErr  error            // if non-nil, GetParametersByPath returns this
	enumerateOnly bool             // when true, suppress storing names already deleted
	pathQueried   string           // last Path the test code asked for (sanity check)
	invalidNames  map[string]bool  // simulate AWS reporting a name as InvalidParameter
}

func (f *fakeUnbootstrapSSM) GetParametersByPath(_ context.Context, in *ssm.GetParametersByPathInput, _ ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	if f.enumerateErr != nil {
		return nil, f.enumerateErr
	}
	f.pathQueried = aws.ToString(in.Path)
	out := &ssm.GetParametersByPathOutput{}
	for _, n := range f.stored {
		out.Parameters = append(out.Parameters, ssmtypes.Parameter{Name: aws.String(n)})
	}
	if f.enumerateOnly {
		// remove already-deleted from the report so re-enumerations are coherent
		var fresh []ssmtypes.Parameter
		for _, p := range out.Parameters {
			already := false
			for _, d := range f.deleted {
				if d == aws.ToString(p.Name) {
					already = true
					break
				}
			}
			if !already {
				fresh = append(fresh, p)
			}
		}
		out.Parameters = fresh
	}
	return out, nil
}

func (f *fakeUnbootstrapSSM) DeleteParameters(_ context.Context, in *ssm.DeleteParametersInput, _ ...func(*ssm.Options)) (*ssm.DeleteParametersOutput, error) {
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	out := &ssm.DeleteParametersOutput{}
	for _, n := range in.Names {
		if f.invalidNames[n] {
			out.InvalidParameters = append(out.InvalidParameters, n)
			continue
		}
		f.deleted = append(f.deleted, n)
		out.DeletedParameters = append(out.DeletedParameters, n)
	}
	return out, nil
}

// fakeUnbootstrapS3 simulates a versioned bucket. headExists toggles whether
// HeadBucket reports the bucket as existing.
type fakeUnbootstrapS3 struct {
	headExists      bool
	versions        []s3types.ObjectVersion
	deleteMarkers   []s3types.DeleteMarkerEntry
	deletedVersions []string // VersionIds passed to DeleteObjects
	bucketDeleted   bool
}

func (f *fakeUnbootstrapS3) HeadBucket(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	if !f.headExists {
		return nil, &s3types.NoSuchBucket{}
	}
	return &s3.HeadBucketOutput{}, nil
}

func (f *fakeUnbootstrapS3) ListObjectVersions(_ context.Context, _ *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	return &s3.ListObjectVersionsOutput{
		Versions:      f.versions,
		DeleteMarkers: f.deleteMarkers,
		IsTruncated:   aws.Bool(false),
	}, nil
}

func (f *fakeUnbootstrapS3) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	for _, oid := range in.Delete.Objects {
		f.deletedVersions = append(f.deletedVersions, aws.ToString(oid.VersionId))
	}
	// Simulate the deletion by clearing what we report.
	f.versions = nil
	f.deleteMarkers = nil
	return &s3.DeleteObjectsOutput{}, nil
}

func (f *fakeUnbootstrapS3) DeleteBucket(_ context.Context, _ *s3.DeleteBucketInput, _ ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	f.bucketDeleted = true
	return &s3.DeleteBucketOutput{}, nil
}

// fakeUnbootstrapKMS pretends one alias exists.
type fakeUnbootstrapKMS struct {
	aliasExists      bool
	keyState         kmstypes.KeyState
	scheduledKeyID   string
	scheduledWindow  int32
	aliasDeleted     bool
}

func (f *fakeUnbootstrapKMS) DescribeKey(_ context.Context, _ *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	if !f.aliasExists {
		return nil, &kmstypes.NotFoundException{}
	}
	state := f.keyState
	if state == "" {
		state = kmstypes.KeyStateEnabled
	}
	return &kms.DescribeKeyOutput{
		KeyMetadata: &kmstypes.KeyMetadata{
			KeyId:    aws.String("11111111-2222-3333-4444-555555555555"),
			KeyState: state,
		},
	}, nil
}

func (f *fakeUnbootstrapKMS) ScheduleKeyDeletion(_ context.Context, in *kms.ScheduleKeyDeletionInput, _ ...func(*kms.Options)) (*kms.ScheduleKeyDeletionOutput, error) {
	f.scheduledKeyID = aws.ToString(in.KeyId)
	f.scheduledWindow = aws.ToInt32(in.PendingWindowInDays)
	return &kms.ScheduleKeyDeletionOutput{}, nil
}

func (f *fakeUnbootstrapKMS) DeleteAlias(_ context.Context, _ *kms.DeleteAliasInput, _ ...func(*kms.Options)) (*kms.DeleteAliasOutput, error) {
	f.aliasDeleted = true
	return &kms.DeleteAliasOutput{}, nil
}

// TestUnbootstrapDeletesAllSSMParamsUnderPrefix verifies the recursive SSM
// enumeration covers everything under /{prefix}/ including slack, config,
// and sandbox sub-paths — the user's explicit ask.
func TestUnbootstrapDeletesAllSSMParamsUnderPrefix(t *testing.T) {
	cfg := &config.Config{ResourcePrefix: "kph"}
	fakeSSM := &fakeUnbootstrapSSM{
		stored: []string{
			"/kph/config/remote-create/safe-phrase",
			"/kph/config/github/app-client-id",
			"/kph/config/github/private-key",
			"/kph/config/github/installation-id",
			"/kph/slack/bot-token",
			"/kph/slack/signing-secret",
			"/kph/slack/workspace",
			"/kph/sandbox/operator/signing-key",
		},
	}
	deps := cmd.UnbootstrapDeps{SSM: fakeSSM}

	var out bytes.Buffer
	err := cmd.RunUnbootstrapWithDeps(cfg, deps, cmd.UnbootstrapOpts{Region: "us-east-1"}, &out)
	if err != nil {
		t.Fatalf("RunUnbootstrapWithDeps returned error: %v", err)
	}

	if fakeSSM.pathQueried != "/kph" {
		t.Errorf("Expected GetParametersByPath path=/kph, got %q", fakeSSM.pathQueried)
	}
	if len(fakeSSM.deleted) != len(fakeSSM.stored) {
		t.Errorf("expected %d deletions, got %d (deleted=%v)",
			len(fakeSSM.stored), len(fakeSSM.deleted), fakeSSM.deleted)
	}
	// Make sure all three categories were covered
	for _, suffix := range []string{"config/", "slack/", "sandbox/"} {
		found := false
		for _, n := range fakeSSM.deleted {
			if strings.Contains(n, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected at least one deletion containing %q; deletions=%v", suffix, fakeSSM.deleted)
		}
	}
}

// TestUnbootstrapEmptiesAndDeletesBuckets verifies the artifacts + state
// buckets are both emptied (versions removed) and then deleted.
func TestUnbootstrapEmptiesAndDeletesBuckets(t *testing.T) {
	cfg := &config.Config{
		ResourcePrefix:  "kph",
		ArtifactsBucket: "km-artifacts-kph-abc123",
	}
	fakeS3 := &fakeUnbootstrapS3{
		headExists: true,
		versions: []s3types.ObjectVersion{
			{Key: aws.String("foo"), VersionId: aws.String("v1")},
			{Key: aws.String("foo"), VersionId: aws.String("v2")},
		},
		deleteMarkers: []s3types.DeleteMarkerEntry{
			{Key: aws.String("foo"), VersionId: aws.String("dm1")},
		},
	}
	deps := cmd.UnbootstrapDeps{S3: fakeS3}

	var out bytes.Buffer
	err := cmd.RunUnbootstrapWithDeps(cfg, deps, cmd.UnbootstrapOpts{Region: "us-east-1"}, &out)
	if err != nil {
		t.Fatalf("RunUnbootstrapWithDeps returned error: %v", err)
	}
	if !fakeS3.bucketDeleted {
		t.Error("expected DeleteBucket to be called at least once")
	}
	// 3 versions/markers per call × 2 buckets (artifacts + state) = 6 deletions
	// (the fake reuses the same versions list, but bucketDeleted single bool is fine
	// for confirming the path executed twice).
	if len(fakeS3.deletedVersions) < 3 {
		t.Errorf("expected at least 3 version deletions, got %d", len(fakeS3.deletedVersions))
	}
}

// TestUnbootstrapBucketAbsentIsNotAnError verifies idempotency — if a bucket
// is already gone (NoSuchBucket on HeadBucket), unbootstrap reports it and
// continues without error. Lets you re-run unbootstrap safely.
func TestUnbootstrapBucketAbsentIsNotAnError(t *testing.T) {
	cfg := &config.Config{
		ResourcePrefix:  "kph",
		ArtifactsBucket: "km-artifacts-kph-abc123",
	}
	fakeS3 := &fakeUnbootstrapS3{headExists: false} // bucket already gone
	deps := cmd.UnbootstrapDeps{S3: fakeS3}

	var out bytes.Buffer
	err := cmd.RunUnbootstrapWithDeps(cfg, deps, cmd.UnbootstrapOpts{Region: "us-east-1"}, &out)
	if err != nil {
		t.Fatalf("expected nil error when bucket absent, got: %v", err)
	}
	if fakeS3.bucketDeleted {
		t.Error("DeleteBucket should not be called when HeadBucket reports NoSuchBucket")
	}
	if !strings.Contains(out.String(), "does not exist") {
		t.Errorf("expected 'does not exist' message in output, got: %s", out.String())
	}
}

// smithyAPIError mimics the smithy.APIError shape returned by AWS SDK v2
// when an operation fails with an HTTP-level error like 404 NotFound.
// HeadBucket returns this (NOT *s3types.NoSuchBucket) when a bucket is
// missing — confirmed against real AWS in 2026-05 against an already-deleted
// km-artifacts bucket.
//
// Must use real smithy.ErrorFault as the return type so this satisfies the
// smithy.APIError interface — otherwise errors.As(err, &smithy.APIError) in
// production code won't match this fake.
type smithyAPIError struct {
	code    string
	message string
}

func (e *smithyAPIError) Error() string             { return e.code + ": " + e.message }
func (e *smithyAPIError) ErrorCode() string         { return e.code }
func (e *smithyAPIError) ErrorMessage() string      { return e.message }
func (e *smithyAPIError) ErrorFault() smithy.ErrorFault {
	return smithy.FaultClient
}

// fakeS3WithGenericNotFound returns a smithy.APIError-shaped 404 from
// HeadBucket — the actual error type real AWS returns for a missing bucket.
type fakeS3WithGenericNotFound struct{ fakeUnbootstrapS3 }

func (f *fakeS3WithGenericNotFound) HeadBucket(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return nil, &smithyAPIError{code: "NotFound", message: "Not Found"}
}

// TestUnbootstrapBucketAbsentSmithy404 covers the production failure mode:
// HeadBucket on a missing bucket returns a generic smithy.APIError with code
// "NotFound" rather than the typed *s3types.NoSuchBucket. Without explicit
// handling for this shape, unbootstrap would surface a "teardown failed"
// warning every time an operator re-ran unbootstrap (idempotency broken).
func TestUnbootstrapBucketAbsentSmithy404(t *testing.T) {
	cfg := &config.Config{
		ResourcePrefix:  "kph",
		ArtifactsBucket: "km-artifacts-kph-abc123",
	}
	deps := cmd.UnbootstrapDeps{S3: &fakeS3WithGenericNotFound{}}

	var out bytes.Buffer
	err := cmd.RunUnbootstrapWithDeps(cfg, deps, cmd.UnbootstrapOpts{Region: "us-east-1"}, &out)
	if err != nil {
		t.Fatalf("expected nil error on smithy.APIError NotFound, got: %v", err)
	}
	if !strings.Contains(out.String(), "does not exist") {
		t.Errorf("expected 'does not exist' message, got: %s", out.String())
	}
	// And critically — no "teardown failed" warning that would scare the operator.
	if strings.Contains(out.String(), "teardown failed") {
		t.Errorf("expected idempotent skip, but output contains 'teardown failed': %s", out.String())
	}
}

// TestUnbootstrapKMSScheduleAndAlias verifies the KMS step schedules the key
// and removes the alias. Window default is the AWS minimum (7).
func TestUnbootstrapKMSScheduleAndAlias(t *testing.T) {
	cfg := &config.Config{ResourcePrefix: "kph", PrimaryRegion: "us-east-1"}
	fakeKMS := &fakeUnbootstrapKMS{aliasExists: true}
	deps := cmd.UnbootstrapDeps{KMS: fakeKMS}

	var out bytes.Buffer
	err := cmd.RunUnbootstrapWithDeps(cfg, deps, cmd.UnbootstrapOpts{Region: "us-east-1"}, &out)
	if err != nil {
		t.Fatalf("RunUnbootstrapWithDeps returned error: %v", err)
	}
	if fakeKMS.scheduledKeyID == "" {
		t.Error("expected ScheduleKeyDeletion to be called")
	}
	if fakeKMS.scheduledWindow != 7 {
		t.Errorf("expected default 7-day window, got %d", fakeKMS.scheduledWindow)
	}
	if !fakeKMS.aliasDeleted {
		t.Error("expected DeleteAlias to be called")
	}
}

// TestUnbootstrapKMSAlreadyPendingSkipsSchedule verifies that a key already
// in PendingDeletion doesn't trigger a second ScheduleKeyDeletion call (which
// would error out from AWS). The alias is still deleted.
func TestUnbootstrapKMSAlreadyPendingSkipsSchedule(t *testing.T) {
	cfg := &config.Config{ResourcePrefix: "kph", PrimaryRegion: "us-east-1"}
	fakeKMS := &fakeUnbootstrapKMS{
		aliasExists: true,
		keyState:    kmstypes.KeyStatePendingDeletion,
	}
	deps := cmd.UnbootstrapDeps{KMS: fakeKMS}

	var out bytes.Buffer
	err := cmd.RunUnbootstrapWithDeps(cfg, deps, cmd.UnbootstrapOpts{Region: "us-east-1"}, &out)
	if err != nil {
		t.Fatalf("RunUnbootstrapWithDeps returned error: %v", err)
	}
	if fakeKMS.scheduledKeyID != "" {
		t.Errorf("ScheduleKeyDeletion should not be called when already pending, got KeyId=%q",
			fakeKMS.scheduledKeyID)
	}
	if !fakeKMS.aliasDeleted {
		t.Error("alias should still be deleted even when key is already pending")
	}
}

// TestUnbootstrapKMSWindowClamped verifies that out-of-range windows are
// clamped to the AWS-allowed [7, 30] interval.
func TestUnbootstrapKMSWindowClamped(t *testing.T) {
	cfg := &config.Config{ResourcePrefix: "kph", PrimaryRegion: "us-east-1"}

	t.Run("below_min", func(t *testing.T) {
		fakeKMS := &fakeUnbootstrapKMS{aliasExists: true}
		var out bytes.Buffer
		_ = cmd.RunUnbootstrapWithDeps(cfg, cmd.UnbootstrapDeps{KMS: fakeKMS},
			cmd.UnbootstrapOpts{Region: "us-east-1", KMSDeletionWindow: 3}, &out)
		if fakeKMS.scheduledWindow != 7 {
			t.Errorf("expected 3 to be clamped to 7, got %d", fakeKMS.scheduledWindow)
		}
	})
	t.Run("above_max", func(t *testing.T) {
		fakeKMS := &fakeUnbootstrapKMS{aliasExists: true}
		var out bytes.Buffer
		_ = cmd.RunUnbootstrapWithDeps(cfg, cmd.UnbootstrapDeps{KMS: fakeKMS},
			cmd.UnbootstrapOpts{Region: "us-east-1", KMSDeletionWindow: 99}, &out)
		if fakeKMS.scheduledWindow != 30 {
			t.Errorf("expected 99 to be clamped to 30, got %d", fakeKMS.scheduledWindow)
		}
	})
}

// TestUnbootstrapZoneSkippedByDefault verifies the Route53 step is a no-op
// when --include-zone is false (the default), even if a Route53 client is wired.
func TestUnbootstrapZoneSkippedByDefault(t *testing.T) {
	cfg := &config.Config{ResourcePrefix: "kph", Domain: "example.com"}
	deps := cmd.UnbootstrapDeps{Route53: &route53.Client{}}

	var out bytes.Buffer
	err := cmd.RunUnbootstrapWithDeps(cfg, deps, cmd.UnbootstrapOpts{Region: "us-east-1", IncludeZone: false}, &out)
	if err != nil {
		t.Fatalf("RunUnbootstrapWithDeps returned error: %v", err)
	}
	if !strings.Contains(out.String(), "preserved (re-run with --include-zone") {
		t.Errorf("expected preserved-zone message, got: %s", out.String())
	}
}

// TestUnbootstrapPerStepFailureContinues verifies that a failure in one step
// (e.g. SSM enumeration error) doesn't abort the rest of unbootstrap. The
// summary still prints; subsequent steps still attempt.
func TestUnbootstrapPerStepFailureContinues(t *testing.T) {
	cfg := &config.Config{
		ResourcePrefix:  "kph",
		ArtifactsBucket: "km-artifacts-kph-abc123",
	}
	deps := cmd.UnbootstrapDeps{
		SSM: &fakeUnbootstrapSSM{enumerateErr: errors.New("simulated AccessDenied")},
		S3:  &fakeUnbootstrapS3{headExists: true},
		KMS: &fakeUnbootstrapKMS{aliasExists: true},
	}

	var out bytes.Buffer
	err := cmd.RunUnbootstrapWithDeps(cfg, deps, cmd.UnbootstrapOpts{Region: "us-east-1"}, &out)
	if err != nil {
		t.Fatalf("expected unbootstrap to continue past per-step error, got: %v", err)
	}
	// S3 + KMS steps must still have run.
	if !deps.S3.(*fakeUnbootstrapS3).bucketDeleted {
		t.Error("S3 step should still execute when SSM step failed")
	}
	if deps.KMS.(*fakeUnbootstrapKMS).scheduledKeyID == "" {
		t.Error("KMS step should still execute when SSM step failed")
	}
}

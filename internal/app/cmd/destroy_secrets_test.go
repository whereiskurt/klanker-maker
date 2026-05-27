package cmd

// Phase 89 Plan 05 — destroy_secrets_test.go
//
// Task 4 (TDD): Tests for deleteSopsBundleNonFatal helper + S3Deleter interface.
//
// RED → GREEN cycle: run `go test ./internal/app/cmd/ -run TestDestroyDeletesSopsBundle`

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeS3Deleter captures DeleteObject calls for assertion.
type fakeS3Deleter struct {
	callCount  int
	lastInput  *s3.DeleteObjectInput
	returnErr  error
}

func (f *fakeS3Deleter) DeleteObject(_ context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.callCount++
	f.lastInput = params
	return &s3.DeleteObjectOutput{}, f.returnErr
}

// TestDestroyDeletesSopsBundle_HappyPath verifies that deleteSopsBundleNonFatal
// calls DeleteObject exactly once with the correct bucket and key, and returns
// (no error to return — function is void).
func TestDestroyDeletesSopsBundle_HappyPath(t *testing.T) {
	const artifactBucket = "acme"
	const sandboxID = "sb-abc123"
	wantKey := "sandboxes/sb-abc123/secrets.enc.yaml"

	fake := &fakeS3Deleter{}
	deleteSopsBundleNonFatal(context.Background(), fake, artifactBucket, sandboxID)

	if fake.callCount != 1 {
		t.Errorf("expected exactly 1 DeleteObject call; got %d", fake.callCount)
	}
	if fake.lastInput == nil {
		t.Fatal("lastInput is nil")
	}
	if aws.ToString(fake.lastInput.Bucket) != artifactBucket {
		t.Errorf("bucket = %q; want %q", aws.ToString(fake.lastInput.Bucket), artifactBucket)
	}
	if aws.ToString(fake.lastInput.Key) != wantKey {
		t.Errorf("key = %q; want %q", aws.ToString(fake.lastInput.Key), wantKey)
	}
}

// TestDestroyDeletesSopsBundle_NonFatalOnError verifies that when DeleteObject
// returns an error, deleteSopsBundleNonFatal does not panic and does not propagate
// the error (it's intentionally non-fatal — S3 lifecycle will GC after 7 days).
func TestDestroyDeletesSopsBundle_NonFatalOnError(t *testing.T) {
	const artifactBucket = "acme"
	const sandboxID = "sb-error1"

	fake := &fakeS3Deleter{
		returnErr: errors.New("NoSuchKey: The specified key does not exist"),
	}

	// Must not panic. Function is void — no return value to check.
	// This is the key assertion: deleteSopsBundleNonFatal silently absorbs the error.
	deleteSopsBundleNonFatal(context.Background(), fake, artifactBucket, sandboxID)

	if fake.callCount != 1 {
		t.Errorf("expected 1 DeleteObject attempt even on error; got %d", fake.callCount)
	}
}

package cmd

// Phase 89 Plan 05 — create_secrets_test.go
//
// Task 3 (TDD): Tests for uploadSopsBundleIfPresent helper + S3Putter interface.
//
// RED → GREEN cycle: run `go test ./internal/app/cmd/ -run TestCreatePutSopsBundle`

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// fakeS3Putter captures PutObject calls for assertion.
type fakeS3Putter struct {
	mu        sync.Mutex
	callCount int
	lastInput *s3.PutObjectInput
	lastBody  []byte
	returnErr error
}

func (f *fakeS3Putter) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	f.lastInput = params
	if params.Body != nil {
		b, _ := io.ReadAll(params.Body)
		f.lastBody = b
	}
	return &s3.PutObjectOutput{}, f.returnErr
}

// fixtureDir returns the path to pkg/profile/testdata/ for use in tests.
func fixtureDir(t *testing.T) string {
	t.Helper()
	// Navigate from internal/app/cmd up to repo root, then to pkg/profile/testdata
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Go up until we find the repo root (contains CLAUDE.md)
	for {
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
	return filepath.Join(dir, "pkg", "profile", "testdata")
}

// TestCreatePutSopsBundle_HappyPath verifies that when a profile has Spec.Secrets.SopsFile
// set, uploadSopsBundleIfPresent calls PutObject once with the correct bucket, key,
// content-type, and body bytes matching the fixture file.
func TestCreatePutSopsBundle_HappyPath(t *testing.T) {
	// Copy the fixture file to a temp dir to simulate a profile in a directory.
	tmpDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir(t), "secrets-fixture.enc.yaml")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// Write fixture to temp dir.
	bundleRelPath := "secrets/test.enc.yaml"
	bundleAbsPath := filepath.Join(tmpDir, "secrets", "test.enc.yaml")
	if err := os.MkdirAll(filepath.Dir(bundleAbsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(bundleAbsPath, fixtureBytes, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	// Profile YAML is "in" tmpDir.
	profilePath := filepath.Join(tmpDir, "profile.yaml")
	p := &profile.SandboxProfile{
		Spec: profile.Spec{
			Secrets: &profile.SecretsSpec{
				SopsFile: bundleRelPath,
			},
		},
	}

	fake := &fakeS3Putter{}
	const artifactBucket = "acme-artifacts"
	const sandboxID = "sb-abc123"

	err = uploadSopsBundleIfPresent(context.Background(), fake, artifactBucket, sandboxID, profilePath, p)
	if err != nil {
		t.Fatalf("uploadSopsBundleIfPresent returned error: %v", err)
	}

	if fake.callCount != 1 {
		t.Errorf("expected exactly 1 PutObject call; got %d", fake.callCount)
	}
	if fake.lastInput == nil {
		t.Fatal("lastInput is nil")
	}
	if aws.ToString(fake.lastInput.Bucket) != artifactBucket {
		t.Errorf("bucket = %q; want %q", aws.ToString(fake.lastInput.Bucket), artifactBucket)
	}
	wantKey := "sandboxes/sb-abc123/secrets.enc.yaml"
	if aws.ToString(fake.lastInput.Key) != wantKey {
		t.Errorf("key = %q; want %q", aws.ToString(fake.lastInput.Key), wantKey)
	}
	if aws.ToString(fake.lastInput.ContentType) != "application/x-yaml" {
		t.Errorf("content-type = %q; want application/x-yaml", aws.ToString(fake.lastInput.ContentType))
	}
	if !bytes.Equal(fake.lastBody, fixtureBytes) {
		t.Errorf("body bytes do not match fixture file")
	}
}

// TestCreatePutSopsBundle_NoOpWhenAbsent verifies that when Spec.Secrets is nil,
// uploadSopsBundleIfPresent makes zero S3 calls and returns nil.
func TestCreatePutSopsBundle_NoOpWhenAbsent(t *testing.T) {
	// Fake that panics on any call.
	type panicPutter struct{}
	pp := &struct {
		callCount int
		fakeS3Putter
	}{}
	// Use a simple profile with no secrets.
	p := &profile.SandboxProfile{
		Spec: profile.Spec{
			Secrets: nil,
		},
	}

	fake := &fakeS3Putter{}
	err := uploadSopsBundleIfPresent(context.Background(), fake, "bucket", "sb-nosecrets", "/tmp/fake.yaml", p)
	if err != nil {
		t.Errorf("expected nil error for no-op case; got %v", err)
	}
	if fake.callCount != 0 {
		t.Errorf("expected zero PutObject calls when Spec.Secrets=nil; got %d", fake.callCount)
	}
	// Suppress unused variable warning.
	_ = pp
}

// TestCreatePutSopsBundle_MissingFileReturnsError verifies that when the SopsFile
// path does not exist, uploadSopsBundleIfPresent returns an error before calling PutObject.
func TestCreatePutSopsBundle_MissingFileReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "profile.yaml")
	p := &profile.SandboxProfile{
		Spec: profile.Spec{
			Secrets: &profile.SecretsSpec{
				SopsFile: "missing/does-not-exist.enc.yaml",
			},
		},
	}

	fake := &fakeS3Putter{}
	err := uploadSopsBundleIfPresent(context.Background(), fake, "bucket", "sb-missing", profilePath, p)
	if err == nil {
		t.Error("expected error when SopsFile path does not exist; got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist.enc.yaml") && !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected error to mention the missing file path; got %q", err.Error())
	}
	if fake.callCount != 0 {
		t.Errorf("expected zero PutObject calls when file is missing (validate should fire first); got %d", fake.callCount)
	}
}

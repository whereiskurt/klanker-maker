package aws_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// mockS3PutAPI records PutObject calls for assertion.
type mockS3PutAPI struct {
	calls []*s3.PutObjectInput
	// failOnKey causes PutObject to return an error when the key contains this string.
	failOnKey string
}

func (m *mockS3PutAPI) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.failOnKey != "" && input.Key != nil {
		if filepath.Base(*input.Key) == m.failOnKey {
			return nil, os.ErrPermission
		}
	}
	m.calls = append(m.calls, input)
	return &s3.PutObjectOutput{}, nil
}

// createTempFile creates a temp file inside dir with the given filename and content.
func createTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to create temp file %s: %v", path, err)
	}
	return path
}

// TestUploadArtifacts_GlobPattern verifies glob expansion calls PutObject for each match.
func TestUploadArtifacts_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	createTempFile(t, dir, "a.txt", "hello")
	createTempFile(t, dir, "b.txt", "world")
	createTempFile(t, dir, "c.log", "skip me") // not matched by *.txt

	mock := &mockS3PutAPI{}
	ctx := context.Background()

	pattern := filepath.Join(dir, "*.txt")
	uploaded, skipped, err := kmaws.UploadArtifacts(ctx, mock, "my-bucket", "sb-abc123", []string{pattern}, 0)
	if err != nil {
		t.Fatalf("UploadArtifacts returned error: %v", err)
	}

	if uploaded != 2 {
		t.Errorf("expected 2 uploaded, got %d", uploaded)
	}
	if len(skipped) != 0 {
		t.Errorf("expected 0 skipped, got %d", len(skipped))
	}
	if len(mock.calls) != 2 {
		t.Errorf("expected 2 PutObject calls, got %d", len(mock.calls))
	}
}

// TestUploadArtifacts_DirectoryPath verifies directory walk collects all files recursively.
func TestUploadArtifacts_DirectoryPath(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	createTempFile(t, dir, "root.txt", "root")
	createTempFile(t, subdir, "nested.txt", "nested")

	mock := &mockS3PutAPI{}
	ctx := context.Background()

	uploaded, skipped, err := kmaws.UploadArtifacts(ctx, mock, "my-bucket", "sb-abc123", []string{dir}, 0)
	if err != nil {
		t.Fatalf("UploadArtifacts returned error: %v", err)
	}

	if uploaded != 2 {
		t.Errorf("expected 2 uploaded (root + nested), got %d", uploaded)
	}
	if len(skipped) != 0 {
		t.Errorf("expected 0 skipped, got %d", len(skipped))
	}
}

// TestUploadArtifacts_SkipsOversizedFile verifies oversized files are skipped with an event.
func TestUploadArtifacts_SkipsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	// Create a 2MB file by writing 2*1024*1024 bytes.
	large := make([]byte, 2*1024*1024)
	largePath := filepath.Join(dir, "large.bin")
	if err := os.WriteFile(largePath, large, 0o644); err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}
	createTempFile(t, dir, "small.txt", "tiny")

	mock := &mockS3PutAPI{}
	ctx := context.Background()

	// maxSizeMB=1 — large.bin (2MB) should be skipped, small.txt (tiny) should upload.
	uploaded, skipped, err := kmaws.UploadArtifacts(ctx, mock, "my-bucket", "sb-abc123", []string{dir}, 1)
	if err != nil {
		t.Fatalf("UploadArtifacts returned error: %v", err)
	}

	if uploaded != 1 {
		t.Errorf("expected 1 uploaded (small.txt), got %d", uploaded)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped event, got %d", len(skipped))
	}
	if skipped[0].Path != largePath {
		t.Errorf("skipped path = %q, want %q", skipped[0].Path, largePath)
	}
	if skipped[0].Reason == "" {
		t.Error("expected non-empty reason in skipped event")
	}
	if skipped[0].SizeMB <= 0 {
		t.Errorf("expected positive SizeMB in skipped event, got %f", skipped[0].SizeMB)
	}
}

// TestUploadArtifacts_MaxSizeMBZeroMeansUnlimited verifies maxSizeMB=0 uploads all files.
func TestUploadArtifacts_MaxSizeMBZeroMeansUnlimited(t *testing.T) {
	dir := t.TempDir()
	// Create a 5MB file
	large := make([]byte, 5*1024*1024)
	largePath := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(largePath, large, 0o644); err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}

	mock := &mockS3PutAPI{}
	ctx := context.Background()

	// maxSizeMB=0 means unlimited — big.bin should be uploaded.
	uploaded, skipped, err := kmaws.UploadArtifacts(ctx, mock, "my-bucket", "sb-abc123", []string{largePath}, 0)
	if err != nil {
		t.Fatalf("UploadArtifacts returned error: %v", err)
	}

	if uploaded != 1 {
		t.Errorf("expected 1 uploaded, got %d", uploaded)
	}
	if len(skipped) != 0 {
		t.Errorf("expected 0 skipped when maxSizeMB=0, got %d", len(skipped))
	}
}

// TestUploadArtifacts_S3KeyFormat verifies keys are "artifacts/{sandboxID}/{filename}".
func TestUploadArtifacts_S3KeyFormat(t *testing.T) {
	dir := t.TempDir()
	createTempFile(t, dir, "report.json", `{"ok":true}`)

	mock := &mockS3PutAPI{}
	ctx := context.Background()

	pattern := filepath.Join(dir, "*.json")
	_, _, err := kmaws.UploadArtifacts(ctx, mock, "my-bucket", "sb-xyz789", []string{pattern}, 0)
	if err != nil {
		t.Fatalf("UploadArtifacts returned error: %v", err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 PutObject call, got %d", len(mock.calls))
	}

	key := *mock.calls[0].Key
	// Key must start with "artifacts/sb-xyz789/"
	expected := "artifacts/sb-xyz789/"
	if len(key) < len(expected) || key[:len(expected)] != expected {
		t.Errorf("S3 key %q does not start with %q", key, expected)
	}
	// Must end with the filename
	if filepath.Base(key) != "report.json" {
		t.Errorf("S3 key %q does not end with report.json", key)
	}
}

// TestUploadArtifacts_NoMatchReturnsEmpty verifies empty result when no files match.
func TestUploadArtifacts_NoMatchReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	// No files in dir — glob matches nothing.
	pattern := filepath.Join(dir, "*.nonexistent")

	mock := &mockS3PutAPI{}
	ctx := context.Background()

	uploaded, skipped, err := kmaws.UploadArtifacts(ctx, mock, "my-bucket", "sb-abc123", []string{pattern}, 0)
	if err != nil {
		t.Errorf("expected nil error for no matches, got: %v", err)
	}
	if uploaded != 0 {
		t.Errorf("expected 0 uploaded, got %d", uploaded)
	}
	if len(skipped) != 0 {
		t.Errorf("expected 0 skipped, got %d", len(skipped))
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 PutObject calls, got %d", len(mock.calls))
	}
}

// TestUploadArtifacts_BestEffortOnPutObjectFailure verifies upload continues after one failure.
func TestUploadArtifacts_BestEffortOnPutObjectFailure(t *testing.T) {
	dir := t.TempDir()
	createTempFile(t, dir, "ok1.txt", "first")
	createTempFile(t, dir, "fail.txt", "will fail")
	createTempFile(t, dir, "ok2.txt", "third")

	// "fail.txt" causes PutObject to return an error.
	mock := &mockS3PutAPI{failOnKey: "fail.txt"}
	ctx := context.Background()

	pattern := filepath.Join(dir, "*.txt")
	uploaded, skipped, err := kmaws.UploadArtifacts(ctx, mock, "my-bucket", "sb-abc123", []string{pattern}, 0)
	if err != nil {
		t.Errorf("expected nil error for best-effort upload, got: %v", err)
	}

	// 2 succeeded (ok1.txt and ok2.txt), 1 failed but not returned in skipped (different category)
	if uploaded != 2 {
		t.Errorf("expected 2 uploaded (best-effort), got %d", uploaded)
	}
	// skipped is for size-limit violations, not PutObject errors
	if len(skipped) != 0 {
		t.Errorf("expected 0 in skipped list (PutObject errors are not skip events), got %d", len(skipped))
	}
}

package aws

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog/log"
)

// S3PutAPI is a narrow interface for S3 artifact upload operations.
// Only PutObject is needed — use the real *s3.Client or mockS3PutAPI in tests.
type S3PutAPI interface {
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// ArtifactSkippedEvent records a file that was not uploaded due to exceeding maxSizeMB.
type ArtifactSkippedEvent struct {
	// Path is the absolute path of the skipped file.
	Path string
	// SizeMB is the file size in megabytes.
	SizeMB float64
	// Reason describes why the file was skipped.
	Reason string
}

// UploadArtifacts collects files matching paths (globs, directories, or single files),
// uploads each to s3://bucket/artifacts/{sandboxID}/{relative-path}, and returns
// the count of uploaded files and a list of size-limit skip events.
//
// paths can contain:
//   - Glob patterns (containing * or ?) — expanded via filepath.Glob
//   - Directory paths — walked recursively via filepath.Walk
//   - Single file paths — uploaded directly
//
// If maxSizeMB > 0, files exceeding that size are skipped and reported in the returned
// skipped list. Set maxSizeMB = 0 for unlimited.
//
// PutObject failures are logged as warnings but do not stop the upload loop (best-effort).
// The function returns nil error unless the context is cancelled.
func UploadArtifacts(ctx context.Context, client S3PutAPI, bucket, sandboxID string, paths []string, maxSizeMB int) (uploaded int, skipped []ArtifactSkippedEvent, err error) {
	// Collect all file paths from the provided patterns.
	var files []string
	for _, p := range paths {
		expanded, expandErr := expandPath(p)
		if expandErr != nil {
			log.Warn().Str("path", p).Err(expandErr).Msg("artifacts: failed to expand path, skipping")
			continue
		}
		files = append(files, expanded...)
	}

	// Deduplicate collected paths.
	files = deduplicate(files)

	for _, filePath := range files {
		// Check context cancellation.
		if ctx.Err() != nil {
			return uploaded, skipped, ctx.Err()
		}

		info, statErr := os.Stat(filePath)
		if statErr != nil {
			log.Warn().Str("file", filePath).Err(statErr).Msg("artifacts: failed to stat file, skipping")
			continue
		}

		// Enforce size limit.
		if maxSizeMB > 0 {
			sizeMB := float64(info.Size()) / (1024 * 1024)
			if sizeMB > float64(maxSizeMB) {
				skipped = append(skipped, ArtifactSkippedEvent{
					Path:   filePath,
					SizeMB: sizeMB,
					Reason: fmt.Sprintf("file size %.2f MB exceeds maxSizeMB %d", sizeMB, maxSizeMB),
				})
				continue
			}
		}

		// Compute S3 key: artifacts/{sandboxID}/{filename} for glob/single-file expansions.
		// For directory walks the relative path from the walked root is used.
		key := artifactKey(sandboxID, filePath)

		// Open and upload.
		f, openErr := os.Open(filePath) //nolint:gosec
		if openErr != nil {
			log.Warn().Str("file", filePath).Err(openErr).Msg("artifacts: failed to open file, skipping")
			continue
		}

		_, putErr := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: awssdk.String(bucket),
			Key:    awssdk.String(key),
			Body:   f,
		})
		f.Close()

		if putErr != nil {
			log.Warn().Str("file", filePath).Str("key", key).Err(putErr).Msg("artifacts: PutObject failed, continuing")
			continue
		}

		uploaded++
	}

	return uploaded, skipped, nil
}

// expandPath expands a single path entry into a list of absolute file paths.
// Glob patterns (containing * or ?) are expanded via filepath.Glob.
// Directory paths are walked recursively.
// Single file paths are returned as-is (if they exist).
func expandPath(p string) ([]string, error) {
	// Detect glob patterns.
	if strings.ContainsAny(p, "*?") {
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, fmt.Errorf("glob expand %q: %w", p, err)
		}
		// Filter out directories from glob results.
		var files []string
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				continue
			}
			if !info.IsDir() {
				files = append(files, m)
			}
		}
		return files, nil
	}

	// Check if path exists.
	info, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			// No match — return empty slice (not an error; consistent with glob behavior).
			return nil, nil
		}
		return nil, fmt.Errorf("stat %q: %w", p, err)
	}

	// Directory: walk recursively.
	if info.IsDir() {
		var files []string
		walkErr := filepath.Walk(p, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !fi.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("walk %q: %w", p, walkErr)
		}
		return files, nil
	}

	// Single file.
	return []string{p}, nil
}

// deduplicate removes duplicate file paths while preserving order.
func deduplicate(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// artifactKey returns the S3 object key for a given artifact file.
// Format: artifacts/{sandboxID}/{filename}
func artifactKey(sandboxID, filePath string) string {
	return fmt.Sprintf("artifacts/%s/%s", sandboxID, filepath.Base(filePath))
}

package check

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// largezipThreshold is the maximum zip size (50 MB) for direct Lambda upload.
// Above this threshold the zip is uploaded to S3 first.
const largezipThreshold = 50 * 1024 * 1024

// S3PutObjectAPI is a narrow S3 interface for large-zip uploads.
type S3PutObjectAPI interface {
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// BuildZip packages a check into a zip archive.
//
//   - snippetPath: the author's Python snippet (stored as "snippet.py" in the zip).
//   - requirementsPath: optional path to requirements.txt; may be "" to skip pip.
//   - bootstrapBytes: the bootstrap handler bytes (stored as "_km_check_bootstrap.py").
//
// When requirementsPath is non-empty, pip installs arch-correct wheels for arm64
// Lambda runtime into the zip directory before archiving.
//
// Returns the zip bytes (in memory). The caller decides whether to upload directly
// (<=50 MB) or via S3 (>50 MB).
func BuildZip(snippetPath, requirementsPath string, bootstrapBytes []byte) ([]byte, error) {
	// Create a temp build directory.
	buildDir, err := os.MkdirTemp("", "km-check-build-*")
	if err != nil {
		return nil, fmt.Errorf("BuildZip: create build dir: %w", err)
	}
	defer os.RemoveAll(buildDir)

	// Write bootstrap handler.
	if err := os.WriteFile(filepath.Join(buildDir, "_km_check_bootstrap.py"), bootstrapBytes, 0o644); err != nil {
		return nil, fmt.Errorf("BuildZip: write bootstrap: %w", err)
	}

	// Copy snippet.
	snippetData, err := os.ReadFile(snippetPath)
	if err != nil {
		return nil, fmt.Errorf("BuildZip: read snippet %q: %w", snippetPath, err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "snippet.py"), snippetData, 0o644); err != nil {
		return nil, fmt.Errorf("BuildZip: write snippet: %w", err)
	}

	// Pip install arch-correct wheels (arm64) when requirements.txt is present.
	if requirementsPath != "" {
		if err := pipInstallArm64(requirementsPath, buildDir); err != nil {
			return nil, fmt.Errorf("BuildZip: pip install: %w", err)
		}
	}

	// Build the zip archive in memory.
	return zipDirectory(buildDir)
}

// pipInstallArm64 runs pip install with arch-correct arm64 wheels for Lambda.
func pipInstallArm64(requirementsPath, targetDir string) error {
	pip := resolvePip()
	if pip == "" {
		return fmt.Errorf("pip not found in PATH; install pip3 or pip before deploying checks with dependencies")
	}
	// Use --platform manylinux2014_aarch64 --only-binary :all: to get arm64 wheels.
	args := []string{
		"install",
		"--platform", "manylinux2014_aarch64",
		"--only-binary", ":all:",
		"--target", targetDir,
		"-r", requirementsPath,
	}
	cmd := exec.Command(pip, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install failed: %w", err)
	}
	return nil
}

// resolvePip returns the name of an available pip binary (pip3 preferred, then pip).
func resolvePip() string {
	for _, name := range []string{"pip3", "pip"} {
		if p, err := exec.LookPath(name); err == nil && p != "" {
			return name
		}
	}
	return ""
}

// zipDirectory creates a zip archive from all files in dir, preserving relative paths.
func zipDirectory(dir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// Use forward slashes in zip (Lambda runs on Linux).
		relPath = strings.ReplaceAll(relPath, string(filepath.Separator), "/")
		f, err := zw.Create(relPath)
		if err != nil {
			return err
		}
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fh.Close()
		_, err = io.Copy(f, fh)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("zipDirectory walk: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("zipDirectory close: %w", err)
	}
	return buf.Bytes(), nil
}

// MaybeUploadLargeZip decides whether to upload the zip to S3 (>50 MB) or
// return the bytes for direct Lambda upload (<=50 MB).
//
// Returns (zipBytes, s3Bucket, s3Key). When returning via S3, zipBytes is nil;
// when returning directly, s3Bucket and s3Key are empty strings.
func MaybeUploadLargeZip(ctx context.Context, s3Client S3PutObjectAPI, zipBytes []byte, bucket, checkName string) (direct []byte, s3Bucket, s3Key string, err error) {
	if len(zipBytes) <= largezipThreshold {
		// Small enough for direct Lambda upload.
		return zipBytes, "", "", nil
	}
	// Large zip: upload to S3.
	key := fmt.Sprintf("checks/%s/package.zip", checkName)
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(zipBytes),
		ContentType: aws.String("application/zip"),
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("MaybeUploadLargeZip: PutObject: %w", err)
	}
	return nil, bucket, key, nil
}

// Package cmd — herdr_mirror_test.go
// Pins the binaries/herdr S3 key contract across every site that uses it.
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHerdrS3Key_PinnedAcrossAllSites is the mechanical pairing guard. The S3
// key is written by init.go, read by the profile fragment, and read again by
// the ensure-install path in herdr.go. A rename in one place and not the others
// produces a sandbox that boots without herdr and an install that 404s, both
// silently. Compare against a single literal here rather than trusting three
// separate string constants to stay in sync.
func TestHerdrS3Key_PinnedAcrossAllSites(t *testing.T) {
	const key = "binaries/herdr"

	sites := []string{
		"init.go",                          // fetchAndUploadHerdr upload target
		"herdr.go",                         // ensure-install download source
		"../../../profiles/base/tools/herdr.yaml", // boot-time fetch
	}
	for _, rel := range sites {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(raw), key) {
			t.Errorf("%s does not contain the S3 key %q", rel, key)
		}
	}
}

// TestHerdrVersion_Pinned asserts the version constant is a concrete pin, not
// "latest". An unpinned third-party binary means a bad upstream release reaches
// every new sandbox at once — the same reasoning as base/security/wiz.yaml's
// WIZ_SENSOR_VERSION comment.
func TestHerdrVersion_Pinned(t *testing.T) {
	if herdrVersion == "" || herdrVersion == "latest" {
		t.Fatalf("herdrVersion = %q; want a concrete version pin", herdrVersion)
	}
}

// TestFetchAndUploadHerdr_SkipsDownloadWhenCached asserts the cached-binary
// branch is taken when build/herdr already exists, so a repeated km init does
// not re-download. The upload still runs and will fail without AWS creds, so
// this asserts only that the error is NOT a download error.
func TestFetchAndUploadHerdr_SkipsDownloadWhenCached(t *testing.T) {
	dir := t.TempDir()
	cached := filepath.Join(dir, "herdr")
	if err := os.WriteFile(cached, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed cached binary: %v", err)
	}
	err := fetchAndUploadHerdr(dir, "km-artifacts-test-does-not-exist")
	if err != nil && strings.Contains(err.Error(), "download herdr") {
		t.Fatalf("download was attempted despite cached binary: %v", err)
	}
	// The cached file must survive a failed upload — deleting a cached artifact
	// on the failure path is the buildLambdaZips defect (see CLAUDE.md Phase 126
	// operator-image findings) pointed at a different file.
	if _, statErr := os.Stat(cached); statErr != nil {
		t.Fatalf("cached binary was removed on the upload-failure path: %v", statErr)
	}
}

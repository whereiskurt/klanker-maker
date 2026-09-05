// Package cmd — herdr_mirror_test.go
// Pins the binaries/herdr S3 key contract across every site that uses it.
package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
		"init.go",  // fetchAndUploadHerdr upload target
		"herdr.go", // ensure-install download source
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

// herdrFragmentFetchCommand parses profiles/base/tools/herdr.yaml and returns
// the spec.execution.initCommandsAppend entry that contains "binaries/herdr" —
// the actual fetch command, not just any string in the file (a fragment could
// satisfy a plain substring check via a comment alone).
func herdrFragmentFetchCommand(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../../profiles/base/tools/herdr.yaml")
	if err != nil {
		t.Fatalf("read fragment: %v", err)
	}

	var doc struct {
		Spec struct {
			Execution struct {
				InitCommandsAppend []string `yaml:"initCommandsAppend"`
			} `yaml:"execution"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse fragment YAML: %v", err)
	}

	for _, cmd := range doc.Spec.Execution.InitCommandsAppend {
		if strings.Contains(cmd, "binaries/herdr") {
			return cmd
		}
	}
	t.Fatal("no spec.execution.initCommandsAppend entry contains \"binaries/herdr\"")
	return ""
}

// TestHerdrFragment_ResolvesBucketBeforeUse pins the fragment against two bugs
// that shipped once and were both proven live:
//
//  1. cloud-init runs km-init.sh as a plain child process and never sources
//     /etc/profile.d, so a bare ${KM_ARTIFACTS_BUCKET} expands to empty and the
//     fetch becomes `s3:///binaries/herdr`. The failure is silent — the
//     fragment's own `|| echo` swallows it into the boot log.
//  2. km-init.sh runs under `set -e`, so an UNGUARDED source of a missing
//     /etc/profile.d/km-identity.sh (e.g. `. /etc/profile.d/km-identity.sh` as
//     the last element of a `||` list) aborts km-init.sh right there — before
//     the fetch's own soft fallback ever runs — killing every LATER
//     initCommand from other fragments. Wrapping it in `[ -r ... ] && .` does
//     not; verified live by comparing the two forms' exit behaviour.
//
// This parses the fragment's YAML and asserts against the specific
// spec.execution.initCommandsAppend entry that fetches the binary (rather
// than a substring check anywhere in the file) so a stray mention in a
// comment elsewhere cannot satisfy it.
func TestHerdrFragment_ResolvesBucketBeforeUse(t *testing.T) {
	cmd := herdrFragmentFetchCommand(t)

	if !strings.Contains(cmd, "/etc/profile.d/km-identity.sh") {
		t.Error("fetch command does not source km-identity.sh before using KM_ARTIFACTS_BUCKET")
	}
	if !strings.Contains(cmd, "[ -r ") {
		t.Error("fetch command sources km-identity.sh without an [ -r ... ] readability guard")
	}
	if strings.Contains(cmd, "exported at the top of the bootstrap and inherited") {
		t.Error("fetch command still carries the disproven inheritance claim")
	}
}

// TestFetchAndUploadHerdr_DigestVerification covers the digest-check paths
// that TestFetchAndUploadHerdr (herdr_mirror_external_test.go, package
// cmd_test) cannot reach: it needs to override the unexported herdrSHA256 var
// so an arbitrary small fixture — not the real ~22MB binary — can pass or
// fail verification deliberately.
func TestFetchAndUploadHerdr_DigestVerification(t *testing.T) {
	const fixtureContent = "fakeherdrbinary"

	// matchingDigest is the real SHA-256 of fixtureContent, computed once so
	// the "cache hit" / "fresh download" happy paths can be exercised without
	// embedding the real herdr binary as a test fixture.
	sum := sha256.Sum256([]byte(fixtureContent))
	matchingDigest := hex.EncodeToString(sum[:])

	withOverriddenDigest := func(t *testing.T, digest string) {
		t.Helper()
		orig := herdrSHA256
		herdrSHA256 = digest
		t.Cleanup(func() { herdrSHA256 = orig })
	}

	t.Run("MismatchFailsAndRemovesCachedFile", func(t *testing.T) {
		buildDir := t.TempDir()
		shimDir := t.TempDir()
		binaryPath := filepath.Join(buildDir, "herdr")

		if err := os.WriteFile(binaryPath, []byte(fixtureContent), 0o755); err != nil {
			t.Fatalf("pre-create herdr: %v", err)
		}
		// Leave herdrSHA256 at its real (production) value — fixtureContent
		// deliberately does not hash to it, so this exercises the mismatch
		// path with no override at all.

		// aws shim: must NOT be invoked — a digest mismatch must fail before
		// any upload is attempted.
		logFile := filepath.Join(t.TempDir(), "aws-calls.log")
		awsShim := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}
		// curl shim: must NOT be invoked — build/herdr already exists.
		curlShim := "#!/bin/sh\necho 'curl called unexpectedly' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "curl"), []byte(curlShim), 0o755); err != nil {
			t.Fatalf("write curl shim: %v", err)
		}
		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		err := fetchAndUploadHerdr(buildDir, "fake-bucket")
		if err == nil {
			t.Fatal("expected a digest mismatch error, got nil")
		}
		for _, want := range []string{"checksum", "mismatch", herdrSHA256} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err.Error(), want)
			}
		}

		// aws must never have been invoked.
		if _, statErr := os.Stat(logFile); statErr == nil {
			logContent, _ := os.ReadFile(logFile)
			if strings.TrimSpace(string(logContent)) != "" {
				t.Errorf("aws was called despite a digest mismatch: %q", string(logContent))
			}
		}

		// The bad cached file must be gone, so a retry re-downloads rather
		// than trusting the poisoned/truncated cache forever.
		if _, statErr := os.Stat(binaryPath); statErr == nil {
			t.Fatal("bad cached herdr binary was not removed after a digest mismatch")
		}
	})

	t.Run("CacheHitWithMatchingDigestUploads", func(t *testing.T) {
		withOverriddenDigest(t, matchingDigest)

		buildDir := t.TempDir()
		shimDir := t.TempDir()
		binaryPath := filepath.Join(buildDir, "herdr")
		if err := os.WriteFile(binaryPath, []byte(fixtureContent), 0o755); err != nil {
			t.Fatalf("pre-create herdr: %v", err)
		}

		logFile := filepath.Join(t.TempDir(), "aws-calls.log")
		awsShim := "#!/bin/sh\necho \"$@\" >> \"" + logFile + "\"\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}
		curlShim := "#!/bin/sh\necho 'curl called unexpectedly' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "curl"), []byte(curlShim), 0o755); err != nil {
			t.Fatalf("write curl shim: %v", err)
		}
		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		if err := fetchAndUploadHerdr(buildDir, "fake-bucket"); err != nil {
			t.Fatalf("unexpected error with a matching digest: %v", err)
		}

		logContent, err := os.ReadFile(logFile)
		if err != nil {
			t.Fatalf("read aws log: %v", err)
		}
		if !strings.Contains(string(logContent), "binaries/herdr") {
			t.Errorf("expected an aws s3 cp to binaries/herdr, got: %q", string(logContent))
		}

		// A verified cache hit must survive — verification is read-only.
		if _, statErr := os.Stat(binaryPath); statErr != nil {
			t.Fatalf("verified cached binary was removed: %v", statErr)
		}
	})

	t.Run("UploadFailureAfterVerifiedCacheHitPreservesFile", func(t *testing.T) {
		withOverriddenDigest(t, matchingDigest)

		buildDir := t.TempDir()
		shimDir := t.TempDir()
		binaryPath := filepath.Join(buildDir, "herdr")
		if err := os.WriteFile(binaryPath, []byte(fixtureContent), 0o755); err != nil {
			t.Fatalf("pre-create herdr: %v", err)
		}

		awsShim := "#!/bin/sh\necho 'upload failed' >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shimDir, "aws"), []byte(awsShim), 0o755); err != nil {
			t.Fatalf("write aws shim: %v", err)
		}
		t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		err := fetchAndUploadHerdr(buildDir, "fake-bucket")
		if err == nil {
			t.Fatal("expected error from aws upload failure, got nil")
		}
		if !strings.Contains(err.Error(), "upload herdr") {
			t.Errorf("expected error to mention %q, got: %v", "upload herdr", err)
		}

		// A verified cached binary must survive a failed upload — deleting a
		// cached artifact on the failure path is the buildLambdaZips defect
		// (see CLAUDE.md Phase 126 operator-image findings) pointed at a
		// different file.
		if _, statErr := os.Stat(binaryPath); statErr != nil {
			t.Fatalf("cached binary was removed on the upload-failure path: %v", statErr)
		}
	})
}

// TestHerdrSHA256_LooksLikeARealDigest guards against the constant regressing
// to empty, a placeholder, or the wrong shape — SHA-256 hex is always exactly
// 64 lowercase hex characters.
func TestHerdrSHA256_LooksLikeARealDigest(t *testing.T) {
	if len(herdrSHA256) != 64 {
		t.Fatalf("herdrSHA256 = %q (len %d); want 64 hex characters", herdrSHA256, len(herdrSHA256))
	}
	for _, r := range herdrSHA256 {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("herdrSHA256 = %q contains non-lowercase-hex character %q", herdrSHA256, r)
		}
	}
	placeholders := []string{
		"0000000000000000000000000000000000000000000000000000000000000000",
		strings.Repeat("0", 64),
		strings.Repeat("f", 64),
		"deadbeef",
	}
	for _, bad := range placeholders {
		if herdrSHA256 == bad {
			t.Fatalf("herdrSHA256 = %q looks like a placeholder, not a real digest", herdrSHA256)
		}
	}
}

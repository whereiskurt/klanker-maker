// Package cmd — herdr_mirror_test.go
// Pins the binaries/herdr S3 key contract across every site that uses it.
package cmd

import (
	"os"
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

// TestHerdrFragment_ResolvesBucketBeforeUse pins the fragment against a bug that
// shipped once and was proven live: cloud-init runs km-init.sh as a plain child
// process and never sources /etc/profile.d, so a bare ${KM_ARTIFACTS_BUCKET}
// expands to empty and the fetch becomes `s3:///binaries/herdr`. The failure is
// silent — the fragment's own `|| echo` swallows it into the boot log.
func TestHerdrFragment_ResolvesBucketBeforeUse(t *testing.T) {
	raw, err := os.ReadFile("../../../profiles/base/tools/herdr.yaml")
	if err != nil {
		t.Fatalf("read fragment: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "/etc/profile.d/km-identity.sh") {
		t.Error("fragment does not source km-identity.sh before using KM_ARTIFACTS_BUCKET")
	}
	if strings.Contains(s, "exported at the top of the bootstrap and inherited") {
		t.Error("fragment still carries the disproven inheritance claim in its comment")
	}
}

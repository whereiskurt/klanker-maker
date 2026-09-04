// Package cmd — herdr_mirror_test.go
// Pins the binaries/herdr S3 key contract across every site that uses it.
package cmd

import (
	"os"
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

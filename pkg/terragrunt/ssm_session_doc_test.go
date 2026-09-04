package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// livePinnedSSMSessionDocDir resolves the ssm-session-doc module directory that
// the LIVE terragrunt unit actually sources. Reading the pin instead of naming a
// version keeps these assertions honest: a test hardcoded to a module version
// silently stops testing anything the moment the live pin moves past it (see the
// ec2spot_timeout_test drift, where every assertion sat inert for a whole phase).
func livePinnedSSMSessionDocDir(t *testing.T) (dir, version string) {
	t.Helper()

	repoRoot := findRepoRoot(t)
	unit := filepath.Join(repoRoot, "infra", "live", "use1", "ssm-session-doc", "terragrunt.hcl")
	src, err := os.ReadFile(unit)
	if err != nil {
		t.Fatalf("read live unit %s: %v", unit, err)
	}

	m := regexp.MustCompile(`infra/modules/ssm-session-doc/(v[0-9]+\.[0-9]+\.[0-9]+)`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("no ssm-session-doc module pin found in %s", unit)
	}
	version = string(m[1])

	dir = filepath.Join(repoRoot, "infra", "modules", "ssm-session-doc", version)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("live unit pins %s but that module directory does not exist: %v", version, err)
	}
	return dir, version
}

// The sandbox session document's idleSessionTimeout is what disconnects an
// operator mid-session; SSM tears the session down server-side after this many
// minutes with no terminal I/O. 60 is the maximum AWS accepts.
func TestSSMSessionDoc_IdleSessionTimeoutIsSixtyMinutes(t *testing.T) {
	t.Parallel()

	dir, version := livePinnedSSMSessionDocDir(t)
	src, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatalf("read main.tf: %v", err)
	}

	m := regexp.MustCompile(`idleSessionTimeout\s*=\s*"(\d+)"`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s/main.tf declares no idleSessionTimeout", version)
	}
	if got := string(m[1]); got != "60" {
		t.Errorf("live-pinned %s has idleSessionTimeout = %q, want \"60\" (AWS max; anything lower disconnects km shell sooner)", version, got)
	}
}

// A version directory is created by copying the previous one, which makes a
// stale Version tag the single easiest mistake to ship — and it lands on the
// deployed AWS resource, where it is the only in-console evidence of which
// module version produced the document.
func TestSSMSessionDoc_VersionTagMatchesItsDirectory(t *testing.T) {
	t.Parallel()

	dir, version := livePinnedSSMSessionDocDir(t)
	src, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatalf("read main.tf: %v", err)
	}

	m := regexp.MustCompile(`Version\s*=\s*"(v[0-9]+\.[0-9]+\.[0-9]+)"`).FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s/main.tf has no Version tag", version)
	}
	if got := string(m[1]); got != version {
		t.Errorf("module directory is %s but its Version tag says %q — stale copy from the previous version", version, got)
	}
}

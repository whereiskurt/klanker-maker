package secrets_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// Every agent dispatch site must put the shim directory on PATH.
//
// dispatch_as_sandbox is defined once per poller (slack, github, h1, webhook)
// plus a tmux twin, and covers eighteen call sites. The shim is what makes
// km-env innermost — the turn shell gets a PATH entry, the agent process gets
// the secrets. Miss one definition and that poller's agent runs with no key and
// dies on a 401, with nothing else in the system noticing.
//
// Relying on /etc/profile.d ordering is not enough and this guard is why:
// nvm's own profile.d script PREPENDS its bin directory and would win, and the
// tmux dispatch uses `bash -c` (non-login) which never sources profile.d at all.
// This is the Phase 131/132 shape — dead under the default configuration,
// invisible to tests that assert mere string presence.
//
// Name-agnostic on purpose: a sixth poller added later is covered without
// editing this test.
func TestEveryDispatchSitePrependsShimDir(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "pkg/compiler/userdata.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)

	defs := regexp.MustCompile(`dispatch_as_sandbox\(\) \{`).FindAllStringIndex(src, -1)
	if len(defs) == 0 {
		t.Fatal("no dispatch_as_sandbox definitions found — this guard needs updating")
	}

	for i, loc := range defs {
		end := len(src)
		if i+1 < len(defs) {
			end = defs[i+1][0]
		}
		// The function body is short; 600 bytes covers it comfortably.
		window := src[loc[1]:minInt(loc[1]+600, end)]
		// Deliberately NOT a bare Contains on "/opt/km/shims": a comment
		// mentioning the path would satisfy that while the dispatch itself ran
		// unshimmed. Require the prepend on the exec line, gated on the bundle
		// so the dormant case stays byte-identical.
		if !dispatchPrepend.MatchString(window) {
			t.Errorf("dispatch_as_sandbox definition #%d (byte %d) does not prepend "+
				"/opt/km/shims on its exec runuser line, gated on .SopsBundlePresent: "+
				"agents dispatched there would run with no secrets",
				i+1, loc[0])
		}
	}
}

// The plaintext env file and its profile.d hook are the whole point of the
// phase. If either comes back, every login shell carries the bundle again.
func TestPlaintextEnvInjectionIsGone(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "pkg/compiler/userdata.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)

	for _, banned := range []string{"/etc/sandbox-secrets.env", "zz-sandbox-secrets.sh"} {
		if strings.Contains(src, banned) {
			t.Errorf("userdata still references %s: secrets would be readable from "+
				"every login shell again", banned)
		}
	}
}

// dispatchPrepend matches an `exec runuser … bash -[l]c "…PATH=/opt/km/shims…"`
// line whose prepend is gated on .SopsBundlePresent. Both halves matter: without
// the exec anchor a stray comment passes, and without the gate the dormant case
// would stop being byte-identical.
var dispatchPrepend = regexp.MustCompile(
	`exec runuser[^\n]*\{\{ if \.SopsBundlePresent \}\}PATH=/opt/km/shims:`)

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

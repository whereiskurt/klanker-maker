package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// extractKmAptHTTPS pulls the km_apt_https() shell function verbatim out of a
// rendered userdata script. Extracting from the real rendered output (rather than
// hand-copying the function into the test) is what makes this a behavioral test:
// if someone edits the template, this test runs the EDITED function.
func extractKmAptHTTPS(t *testing.T, script string) string {
	t.Helper()
	const start = "km_apt_https() {"
	i := strings.Index(script, start)
	if i < 0 {
		t.Fatalf("km_apt_https() not found in rendered userdata")
	}
	rest := script[i:]
	j := strings.Index(rest, "\n}\n")
	if j < 0 {
		t.Fatalf("unterminated km_apt_https() body in rendered userdata")
	}
	return rest[:j+len("\n}\n")]
}

// requireGNUSed skips the bash-execution tests when the host sed is not GNU sed.
// The function under test uses `sed -i -E` (GNU form, which is what actually runs
// on the Ubuntu instance). BSD sed on macOS reads `-i`'s argument positionally and
// would consume `-E` as a backup suffix, so running it there proves nothing.
// Linux CI is the authoritative run; macOS developers get a green path via
// `brew install gnu-sed`.
func requireGNUSed(t *testing.T) string {
	t.Helper()
	if out, err := exec.Command("sed", "--version").CombinedOutput(); err == nil &&
		strings.Contains(string(out), "GNU sed") {
		return "sed"
	}
	if p, err := exec.LookPath("gsed"); err == nil {
		return p
	}
	t.Skip("needs GNU sed: install with `brew install gnu-sed` (Linux CI runs this test)")
	return ""
}

// runKmAptHTTPS writes the fixture sources tree under root, then executes the
// extracted function against it via the KM_APT_ROOT seam.
func runKmAptHTTPS(t *testing.T, fn, root string, files map[string]string) map[string]string {
	t.Helper()
	sedBin := requireGNUSed(t)
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}

	// Harness preamble, three parts:
	//   1. `apt-get` stub — the function early-returns on `command -v apt-get`,
	//      which is false on a macOS dev box. `command -v` does resolve shell
	//      functions, so a no-op function satisfies the guard portably.
	//   2. `sed` shim — routes to GNU sed (may be `gsed`) so `sed -i -E` parses.
	//   3. `set -u` mirrors the generated script's `set -euo pipefail`, so an
	//      unguarded ${KM_APT_ROOT} in the template fails the test rather than
	//      aborting every real sandbox bootstrap.
	preamble := "set -u\n" +
		"apt-get() { :; }\n" +
		"sed() { command " + sedBin + " \"$@\"; }\n"
	harness := preamble + fn + "\nkm_apt_https\n"
	cmd := exec.Command("bash", "-c", harness)
	cmd.Env = append(os.Environ(), "KM_APT_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("km_apt_https failed: %v\n%s", err, out)
	}

	got := make(map[string]string, len(files))
	for rel := range files {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read back %s: %v", rel, err)
		}
		got[rel] = string(b)
	}
	return got
}

// TestKmAptHTTPS_RewritesSchemeAndMirrorHost is the behavioral test. It proves
// the function (a) upgrades http:// to https:// because the sandbox SG allows only
// 443, (b) redirects the degraded regional EC2 mirror to archive.ubuntu.com, and
// (c) leaves every other apt source alone.
func TestKmAptHTTPS_RewritesSchemeAndMirrorHost(t *testing.T) {
	script := generateLearnV2Userdata(t)
	fn := extractKmAptHTTPS(t, script)

	files := map[string]string{
		// deb822 form — Ubuntu 24.04 noble base sources.
		"etc/apt/sources.list.d/ubuntu.sources": "Types: deb\n" +
			"URIs: http://us-east-1.ec2.archive.ubuntu.com/ubuntu/\n" +
			"Suites: noble noble-updates\n" +
			"Components: main universe\n" +
			"\n" +
			"Types: deb\n" +
			"URIs: http://security.ubuntu.com/ubuntu\n" +
			"Suites: noble-security\n" +
			"Components: main\n",
		// one-line form — legacy sources.list.
		"etc/apt/sources.list": "deb http://eu-west-2.ec2.archive.ubuntu.com/ubuntu jammy main\n" +
			"deb http://us-east-1.ec2.ports.ubuntu.com/ubuntu-ports noble main\n",
		// a PPA and a vendor repo, both of which must be left on their own hosts.
		"etc/apt/sources.list.d/mozillateam.list":   "deb https://ppa.launchpadcontent.net/mozillateam/ppa/ubuntu noble main\n",
		"etc/apt/sources.list.d/google-chrome.list": "deb [arch=amd64] https://dl.google.com/linux/chrome/deb/ stable main\n",
	}

	got := runKmAptHTTPS(t, fn, t.TempDir(), files)

	assertContains := func(file, want string) {
		t.Helper()
		if !strings.Contains(got[file], want) {
			t.Errorf("%s: missing %q\n--- got ---\n%s", file, want, got[file])
		}
	}
	assertAbsent := func(file, unwanted string) {
		t.Helper()
		if strings.Contains(got[file], unwanted) {
			t.Errorf("%s: still contains %q\n--- got ---\n%s", file, unwanted, got[file])
		}
	}

	// (a) scheme upgraded everywhere.
	for f := range files {
		assertAbsent(f, "http://")
	}
	// (b) the degraded regional mirror is gone, in both file shapes and any region.
	assertAbsent("etc/apt/sources.list.d/ubuntu.sources", "ec2.archive.ubuntu.com")
	assertContains("etc/apt/sources.list.d/ubuntu.sources", "URIs: https://archive.ubuntu.com/ubuntu/")
	assertAbsent("etc/apt/sources.list", "ec2.archive.ubuntu.com")
	assertContains("etc/apt/sources.list", "deb https://archive.ubuntu.com/ubuntu jammy main")
	// arm64 ports analog.
	assertAbsent("etc/apt/sources.list", "ec2.ports.ubuntu.com")
	assertContains("etc/apt/sources.list", "deb https://ports.ubuntu.com/ubuntu-ports noble main")
	// (c) everything else untouched apart from the scheme.
	assertContains("etc/apt/sources.list.d/ubuntu.sources", "URIs: https://security.ubuntu.com/ubuntu")
	assertContains("etc/apt/sources.list.d/mozillateam.list", "https://ppa.launchpadcontent.net/mozillateam/ppa/ubuntu")
	assertContains("etc/apt/sources.list.d/google-chrome.list", "https://dl.google.com/linux/chrome/deb/")
}

// TestKmAptHTTPS_Idempotent guards the fact that km_apt_https is invoked four
// times per boot (userdata lines 63, 74, 3593, 3630/3639). A second pass must not
// mangle already-rewritten sources — e.g. by producing archive.archive.ubuntu.com.
func TestKmAptHTTPS_Idempotent(t *testing.T) {
	script := generateLearnV2Userdata(t)
	fn := extractKmAptHTTPS(t, script)

	files := map[string]string{
		"etc/apt/sources.list": "deb http://us-east-1.ec2.archive.ubuntu.com/ubuntu noble main\n",
	}
	root := t.TempDir()
	first := runKmAptHTTPS(t, fn, root, files)
	second := runKmAptHTTPS(t, fn, root, map[string]string{"etc/apt/sources.list": first["etc/apt/sources.list"]})

	if first["etc/apt/sources.list"] != second["etc/apt/sources.list"] {
		t.Errorf("km_apt_https is not idempotent:\nfirst:  %q\nsecond: %q",
			first["etc/apt/sources.list"], second["etc/apt/sources.list"])
	}
	if want := "deb https://archive.ubuntu.com/ubuntu noble main\n"; first["etc/apt/sources.list"] != want {
		t.Errorf("first pass = %q, want %q", first["etc/apt/sources.list"], want)
	}
}

// TestUserdata_AptRetriesConfigured pins the Acquire::Retries line. A degraded
// mirror or PPA answering 503 should be retried rather than surfacing as an
// immediate `Ign:` that apt only revisits at the end of the run.
func TestUserdata_AptRetriesConfigured(t *testing.T) {
	script := generateLearnV2Userdata(t)
	if !strings.Contains(script, `Acquire::Retries "3";`) {
		t.Errorf("rendered userdata does not configure Acquire::Retries")
	}
}

package secrets_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
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

// km agent run is the SIXTH agent-dispatch path and the only one that does not
// live in pkg/compiler/userdata.go, which is exactly why it was missed: the
// guard above reads one file and is structurally blind to everything else.
//
// The script internal/app/cmd/agent.go builds is executed by tmux under
// `bash -c` — non-login AND non-interactive — so it reads neither
// /etc/profile.d/zz-km-shims.sh nor ~/.bashrc. It must set the PATH entry
// itself or every km agent run, km agent run --wait and scheduled
// `km at … agent run` on a SOPS profile dispatches an agent with no
// credentials at all.
func TestAgentRunPrependsShimDir(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "internal/app/cmd/agent.go"))
	if err != nil {
		t.Fatal(err)
	}
	// The generated script's own line, not a comment mentioning the path:
	// require the `export` so a stray reference cannot satisfy this.
	if !strings.Contains(string(body), "export PATH="+secrets.ShimDir+":$PATH") {
		t.Errorf("internal/app/cmd/agent.go does not export PATH=%s:$PATH in the "+
			"generated agent-run script: km agent run would dispatch its agent "+
			"with no secrets", secrets.ShimDir)
	}
}

// The plaintext env file and its profile.d hook are the whole point of the
// phase. If either comes back anywhere that actually RUNS — Go source or a
// shipped profile's shell — either secrets are readable from every login shell
// again, or (the Phase 133 shape) something reads a file that no longer exists
// and silently gets nothing.
//
// Repo-wide on purpose. Scanning one file is what let km agent run and four
// shipped profiles keep pointing at the deleted file through a whole branch;
// a guard that covers only the file the last bug was in cannot see the next
// one. Comment lines are exempt: pkg/profile/types.go and several profile
// headers legitimately DESCRIBE the migration away from these paths.
func TestPlaintextEnvInjectionIsGone(t *testing.T) {
	root := repoRoot(t)
	banned := []string{"/etc/sandbox-secrets.env", "zz-sandbox-secrets.sh"}

	// Historical records and the guards that assert the absence itself.
	skipDirs := map[string]bool{
		".git": true, ".planning": true, ".superpowers": true,
		"docs": true, "vendor": true, "node_modules": true, "dist": true,
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}

		var commentPrefix string
		switch {
		case strings.HasSuffix(path, "_test.go"):
			// Test files assert the absence and must name what they ban.
			return nil
		case strings.HasSuffix(path, ".go"):
			commentPrefix = "//"
		case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
			// Only shipped profiles: their initCommands are real shell.
			if !strings.HasPrefix(rel, "profiles"+string(filepath.Separator)) {
				return nil
			}
			commentPrefix = "#"
		default:
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if commentPrefix != "" && strings.HasPrefix(trimmed, commentPrefix) {
				continue
			}
			for _, b := range banned {
				if strings.Contains(line, b) {
					t.Errorf("%s:%d references %s, which Phase 133 deleted from every "+
						"sandbox: a consumer reading it gets nothing, silently. Route it "+
						"through km-env instead (see docs/brokered-secrets.md, "+
						"\"Migrating a non-agent secret consumer\")", rel, i+1, b)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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

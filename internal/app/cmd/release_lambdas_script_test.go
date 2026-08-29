package cmd_test

// Drift guard: scripts/build-release-lambdas.sh must build exactly the Lambdas
// that lambdaBuilds() does.
//
// The script produces the km_v<version>_lambdas.tar.xz release asset that
// containers/operator/Dockerfile unpacks into /klanker-maker/build/. Every entry
// in lambdaBuilds() has a terragrunt module reading its zip via
// filebase64sha256() at PLAN time, so a Lambda added to the Go list but not to
// the script yields an asset that is silently missing a zip — and `km init
// --plan` in the container fails on that one module with an error that points at
// terraform, not at the release pipeline.
//
// This is the same footgun recorded as project_km_init_skips_existing_lambda_zips
// (a Lambda missing from the build list is never built and nothing says so),
// caught here at test time instead.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// repoRootForReleaseTest walks up from the test's working directory to the
// go.mod anchor. Not findRepoRoot() — that is unexported and this test lives in
// package cmd_test.
func repoRootForReleaseTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate repo root (no go.mod found walking up from the test directory)")
	return ""
}

func TestReleaseLambdaScript_TracksLambdaBuilds(t *testing.T) {
	root := repoRootForReleaseTest(t)
	scriptPath := filepath.Join(root, "scripts", "build-release-lambdas.sh")
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read %s: %v", scriptPath, err)
	}

	// Parse the LAMBDAS=( ... ) array literal.
	block := regexp.MustCompile(`(?s)\nLAMBDAS=\(\n(.*?)\n\)`).FindSubmatch(data)
	if block == nil {
		t.Fatalf("could not find a LAMBDAS=( ... ) array in %s — if the script's shape changed, update this guard", scriptPath)
	}
	inScript := map[string]bool{}
	for _, line := range strings.Split(string(block[1]), "\n") {
		if name := strings.TrimSpace(line); name != "" && !strings.HasPrefix(name, "#") {
			inScript[name] = true
		}
	}

	want := cmd.LambdaBuildNames()
	if len(want) == 0 {
		t.Fatal("LambdaBuildNames() returned nothing")
	}
	for _, name := range want {
		if !inScript[name] {
			t.Errorf("lambdaBuilds() builds %q but scripts/build-release-lambdas.sh does not — the release asset would ship without %s.zip, and `km init --plan` in the operator image fails on that module", name, name)
		}
		delete(inScript, name)
	}
	for extra := range inScript {
		t.Errorf("scripts/build-release-lambdas.sh builds %q, which lambdaBuilds() does not — remove it or add it to lambdaBuilds()", extra)
	}
}

package cmd_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// Every Phase 133 sandbox-side binary. Named once so a fourth is added in one
// place rather than two, and so neither guard below can drift from the other.
// A name is added here in the SAME change that adds its userdata download —
// uploading a binary nothing fetches is harmless, but listing one here before
// the download exists turns this guard red for a reason it was not built to
// report.
var secretsBinaries = []string{"km-secretsd", "km-env", "km-creds"}

// A sidecar the userdata downloads but km init never uploads 404s the gated
// download and aborts bootstrap. This pairs the two mechanically.
func TestSidecarBuilds_CoversEverySecretsBinary(t *testing.T) {
	have := map[string]bool{}
	for _, n := range cmd.SidecarBuildNames() {
		have[n] = true
	}
	for _, want := range secretsBinaries {
		if !have[want] {
			t.Errorf("sidecarBuilds() omits %s: userdata downloads it and boot would 404", want)
		}
	}
}

func TestUserdataDownloadsMatchSidecarBuilds(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "compiler", "userdata.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	built := map[string]bool{}
	for _, n := range cmd.SidecarBuildNames() {
		built[n] = true
	}
	for _, n := range secretsBinaries {
		if !strings.Contains(src, "sidecars/"+n) {
			t.Errorf("userdata never downloads %s", n)
		}
		if !built[n] {
			t.Errorf("userdata downloads %s but km init never uploads it", n)
		}
	}
}

// TestGoBuilderStagesPinBuildPlatform: every Dockerfile that compiles Go must
// pin its builder stage to $BUILDPLATFORM.
//
// buildAndPushSidecarImages passes --platform linux/amd64, which cascades to
// EVERY stage of the Dockerfile. Without the pin, the golang builder image is
// pulled as amd64 and the whole Go toolchain runs under Rosetta/qemu on an
// arm64 host, where the compiler segfaults intermittently in
// runtime.findRunnable (observed 2026-09-16 during km init). The fix is to run
// the builder natively and let the Dockerfile's own GOARCH=amd64 cross-compile,
// which is what it always asked for. Name-agnostic so a fourth Go-compiling
// sidecar added later is covered without an edit.
//
// The predicate is a non-comment `go build` line, so containers/operator's
// prose mention and containers/Dockerfile.ebpf-generate (bpf2go, built with a
// plain native `docker build` by the Makefile) are correctly left out.
func TestGoBuilderStagesPinBuildPlatform(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goBuild := regexp.MustCompile(`\bgo build\b`)
	fromGolang := regexp.MustCompile(`^\s*FROM\b.*\bgolang:`)

	var checked []string
	walkErr := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules", "build", "dist", ".terragrunt-cache", ".terraform":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(d.Name(), "Dockerfile") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(body), "\n")
		compilesGo := false
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if goBuild.MatchString(l) {
				compilesGo = true
				break
			}
		}
		if !compilesGo {
			return nil
		}
		rel, _ := filepath.Rel(repoRoot, path)
		checked = append(checked, rel)
		for i, l := range lines {
			if fromGolang.MatchString(l) && !strings.Contains(l, "--platform=$BUILDPLATFORM") {
				t.Errorf("%s:%d: builder stage %q is not pinned to $BUILDPLATFORM — "+
					"--platform linux/amd64 on the buildx call cascades here and runs the Go "+
					"toolchain under emulation; write `FROM --platform=$BUILDPLATFORM golang:...`",
					rel, i+1, strings.TrimSpace(l))
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if len(checked) < 3 {
		t.Fatalf("expected at least the three Go sidecar Dockerfiles to be checked, got %v", checked)
	}
}

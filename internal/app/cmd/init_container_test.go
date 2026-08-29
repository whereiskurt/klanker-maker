package cmd

// Tests for the two init.go defects that block running km against a repo-less
// install — the operator container image, where infra/ and build/*.zip are baked
// in but the Go source tree is not.
//
// Both fixes are correct independent of containers; the image is just what made
// them visible.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// TestRunInitDryRun_PrintsRegionLabelPaths pins that --dry-run prints the module
// directories the apply actually uses.
//
// runInitDryRun built its regionDir from the raw region ("us-east-1") while every
// other call site — RunInitWithRunner, RunInitPlanWithRunner, RunInitScopedWithRunner
// — uses compiler.RegionLabel(region) ("use1"). The banner one line above already
// printed the label correctly, so the output contradicted itself and named a
// directory that does not exist in the repo.
func TestRunInitDryRun_PrintsRegionLabelPaths(t *testing.T) {
	const region = "us-east-1"
	label := compiler.RegionLabel(region)
	if label == region {
		t.Fatalf("RegionLabel(%q) == %q; this test needs a region whose label differs from its name", region, label)
	}

	cfg := &config.Config{PrimaryRegion: region}
	out := captureStdout(func() {
		if err := runInitDryRun(cfg, region); err != nil {
			t.Errorf("runInitDryRun: %v", err)
		}
	})

	wantPath := filepath.Join("infra", "live", label, "network")
	if !strings.Contains(out, wantPath) {
		t.Errorf("runInitDryRun output does not name %q — the printed module paths must match the ones the apply uses.\n--- output ---\n%s", wantPath, out)
	}
	badPath := filepath.Join("infra", "live", region, "network")
	if strings.Contains(out, badPath) {
		t.Errorf("runInitDryRun printed %q, but apply uses the region LABEL (%q). regionDir must be built with compiler.RegionLabel(region).", badPath, label)
	}
}

// TestBuildLambdaZips_KeepsPrebuiltZipsWhenSourceAbsent pins that a zip is only
// removed when it is actually about to be rebuilt.
//
// buildLambdaZips unconditionally os.Remove'd the zip ("Always rebuild") and only
// THEN checked whether the Lambda's source directory existed, skipping if not. In
// a repo-less install — the operator image, which ships prebuilt zips and no Go
// source — the first `km init --plan` therefore deleted the very zips the
// Lambda-owning terragrunt modules read via filebase64sha256(), and every
// subsequent run failed. Warn-and-continue at the call site hid it.
func TestBuildLambdaZips_KeepsPrebuiltZipsWhenSourceAbsent(t *testing.T) {
	repoRoot := t.TempDir()
	buildDir := filepath.Join(repoRoot, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build: %v", err)
	}

	// Prebuilt zips, as the container image ships them. repoRoot has no Go
	// source, so every Lambda takes the source-not-found branch.
	const sentinel = "prebuilt-from-release"
	lambdas := lambdaBuilds()
	if len(lambdas) == 0 {
		t.Fatal("lambdaBuilds() returned no entries")
	}
	for _, lb := range lambdas {
		zp := filepath.Join(buildDir, lb.name+".zip")
		if err := os.WriteFile(zp, []byte(sentinel), 0o644); err != nil {
			t.Fatalf("seed %s: %v", zp, err)
		}
	}

	if err := buildLambdaZips(repoRoot); err != nil {
		t.Fatalf("buildLambdaZips: %v", err)
	}

	for _, lb := range lambdas {
		zp := filepath.Join(buildDir, lb.name+".zip")
		data, err := os.ReadFile(zp)
		if err != nil {
			t.Errorf("%s.zip was deleted despite its source being absent — the zip must not be removed until it is actually going to be rebuilt: %v", lb.name, err)
			continue
		}
		if string(data) != sentinel {
			t.Errorf("%s.zip contents changed: got %q, want %q", lb.name, string(data), sentinel)
		}
	}

	// The terraform binary exists solely to be zipped INTO ttl-handler.zip. With
	// no ttl-handler source there is nothing to put it in, so downloading it is
	// pure waste — ~90MB over the network on every `km init --plan` in the
	// operator image, whose container filesystem is thrown away each run.
	//
	// This assertion doubles as the test's own network guard: before the fix,
	// buildLambdaZips downloaded terraform unconditionally, which made this test
	// hit releases.hashicorp.com.
	if _, err := os.Stat(filepath.Join(buildDir, "terraform")); err == nil {
		t.Error("terraform was downloaded even though no Lambda needing it was built; gate the download on the ttl-handler source being present")
	}
}

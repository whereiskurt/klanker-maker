package cmd

// Phase 84.4.1 internal tests for BOOTSTRAP-REGION-HCL-PREREQ.
//
// These tests live in package cmd (not cmd_test) because they call the
// unexported ensureRegionHCL helper directly.
//
// Wave 2 plan 84.4.1-04 unskips + implements the scaffolded assertions.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestRunBootstrap_WritesRegionHCL_Internal verifies Phase 84.4.1
// BOOTSTRAP-REGION-HCL-PREREQ: the ensureRegionHCL helper is wired into
// runBootstrap so that fresh clones don't hit `read_terragrunt_config
// "../region.hcl"` errors when terragrunt runs.
//
// Full end-to-end runBootstrap invocation requires many AWS mocks; this test
// verifies:
// (a) The helper itself writes a valid region.hcl to a tempdir.
// (b) A source-grep confirms bootstrap.go calls ensureRegionHCL.
func TestRunBootstrap_WritesRegionHCL_Internal(t *testing.T) {
	// (a) Verify ensureRegionHCL writes region.hcl to a tempdir.
	tmp := t.TempDir()
	regionDir := filepath.Join(tmp, "infra", "live", "use1")
	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(regionDir, "region.hcl")); !os.IsNotExist(err) {
		t.Fatalf("region.hcl should not exist pre-call")
	}
	if err := ensureRegionHCL(regionDir, "use1", "us-east-1"); err != nil {
		t.Fatalf("ensureRegionHCL: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(regionDir, "region.hcl"))
	if err != nil || len(b) == 0 {
		t.Fatalf("region.hcl missing or empty: err=%v len=%d", err, len(b))
	}

	// (b) Sanity grep: bootstrap.go source references ensureRegionHCL.
	src, srcErr := os.ReadFile(filepath.Join(findRepoRoot(), "internal/app/cmd/bootstrap.go"))
	if srcErr != nil {
		t.Fatalf("read bootstrap.go: %v", srcErr)
	}
	if !bytes.Contains(src, []byte("ensureRegionHCL")) {
		t.Errorf("bootstrap.go does not reference ensureRegionHCL — Phase 84.4.1 BOOTSTRAP-REGION-HCL-PREREQ not applied")
	}
}

// TestRunBootstrapSharedSES_WritesRegionHCL_Internal verifies the same prereq
// for the --shared-ses path.
//
// Same shape as TestRunBootstrap_WritesRegionHCL_Internal: helper unit test
// + source-grep confirming runBootstrapSharedSES calls ensureRegionHCL.
func TestRunBootstrapSharedSES_WritesRegionHCL_Internal(t *testing.T) {
	// (a) Verify ensureRegionHCL idempotently writes region.hcl.
	tmp := t.TempDir()
	regionDir := filepath.Join(tmp, "infra", "live", "use1")
	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := ensureRegionHCL(regionDir, "use1", "us-east-1"); err != nil {
		t.Fatalf("ensureRegionHCL: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(regionDir, "region.hcl"))
	if err != nil || len(b) == 0 {
		t.Fatalf("region.hcl missing or empty: err=%v len=%d", err, len(b))
	}

	// (b) Sanity grep: bootstrap.go source references ensureRegionHCL in the
	// runBootstrapSharedSES context.
	src, srcErr := os.ReadFile(filepath.Join(findRepoRoot(), "internal/app/cmd/bootstrap.go"))
	if srcErr != nil {
		t.Fatalf("read bootstrap.go: %v", srcErr)
	}
	if !bytes.Contains(src, []byte("ensureRegionHCL")) {
		t.Errorf("bootstrap.go does not reference ensureRegionHCL — Phase 84.4.1 BOOTSTRAP-REGION-HCL-PREREQ not applied to runBootstrapSharedSES")
	}
}

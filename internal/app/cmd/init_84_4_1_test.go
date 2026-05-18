package cmd

// Phase 84.4.1 internal tests for TERRAFORM-VERSION-CACHE-INVALIDATION.
//
// These tests live in package cmd (not cmd_test) because they call the
// unexported terraformIsCurrent helper and reference the unexported
// tfDesiredVersion constant directly.
//
// Wave 2 plan 84.4.1-04 unskips + implements the scaffolded assertions.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTerraformIsCurrent_CacheInvalidation verifies Phase 84.4.1
// TERRAFORM-VERSION-CACHE-INVALIDATION: a cached terraform binary from a
// previous tfVersion must trigger a re-download when the desired version
// changes.
//
// Four cases:
// 1. No binary → not current.
// 2. Binary present, stale sidecar (1.6.6) → not current.
// 3. Binary present, matching sidecar → current.
// 4. Binary present, missing sidecar → not current.
func TestTerraformIsCurrent_CacheInvalidation(t *testing.T) {
	tmp := t.TempDir()
	terraformPath := filepath.Join(tmp, "terraform")
	versionFile := filepath.Join(tmp, "terraform.version")

	// Case 1: no binary → needs download.
	if terraformIsCurrent(tmp) {
		t.Error("expected terraformIsCurrent=false when binary missing")
	}

	// Case 2: stale binary + stale sidecar.
	if err := os.WriteFile(terraformPath, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	if err := os.WriteFile(versionFile, []byte("1.6.6\n"), 0o644); err != nil {
		t.Fatalf("write stale sidecar: %v", err)
	}
	if terraformIsCurrent(tmp) {
		t.Errorf("expected terraformIsCurrent=false when sidecar=1.6.6 mismatches desired %q", tfDesiredVersion)
	}

	// Case 3: matching sidecar → current.
	if err := os.WriteFile(versionFile, []byte(tfDesiredVersion+"\n"), 0o644); err != nil {
		t.Fatalf("write current sidecar: %v", err)
	}
	if !terraformIsCurrent(tmp) {
		t.Errorf("expected terraformIsCurrent=true when sidecar matches %q; got false", tfDesiredVersion)
	}

	// Case 4: missing sidecar → not current (even if binary exists).
	if err := os.Remove(versionFile); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}
	if terraformIsCurrent(tmp) {
		t.Error("expected terraformIsCurrent=false when sidecar missing")
	}
}

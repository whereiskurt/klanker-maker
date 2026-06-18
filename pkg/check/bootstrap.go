// Package check provides Lambda CRUD, Python packaging, DDB row management,
// and trigger-baking for the km check serverless check runner (Phase 116).
package check

import (
	_ "embed"
	"os"
	"path/filepath"
	"testing"
)

// bootstrapPy holds the canonical _km_check_bootstrap.py Lambda handler bytes.
// This is the ONLY copy the km binary ships; the copy under
// profiles/checks/_bootstrap/_km_check_bootstrap.py is the single source of
// truth (kept via the byte-identity assertion below).
//
//go:embed _km_check_bootstrap.py
var bootstrapPy []byte

// BootstrapBytes returns the embedded bootstrap handler bytes. Every check zip
// ships this file as "_km_check_bootstrap.py".
func BootstrapBytes() []byte {
	return bootstrapPy
}

// BootstrapHandler is the Python entrypoint name to set on every check Lambda.
// The bootstrap's handler function is named "handler" (see _km_check_bootstrap.py).
const BootstrapHandler = "_km_check_bootstrap.handler"

// AssertBootstrapByteIdentity verifies (in tests only) that the embedded copy
// is byte-identical to the canonical source at
// profiles/checks/_bootstrap/_km_check_bootstrap.py.
//
// Callers in unit tests:
//
//	check.AssertBootstrapByteIdentity(t, "/path/to/repo/root")
func AssertBootstrapByteIdentity(t *testing.T, repoRoot string) {
	t.Helper()
	canonical := filepath.Join(repoRoot, "profiles", "checks", "_bootstrap", "_km_check_bootstrap.py")
	got, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("AssertBootstrapByteIdentity: read canonical bootstrap: %v", err)
	}
	if string(got) != string(bootstrapPy) {
		t.Fatal("AssertBootstrapByteIdentity: embedded bootstrap differs from canonical source; sync pkg/check/_km_check_bootstrap.py")
	}
}

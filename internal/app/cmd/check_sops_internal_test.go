package cmd

import (
	"reflect"
	"testing"
)

// TestMergeSecretPaths verifies SOPS-derived paths fold into the explicit
// --secret list, preserving order and deduplicating exact matches.
func TestMergeSecretPaths(t *testing.T) {
	base := []string{"/km/checks/c/MANUAL", "/km/checks/c/A"}
	extra := []string{"/km/checks/c/A", "/km/checks/c/B"} // A already present
	got := mergeSecretPaths(base, extra)
	want := []string{"/km/checks/c/MANUAL", "/km/checks/c/A", "/km/checks/c/B"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeSecretPaths = %v, want %v", got, want)
	}
}

// TestMergeSecretPaths_NilBase verifies a nil base yields just the extras.
func TestMergeSecretPaths_NilBase(t *testing.T) {
	got := mergeSecretPaths(nil, []string{"/km/checks/c/A", "/km/checks/c/A"})
	want := []string{"/km/checks/c/A"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeSecretPaths(nil,...) = %v, want %v", got, want)
	}
}

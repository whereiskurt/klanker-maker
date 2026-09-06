package predicate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCgroupID_ReturnsDirectoryInode(t *testing.T) {
	dir := t.TempDir()
	got, err := CgroupID(dir)
	if err != nil {
		t.Fatalf("CgroupID(%q): %v", dir, err)
	}
	if got == 0 {
		t.Fatal("CgroupID returned 0; a real cgroup id is never 0, and 0 is the sentinel meaning 'unset'")
	}

	// Two different directories must not collide, or the cgroup clause of the
	// enforcement predicate would match the wrong scope.
	other := filepath.Join(t.TempDir(), "sibling")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	got2, err := CgroupID(other)
	if err != nil {
		t.Fatal(err)
	}
	if got == got2 {
		t.Fatalf("distinct directories returned the same id (%d)", got)
	}
}

func TestCgroupID_MissingPathIsAnError(t *testing.T) {
	// A missing scope must be a loud error, never a silent 0 — 0 is the
	// "no cgroup clause" sentinel, so swallowing this would quietly disable
	// half the enforcement predicate.
	if _, err := CgroupID(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing path, got nil")
	}
}

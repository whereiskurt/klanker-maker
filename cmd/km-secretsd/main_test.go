package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// shortSocketDir returns a directory suitable for binding a unix socket.
// sockaddr_un.sun_path is capped around 100-108 bytes depending on platform,
// and t.TempDir()'s default location can exceed that on macOS; fall back to
// a shorter base under os.TempDir() root when it would.
func shortSocketDir(t *testing.T) string {
	t.Helper()
	if d := t.TempDir(); len(d)+len("/s.sock") < 100 {
		return d
	}
	// os.TempDir() (what t.TempDir() is rooted at) is itself long on macOS
	// (/var/folders/.../T), so the fallback needs a genuinely short base, not
	// just a different name under the same root.
	d, err := os.MkdirTemp("/tmp", "kmsockd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	if len(d)+len("/s.sock") >= 100 {
		t.Skipf("temp dir %q is too long for a unix socket path on this platform", d)
	}
	return d
}

func TestSocketLive_ReportsTrueWhenSomethingIsListening(t *testing.T) {
	p := filepath.Join(shortSocketDir(t), "s.sock")
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if !socketLive(p) {
		t.Error("socketLive() = false, want true for a live listener")
	}
}

func TestSocketLive_ReportsFalseForAStaleInode(t *testing.T) {
	// A regular file at the socket path with nothing listening behind it —
	// the shape left by an unclean shutdown.
	p := filepath.Join(t.TempDir(), "s.sock")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if socketLive(p) {
		t.Error("socketLive() = true for a stale, non-listening inode")
	}
}

func TestSocketLive_ReportsFalseWhenNothingExists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "absent.sock")

	if socketLive(p) {
		t.Error("socketLive() = true for a nonexistent path")
	}
}

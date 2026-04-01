//go:build linux

package tls

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyLibrary(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected uint8
	}{
		{"openssl lib64", "/usr/lib64/libssl.so.3", LibOpenSSL},
		{"openssl debian", "/usr/lib/x86_64-linux-gnu/libssl.so.3", LibOpenSSL},
		{"openssl 1.1", "/usr/lib64/libssl.so.1.1", LibOpenSSL},
		{"gnutls", "/usr/lib64/libgnutls.so.30", LibGnuTLS},
		{"nss", "/usr/lib64/libnspr4.so", LibNSS},
		{"unknown", "/usr/lib64/libcurl.so.4", 0},
		{"empty path", "", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyLibrary(tc.path)
			if got != tc.expected {
				t.Errorf("classifyLibrary(%q) = %d, want %d", tc.path, got, tc.expected)
			}
		})
	}
}

func TestDiscoverLibrariesFromMaps(t *testing.T) {
	// Create a temp directory simulating /proc/<pid>/maps
	tmpDir := t.TempDir()

	// Simulate /proc/100/maps
	pid100Dir := filepath.Join(tmpDir, "100")
	if err := os.MkdirAll(pid100Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	maps100 := `7f1234000000-7f1234100000 r-xp 00000000 08:01 1234 /usr/lib64/libc.so.6
7f1234200000-7f1234300000 r-xp 00000000 08:01 5678 /usr/lib64/libssl.so.3
7f1234400000-7f1234500000 r-xp 00000000 08:01 9012 /usr/lib64/libcrypto.so.3
`
	if err := os.WriteFile(filepath.Join(pid100Dir, "maps"), []byte(maps100), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate /proc/200/maps with the same libssl
	pid200Dir := filepath.Join(tmpDir, "200")
	if err := os.MkdirAll(pid200Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	maps200 := `7f5678000000-7f5678100000 r-xp 00000000 08:01 5678 /usr/lib64/libssl.so.3
7f5678200000-7f5678300000 r-xp 00000000 08:01 1111 /usr/lib64/libgnutls.so.30
`
	if err := os.WriteFile(filepath.Join(pid200Dir, "maps"), []byte(maps200), 0o644); err != nil {
		t.Fatal(err)
	}

	libs, err := discoverLibrariesFromDir(tmpDir)
	if err != nil {
		t.Fatalf("discoverLibrariesFromDir: %v", err)
	}

	if len(libs) != 2 {
		t.Fatalf("expected 2 libraries, got %d: %+v", len(libs), libs)
	}

	// Check we found both libssl and libgnutls, deduplicated
	foundSSL := false
	foundGnuTLS := false
	for _, lib := range libs {
		switch lib.Type {
		case LibOpenSSL:
			foundSSL = true
			if lib.Path != "/usr/lib64/libssl.so.3" {
				t.Errorf("unexpected openssl path: %s", lib.Path)
			}
			// Should have PIDs from both processes
			if len(lib.PIDs) != 2 {
				t.Errorf("expected 2 PIDs for libssl, got %d: %v", len(lib.PIDs), lib.PIDs)
			}
		case LibGnuTLS:
			foundGnuTLS = true
			if lib.Path != "/usr/lib64/libgnutls.so.30" {
				t.Errorf("unexpected gnutls path: %s", lib.Path)
			}
			if len(lib.PIDs) != 1 {
				t.Errorf("expected 1 PID for gnutls, got %d", len(lib.PIDs))
			}
		}
	}
	if !foundSSL {
		t.Error("libssl.so not found in results")
	}
	if !foundGnuTLS {
		t.Error("libgnutls.so not found in results")
	}
}

func TestDiscoverLibrariesEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a pid dir with no TLS libraries
	pidDir := filepath.Join(tmpDir, "300")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mapsContent := `7f1234000000-7f1234100000 r-xp 00000000 08:01 1234 /usr/lib64/libc.so.6
`
	if err := os.WriteFile(filepath.Join(pidDir, "maps"), []byte(mapsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	libs, err := discoverLibrariesFromDir(tmpDir)
	if err != nil {
		t.Fatalf("discoverLibrariesFromDir: %v", err)
	}
	if len(libs) != 0 {
		t.Errorf("expected 0 libraries, got %d", len(libs))
	}
}

func TestFindSystemLibsslNotExist(t *testing.T) {
	_, err := FindSystemLibssl()
	// On macOS/CI test environments, the paths won't exist, so we expect error.
	// On an actual AL2023/Ubuntu box with libssl.so.3, this would succeed.
	if err == nil {
		t.Log("FindSystemLibssl succeeded (running on a system with libssl.so.3)")
	}
}

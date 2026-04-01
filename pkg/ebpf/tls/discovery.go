//go:build linux

package tls

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LibraryInfo describes a TLS library found on the system.
type LibraryInfo struct {
	Path string   // Absolute path to the shared library
	Type uint8    // Library type: LibOpenSSL, LibGnuTLS, LibNSS
	PIDs []uint32 // Process IDs that have this library loaded
}

// classifyLibrary returns the library type constant for a shared library path.
// Returns 0 for non-TLS or unrecognized libraries.
func classifyLibrary(path string) uint8 {
	base := filepath.Base(path)
	switch {
	case strings.HasPrefix(base, "libssl.so"):
		return LibOpenSSL
	case strings.HasPrefix(base, "libgnutls.so"):
		return LibGnuTLS
	case strings.HasPrefix(base, "libnspr4.so"):
		return LibNSS
	default:
		return 0
	}
}

// DiscoverLibraries scans /proc/*/maps to find loaded TLS libraries.
// It deduplicates by path and collects all PIDs using each library.
func DiscoverLibraries() ([]LibraryInfo, error) {
	return discoverLibrariesFromDir("/proc")
}

// discoverLibrariesFromDir is the testable implementation of DiscoverLibraries.
// It scans pidDir/<pid>/maps for TLS library mappings.
func discoverLibrariesFromDir(pidDir string) ([]LibraryInfo, error) {
	// Map from library path -> LibraryInfo for deduplication.
	byPath := make(map[string]*LibraryInfo)

	entries, err := os.ReadDir(pidDir)
	if err != nil {
		return nil, fmt.Errorf("read proc dir %s: %w", pidDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// Only process numeric directory names (PIDs).
		pid, err := strconv.ParseUint(entry.Name(), 10, 32)
		if err != nil {
			continue
		}

		mapsPath := filepath.Join(pidDir, entry.Name(), "maps")
		if err := scanMapsFile(mapsPath, uint32(pid), byPath); err != nil {
			// Permission denied is expected for other users' processes.
			continue
		}
	}

	result := make([]LibraryInfo, 0, len(byPath))
	for _, info := range byPath {
		result = append(result, *info)
	}
	return result, nil
}

// scanMapsFile reads a /proc/<pid>/maps file and extracts TLS library paths.
func scanMapsFile(path string, pid uint32, byPath map[string]*LibraryInfo) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: address perms offset dev inode pathname
		// We only care about the pathname (last field).
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		libPath := fields[len(fields)-1]
		if !strings.HasPrefix(libPath, "/") {
			continue
		}

		libType := classifyLibrary(libPath)
		if libType == 0 {
			continue
		}

		if existing, ok := byPath[libPath]; ok {
			// Deduplicate PIDs too.
			hasPID := false
			for _, p := range existing.PIDs {
				if p == pid {
					hasPID = true
					break
				}
			}
			if !hasPID {
				existing.PIDs = append(existing.PIDs, pid)
			}
		} else {
			byPath[libPath] = &LibraryInfo{
				Path: libPath,
				Type: libType,
				PIDs: []uint32{pid},
			}
		}
	}
	return scanner.Err()
}

// systemLibsslPaths are the common paths where libssl.so.3 is installed.
var systemLibsslPaths = []string{
	"/usr/lib64/libssl.so.3",                    // AL2023 / Fedora / RHEL
	"/usr/lib/x86_64-linux-gnu/libssl.so.3",     // Debian / Ubuntu
	"/usr/lib/aarch64-linux-gnu/libssl.so.3",    // Debian ARM64
}

// FindSystemLibssl checks common system paths for libssl.so.3 and returns
// the first path that exists. This is a fallback when /proc scanning finds
// nothing (the library might not be loaded yet by any process).
func FindSystemLibssl() (string, error) {
	for _, p := range systemLibsslPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("libssl.so.3 not found in common system paths")
}

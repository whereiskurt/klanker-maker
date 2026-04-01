//go:build linux

package ebpf

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// CgroupPath returns the cgroup v2 path for a given sandbox ID.
// Path format: /sys/fs/cgroup/km.slice/km-{sandboxID}.scope
// Uses systemd-style slice/scope naming under a dedicated km.slice parent.
func CgroupPath(sandboxID string) string {
	return fmt.Sprintf("/sys/fs/cgroup/km.slice/km-%s.scope", sandboxID)
}

// detectCgroup2Mount parses /proc/mounts to find the cgroup2 mount point.
// Returns "/sys/fs/cgroup" if not found (the common default on modern systems).
func detectCgroup2Mount() string {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return "/sys/fs/cgroup"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		// Format: device mountpoint fstype options dump pass
		if len(fields) >= 3 && fields[2] == "cgroup2" {
			return fields[1]
		}
	}
	return "/sys/fs/cgroup"
}

// CreateSandboxCgroup creates the cgroup directory for a sandbox under the
// km.slice parent. The cgroup uses systemd-style naming:
//
//	{cgroupMount}/km.slice/km-{sandboxID}.scope
//
// Note: The sandbox user's shell process is moved into this cgroup by writing
// its PID to {cgroupPath}/cgroup.procs during user-data bootstrap, not by
// the km CLI itself (which may not have the sandbox process PID).
func CreateSandboxCgroup(sandboxID string) (string, error) {
	cgroupMount := detectCgroup2Mount()
	cgroupPath := fmt.Sprintf("%s/km.slice/km-%s.scope", cgroupMount, sandboxID)

	if err := os.MkdirAll(cgroupPath, 0o755); err != nil {
		return "", fmt.Errorf("create cgroup %s: %w", cgroupPath, err)
	}
	return cgroupPath, nil
}

// RemoveSandboxCgroup removes the cgroup directory for a sandbox.
// The cgroup must be empty of processes before removal; if it still has
// processes this will return an EBUSY error.
//
// Also attempts best-effort removal of the km.slice parent if it is empty.
func RemoveSandboxCgroup(sandboxID string) error {
	cgroupMount := detectCgroup2Mount()
	cgroupPath := fmt.Sprintf("%s/km.slice/km-%s.scope", cgroupMount, sandboxID)

	if err := os.Remove(cgroupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove cgroup %s: %w", cgroupPath, err)
	}

	// Best-effort: remove km.slice parent if it is now empty.
	slicePath := fmt.Sprintf("%s/km.slice", cgroupMount)
	entries, err := os.ReadDir(slicePath)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(slicePath)
	}

	return nil
}

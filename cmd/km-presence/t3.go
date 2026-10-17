package main

import (
	"os"
	"path/filepath"
	"strings"
)

// t3UnitCgroup is the cgroup path fragment of the km-owned T3 Code server unit
// that `km t3 start` writes. t3's agent children stay inside it because the
// unit unsets XDG_RUNTIME_DIR, which stops t3 from wrapping them in
// `systemd-run --user` scopes.
const t3UnitCgroup = "/km-t3code.service"

// procRoot is the procfs mount, swapped by tests.
var procRoot = "/proc"

// inT3Unit reports whether pid runs inside km-t3code.service. A read error
// reports false, so an unknown process keeps signal 5's existing behaviour
// rather than silently dropping out of it.
func inT3Unit(pid string) bool {
	b, err := os.ReadFile(filepath.Join(procRoot, pid, "cgroup"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), t3UnitCgroup)
}

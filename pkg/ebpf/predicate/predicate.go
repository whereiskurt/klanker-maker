// Package predicate holds the portable half of km's eBPF enforcement
// predicate, plus the guards that keep the kernel-side half honest.
//
// Since Phase 135 the BPF network programs attach at the ROOT cgroup rather
// than the per-sandbox scope, because no interactive session ever reached that
// scope: cgroup v2 permits a migration only if the caller can write the
// cgroup.procs of the COMMON ANCESTOR of the source and destination cgroups,
// and for any interactive session that ancestor is the root cgroup
// (root:root 0644). Every km shell, ssh, herdr and VS Code session therefore
// failed the join with EPERM — swallowed by 2>/dev/null — and ran unenforced.
//
// Enforcement now selects on identity instead of placement:
//
//	enforced = (uid == sandbox_uid) || (cgroup_id == km_scope_id)
//
// This package is its own compilation unit because pkg/ebpf is linux-only and
// does not build at all on a dev machine (it carries bpf.c and no darwin Go
// files). Keeping the id lookup and the source guards here means both are
// exercised by a plain `go test ./...` on macOS — which matters, because the
// bug this phase fixes survived for months precisely because nothing about it
// was reachable from a dev machine.
package predicate

import (
	"fmt"
	"os"
	"syscall"
)

// CgroupID returns the cgroup v2 id of the cgroup directory at path.
//
// A cgroup v2 id IS the cgroup directory's inode number — the same value the
// kernel reports from bpf_get_current_cgroup_id() and bpf_skb_cgroup_id().
// It is assigned when the directory is created, so it changes across reboots
// and MUST be read at attach time rather than persisted or passed through
// userdata.
//
// Zero is never returned with a nil error: 0 is the sentinel the BPF side
// reads as "no cgroup clause", so a silent zero would disable half the
// enforcement predicate.
func CgroupID(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("stat cgroup %s: %w", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("stat cgroup %s: unexpected stat type %T", path, info.Sys())
	}
	if st.Ino == 0 {
		return 0, fmt.Errorf("stat cgroup %s: inode 0 is the 'unset' sentinel", path)
	}
	return uint64(st.Ino), nil
}

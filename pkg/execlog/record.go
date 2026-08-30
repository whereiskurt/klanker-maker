// Package execlog holds the process-execution vocabulary shared by the eBPF
// exec tracer and the on-box km-netpolicy helper.
//
// It is the process-side counterpart to pkg/flowlog: flowlog records where a
// sandbox went, execlog records what was running when it went there. The two
// join on pid (see join.go), which is the whole reason this package exists.
//
// Unlike flowlog there is exactly ONE producer — the root exec daemon — so
// there is one file, and none of flowlog's multi-writer directory-permission
// reasoning applies here. The directory is root-only instead, because argv is
// recorded unredacted and root's argv must not be readable by the sandbox user.
package execlog

import "time"

// Record kinds. Exit records carry only pid, timestamp and comm; they exist so
// the pid join can bound a process's lifetime and refuse to attribute a flow to
// a pid that has since been reused.
const (
	KindExec = "exec"
	KindExit = "exit"
)

// DefaultDir is where the daemon writes and km-netpolicy reads.
//
// Under /var/lib rather than /run for the same reason the deny list and flow
// store are: a trace silently emptied by a reboot would misrepresent what a box
// did, and this one feeds an operator's forensics rather than a live decision.
const DefaultDir = "/var/lib/km/execs"

// DefaultMaxBytes caps the live file before rotation. One previous generation
// is retained, so the store costs at most 2x this on disk.
const DefaultMaxBytes int64 = 16 << 20 // 16 MiB

// Record is one process event.
//
// Ret is deliberately NOT omitempty: zero means the exec succeeded, and a field
// absent because it was zero would be indistinguishable from one absent because
// it was never recorded. Every other optional field is omitempty for the mirror
// of that reason — a zero pid or an empty comm is not an observation.
type Record struct {
	TS        time.Time `json:"ts"`
	Kind      string    `json:"kind"`
	PID       int       `json:"pid"`
	PPID      int       `json:"ppid,omitempty"`
	UID       int       `json:"uid"`
	Comm      string    `json:"comm,omitempty"`
	Args      []string  `json:"args,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Ret       int       `json:"ret"`
	CgroupID  uint64    `json:"cgroup_id,omitempty"`
}

// Exe returns the executable the record describes: argv[0] when argv was
// captured, otherwise the kernel's 16-byte comm. Callers display this, so it
// must never be empty for an exec record that captured anything at all.
func (r Record) Exe() string {
	if len(r.Args) > 0 && r.Args[0] != "" {
		return r.Args[0]
	}
	return r.Comm
}

// Path returns the store file inside dir.
func Path(dir string) string { return dir + "/execs.jsonl" }

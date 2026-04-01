//go:build linux

package ebpf

import (
	"fmt"
	"os"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

// PinPath returns the bpffs directory path for a sandbox's pinned programs
// and maps. All link files and auto-pinned maps live under this directory.
//
// Format: /sys/fs/bpf/km/{sandboxID}/
func PinPath(sandboxID string) string {
	return fmt.Sprintf("/sys/fs/bpf/km/%s/", sandboxID)
}

// pinLinkNames lists the filenames pinned under PinPath for the four cgroup
// program links.
var pinLinkNames = []string{
	"connect4_link",
	"sendmsg4_link",
	"sockops_link",
	"egress_link",
}

// IsPinned reports whether a sandbox has active pinned BPF state — that is,
// whether the pin directory exists and all four link files are present.
func IsPinned(sandboxID string) bool {
	pinPath := PinPath(sandboxID)
	for _, name := range pinLinkNames {
		if _, err := os.Stat(pinPath + name); err != nil {
			return false
		}
	}
	return true
}

// ListPinned returns the sandbox IDs for all currently pinned sandboxes by
// listing subdirectories under /sys/fs/bpf/km/.
func ListPinned() ([]string, error) {
	const kmBpfDir = "/sys/fs/bpf/km/"
	entries, err := os.ReadDir(kmBpfDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list pinned sandboxes in %s: %w", kmBpfDir, err)
	}

	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// Cleanup removes all BPF pinned artifacts for a sandbox and tears down its
// cgroup. It is safe to call even if the pin directory does not exist.
//
// Removal order:
//  1. All files inside PinPath (links + auto-pinned maps)
//  2. PinPath directory itself
//  3. Best-effort removal of /sys/fs/bpf/km/ if it is now empty
//  4. RemoveSandboxCgroup (best-effort)
func Cleanup(sandboxID string) error {
	pinPath := PinPath(sandboxID)

	if err := os.RemoveAll(pinPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove bpffs dir %s: %w", pinPath, err)
	}

	// Best-effort: remove parent /sys/fs/bpf/km/ if now empty.
	const kmBpfDir = "/sys/fs/bpf/km/"
	entries, err := os.ReadDir(kmBpfDir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(kmBpfDir)
	}

	// Best-effort: remove sandbox cgroup (may already be gone).
	_ = RemoveSandboxCgroup(sandboxID)

	return nil
}

// RecoverPinned reconstructs an Enforcer from previously pinned bpffs state.
// This is used when the km CLI restarts and needs to manage an already-running
// enforcement session (e.g. km destroy must clean up live BPF state).
//
// The recovered Enforcer holds live file descriptors to the pinned links and
// maps; calling Close() on it will unpin everything and detach the programs.
//
// Note: The Config is not stored in bpffs so it is not recoverable; the
// returned Enforcer will have a zero Config with only SandboxID set.
func RecoverPinned(sandboxID string) (*Enforcer, error) {
	pinPath := PinPath(sandboxID)

	if _, err := os.Stat(pinPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no pinned state for sandbox %q", sandboxID)
		}
		return nil, fmt.Errorf("stat pin path %s: %w", pinPath, err)
	}

	// Load pinned links.
	connectLink, err := link.LoadPinnedLink(pinPath+"connect4_link", nil)
	if err != nil {
		return nil, fmt.Errorf("load pinned connect4_link: %w", err)
	}

	sendmsgLink, err := link.LoadPinnedLink(pinPath+"sendmsg4_link", nil)
	if err != nil {
		connectLink.Close()
		return nil, fmt.Errorf("load pinned sendmsg4_link: %w", err)
	}

	sockopsLink, err := link.LoadPinnedLink(pinPath+"sockops_link", nil)
	if err != nil {
		sendmsgLink.Close()
		connectLink.Close()
		return nil, fmt.Errorf("load pinned sockops_link: %w", err)
	}

	egressLink, err := link.LoadPinnedLink(pinPath+"egress_link", nil)
	if err != nil {
		sockopsLink.Close()
		sendmsgLink.Close()
		connectLink.Close()
		return nil, fmt.Errorf("load pinned egress_link: %w", err)
	}

	// Load pinned allowed_cidrs map for AllowCIDR/AllowIP calls.
	allowedCidrs, err := ebpf.LoadPinnedMap(pinPath+"allowed_cidrs", nil)
	if err != nil {
		egressLink.Close()
		sockopsLink.Close()
		sendmsgLink.Close()
		connectLink.Close()
		return nil, fmt.Errorf("load pinned allowed_cidrs map: %w", err)
	}

	// Reconstruct a minimal bpfObjects with the recovered handles.
	// Maps that are not needed for AllowCIDR/AllowIP/MarkForProxy are left nil;
	// the caller must not invoke methods that require them.
	objs := &bpfObjects{}
	objs.AllowedCidrs = allowedCidrs

	return &Enforcer{
		config:      Config{SandboxID: sandboxID},
		cgroupPath:  CgroupPath(sandboxID),
		objs:        objs,
		connectLink: connectLink,
		sendmsgLink: sendmsgLink,
		sockopsLink: sockopsLink,
		egressLink:  egressLink,
		pinPath:     pinPath,
	}, nil
}

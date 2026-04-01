//go:build linux && !amd64

// Stub for non-amd64 platforms (e.g., arm64 Lambda).
// The eBPF enforcer only runs on EC2 x86_64 instances.
// This stub allows the package to compile on arm64 without BPF objects.
package ebpf

import (
	"fmt"
	"net"

	"github.com/cilium/ebpf"
)

// Enforcer is a no-op stub on non-amd64 platforms.
type Enforcer struct{}

// NewEnforcer returns an error on non-amd64 platforms.
func NewEnforcer(cfg Config) (*Enforcer, error) {
	return nil, fmt.Errorf("eBPF enforcer is only supported on amd64")
}

func (e *Enforcer) AllowIP(ip net.IP) error          { return nil }
func (e *Enforcer) AllowCIDR(cidr string) error       { return nil }
func (e *Enforcer) MarkForProxy(ip net.IP) error      { return nil }
func (e *Enforcer) Events() *ebpf.Map                 { return nil }
func (e *Enforcer) Close() error                       { return nil }

// IsPinned returns false on non-amd64.
func IsPinned(sandboxID string) bool { return false }

// Cleanup is a no-op on non-amd64.
func Cleanup(sandboxID string) error { return nil }

// RecoverPinned returns an error on non-amd64.
func RecoverPinned(sandboxID string) (*Enforcer, error) {
	return nil, fmt.Errorf("eBPF enforcer is only supported on amd64")
}

// ListPinned returns nil on non-amd64.
func ListPinned() ([]string, error) { return nil, nil }

// PinPath returns the bpffs pin path for a sandbox.
func PinPath(sandboxID string) string { return fmt.Sprintf("/sys/fs/bpf/km/%s", sandboxID) }

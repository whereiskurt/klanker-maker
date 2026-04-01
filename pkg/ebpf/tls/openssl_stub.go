//go:build linux && !amd64

package tls

import (
	"fmt"

	"github.com/cilium/ebpf"
)

// OpenSSLProbe is a stub for non-amd64 platforms.
// TLS uprobe observation is only supported on x86_64.
type OpenSSLProbe struct {
	closed bool
}

// AttachOpenSSL returns an error on non-amd64 platforms.
func AttachOpenSSL(_ string) (*OpenSSLProbe, error) {
	return nil, fmt.Errorf("uprobe only supported on amd64")
}

// EventsMap returns nil on non-amd64 platforms.
func (p *OpenSSLProbe) EventsMap() *ebpf.Map {
	return nil
}

// SetLibraryEnabled is a no-op on non-amd64 platforms.
func (p *OpenSSLProbe) SetLibraryEnabled(_ uint8, _ bool) error {
	return fmt.Errorf("uprobe only supported on amd64")
}

// Close is a no-op on non-amd64 platforms.
func (p *OpenSSLProbe) Close() error {
	return nil
}

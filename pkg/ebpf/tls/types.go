//go:build linux

package tls

import (
	"fmt"
	"net/netip"
)

// MaxPayloadLen is the maximum number of plaintext bytes captured per event.
// Matches MAX_PAYLOAD_LEN in ssl_common.h (16KB = TLS max fragment size).
const MaxPayloadLen = 16384

// Library type constants — identifies which TLS library produced the event.
// Values match LIB_* defines in ssl_common.h.
const (
	LibOpenSSL = 1
	LibGnuTLS  = 2
	LibNSS     = 3
	LibGo      = 4
	LibRustls  = 5
)

// Direction constants for TLSEvent.Direction.
const (
	DirWrite = 0 // SSL_write — outbound plaintext
	DirRead  = 1 // SSL_read  — inbound plaintext
)

// TLSEvent is the Go representation of struct ssl_event from ssl_common.h.
// The layout must match the BPF struct exactly for ring buffer deserialization.
type TLSEvent struct {
	TimestampNs uint64
	Pid         uint32
	Tid         uint32
	Fd          uint32
	RemoteIP    uint32
	RemotePort  uint16
	Direction   uint8
	LibraryType uint8
	PayloadLen  uint32
	Payload     [MaxPayloadLen]byte
}

// PayloadBytes returns a slice of the captured plaintext payload.
func (e *TLSEvent) PayloadBytes() []byte {
	n := min(e.PayloadLen, MaxPayloadLen)
	return e.Payload[:n]
}

// RemoteAddr converts the uint32 IP and uint16 port into a netip.AddrPort.
// The IP is stored in network byte order (big-endian) by the BPF program.
func (e *TLSEvent) RemoteAddr() netip.AddrPort {
	ip := netip.AddrFrom4([4]byte{
		byte(e.RemoteIP),
		byte(e.RemoteIP >> 8),
		byte(e.RemoteIP >> 16),
		byte(e.RemoteIP >> 24),
	})
	return netip.AddrPortFrom(ip, e.RemotePort)
}

// LibraryName returns a human-readable name for the TLS library.
func (e *TLSEvent) LibraryName() string {
	switch e.LibraryType {
	case LibOpenSSL:
		return "openssl"
	case LibGnuTLS:
		return "gnutls"
	case LibNSS:
		return "nss"
	case LibGo:
		return "go"
	case LibRustls:
		return "rustls"
	default:
		return fmt.Sprintf("unknown(%d)", e.LibraryType)
	}
}

// DirectionName returns "write" or "read" for the event direction.
func (e *TLSEvent) DirectionName() string {
	if e.Direction == DirWrite {
		return "write"
	}
	return "read"
}

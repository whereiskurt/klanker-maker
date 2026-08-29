// Package audit provides helper functions for eBPF event conversion.
// These are platform-independent and can be used in tests on all platforms.
package audit

import (
	"encoding/binary"
	"net"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// Action constants — must match BPF-side values in common.h.
const (
	actionDeny     = 0
	actionAllow    = 1
	actionRedirect = 2
)

// Layer constants — must match BPF-side LAYER_* defines in common.h.
const (
	layerConnect4  = 1
	layerSendmsg4  = 2
	layerEgressSKB = 3
	layerSockops   = 4
)

// actionString converts an action byte to a human-readable string.
func actionString(action uint8) string {
	switch action {
	case actionDeny:
		return "deny"
	case actionAllow:
		return "allow"
	case actionRedirect:
		return "redirect"
	default:
		return "unknown"
	}
}

// verdictFor maps a BPF-side action byte onto the flowlog verdict vocabulary,
// so a flow record means the same thing whether it came from the eBPF path,
// the DNS proxy, or the HTTP proxy. Anything other than deny/redirect is
// treated as allow — the BPF side has no fourth action, but a forward-compat
// unknown byte should read as "let through", not silently vanish from the
// census.
func verdictFor(action uint8) string {
	switch action {
	case actionDeny:
		return flowlog.VerdictDeny
	case actionRedirect:
		return flowlog.VerdictRedirect
	default:
		return flowlog.VerdictAllow
	}
}

// layerString converts a layer byte to a human-readable string.
func layerString(layer uint8) string {
	switch layer {
	case layerConnect4:
		return "connect4"
	case layerSendmsg4:
		return "sendmsg4"
	case layerEgressSKB:
		return "egress_skb"
	case layerSockops:
		return "sockops"
	default:
		return "unknown"
	}
}

// uint32ToIP converts a uint32 IPv4 address in network byte order to net.IP.
func uint32ToIP(ip uint32) net.IP {
	result := make(net.IP, 4)
	binary.BigEndian.PutUint32(result, ip)
	return result
}

// nullTermString extracts a null-terminated C string from a byte slice.
func nullTermString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

// Package audit provides helper functions for eBPF event conversion.
// These are platform-independent and can be used in tests on all platforms.
package audit

import (
	"encoding/binary"
	"math/bits"
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

// uint32ToIP converts a bpfEvent IP field back to the true dotted-quad
// net.IP it represents.
//
// The BPF side stores src_ip/dst_ip in NETWORK byte order — i.e. literally
// the wire bytes of the address, so 140.82.114.3 is laid down in memory as
// the four bytes {140,82,114,3}. audit.go decodes the whole bpfEvent struct
// with binary.LittleEndian (correct for the struct's scalar LAYOUT — there
// is no other sane way to decode a packed C struct), which reads those same
// four bytes into a uint32 by treating the FIRST byte as least-significant.
// To recover the original address we have to write the bytes back out in
// that same order, and a LittleEndian ENCODE of a LittleEndian-DECODED value
// does exactly that (decode-then-encode at the same endianness is a no-op on
// the underlying bytes).
//
// The previous implementation used binary.BigEndian.PutUint32 here, which
// reverses the bytes a SECOND time and renders every address backwards —
// confirmed live: a curl to github.com (140.82.114.3) was logged with
// dst_ip="3.114.82.140". See TestUint32ToIP's 0x0372528C case.
func uint32ToIP(ip uint32) net.IP {
	result := make(net.IP, 4)
	binary.LittleEndian.PutUint32(result, ip)
	return result
}

// normalizeDstPort corrects the ring-buffer dst_port field to host byte
// order. Unlike the IP fields, the BPF-side emit paths do NOT agree on what
// byte order they hand off, so the fix is conditioned on the event's layer
// rather than applied uniformly:
//
//   - LAYER_CONNECT4 (pkg/ebpf/bpf.c's connect4 program, ~line 186):
//     `__u16 dst_port = (__u16)ctx->user_port;` — stored RAW, with no
//     conversion. bpf.c's own adjacent comment claims "user_port is in host
//     byte order (modern kernels 5.x+)" — that comment is wrong. Confirmed
//     live: a connection to port 443 was recorded as dst_port=47873
//     (0xBB01), the byte-reversal of 0x01BB=443. These events need a swap.
//
//   - LAYER_SENDMSG4 (bpf.c's sendmsg4 program, ~line 266) and
//     LAYER_EGRESS_SKB (bpf.c's egress_filter program, near its
//     emit_event_skb call): both call bpf_ntohs() on the port THEMSELVES,
//     in C, before ever calling emit_event/emit_event_skb — so the value
//     already sitting in the ring buffer for these two layers is host byte
//     order. Swapping it again would corrupt it (sendmsg4's dst_port is
//     always 53 by construction — a second swap would turn it into 52021).
//
// LAYER_SOCKOPS never calls emit_event at all, so it never reaches here;
// an unrecognized layer value is left unswapped, the same fail-safe default
// as the two already-correct layers.
func normalizeDstPort(layer uint8, port uint16) uint16 {
	if layer == layerConnect4 {
		return bits.ReverseBytes16(port)
	}
	return port
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

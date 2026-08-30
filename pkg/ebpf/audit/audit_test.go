// Tests for audit helper functions. No build tag — these run on all platforms.
package audit

import (
	"net"
	"testing"
)

func TestActionString(t *testing.T) {
	tests := []struct {
		action   uint8
		expected string
	}{
		{actionDeny, "deny"},
		{actionAllow, "allow"},
		{actionRedirect, "redirect"},
		{255, "unknown"},
	}
	for _, tc := range tests {
		got := actionString(tc.action)
		if got != tc.expected {
			t.Errorf("actionString(%d) = %q, want %q", tc.action, got, tc.expected)
		}
	}
}

func TestLayerString(t *testing.T) {
	tests := []struct {
		layer    uint8
		expected string
	}{
		{layerConnect4, "connect4"},
		{layerSendmsg4, "sendmsg4"},
		{layerEgressSKB, "egress_skb"},
		{layerSockops, "sockops"},
		{0, "unknown"},
		{255, "unknown"},
	}
	for _, tc := range tests {
		got := layerString(tc.layer)
		if got != tc.expected {
			t.Errorf("layerString(%d) = %q, want %q", tc.layer, got, tc.expected)
		}
	}
}

// dottedQuadToLE builds the uint32 value a LittleEndian binary.Read produces
// for a bpfEvent IP field the BPF side populated, in memory, with the four
// wire-order octets of a.b.c.d — i.e. exactly the value uint32ToIP must turn
// back into "a.b.c.d". Test data is built through this helper (rather than
// hand-computed hex) specifically so it can't accidentally encode the same
// byte-order mistake the previous uint32ToIP implementation made.
func dottedQuadToLE(a, b, c, d byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24
}

func TestUint32ToIP(t *testing.T) {
	tests := []struct {
		input    uint32
		expected string
	}{
		// The load-bearing regression case: this exact value was observed
		// live on a curl to github.com, whose real destination was
		// 140.82.114.3. The PREVIOUS uint32ToIP (binary.BigEndian.PutUint32)
		// rendered 0x0372528C as "3.114.82.140" — the address reversed.
		// binary.LittleEndian.PutUint32 is what recovers the true address.
		{0x0372528C, "140.82.114.3"},
		{dottedQuadToLE(10, 0, 1, 5), "10.0.1.5"},
		{dottedQuadToLE(169, 254, 169, 254), "169.254.169.254"},
		{dottedQuadToLE(127, 0, 0, 1), "127.0.0.1"},
		// Byte-palindromic addresses render identically under BigEndian OR
		// LittleEndian conversion, so these two cases alone could never have
		// caught the original bug — kept only as a baseline sanity check.
		{0x01010101, "1.1.1.1"},
		{0x00000000, "0.0.0.0"},
		{0xFFFFFFFF, "255.255.255.255"},
	}
	for _, tc := range tests {
		got := uint32ToIP(tc.input)
		expected := net.ParseIP(tc.expected).To4()
		if !got.Equal(expected) {
			t.Errorf("uint32ToIP(0x%08X) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}

func TestNormalizeDstPort(t *testing.T) {
	tests := []struct {
		name  string
		layer uint8
		port  uint16
		want  uint16
	}{
		// connect4 stores the RAW bpf_sock_addr.user_port with no
		// conversion (bpf.c's inline comment claiming it's already host
		// byte order is wrong) — this is the live-observed regression:
		// a connection to port 443 was recorded as dst_port=0xBB01=47873.
		{"connect4 swaps network-order port", layerConnect4, 0xBB01, 443},
		{"connect4 swaps another port", layerConnect4, 0x5000, 80},
		// sendmsg4 calls bpf_ntohs() on the BPF side BEFORE emitting, so its
		// dst_port is already host order (and always 53, since sendmsg4
		// only ever fires for DNS) — swapping it again would corrupt it to
		// 52021.
		{"sendmsg4 already host order, no swap", layerSendmsg4, 53, 53},
		// egress_skb also calls bpf_ntohs() on the BPF side before calling
		// emit_event_skb — already host order, no swap.
		{"egress_skb already host order, no swap", layerEgressSKB, 8080, 8080},
		// sockops never emits an event, but an unrecognized/unused layer
		// value must fail safe (unswapped), same as the two layers above.
		{"unknown layer, no swap", layerSockops, 22, 22},
		{"unrecognized layer value, no swap", 255, 12345, 12345},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeDstPort(tc.layer, tc.port)
			if got != tc.want {
				t.Errorf("normalizeDstPort(layer=%d, 0x%04X) = %d, want %d", tc.layer, tc.port, got, tc.want)
			}
		})
	}
}

func TestNullTermString(t *testing.T) {
	tests := []struct {
		input    []byte
		expected string
	}{
		{[]byte{'c', 'u', 'r', 'l', 0, 0, 0, 0}, "curl"},
		{[]byte{'n', 'g', 'i', 'n', 'x', 0}, "nginx"},
		{[]byte{0, 0, 0, 0}, ""},
		{[]byte{'a', 'b', 'c'}, "abc"}, // no null terminator
	}
	for _, tc := range tests {
		got := nullTermString(tc.input)
		if got != tc.expected {
			t.Errorf("nullTermString(%v) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

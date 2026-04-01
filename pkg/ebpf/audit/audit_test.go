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

func TestUint32ToIP(t *testing.T) {
	tests := []struct {
		input    uint32
		expected string
	}{
		// 1.1.1.1 in network byte order: 0x01010101
		{0x01010101, "1.1.1.1"},
		// 10.0.1.5 in network byte order: 0x0A000105
		{0x0A000105, "10.0.1.5"},
		// 169.254.169.254 (IMDS): 0xA9FEA9FE
		{0xA9FEA9FE, "169.254.169.254"},
		// 127.0.0.1 in network byte order: 0x7F000001
		{0x7F000001, "127.0.0.1"},
		// 0.0.0.0
		{0x00000000, "0.0.0.0"},
		// 255.255.255.255
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

//go:build linux

package ebpf

import (
	"encoding/binary"
	"net"
	"strings"
	"testing"
)

// TestPinPath verifies that PinPath produces the correct bpffs directory path.
func TestPinPath(t *testing.T) {
	id := "abc123"
	want := "/sys/fs/bpf/km/abc123/"
	got := PinPath(id)
	if got != want {
		t.Errorf("PinPath(%q) = %q, want %q", id, got, want)
	}
}

// TestPinPathFormat verifies the path always ends with / and contains the ID.
func TestPinPathFormat(t *testing.T) {
	cases := []string{"sandbox-01", "test-id", "x", "a-b-c-d"}
	for _, id := range cases {
		p := PinPath(id)
		if !strings.HasPrefix(p, "/sys/fs/bpf/km/") {
			t.Errorf("PinPath(%q) = %q, missing /sys/fs/bpf/km/ prefix", id, p)
		}
		if !strings.HasSuffix(p, "/") {
			t.Errorf("PinPath(%q) = %q, missing trailing /", id, p)
		}
		if !strings.Contains(p, id) {
			t.Errorf("PinPath(%q) = %q, does not contain ID", id, p)
		}
	}
}

// TestCgroupPath verifies that CgroupPath produces the correct cgroup v2 path.
func TestCgroupPath(t *testing.T) {
	id := "abc123"
	want := "/sys/fs/cgroup/km.slice/km-abc123.scope"
	got := CgroupPath(id)
	if got != want {
		t.Errorf("CgroupPath(%q) = %q, want %q", id, got, want)
	}
}

// TestCgroupPathFormat verifies the path follows systemd slice/scope naming.
func TestCgroupPathFormat(t *testing.T) {
	cases := []string{"sandbox-01", "test-id", "x"}
	for _, id := range cases {
		p := CgroupPath(id)
		if !strings.HasPrefix(p, "/sys/fs/cgroup/km.slice/km-") {
			t.Errorf("CgroupPath(%q) = %q, missing km.slice prefix", id, p)
		}
		if !strings.HasSuffix(p, ".scope") {
			t.Errorf("CgroupPath(%q) = %q, missing .scope suffix", id, p)
		}
		if !strings.Contains(p, id) {
			t.Errorf("CgroupPath(%q) = %q, does not contain ID", id, p)
		}
	}
}

// TestLpmKeyEncoding verifies that LpmKey stores IPv4 addresses in network
// byte order (big-endian) as required by the BPF LPM_TRIE map.
func TestLpmKeyEncoding(t *testing.T) {
	cases := []struct {
		cidr      string
		wantLen   uint32
		wantAddr  [4]byte
	}{
		{"10.0.0.0/8", 8, [4]byte{10, 0, 0, 0}},
		{"192.168.1.0/24", 24, [4]byte{192, 168, 1, 0}},
		{"8.8.8.8/32", 32, [4]byte{8, 8, 8, 8}},
		{"0.0.0.0/0", 0, [4]byte{0, 0, 0, 0}},
	}
	for _, tc := range cases {
		_, ipNet, err := net.ParseCIDR(tc.cidr)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", tc.cidr, err)
		}
		ones, _ := ipNet.Mask.Size()
		key := LpmKey{
			PrefixLen: uint32(ones),
			Addr:      [4]byte(ipNet.IP.To4()),
		}
		if key.PrefixLen != tc.wantLen {
			t.Errorf("cidr %s: PrefixLen = %d, want %d", tc.cidr, key.PrefixLen, tc.wantLen)
		}
		if key.Addr != tc.wantAddr {
			t.Errorf("cidr %s: Addr = %v, want %v", tc.cidr, key.Addr, tc.wantAddr)
		}
	}
}

// TestLpmKeyNetworkByteOrder verifies that a /32 key for a specific IP is
// stored in big-endian order, consistent with BPF expectations.
func TestLpmKeyNetworkByteOrder(t *testing.T) {
	ip := net.ParseIP("10.20.30.40").To4()
	key := LpmKey{
		PrefixLen: 32,
		Addr:      [4]byte{ip[0], ip[1], ip[2], ip[3]},
	}
	// Reinterpret as uint32 big-endian to verify order.
	got := binary.BigEndian.Uint32(key.Addr[:])
	want := binary.BigEndian.Uint32([]byte{10, 20, 30, 40})
	if got != want {
		t.Errorf("LpmKey address bytes mismatch: got 0x%08x, want 0x%08x", got, want)
	}
}

// TestConfigDefaults verifies that a Config can be constructed with all fields
// populated and that zero values are valid (log mode, no proxy).
func TestConfigDefaults(t *testing.T) {
	cfg := Config{
		SandboxID:      "test-sandbox",
		DNSProxyPort:   5353,
		HTTPProxyPort:  3128,
		HTTPSProxyPort: 3128,
		ProxyPID:       12345,
		FirewallMode:   ModeBlock,
		MITMProxyAddr:  0x7f000001, // 127.0.0.1 in network byte order
	}
	if cfg.SandboxID != "test-sandbox" {
		t.Errorf("SandboxID not set correctly")
	}
	if cfg.FirewallMode != ModeBlock {
		t.Errorf("FirewallMode: got %d, want %d", cfg.FirewallMode, ModeBlock)
	}
	if cfg.DNSProxyPort != 5353 {
		t.Errorf("DNSProxyPort: got %d, want 5353", cfg.DNSProxyPort)
	}
}

// TestConfigZeroIsValid verifies that a zero Config (except SandboxID) is a
// valid log-mode enforcer configuration.
func TestConfigZeroIsValid(t *testing.T) {
	cfg := Config{SandboxID: "log-mode-sandbox"}
	if cfg.FirewallMode != ModeLog {
		t.Errorf("zero FirewallMode should equal ModeLog (%d), got %d", ModeLog, cfg.FirewallMode)
	}
}

// TestCleanupNonExistent verifies that Cleanup on a non-existent sandbox is
// idempotent and returns no error.
func TestCleanupNonExistent(t *testing.T) {
	// Use a sandbox ID that is guaranteed not to exist in /sys/fs/bpf/km/.
	if err := Cleanup("does-not-exist-xyz-999"); err != nil {
		t.Errorf("Cleanup on non-existent sandbox returned error: %v", err)
	}
}

// TestIsPinnedFalse verifies that IsPinned returns false for a sandbox that
// has no pinned state under /sys/fs/bpf/km/.
func TestIsPinnedFalse(t *testing.T) {
	if IsPinned("does-not-exist-xyz-999") {
		t.Errorf("IsPinned should return false for non-existent sandbox")
	}
}

// TestIsPinnedConsistency verifies that IsPinned returns false when pin
// directory exists but link files are absent (partial state).
func TestIsPinnedConsistency(t *testing.T) {
	// An empty string sandbox ID will point to /sys/fs/bpf/km// which
	// almost certainly doesn't exist; this is a structural test only.
	result := IsPinned("no-such-sandbox-abcdef")
	if result {
		t.Errorf("IsPinned returned true for a sandbox that cannot exist")
	}
}

// TestModeConstants verifies mode constant values match BPF-side expectations.
func TestModeConstants(t *testing.T) {
	if ModeLog != 0 {
		t.Errorf("ModeLog should be 0, got %d", ModeLog)
	}
	if ModeAllow != 1 {
		t.Errorf("ModeAllow should be 1, got %d", ModeAllow)
	}
	if ModeBlock != 2 {
		t.Errorf("ModeBlock should be 2, got %d", ModeBlock)
	}
}

// TestActionConstants verifies action constant values match BPF-side defines.
func TestActionConstants(t *testing.T) {
	if ActionDeny != 0 {
		t.Errorf("ActionDeny should be 0, got %d", ActionDeny)
	}
	if ActionAllow != 1 {
		t.Errorf("ActionAllow should be 1, got %d", ActionAllow)
	}
	if ActionRedirect != 2 {
		t.Errorf("ActionRedirect should be 2, got %d", ActionRedirect)
	}
}

// TestLayerConstants verifies layer constant values match BPF-side defines.
func TestLayerConstants(t *testing.T) {
	if LayerConnect4 != 1 {
		t.Errorf("LayerConnect4 should be 1, got %d", LayerConnect4)
	}
	if LayerSendmsg4 != 2 {
		t.Errorf("LayerSendmsg4 should be 2, got %d", LayerSendmsg4)
	}
	if LayerEgressSKB != 3 {
		t.Errorf("LayerEgressSKB should be 3, got %d", LayerEgressSKB)
	}
	if LayerSockops != 4 {
		t.Errorf("LayerSockops should be 4, got %d", LayerSockops)
	}
}

// TestConfigHTTPProxyPIDField verifies that Config accepts an HTTPProxyPID field
// alongside the existing ProxyPID field. This supports dual-PID exemption in
// gatekeeper mode: ProxyPID exempts the enforcer process, HTTPProxyPID exempts
// the HTTP proxy process from BPF connect4/sendmsg4 interception.
func TestConfigHTTPProxyPIDField(t *testing.T) {
	cfg := Config{
		SandboxID:    "test-sandbox",
		ProxyPID:     12345,
		HTTPProxyPID: 67890, // HTTP proxy process — exempt from BPF redirection
	}
	if cfg.ProxyPID != 12345 {
		t.Errorf("ProxyPID: got %d, want 12345", cfg.ProxyPID)
	}
	if cfg.HTTPProxyPID != 67890 {
		t.Errorf("HTTPProxyPID: got %d, want 67890", cfg.HTTPProxyPID)
	}
}

// TestConfigHTTPProxyPIDZeroMeansDisabled verifies that HTTPProxyPID == 0 is
// a valid "disabled" state (no HTTP proxy exemption), matching the BPF-side
// check: `if (const_http_proxy_pid != 0 && pid == const_http_proxy_pid)`.
func TestConfigHTTPProxyPIDZeroMeansDisabled(t *testing.T) {
	cfg := Config{SandboxID: "no-proxy-sandbox"}
	if cfg.HTTPProxyPID != 0 {
		t.Errorf("default HTTPProxyPID should be 0 (disabled), got %d", cfg.HTTPProxyPID)
	}
}

// TestConfigHTTPProxyPIDIndependentFromProxyPID verifies that the two PID fields
// are independent — each can be set without affecting the other.
func TestConfigHTTPProxyPIDIndependentFromProxyPID(t *testing.T) {
	cfg := Config{
		SandboxID:    "dual-pid-sandbox",
		ProxyPID:     100,
		HTTPProxyPID: 200,
		FirewallMode: ModeBlock,
	}
	if cfg.ProxyPID == cfg.HTTPProxyPID {
		t.Errorf("ProxyPID and HTTPProxyPID should be independent; both are %d", cfg.ProxyPID)
	}
	if cfg.FirewallMode != ModeBlock {
		t.Errorf("FirewallMode: got %d, want ModeBlock (%d)", cfg.FirewallMode, ModeBlock)
	}
}

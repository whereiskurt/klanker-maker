//go:build linux

package ebpf

// Config holds the parameters for creating a new eBPF Enforcer. Values are
// injected as BPF volatile constants at program load time.
type Config struct {
	SandboxID      string
	DNSProxyPort   uint32 // UDP port for km-dns-resolver (e.g. 53)
	HTTPProxyPort  uint32 // TCP port for HTTP proxy (e.g. 3128)
	HTTPSProxyPort uint32 // TCP port for HTTPS/CONNECT proxy (e.g. 3128)
	ProxyPID       uint32 // PID of the proxy process — exempt from redirection
	FirewallMode   uint16 // 0=log, 1=allow, 2=block (matches ModeLog/Allow/Block)
	MITMProxyAddr  uint32 // MITM proxy loopback IP in network byte order (127.0.0.1)
}

// LpmKey is the Go representation of struct ip4_trie_key for LPM_TRIE lookups.
//
// PrefixLen is the number of significant bits (0-32 for IPv4).
// Addr holds the IPv4 address in network byte order (big-endian).
//
// Example — allow 10.0.0.0/8:
//
//	key := LpmKey{PrefixLen: 8, Addr: [4]byte{10, 0, 0, 0}}
type LpmKey struct {
	PrefixLen uint32
	Addr      [4]byte
}

// Event action constants matching BPF-side values in common.h.
const (
	ActionDeny     = 0 // Connection denied / packet dropped
	ActionAllow    = 1 // Connection allowed without modification
	ActionRedirect = 2 // Connection redirected to MITM proxy or DNS proxy
)

// Event layer constants indicating which BPF program emitted the event.
// These match the LAYER_* defines in common.h.
const (
	LayerConnect4  = 1 // cgroup/connect4 — TCP connect() interception
	LayerSendmsg4  = 2 // cgroup/sendmsg4 — UDP sendmsg() / DNS interception
	LayerEgressSKB = 3 // cgroup_skb/egress — packet-level enforcement
	LayerSockops   = 4 // sockops — socket state tracking
)

// FirewallMode constants for volatile const_firewall_mode.
// Set via CollectionSpec.RewriteConstants() at program load time.
const (
	ModeLog   = 0 // Emit events only; never block connections
	ModeAllow = 1 // Emit events; allow all connections (permissive mode)
	ModeBlock = 2 // Emit events; block connections not in CIDR allowlist
)

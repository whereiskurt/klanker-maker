//go:build linux

package ebpf

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

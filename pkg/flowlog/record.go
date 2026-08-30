// Package flowlog holds the egress-observation vocabulary shared by the DNS
// proxy, the HTTP proxy, the eBPF audit consumer, and the on-box km-netpolicy
// helper.
//
// It is the read side of what pkg/netpolicy is the write side of: netpolicy
// decides what a sandbox may reach, flowlog records what it actually reached.
//
// Each producer owns exactly one file and appends to it. Nothing merges at
// write time. A single shared file would put three writers in a rotation race;
// a socket daemon would add a process whose death silently loses records.
// Per-producer files also degrade honestly — a dead producer reads as "that
// source is missing", never as a corrupt store.
package flowlog

import "time"

// Producer identifiers. These name the observing component, not the protocol:
// a CONNECT to :443 seen by the HTTP proxy is SrcHTTP even though the payload
// is TLS.
const (
	SrcEBPF = "ebpf"
	SrcDNS  = "dns"
	SrcHTTP = "http"
	// SrcResolver is the eBPF path's DNS server. It is a separate producer from
	// SrcDNS even though both answer DNS: under ebpf/both the bootstrap leaves
	// km-dns-proxy disabled and this resolver serves every query instead, and
	// naming it honestly is what lets `flows` tell an operator WHICH component
	// decided. Distinct files also mean the two can never race on one file if a
	// future profile ever runs both.
	SrcResolver = "resolver"
)

// Verdicts, matching the eBPF ActionDeny/Allow/Redirect vocabulary so a record
// means the same thing regardless of which producer wrote it.
const (
	VerdictAllow    = "allow"
	VerdictDeny     = "deny"
	VerdictRedirect = "redirect"
)

// DefaultDir is where producers write and km-netpolicy reads.
//
// Under /var/lib rather than /run for the same reason the deny list is: /run is
// a tmpfs cleared on boot, and a census silently emptied by a reboot would
// invite an operator to pin a set far narrower than the box actually needs.
const DefaultDir = "/var/lib/km/flows"

// DefaultMaxBytes caps each producer's live file before rotation. One previous
// generation is retained, so a producer costs at most 2x this on disk.
const DefaultMaxBytes int64 = 16 << 20 // 16 MiB

// Record is one observed egress decision.
//
// Producers fill only what they observed and omit the rest: the DNS proxy knows
// a name and a verdict but not the eventual peer; the HTTP proxy knows host,
// port and verdict; the eBPF path knows address, port, pid and comm but not the
// name. Every optional field is omitempty because a zero value here would be
// indistinguishable from an observation of zero, and an operator pins against
// this data.
type Record struct {
	TS      time.Time `json:"ts"`
	Src     string    `json:"src"`
	Verdict string    `json:"verdict"`
	Host    string    `json:"host,omitempty"`
	Addr    string    `json:"addr,omitempty"`
	Port    int       `json:"port,omitempty"`
	Proto   string    `json:"proto,omitempty"`
	PID     int       `json:"pid,omitempty"`
	Comm    string    `json:"comm,omitempty"`
}

// FileFor returns the conventional file path for a producer within dir.
func FileFor(dir, src string) string { return dir + "/flows." + src + ".jsonl" }

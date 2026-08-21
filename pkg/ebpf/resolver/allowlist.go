// Package resolver provides a userspace DNS resolver daemon for the eBPF
// network enforcement layer. It receives DNS queries redirected by the BPF
// sendmsg4 program, enforces the domain allowlist, resolves allowed domains
// via an upstream DNS server, and pushes resolved IPs into BPF maps.
package resolver

import (
	"net"
	"strings"
	"sync"
	"time"
)

// resolvedEntry stores the IPs resolved for a domain and the time at which
// the entry expires (derived from the DNS TTL at resolution time).
type resolvedEntry struct {
	ips    []net.IP
	expiry time.Time
}

// Allowlist holds the set of permitted domain suffixes and a cache of
// previously resolved IPs with TTL-based expiry.
//
// All methods are safe for concurrent use.
type Allowlist struct {
	// suffixes stores the normalized allowed suffixes (lowercase, no leading
	// dot). The IsAllowed logic mirrors sidecars/dns-proxy/dnsproxy.IsAllowed.
	suffixes []string
	// allowAll is true when the suffixes list contains "*", meaning all domains are permitted.
	allowAll bool
	// denied stores the normalized denied suffixes. A match here blocks the
	// name regardless of what suffixes or allowAll say.
	denied []string
	// denyAll is true when the denied list contains "*", sealing the sandbox.
	denyAll bool

	mu       sync.RWMutex
	resolved map[string]resolvedEntry // domain (without trailing dot) -> entry
}

// NewAllowlist creates an Allowlist from slices of allowed and denied domain
// suffixes. Both lists are normalized: lowercased, trailing dots stripped,
// leading dots stripped. Both ".github.com" and "github.com" are equivalent.
//
// denied takes precedence over suffixes, including over the "*" wildcard. A nil
// or empty denied list blocks nothing and leaves behaviour unchanged.
func NewAllowlist(suffixes, denied []string) *Allowlist {
	allowSet, allowAll := normalizeSuffixes(suffixes)
	denySet, denyAll := normalizeSuffixes(denied)
	return &Allowlist{
		suffixes: allowSet,
		allowAll: allowAll,
		denied:   denySet,
		denyAll:  denyAll,
		resolved: make(map[string]resolvedEntry),
	}
}

// normalizeSuffixes lowercases entries, strips leading and trailing dots, drops
// empties, and reports whether the list contained the "*" wildcard.
func normalizeSuffixes(in []string) (out []string, star bool) {
	out = make([]string, 0, len(in))
	for _, s := range in {
		if s == "*" {
			star = true
			continue
		}
		s = strings.ToLower(strings.TrimSuffix(s, "."))
		s = strings.TrimPrefix(s, ".") // handle ".amazonaws.com" format
		if s != "" {
			out = append(out, s)
		}
	}
	return out, star
}

// IsAllowed reports whether name is permitted by the allowlist.
// The trailing DNS dot is stripped and comparison is case-insensitive.
// A domain is allowed if it equals a suffix exactly, or if it ends with
// ".<suffix>". An empty suffixes list denies everything. An empty name
// is always denied.
//
// This is the same algorithm as sidecars/dns-proxy/dnsproxy.IsAllowed.
func (a *Allowlist) IsAllowed(name string) bool {
	name = strings.TrimSuffix(name, ".")
	name = strings.ToLower(name)
	if name == "" {
		return false
	}

	// Deny is evaluated first and beats every allow, including allowAll.
	if a.isDeniedNormalized(name) {
		return false
	}

	if a.allowAll {
		return true
	}
	return matchesAny(name, a.suffixes)
}

// IsDenied reports whether name is on the deny list, independent of whether the
// allowlist would otherwise have permitted it.
//
// `km ebpf-attach` uses this to decide which allowed hosts to pre-resolve into
// the BPF trie at startup. Seeding a denied host's IPs there would let a caller
// reach it by address even though the resolver refuses to answer for the name.
func (a *Allowlist) IsDenied(name string) bool {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if name == "" {
		return false
	}
	return a.isDeniedNormalized(name)
}

// isDeniedNormalized expects name already lowercased with its trailing dot
// stripped.
func (a *Allowlist) isDeniedNormalized(name string) bool {
	if a.denyAll {
		return true
	}
	return matchesAny(name, a.denied)
}

// matchesAny reports whether name equals any normalized suffix or ends with
// ".<suffix>". name must already be lowercased with its trailing dot stripped.
func matchesAny(name string, suffixes []string) bool {
	for _, s := range suffixes {
		if name == s || strings.HasSuffix(name, "."+s) {
			return true
		}
	}
	return false
}

// AddResolved stores the IPs resolved for domain with an expiry based on ttl.
// A subsequent call with the same domain replaces the previous entry.
// Thread-safe.
func (a *Allowlist) AddResolved(domain string, ips []net.IP, ttl time.Duration) {
	domain = strings.TrimSuffix(strings.ToLower(domain), ".")
	// Make a defensive copy of the IP slice.
	cp := make([]net.IP, len(ips))
	for i, ip := range ips {
		tmp := make(net.IP, len(ip))
		copy(tmp, ip)
		cp[i] = tmp
	}
	a.mu.Lock()
	a.resolved[domain] = resolvedEntry{ips: cp, expiry: time.Now().Add(ttl)}
	a.mu.Unlock()
}

// Sweep removes all expired resolved entries and returns the IPs that were
// evicted. The caller may use the returned IPs to revoke them from the BPF
// allowlist map.
// Thread-safe.
func (a *Allowlist) Sweep() []net.IP {
	now := time.Now()
	var evicted []net.IP

	a.mu.Lock()
	for domain, entry := range a.resolved {
		if now.After(entry.expiry) {
			evicted = append(evicted, entry.ips...)
			delete(a.resolved, domain)
		}
	}
	a.mu.Unlock()

	return evicted
}

// IsResolved reports whether ip is present in any active (non-expired)
// resolved entry. Expired entries are not removed by this call; use Sweep()
// for that. Thread-safe.
func (a *Allowlist) IsResolved(ip net.IP) bool {
	now := time.Now()
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, entry := range a.resolved {
		if now.After(entry.expiry) {
			continue
		}
		for _, resolved := range entry.ips {
			if resolved.Equal(ip) {
				return true
			}
		}
	}
	return false
}

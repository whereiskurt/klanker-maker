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

	mu       sync.RWMutex
	resolved map[string]resolvedEntry // domain (without trailing dot) -> entry
}

// NewAllowlist creates an Allowlist from a slice of domain suffixes.
// Suffixes are normalized: lowercased, trailing dots stripped, leading dots
// stripped. Both ".github.com" and "github.com" are equivalent.
func NewAllowlist(suffixes []string) *Allowlist {
	normalized := make([]string, 0, len(suffixes))
	for _, s := range suffixes {
		s = strings.ToLower(strings.TrimSuffix(s, "."))
		s = strings.TrimPrefix(s, ".") // handle ".amazonaws.com" format
		if s != "" {
			normalized = append(normalized, s)
		}
	}
	return &Allowlist{
		suffixes: normalized,
		resolved: make(map[string]resolvedEntry),
	}
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
	for _, s := range a.suffixes {
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

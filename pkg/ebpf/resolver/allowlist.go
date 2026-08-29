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

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
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
	// denier answers the deny question, unioning the profile-baked list with an
	// optional runtime file the sandbox appends to itself. May be nil.
	denier *netpolicy.Denier
	// pinner narrows whatever the allowlist permits down to what the sandbox
	// has actually pinned. May be nil.
	pinner *netpolicy.Pinner

	mu       sync.RWMutex
	resolved map[string]resolvedEntry // domain (without trailing dot) -> entry
}

// NewAllowlist creates an Allowlist from a slice of allowed domain suffixes and
// an optional Denier. Suffixes are normalized: lowercased, trailing dots
// stripped, leading dots stripped. Both ".github.com" and "github.com" are
// equivalent.
//
// denier takes precedence over suffixes, including over the "*" wildcard, and is
// consulted per query rather than snapshotted — so a deny the sandbox appends to
// its runtime list is honoured by an already-running resolver. A nil denier
// blocks nothing.
//
// pinner is applied after allowAll/suffix matching, so it narrows exactly what
// the allowlist would otherwise have permitted. A nil pinner allows everything,
// which is the shape for a sandbox that has never pinned.
func NewAllowlist(suffixes []string, denier *netpolicy.Denier, pinner *netpolicy.Pinner) *Allowlist {
	allowSet, allowAll := normalizeSuffixes(suffixes)
	return &Allowlist{
		suffixes: allowSet,
		allowAll: allowAll,
		denier:   denier,
		pinner:   pinner,
		resolved: make(map[string]resolvedEntry),
	}
}

// denierFor builds the resolver's Denier from its config, returning nil when the
// sandbox declares no denies at all so the hot path does no deny work.
func denierFor(cfg ResolverConfig) *netpolicy.Denier {
	var store *netpolicy.Store
	if cfg.RuntimeDenyFile != "" {
		store = netpolicy.NewStore(cfg.RuntimeDenyFile, netpolicy.DefaultReloadInterval)
	}
	if len(cfg.DeniedSuffixes) == 0 && store == nil {
		return nil
	}
	return netpolicy.NewDenier(cfg.DeniedSuffixes, store)
}

// pinnerFor builds the resolver's Pinner from its config, mirroring denierFor.
// A nil pinner (empty PinFile) allows everything, which is the resolver's
// pre-pin behaviour.
func pinnerFor(cfg ResolverConfig) *netpolicy.Pinner {
	var store *netpolicy.PinStore
	if cfg.PinFile != "" {
		store = netpolicy.NewPinStore(cfg.PinFile, netpolicy.DefaultReloadInterval)
	}
	return netpolicy.NewPinner(store)
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
	if a.denier.IsDenied(name) {
		return false
	}

	if !a.allowAll && !matchesAny(name, a.suffixes) {
		return false
	}

	// Pins narrow whatever the allowlist permitted. Applied after allowAll so a
	// wide-open profile collapses to exactly the pinned set — the motivating
	// case, since * ∩ observed = observed.
	if !a.pinner.Allows(name) {
		return false
	}

	return true
}

// IsDenied reports whether name is on the deny list, independent of whether the
// allowlist would otherwise have permitted it.
//
// `km ebpf-attach` uses this to decide which allowed hosts to pre-resolve into
// the BPF trie at startup. Seeding a denied host's IPs there would let a caller
// reach it by address even though the resolver refuses to answer for the name.
func (a *Allowlist) IsDenied(name string) bool {
	return a.denier.IsDenied(name)
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

// NameForIP returns the domain that resolved to ip, or "" if this resolver
// never handed that address out.
//
// Expired entries are deliberately still consulted: a connection to an
// address whose TTL has since lapsed was still made to that name, and the
// census is a record of what happened, not of what is currently resolvable.
// This is why NameForIP does NOT reuse IsResolved's active-only filter.
func (a *Allowlist) NameForIP(ip string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for domain, entry := range a.resolved {
		for _, got := range entry.ips {
			if got.String() == ip {
				return domain
			}
		}
	}
	return ""
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

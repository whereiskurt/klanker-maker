// Package netpolicy holds the egress deny-list vocabulary shared by the DNS
// proxy, the HTTP proxy, the eBPF resolver, and the on-box km-netpolicy helper.
//
// A sandbox's effective deny set is the union of two sources:
//
//   - the static list baked in from the profile at create time
//     (spec.network.egress.denied*), delivered as process env
//   - the runtime list a sandbox appends to itself from user-land
//
// Union is the only combining rule, and it is what makes runtime mutation safe:
// adding an entry can only ever shrink the reachable set, so "never widens" is a
// property of the data structure rather than a policy check that has to be
// trusted. The runtime file is held append-only by the kernel (chattr +a), so
// the same property holds at the filesystem layer.
package netpolicy

import (
	"strings"
)

// maxNameLen is the maximum length of a fully-qualified DNS name.
const maxNameLen = 253

// ParseLine normalizes a single deny-list line and reports whether it yielded a
// usable pattern.
//
// Blank lines and "#" comments return ok=false without being errors. So does
// anything malformed: a line carrying a scheme, a path, a port, whitespace, or
// a control character is DROPPED rather than accepted. A pattern that silently
// matches nothing would be worse than no pattern at all, because the operator
// would believe a destination was blocked when it was not.
//
// A leading dot is preserved (it is equivalent to its absence when matching, but
// keeping it means `km-netpolicy list` echoes back what the caller wrote). The
// trailing dot is stripped and the result is lowercased.
func ParseLine(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", false
	}

	// "*" is the seal-everything pattern and the only one containing a star.
	// "*.example.com" is rejected rather than silently treated as a suffix,
	// because a caller writing it clearly expects glob semantics that this
	// matcher does not implement.
	if s == "*" {
		return "*", true
	}
	if strings.ContainsRune(s, '*') {
		return "", false
	}

	if strings.ContainsAny(s, " \t\r\n/:\\?#@") {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}

	s = strings.ToLower(strings.TrimSuffix(s, "."))
	// A bare "." (or a line that was only dots) carries no meaning.
	if s == "" || strings.Trim(s, ".") == "" {
		return "", false
	}
	if len(s) > maxNameLen {
		return "", false
	}
	return s, true
}

// ParseLines applies ParseLine to every line of a deny-list file body and
// returns the accepted patterns plus the count of lines that were dropped as
// malformed. Blank lines and comments are not counted as dropped.
func ParseLines(body string) (patterns []string, dropped int) {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		p, ok := ParseLine(line)
		if !ok {
			dropped++
			continue
		}
		patterns = append(patterns, p)
	}
	return patterns, dropped
}

// Match reports whether host matches any of the patterns.
//
// This is the single canonical deny matcher. The DNS proxy, the HTTP proxy and
// the eBPF resolver all route through it so a name can never be blocked at one
// layer and reachable at another.
//
// A port is stripped, the trailing dot is stripped, and comparison is
// case-insensitive. "*" matches everything. Every other pattern matches the apex
// AND any subdomain, with or without a leading dot — deliberately broader than
// the allow-side matcher, where strictness errs toward permitting less. The same
// strictness on a deny list would fail open.
func Match(host string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}

	h := stripPort(host)
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "" {
		return false
	}

	for _, p := range patterns {
		if p == "*" {
			return true
		}
		p = strings.ToLower(strings.TrimSuffix(p, "."))
		p = strings.TrimPrefix(p, ".")
		if p == "" {
			continue
		}
		if h == p || strings.HasSuffix(h, "."+p) {
			return true
		}
	}
	return false
}

// stripPort removes a trailing ":port" from host, leaving bare hostnames and
// bracketed IPv6 literals intact.
func stripPort(host string) string {
	if host == "" {
		return ""
	}
	// Bracketed IPv6 with or without a port.
	if host[0] == '[' {
		if end := strings.LastIndex(host, "]"); end > 0 {
			return host[1:end]
		}
		return host
	}
	// Only treat a single trailing colon group as a port; a bare IPv6 literal
	// has several colons and must be left alone.
	if i := strings.LastIndex(host, ":"); i >= 0 && strings.Count(host, ":") == 1 {
		return host[:i]
	}
	return host
}

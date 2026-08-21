// Package dnsproxy provides DNS filtering proxy logic for the km sidecar.
// It resolves allowed domain names by forwarding to an upstream resolver and
// returns NXDOMAIN for all other names.
package dnsproxy

import (
	"net"
	"strings"

	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
)

// IsAllowed reports whether name is permitted by the suffixes list.
// The trailing DNS dot is stripped before comparison. Matching is
// case-insensitive. A name is allowed if it equals a suffix or ends
// with ".<suffix>". An empty suffixes list denies everything.
func IsAllowed(name string, suffixes []string) bool {
	return matchesSuffix(name, suffixes)
}

// IsDenied reports whether name is blocked by the denied list. Matching is
// identical to IsAllowed — trailing dot stripped, case-insensitive, an entry
// matches the apex and any subdomain, with or without a leading dot — but the
// answer is inverted: an empty list denies nothing, and "*" denies everything.
//
// Callers must consult IsDenied BEFORE IsAllowed. A deny beats every allow,
// including the "*" allow-all wildcard; that ordering is what lets an operator
// layer known-bad hosts on top of a wide-open learn-mode profile.
func IsDenied(name string, denied []string) bool {
	if len(denied) == 0 {
		return false
	}
	return matchesSuffix(name, denied)
}

// matchesSuffix reports whether name matches any entry in patterns, where "*"
// matches everything. Shared by IsAllowed and IsDenied so the two lists can
// never drift in how they interpret a pattern.
func matchesSuffix(name string, patterns []string) bool {
	name = strings.TrimSuffix(name, ".")
	name = strings.ToLower(name)

	for _, s := range patterns {
		if s == "*" {
			return true
		}
		s = strings.ToLower(strings.TrimSuffix(s, "."))
		s = strings.TrimPrefix(s, ".") // handle ".amazonaws.com" format from profile
		if name == s || strings.HasSuffix(name, "."+s) {
			return true
		}
	}
	return false
}

// NewHandler returns a dns.HandlerFunc that enforces the allowlist, with
// deniedSuffixes taking precedence over it. Allowed queries are forwarded to
// upstream (host:port, where port 53 is appended if upstreamAddr has no port).
// Blocked queries receive NXDOMAIN. Every query is logged as a JSON line to
// zerolog, carrying a "denied" field so an explicit deny is distinguishable
// from a name that simply never matched the allowlist.
func NewHandler(allowedSuffixes, deniedSuffixes []string, upstreamAddr, sandboxID string) dns.HandlerFunc {
	// Ensure upstream has a port.
	upstream := upstreamAddr
	if _, _, err := net.SplitHostPort(upstream); err != nil {
		upstream = net.JoinHostPort(upstream, "53")
	}

	return func(w dns.ResponseWriter, r *dns.Msg) {
		if len(r.Question) == 0 {
			m := new(dns.Msg)
			m.SetRcode(r, dns.RcodeFormatError)
			_ = w.WriteMsg(m)
			return
		}

		q := r.Question[0]
		domain := q.Name
		// Deny is evaluated first and beats every allow, including "*".
		denied := IsDenied(domain, deniedSuffixes)
		allowed := !denied && IsAllowed(domain, allowedSuffixes)

		log.Info().
			Str("sandbox_id", sandboxID).
			Str("event_type", "dns_query").
			Str("domain", domain).
			Bool("allowed", allowed).
			Bool("denied", denied).
			Msg("")

		if !allowed {
			m := new(dns.Msg)
			m.SetRcode(r, dns.RcodeNameError)
			_ = w.WriteMsg(m)
			return
		}

		// Forward to upstream.
		client := &dns.Client{}
		resp, _, err := client.Exchange(r, upstream)
		if err != nil {
			log.Error().
				Str("sandbox_id", sandboxID).
				Str("event_type", "dns_upstream_error").
				Str("domain", domain).
				Err(err).
				Msg("")
			m := new(dns.Msg)
			m.SetRcode(r, dns.RcodeServerFailure)
			_ = w.WriteMsg(m)
			return
		}

		_ = w.WriteMsg(resp)
	}
}

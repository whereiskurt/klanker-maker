// Package dnsproxy provides DNS filtering proxy logic for the km sidecar.
// It resolves allowed domain names by forwarding to an upstream resolver and
// returns NXDOMAIN for all other names.
package dnsproxy

import (
	"net"

	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

// IsAllowed reports whether name is permitted by the suffixes list.
// The trailing DNS dot is stripped before comparison. Matching is
// case-insensitive. A name is allowed if it equals a suffix or ends
// with ".<suffix>". An empty suffixes list denies everything.
func IsAllowed(name string, suffixes []string) bool {
	return netpolicy.Match(name, suffixes)
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
	return netpolicy.Match(name, denied)
}

// NewHandler returns a dns.HandlerFunc that enforces the allowlist, with denier
// taking precedence over it. Allowed queries are forwarded to upstream
// (host:port, where port 53 is appended if upstreamAddr has no port). Blocked
// queries receive NXDOMAIN. Every query is logged as a JSON line to zerolog,
// carrying a "denied" field so an explicit deny is distinguishable from a name
// that simply never matched the allowlist.
//
// denier may be nil, which is the shape for a sandbox with no denies at all. It
// is consulted per query rather than snapshotted, so a deny the sandbox appends
// to its runtime list at 10:00 is enforced on the next query without a restart.
//
// pinner may also be nil, which is the shape for a sandbox that has never
// pinned. It is applied as a conjunction AFTER the existing allow check, never
// as a replacement for it — that ordering is what keeps the change monotone:
// A && P is a subset of A for any P.
func NewHandler(allowedSuffixes []string, denier *netpolicy.Denier, pinner *netpolicy.Pinner, upstreamAddr, sandboxID string) dns.HandlerFunc {
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
		denied := denier.IsDenied(domain)
		preAllowed := !denied && IsAllowed(domain, allowedSuffixes)
		allowed := preAllowed && pinner.Allows(domain)

		log.Info().
			Str("sandbox_id", sandboxID).
			Str("event_type", "dns_query").
			Str("domain", domain).
			Bool("allowed", allowed).
			Bool("denied", denied).
			Bool("pinned_out", preAllowed && !pinner.Allows(domain)).
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

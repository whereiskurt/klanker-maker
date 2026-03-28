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
	name = strings.TrimSuffix(name, ".")
	name = strings.ToLower(name)

	for _, s := range suffixes {
		s = strings.ToLower(strings.TrimSuffix(s, "."))
		s = strings.TrimPrefix(s, ".") // handle ".amazonaws.com" format from profile
		if name == s || strings.HasSuffix(name, "."+s) {
			return true
		}
	}
	return false
}

// NewHandler returns a dns.HandlerFunc that enforces the allowlist.
// Allowed queries are forwarded to upstream (host:port, where port 53 is
// appended if upstreamAddr has no port). Denied queries receive NXDOMAIN.
// Every query is logged as a JSON line to zerolog.
func NewHandler(allowedSuffixes []string, upstreamAddr, sandboxID string) dns.HandlerFunc {
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
		allowed := IsAllowed(domain, allowedSuffixes)

		log.Info().
			Str("sandbox_id", sandboxID).
			Str("event_type", "dns_query").
			Str("domain", domain).
			Bool("allowed", allowed).
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

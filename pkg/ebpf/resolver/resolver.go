package resolver

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
)

// MapUpdater is the interface for pushing resolved IPs into BPF maps.
// The Enforcer (pkg/ebpf/enforcer.go) implements this. The interface
// exists so the resolver can be unit-tested without a loaded BPF program.
type MapUpdater interface {
	// AllowIP adds ip to the BPF CIDR allowlist as a /32 host route.
	AllowIP(ip net.IP) error
	// MarkForProxy adds ip to the BPF http_proxy_ips map so that TCP
	// connections to that IP are transparently redirected to the L7 proxy.
	MarkForProxy(ip net.IP) error
}

// ResolverConfig holds all parameters for the DNS resolver daemon.
type ResolverConfig struct {
	// ListenAddr is the UDP/TCP address the daemon listens on.
	// BPF sendmsg4 redirects sandbox DNS to this address.
	// Example: "127.0.0.1:5353"
	ListenAddr string

	// UpstreamAddr is the DNS resolver forwarded queries are sent to.
	// On AWS this is typically the VPC resolver: "169.254.169.253:53".
	// Port 53 is appended if no port is present.
	UpstreamAddr string

	// AllowedSuffixes is the list of permitted domain suffixes from the
	// SandboxProfile. Format mirrors the existing dns-proxy: bare or
	// leading-dot ("github.com" / ".github.com" both accepted).
	AllowedSuffixes []string

	// SandboxID is included in log fields for correlation.
	SandboxID string

	// MapUpdater is called for each resolved IP. Required.
	MapUpdater MapUpdater

	// ProxyHosts lists domain suffixes whose resolved IPs must also be
	// passed to MapUpdater.MarkForProxy (e.g. GitHub API, Bedrock).
	// An IP that matches a proxy host has both AllowIP and MarkForProxy
	// called on it.
	ProxyHosts []string

	// SweepInterval controls how often expired resolved entries are purged.
	// Defaults to 30 seconds if zero.
	SweepInterval time.Duration
}

// Resolver is the DNS resolver daemon.
//
// It binds a DNS server on ResolverConfig.ListenAddr (UDP + TCP), enforces
// the domain allowlist, forwards allowed A queries to the upstream resolver,
// pushes resolved IPs into BPF maps via MapUpdater, and refuses all AAAA
// queries (IPv4-only enforcement; IPv6 adds significant BPF complexity).
//
// Call Start(ctx) to run the daemon; it blocks until ctx is cancelled.
// Call Stop() for a graceful shutdown independent of context cancellation.
type Resolver struct {
	cfg       ResolverConfig
	allowlist *Allowlist

	upstream   string        // normalized upstream address (host:port)
	sweepEvery time.Duration // resolved-entry sweep interval

	serverUDP *dns.Server
	serverTCP *dns.Server
}

// NewResolver constructs a Resolver from cfg. The Resolver is not started
// until Start(ctx) is called.
func NewResolver(cfg ResolverConfig) *Resolver {
	upstream := cfg.UpstreamAddr
	if _, _, err := net.SplitHostPort(upstream); err != nil {
		upstream = net.JoinHostPort(upstream, "53")
	}

	sweepEvery := cfg.SweepInterval
	if sweepEvery <= 0 {
		sweepEvery = 30 * time.Second
	}

	return &Resolver{
		cfg:        cfg,
		allowlist:  NewAllowlist(cfg.AllowedSuffixes),
		upstream:   upstream,
		sweepEvery: sweepEvery,
	}
}

// Start runs the DNS daemon until ctx is cancelled.
//
// It binds both UDP and TCP listeners on cfg.ListenAddr, starts a background
// TTL sweep goroutine, and blocks until ctx.Done() is closed or Stop() is
// called. Returns any server start error, or nil on clean shutdown.
func (r *Resolver) Start(ctx context.Context) error {
	mux := dns.NewServeMux()
	mux.HandleFunc(".", r.handleQuery)

	r.serverUDP = &dns.Server{Addr: r.cfg.ListenAddr, Net: "udp", Handler: mux}
	r.serverTCP = &dns.Server{Addr: r.cfg.ListenAddr, Net: "tcp", Handler: mux}

	errCh := make(chan error, 2)

	go func() {
		if err := r.serverUDP.ListenAndServe(); err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	go func() {
		if err := r.serverTCP.ListenAndServe(); err != nil {
			select {
			case errCh <- err:
			default:
			}
		}
	}()

	// Background TTL sweep goroutine.
	go func() {
		ticker := time.NewTicker(r.sweepEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				evicted := r.allowlist.Sweep()
				if len(evicted) > 0 {
					log.Debug().
						Str("sandbox_id", r.cfg.SandboxID).
						Int("evicted_ips", len(evicted)).
						Msg("dns resolver: swept expired IP entries")
				}
			}
		}
	}()

	log.Info().
		Str("sandbox_id", r.cfg.SandboxID).
		Str("listen_addr", r.cfg.ListenAddr).
		Str("upstream", r.upstream).
		Msg("dns resolver: started")

	select {
	case <-ctx.Done():
		return r.Stop()
	case err := <-errCh:
		return err
	}
}

// Stop shuts down both UDP and TCP DNS servers gracefully.
func (r *Resolver) Stop() error {
	var firstErr error
	if r.serverUDP != nil {
		if err := r.serverUDP.Shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.serverTCP != nil {
		if err := r.serverTCP.Shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	log.Info().
		Str("sandbox_id", r.cfg.SandboxID).
		Msg("dns resolver: stopped")
	return firstErr
}

// handleQuery is the miekg/dns handler function.
// It implements the allowlist check → forward → BPF map update flow.
func (r *Resolver) handleQuery(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 0 {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeFormatError)
		_ = w.WriteMsg(m)
		return
	}

	q := req.Question[0]
	domain := q.Name // DNS wire format; includes trailing dot.

	// Refuse AAAA queries — BPF enforcement is IPv4 only.
	// Return NOERROR with empty answer section (not NXDOMAIN) so that
	// resolvers don't interpret the empty response as "domain not found".
	if q.Qtype == dns.TypeAAAA {
		m := new(dns.Msg)
		m.SetReply(req)
		m.Authoritative = true
		_ = w.WriteMsg(m)
		return
	}

	allowed := r.allowlist.IsAllowed(domain)

	log.Info().
		Str("sandbox_id", r.cfg.SandboxID).
		Str("event_type", "dns_query").
		Str("domain", domain).
		Uint16("qtype", q.Qtype).
		Bool("allowed", allowed).
		Msg("")

	if !allowed {
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeNameError) // NXDOMAIN
		_ = w.WriteMsg(m)
		return
	}

	// Forward to upstream resolver.
	client := &dns.Client{}
	resp, _, err := client.Exchange(req, r.upstream)
	if err != nil {
		log.Error().
			Str("sandbox_id", r.cfg.SandboxID).
			Str("event_type", "dns_upstream_error").
			Str("domain", domain).
			Err(err).
			Msg("")
		m := new(dns.Msg)
		m.SetRcode(req, dns.RcodeServerFailure)
		_ = w.WriteMsg(m)
		return
	}

	// Extract A records and push to BPF maps.
	if q.Qtype == dns.TypeA && r.cfg.MapUpdater != nil {
		var ips []net.IP
		var minTTL uint32 = ^uint32(0) // start at max, take minimum

		for _, ans := range resp.Answer {
			if a, ok := ans.(*dns.A); ok {
				ip := a.A.To4()
				if ip == nil {
					continue
				}
				ips = append(ips, ip)
				if a.Hdr.Ttl < minTTL {
					minTTL = a.Hdr.Ttl
				}

				if err := r.cfg.MapUpdater.AllowIP(ip); err != nil {
					log.Error().
						Str("sandbox_id", r.cfg.SandboxID).
						Str("event_type", "bpf_allow_ip_error").
						Str("domain", domain).
						Str("ip", ip.String()).
						Err(err).
						Msg("")
				}

				// If domain is in ProxyHosts, also mark for L7 proxy.
				if r.isProxyHost(domain) {
					if err := r.cfg.MapUpdater.MarkForProxy(ip); err != nil {
						log.Error().
							Str("sandbox_id", r.cfg.SandboxID).
							Str("event_type", "bpf_mark_proxy_error").
							Str("domain", domain).
							Str("ip", ip.String()).
							Err(err).
							Msg("")
					}
				}
			}
		}

		if len(ips) > 0 {
			// Use the minimum TTL from the response; floor at 5 seconds.
			ttl := time.Duration(minTTL) * time.Second
			if ttl < 5*time.Second {
				ttl = 5 * time.Second
			}
			r.allowlist.AddResolved(domain, ips, ttl)
		}
	}

	_ = w.WriteMsg(resp)
}

// isProxyHost reports whether domain (with trailing dot) should be marked for
// L7 proxy interception. Matching is case-insensitive suffix matching using
// the same algorithm as IsAllowed.
func (r *Resolver) isProxyHost(domain string) bool {
	if len(r.cfg.ProxyHosts) == 0 {
		return false
	}
	name := strings.TrimSuffix(strings.ToLower(domain), ".")
	for _, ph := range r.cfg.ProxyHosts {
		ph = strings.ToLower(strings.TrimSuffix(ph, "."))
		ph = strings.TrimPrefix(ph, ".")
		if ph == "" {
			continue
		}
		if name == ph || strings.HasSuffix(name, "."+ph) {
			return true
		}
	}
	return false
}

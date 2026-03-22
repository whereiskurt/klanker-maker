package dnsproxy_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/whereiskurt/klankrmkr/sidecars/dns-proxy/dnsproxy"
	"github.com/miekg/dns"
)

// startMockUpstream runs a minimal UDP DNS server that returns a fake A record for any query.
func startMockUpstream(t *testing.T) (addr string, stop func()) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock upstream: %v", err)
	}
	addr = pc.LocalAddr().String()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, remote, rerr := pc.ReadFrom(buf)
			if rerr != nil {
				return
			}
			req := new(dns.Msg)
			if err := req.Unpack(buf[:n]); err != nil {
				continue
			}
			resp := new(dns.Msg)
			resp.SetReply(req)
			if len(req.Question) > 0 {
				resp.Answer = append(resp.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name:   req.Question[0].Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    60,
					},
					A: net.ParseIP("1.2.3.4"),
				})
			}
			out, _ := resp.Pack()
			_, _ = pc.WriteTo(out, remote)
		}
	}()

	return addr, func() { pc.Close() }
}

// startDNSProxy starts an in-process DNS proxy and returns its UDP address.
func startDNSProxy(t *testing.T, allowedSuffixes []string, upstreamAddr string) (addr string, stop func()) {
	t.Helper()

	// Find a free UDP port.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	listenAddr := pc.LocalAddr().String()
	pc.Close()

	handler := dnsproxy.NewHandler(allowedSuffixes, upstreamAddr, "test-sandbox")
	mux := dns.NewServeMux()
	mux.HandleFunc(".", handler)

	server := &dns.Server{
		Addr:    listenAddr,
		Net:     "udp",
		Handler: mux,
	}

	started := make(chan struct{})
	server.NotifyStartedFunc = func() { close(started) }

	go func() {
		_ = server.ListenAndServe()
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("DNS proxy server did not start in time")
	}

	return listenAddr, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.ShutdownContext(ctx)
	}
}

func queryDNS(t *testing.T, serverAddr, domain string) *dns.Msg {
	t.Helper()
	c := &dns.Client{Net: "udp", Timeout: 3 * time.Second}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	resp, _, err := c.Exchange(m, serverAddr)
	if err != nil {
		t.Fatalf("DNS exchange failed: %v", err)
	}
	return resp
}

func TestDNSProxy_AllowedSuffix(t *testing.T) {
	upstream, stopUpstream := startMockUpstream(t)
	defer stopUpstream()

	addr, stop := startDNSProxy(t, []string{"example.com"}, upstream)
	defer stop()

	resp := queryDNS(t, addr, "allowed.example.com")
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("expected NOERROR for allowed suffix, got rcode %d", resp.Rcode)
	}
	if len(resp.Answer) == 0 {
		t.Error("expected at least one answer for allowed suffix")
	}
}

func TestDNSProxy_DeniedSuffix(t *testing.T) {
	upstream, stopUpstream := startMockUpstream(t)
	defer stopUpstream()

	addr, stop := startDNSProxy(t, []string{"example.com"}, upstream)
	defer stop()

	resp := queryDNS(t, addr, "evil.com")
	if resp.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN for denied suffix, got rcode %d", resp.Rcode)
	}
}

func TestDNSProxy_AllowedExact(t *testing.T) {
	upstream, stopUpstream := startMockUpstream(t)
	defer stopUpstream()

	addr, stop := startDNSProxy(t, []string{"example.com"}, upstream)
	defer stop()

	resp := queryDNS(t, addr, "example.com")
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("expected NOERROR for exact allowed domain, got rcode %d", resp.Rcode)
	}
}

func TestDNSProxy_EmptyAllowlist(t *testing.T) {
	upstream, stopUpstream := startMockUpstream(t)
	defer stopUpstream()

	addr, stop := startDNSProxy(t, []string{}, upstream)
	defer stop()

	resp := queryDNS(t, addr, "example.com")
	if resp.Rcode != dns.RcodeNameError {
		t.Errorf("expected NXDOMAIN for empty allowlist, got rcode %d", resp.Rcode)
	}
}

func TestIsAllowed_SuffixMatch(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		suffixes []string
		want     bool
	}{
		{"exact match", "example.com", []string{"example.com"}, true},
		{"subdomain match", "sub.example.com", []string{"example.com"}, true},
		{"deep subdomain match", "a.b.example.com", []string{"example.com"}, true},
		{"no match", "evil.com", []string{"example.com"}, false},
		{"empty suffixes", "example.com", []string{}, false},
		{"trailing dot stripped", "example.com.", []string{"example.com"}, true},
		{"case insensitive", "EXAMPLE.COM", []string{"example.com"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dnsproxy.IsAllowed(tc.domain, tc.suffixes)
			if got != tc.want {
				t.Errorf("IsAllowed(%q, %v) = %v, want %v", tc.domain, tc.suffixes, got, tc.want)
			}
		})
	}
}

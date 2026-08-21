package dnsproxy_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
	"github.com/whereiskurt/klanker-maker/sidecars/dns-proxy/dnsproxy"
	"github.com/miekg/dns"
)

// startDNSProxyWithDeny mirrors startDNSProxy but threads a deny list through.
func startDNSProxyWithDeny(t *testing.T, allowed, denied []string, upstreamAddr string) (addr string, stop func()) {
	t.Helper()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	listenAddr := pc.LocalAddr().String()
	pc.Close()

	handler := dnsproxy.NewHandler(allowed, netpolicy.NewDenier(denied, nil), upstreamAddr, "test-sandbox")
	mux := dns.NewServeMux()
	mux.HandleFunc(".", handler)

	server := &dns.Server{Addr: listenAddr, Net: "udp", Handler: mux}
	started := make(chan struct{})
	server.NotifyStartedFunc = func() { close(started) }
	go func() { _ = server.ListenAndServe() }()

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

// The headline behaviour of the whole change: a wide-open "*" allowlist with a
// deny layered on top resolves everything EXCEPT the denied name.
func TestDNSProxy_DenyBeatsWildcardAllow(t *testing.T) {
	upstream, stopUpstream := startMockUpstream(t)
	defer stopUpstream()

	addr, stop := startDNSProxyWithDeny(t, []string{"*"}, []string{"evil.example.com"}, upstream)
	defer stop()

	blocked := queryDNS(t, addr, "evil.example.com")
	if blocked.Rcode != dns.RcodeNameError {
		t.Errorf("denied name: got rcode %s, want NXDOMAIN", dns.RcodeToString[blocked.Rcode])
	}

	allowed := queryDNS(t, addr, "github.com")
	if allowed.Rcode != dns.RcodeSuccess {
		t.Errorf("non-denied name under \"*\": got rcode %s, want SUCCESS", dns.RcodeToString[allowed.Rcode])
	}
}

// A deny must also beat an explicit allow of the same apex, not just "*".
func TestDNSProxy_DenyBeatsExplicitAllow(t *testing.T) {
	upstream, stopUpstream := startMockUpstream(t)
	defer stopUpstream()

	addr, stop := startDNSProxyWithDeny(t,
		[]string{".example.com"}, []string{"evil.example.com"}, upstream)
	defer stop()

	blocked := queryDNS(t, addr, "evil.example.com")
	if blocked.Rcode != dns.RcodeNameError {
		t.Errorf("denied subdomain of an allowed suffix: got rcode %s, want NXDOMAIN",
			dns.RcodeToString[blocked.Rcode])
	}

	sibling := queryDNS(t, addr, "good.example.com")
	if sibling.Rcode != dns.RcodeSuccess {
		t.Errorf("sibling of denied name: got rcode %s, want SUCCESS",
			dns.RcodeToString[sibling.Rcode])
	}
}

func TestIsDenied_ExactAndSuffix(t *testing.T) {
	denied := []string{"evil.example.com", ".tracker.net"}

	cases := []struct {
		name string
		q    string
		want bool
	}{
		{"exact match", "evil.example.com", true},
		{"trailing dot stripped", "evil.example.com.", true},
		{"case insensitive", "EVIL.Example.COM", true},
		{"subdomain of denied apex", "a.evil.example.com", true},
		{"leading-dot entry matches apex", "tracker.net", true},
		{"leading-dot entry matches subdomain", "cdn.tracker.net", true},
		{"unrelated host", "github.com", false},
		{"substring but not suffix", "notevil.example.com.attacker.io", false},
		{"prefix collision", "evil.example.community", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dnsproxy.IsDenied(tc.q, denied); got != tc.want {
				t.Errorf("IsDenied(%q) = %v, want %v", tc.q, got, tc.want)
			}
		})
	}
}

func TestIsDenied_EmptyListDeniesNothing(t *testing.T) {
	if dnsproxy.IsDenied("anything.example.com", nil) {
		t.Error("empty deny list must deny nothing")
	}
}

// "*" in the deny list seals the box completely, mirroring "*" on the allow
// side. This is the kill-switch case, not a placeholder.
func TestIsDenied_StarDeniesEverything(t *testing.T) {
	if !dnsproxy.IsDenied("evil.example.com", []string{"*"}) {
		t.Error(`"*" in the deny list must deny everything`)
	}
}

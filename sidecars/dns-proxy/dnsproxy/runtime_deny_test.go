package dnsproxy_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
	"github.com/whereiskurt/klanker-maker/sidecars/dns-proxy/dnsproxy"
	"github.com/miekg/dns"
)

func startDNSProxyWithDenier(t *testing.T, allowed []string, d *netpolicy.Denier, upstreamAddr string) (string, func()) {
	t.Helper()

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	listenAddr := pc.LocalAddr().String()
	pc.Close()

	handler := dnsproxy.NewHandler(allowed, d, upstreamAddr, "test-sandbox")
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

// The headline runtime behaviour: a name that resolves fine stops resolving
// once it is appended to the deny file, with no restart and no reconfiguration.
func TestDNSProxy_RuntimeDenyTakesEffectWithoutRestart(t *testing.T) {
	upstream, stopUpstream := startMockUpstream(t)
	defer stopUpstream()

	path := filepath.Join(t.TempDir(), "deny.list")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed deny file: %v", err)
	}
	denier := netpolicy.NewDenier(nil, netpolicy.NewStore(path, 0))

	addr, stop := startDNSProxyWithDenier(t, []string{"*"}, denier, upstream)
	defer stop()

	before := queryDNS(t, addr, "later.example.com")
	if before.Rcode != dns.RcodeSuccess {
		t.Fatalf("before the deny: got %s, want SUCCESS", dns.RcodeToString[before.Rcode])
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("later.example.com\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	after := queryDNS(t, addr, "later.example.com")
	if after.Rcode != dns.RcodeNameError {
		t.Errorf("after the deny: got %s, want NXDOMAIN", dns.RcodeToString[after.Rcode])
	}

	// Everything else under "*" must still resolve — narrowing, not sealing.
	other := queryDNS(t, addr, "unrelated.example.org")
	if other.Rcode != dns.RcodeSuccess {
		t.Errorf("unrelated host: got %s, want SUCCESS", dns.RcodeToString[other.Rcode])
	}
}

// A nil denier is the shape for a sandbox with no denies at all.
func TestDNSProxy_NilDenierAllowsEverythingUnderStar(t *testing.T) {
	upstream, stopUpstream := startMockUpstream(t)
	defer stopUpstream()

	addr, stop := startDNSProxyWithDenier(t, []string{"*"}, nil, upstream)
	defer stop()

	resp := queryDNS(t, addr, "anything.example.com")
	if resp.Rcode != dns.RcodeSuccess {
		t.Errorf("got %s, want SUCCESS", dns.RcodeToString[resp.Rcode])
	}
}

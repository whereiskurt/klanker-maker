package dnsproxy_test

import (
	"os"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
	"github.com/whereiskurt/klanker-maker/sidecars/dns-proxy/dnsproxy"
)

// fakeWriter captures the ResponseWriter interface the handler needs.
type fakeWriter struct{ dns.ResponseWriter }

func (fakeWriter) WriteMsg(*dns.Msg) error { return nil }

func TestHandler_RecordsAllowAndDenyFlows(t *testing.T) {
	dir := t.TempDir()
	path := flowlog.FileFor(dir, flowlog.SrcDNS)
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	h := dnsproxy.NewHandlerWithFlows(
		[]string{"github.com"},
		netpolicy.NewDenier([]string{"evil.example"}, nil),
		netpolicy.NewPinner(nil),
		"127.0.0.1:53", "sb-test", w,
	)

	for _, name := range []string{"api.github.com.", "evil.example."} {
		m := new(dns.Msg)
		m.SetQuestion(name, dns.TypeA)
		h(fakeWriter{}, m)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no flow file written: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, `"verdict":"allow"`) {
		t.Errorf("want an allow record:\n%s", got)
	}
	if !strings.Contains(got, `"verdict":"deny"`) {
		t.Errorf("want a deny record:\n%s", got)
	}
	if !strings.Contains(got, `"src":"dns"`) {
		t.Errorf("want the dns producer tag:\n%s", got)
	}
}

func TestHandler_NilWriterIsSafe(t *testing.T) {
	// Flow recording must never be load-bearing for an egress decision.
	h := dnsproxy.NewHandlerWithFlows(
		[]string{"github.com"}, nil, nil, "127.0.0.1:53", "sb-test", nil,
	)
	m := new(dns.Msg)
	m.SetQuestion("api.github.com.", dns.TypeA)
	h(fakeWriter{}, m) // must not panic
}

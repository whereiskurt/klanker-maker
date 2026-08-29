package resolver

import (
	"os"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// fakeRW satisfies dns.ResponseWriter with just the one method handleQuery uses.
type fakeRW struct{ dns.ResponseWriter }

func (fakeRW) WriteMsg(*dns.Msg) error { return nil }

// The eBPF resolver is the ONLY name-bearing flow producer under ebpf/both
// enforcement: the BPF program emits ring-buffer events for deny and redirect
// only, and there is no allow event on the connect4 fast path. Without this,
// the census on those modes holds denies and a few L7-proxy redirects, so
// PinCandidates (which skips address-only rows) yields an EMPTY allowed set —
// and `km-netpolicy pin` is irreversible.
//
// It also puts root's lookups into the census: /etc/resolv.conf points every
// process at this resolver and DNS is neither uid- nor cgroup-scoped, so
// without a record here root's destinations are invisible AND pinned out.
func TestResolver_RecordsAllowAndDenyFlows(t *testing.T) {
	dir := t.TempDir()
	path := flowlog.FileFor(dir, flowlog.SrcResolver)
	w := flowlog.NewWriter(path, 1<<20)
	defer w.Close()

	r := NewResolver(ResolverConfig{
		ListenAddr: "127.0.0.1:0",
		// Deliberately dead: the flow record is written before the forward, so
		// an unreachable upstream still exercises both verdicts.
		UpstreamAddr:    "127.0.0.1:1",
		AllowedSuffixes: []string{"github.com"},
		SandboxID:       "sb-test",
		Flows:           w,
	})

	for _, name := range []string{"api.github.com.", "not-allowed.example."} {
		m := new(dns.Msg)
		m.SetQuestion(name, dns.TypeA)
		r.handleQuery(fakeRW{}, m)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("resolver wrote no flow file: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, `"src":"resolver"`) {
		t.Errorf("want the resolver producer tag:\n%s", got)
	}
	if !strings.Contains(got, `"verdict":"allow"`) || !strings.Contains(got, `"host":"api.github.com"`) {
		t.Errorf("want an allow record naming the host:\n%s", got)
	}
	if !strings.Contains(got, `"verdict":"deny"`) {
		t.Errorf("want a deny record:\n%s", got)
	}
}

// A nil Flows writer must leave the resolver byte-identical to before flow
// recording existed — recording must never be load-bearing for an answer the
// sandbox is blocked on.
func TestResolver_NilFlowsWriterIsSafe(t *testing.T) {
	r := NewResolver(ResolverConfig{
		ListenAddr:      "127.0.0.1:0",
		UpstreamAddr:    "127.0.0.1:1",
		AllowedSuffixes: []string{"github.com"},
	})
	m := new(dns.Msg)
	m.SetQuestion("api.github.com.", dns.TypeA)
	r.handleQuery(fakeRW{}, m) // must not panic
}

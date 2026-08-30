package resolver_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/ebpf/resolver"
	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

// A denied name must not resolve even when the allowlist is the "*" wildcard.
// In eBPF mode the resolver is the only thing that ever puts an IP into the BPF
// LPM trie, so refusing to resolve is what actually blocks the egress.
func TestAllowlist_DenyBeatsWildcardAllow(t *testing.T) {
	al := resolver.NewAllowlist([]string{"*"}, netpolicy.NewDenier([]string{"evil.example.com"}, nil), nil)

	if al.IsAllowed("evil.example.com") {
		t.Error("denied name resolved under a \"*\" allowlist")
	}
	if al.IsAllowed("a.evil.example.com") {
		t.Error("subdomain of a denied name resolved under a \"*\" allowlist")
	}
	if !al.IsAllowed("github.com") {
		t.Error("non-denied name did not resolve under a \"*\" allowlist")
	}
}

func TestAllowlist_DenyBeatsExplicitAllow(t *testing.T) {
	al := resolver.NewAllowlist([]string{".example.com"}, netpolicy.NewDenier([]string{"evil.example.com"}, nil), nil)

	if al.IsAllowed("evil.example.com") {
		t.Error("denied name resolved despite matching an allowed suffix")
	}
	if !al.IsAllowed("good.example.com") {
		t.Error("sibling of a denied name failed to resolve")
	}
}

func TestAllowlist_LeadingDotDenyEntry(t *testing.T) {
	al := resolver.NewAllowlist([]string{"*"}, netpolicy.NewDenier([]string{".tracker.net"}, nil), nil)

	if al.IsAllowed("tracker.net") {
		t.Error("leading-dot deny entry failed to match the apex")
	}
	if al.IsAllowed("cdn.tracker.net") {
		t.Error("leading-dot deny entry failed to match a subdomain")
	}
}

// IsDenied is consulted directly by `km ebpf-attach` when deciding which hosts
// to pre-resolve and seed into the BPF trie. Seeding a denied host's IPs would
// punch a hole straight through the deny list at the IP layer.
func TestAllowlist_IsDenied(t *testing.T) {
	al := resolver.NewAllowlist(nil, netpolicy.NewDenier([]string{"evil.example.com", ".tracker.net"}, nil), nil)

	cases := []struct {
		name string
		want bool
	}{
		{"evil.example.com", true},
		{"api.evil.example.com", true},
		{"tracker.net", true},
		{"cdn.tracker.net", true},
		{"EVIL.example.COM", true},
		{"evil.example.com.", true},
		{"github.com", false},
		{"evil.example.community", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := al.IsDenied(tc.name); got != tc.want {
			t.Errorf("IsDenied(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAllowlist_IsDenied_EmptyListDeniesNothing(t *testing.T) {
	al := resolver.NewAllowlist([]string{"*"}, nil, nil)
	if al.IsDenied("anything.example.com") {
		t.Error("empty deny list must deny nothing")
	}
}

// In ebpf-only enforcement the resolver IS the DNS server — there is no
// km-dns-proxy sidecar to fall back on. If the resolver ignored the runtime deny
// file, `km-netpolicy deny x` would report success while x stayed reachable,
// which is worse than not offering the feature.
func TestAllowlist_HonoursRuntimeDenyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deny.list")
	if err := os.WriteFile(path, []byte("runtime.example.net\n"), 0o644); err != nil {
		t.Fatalf("seed deny file: %v", err)
	}

	al := resolver.NewAllowlist([]string{"*"},
		netpolicy.NewDenier([]string{"static.example.com"}, netpolicy.NewStore(path, 0)), nil)

	if al.IsAllowed("static.example.com") {
		t.Error("static deny not enforced by the resolver")
	}
	if al.IsAllowed("runtime.example.net") {
		t.Error("runtime deny not enforced by the resolver")
	}
	if al.IsAllowed("api.runtime.example.net") {
		t.Error("runtime deny must cover subdomains")
	}
	if !al.IsAllowed("github.com") {
		t.Error("unrelated name blocked")
	}
}

// An append must be picked up by an already-running resolver.
func TestAllowlist_RuntimeDenyAppendTakesEffect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deny.list")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed deny file: %v", err)
	}

	al := resolver.NewAllowlist([]string{"*"}, netpolicy.NewDenier(nil, netpolicy.NewStore(path, 0)), nil)

	if !al.IsAllowed("later.example.com") {
		t.Fatal("name should resolve before the deny is appended")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("later.example.com\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	if al.IsAllowed("later.example.com") {
		t.Error("appended runtime deny not picked up by a running resolver")
	}
}

func TestAllowlist_EmptyDenyListIsInert(t *testing.T) {
	al := resolver.NewAllowlist([]string{".github.com"}, nil, nil)

	if !al.IsAllowed("api.github.com") {
		t.Error("allowed name blocked with an empty deny list")
	}
	if al.IsAllowed("evil.example.com") {
		t.Error("name outside the allowlist resolved with an empty deny list")
	}
}

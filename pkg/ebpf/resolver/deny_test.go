package resolver_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/ebpf/resolver"
)

// A denied name must not resolve even when the allowlist is the "*" wildcard.
// In eBPF mode the resolver is the only thing that ever puts an IP into the BPF
// LPM trie, so refusing to resolve is what actually blocks the egress.
func TestAllowlist_DenyBeatsWildcardAllow(t *testing.T) {
	al := resolver.NewAllowlist([]string{"*"}, []string{"evil.example.com"})

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
	al := resolver.NewAllowlist([]string{".example.com"}, []string{"evil.example.com"})

	if al.IsAllowed("evil.example.com") {
		t.Error("denied name resolved despite matching an allowed suffix")
	}
	if !al.IsAllowed("good.example.com") {
		t.Error("sibling of a denied name failed to resolve")
	}
}

func TestAllowlist_LeadingDotDenyEntry(t *testing.T) {
	al := resolver.NewAllowlist([]string{"*"}, []string{".tracker.net"})

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
	al := resolver.NewAllowlist(nil, []string{"evil.example.com", ".tracker.net"})

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
	al := resolver.NewAllowlist([]string{"*"}, nil)
	if al.IsDenied("anything.example.com") {
		t.Error("empty deny list must deny nothing")
	}
}

func TestAllowlist_EmptyDenyListIsInert(t *testing.T) {
	al := resolver.NewAllowlist([]string{".github.com"}, nil)

	if !al.IsAllowed("api.github.com") {
		t.Error("allowed name blocked with an empty deny list")
	}
	if al.IsAllowed("evil.example.com") {
		t.Error("name outside the allowlist resolved with an empty deny list")
	}
}

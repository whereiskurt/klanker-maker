package flowlog_test

import (
	"reflect"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func censusOf(hosts ...string) flowlog.Census {
	var ds []flowlog.Dest
	for _, h := range hosts {
		ds = append(ds, flowlog.Dest{Host: h, Count: 1})
	}
	return flowlog.Census{Allowed: ds}
}

func TestPinCandidates_CollapsedByDefault(t *testing.T) {
	suf, hosts := flowlog.PinCandidates(censusOf("api.github.com", "raw.github.com", "pypi.org"), false)

	wantSuf := []string{".github.com", ".pypi.org"}
	if !reflect.DeepEqual(suf, wantSuf) {
		t.Errorf("suffixes = %v, want %v", suf, wantSuf)
	}
	// Default mode collapses hosts too, so a CDN hostname the session happened
	// not to touch does not strand the box.
	if !reflect.DeepEqual(hosts, wantSuf) {
		t.Errorf("hosts = %v, want %v", hosts, wantSuf)
	}
}

func TestPinCandidates_ExactKeepsLiteralHosts(t *testing.T) {
	suf, hosts := flowlog.PinCandidates(censusOf("api.github.com", "pypi.org"), true)

	// DNS is ALWAYS collapsed regardless of --exact: names under a domain vary
	// per request and an exact DNS allowlist stops resolving almost at once.
	wantSuf := []string{".github.com", ".pypi.org"}
	if !reflect.DeepEqual(suf, wantSuf) {
		t.Errorf("suffixes = %v, want %v (must collapse even with exact)", suf, wantSuf)
	}
	wantHosts := []string{"api.github.com", "pypi.org"}
	if !reflect.DeepEqual(hosts, wantHosts) {
		t.Errorf("hosts = %v, want %v", hosts, wantHosts)
	}
}

func TestPinCandidates_IgnoresDeniedAndAddressOnly(t *testing.T) {
	c := flowlog.Census{
		Allowed: []flowlog.Dest{{Host: "api.github.com"}, {Addr: "203.0.113.9"}},
		Denied:  []flowlog.Dest{{Host: "evil.example.com"}},
	}
	suf, hosts := flowlog.PinCandidates(c, true)

	for _, got := range append(append([]string{}, suf...), hosts...) {
		if got == "evil.example.com" || got == ".example.com" {
			t.Fatalf("a denied host must never become pinnable: %v / %v", suf, hosts)
		}
		if got == "203.0.113.9" {
			t.Fatal("an address-only observation is not an allowlist entry")
		}
	}
	if len(hosts) != 1 || hosts[0] != "api.github.com" {
		t.Errorf("hosts = %v", hosts)
	}
}

// publicsuffix does not error on an IP literal — EffectiveTLDPlusOne("1.2.3.4")
// returns "3.4" under the unmanaged-TLD rule — so a direct-to-IP HTTPS
// connection recorded by the HTTP proxy used to put a nonsense ".3.4" suffix
// into the pin file and, via `km-netpolicy profile`, into a generated
// SandboxProfile. It must reach the host list verbatim instead, so the
// destination stays reachable through the exact-matching host side.
func TestPinCandidates_IPLiteralsNeverBecomeSuffixes(t *testing.T) {
	for _, exact := range []bool{false, true} {
		suf, hosts := flowlog.PinCandidates(censusOf("api.github.com", "203.0.113.9"), exact)
		for _, s := range suf {
			if s == ".113.9" || s == "203.0.113.9" {
				t.Errorf("exact=%v: IP literal leaked into the DNS suffixes: %v", exact, suf)
			}
		}
		found := false
		for _, h := range hosts {
			if h == "203.0.113.9" {
				found = true
			}
		}
		if !found {
			t.Errorf("exact=%v: an observed IP-literal destination must stay pinnable as a host: %v", exact, hosts)
		}
	}
}

func TestPinCandidates_EmptyCensusYieldsEmptySets(t *testing.T) {
	suf, hosts := flowlog.PinCandidates(flowlog.Census{}, false)
	if len(suf) != 0 || len(hosts) != 0 {
		t.Fatalf("empty census must yield empty sets, got %v / %v", suf, hosts)
	}
}

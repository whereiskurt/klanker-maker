package netpolicy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

// gen builds a generation with the same entries in both scopes, which is what
// the collapsed default writes.
func gen(entries ...string) netpolicy.PinGeneration {
	return netpolicy.PinGeneration{DNS: entries, Hosts: entries}
}

func TestPinsAllow_NoPinsAllowsEverything(t *testing.T) {
	// An unpinned box must behave exactly as it did before pins existed.
	if !netpolicy.PinsAllow("anything.example", nil) {
		t.Error("no pins must not narrow anything")
	}
}

func TestPinsAllow_SingleGenerationIsMembership(t *testing.T) {
	gens := []netpolicy.PinGeneration{gen(".github.com", "pypi.org")}
	if !netpolicy.PinsAllow("api.github.com", gens) {
		t.Error("subdomain of a dotted pin must be allowed")
	}
	if !netpolicy.PinsAllow("pypi.org", gens) {
		t.Error("exact pin must be allowed")
	}
	if netpolicy.PinsAllow("files.pypi.org", gens) {
		t.Error("bare pin must NOT cover subdomains on the allow side")
	}
	if netpolicy.PinsAllow("evil.example", gens) {
		t.Error("unpinned host must be denied")
	}
}

func TestPinsAllow_GenerationsIntersect(t *testing.T) {
	// Successive pins can only narrow: a host must be present in EVERY
	// generation to survive. This is the whole guarantee.
	gens := []netpolicy.PinGeneration{
		gen(".github.com", ".pypi.org"),
		gen(".github.com"),
	}
	if !netpolicy.PinsAllow("api.github.com", gens) {
		t.Error("host in both generations must survive")
	}
	if netpolicy.PinsAllow("files.pypi.org", gens) {
		t.Error("host dropped by a later pin must be denied — pins intersect")
	}
}

func TestPinsAllow_EmptyGenerationIsDenyAll(t *testing.T) {
	// Pinning a box that has touched nothing yields the empty set. Deny-all is
	// the correct reading of "allow only what I observed" when nothing was
	// observed; the CLI is what must warn, not the algebra.
	if netpolicy.PinsAllow("anything.example", []netpolicy.PinGeneration{{}}) {
		t.Error("an empty generation must deny everything")
	}
}

// The two scopes gate two different matchers, which is the whole reason
// --exact can narrow anything: DNS has to stay collapsed to eTLD+1 or the box
// stops resolving, while the host side can hold the literal observed names.
// Before this split, `pin --exact` wrote one flat list read by both matchers,
// so the collapsed suffix beside each literal host matched every subdomain and
// --exact narrowed nothing at all.
func TestPinScopes_ExactHostNarrowsWhileDNSStaysCollapsed(t *testing.T) {
	gens := []netpolicy.PinGeneration{{
		DNS:   []string{".github.com"},
		Hosts: []string{"api.github.com"},
	}}

	if !netpolicy.PinsAllowDNS("gist.github.com", gens) {
		t.Error("DNS scope is collapsed to eTLD+1: a sibling name must still resolve")
	}
	if netpolicy.PinsAllowHost("gist.github.com", gens) {
		t.Error("host scope is exact: a name outside the pinned hosts must be refused")
	}
	if !netpolicy.PinsAllowHost("api.github.com", gens) {
		t.Error("the pinned literal host must survive its own pin")
	}
	// The conservative both-scopes answer, used by the boot-time BPF pre-seed
	// loop, must take the narrower of the two.
	if netpolicy.PinsAllow("gist.github.com", gens) {
		t.Error("the both-scopes answer must be the intersection of the two")
	}
}

func TestParsePinBlocks_UntaggedEntryAppliesToBothScopes(t *testing.T) {
	gens := netpolicy.ParsePinBlocks("# pin 1 2026-08-28T14:02:11Z\n.github.com\n")
	if len(gens) != 1 {
		t.Fatalf("want 1 generation, got %+v", gens)
	}
	if len(gens[0].DNS) != 1 || len(gens[0].Hosts) != 1 {
		t.Fatalf("an untagged entry must land in both scopes: %+v", gens[0])
	}
}

func TestParsePinBlocks_ScopePrefixesSplit(t *testing.T) {
	gens := netpolicy.ParsePinBlocks(
		"# pin 1 2026-08-28T14:02:11Z\ndns:.github.com\nhost:api.github.com\n")
	if len(gens) != 1 {
		t.Fatalf("want 1 generation, got %+v", gens)
	}
	if len(gens[0].DNS) != 1 || gens[0].DNS[0] != ".github.com" {
		t.Fatalf("dns scope wrong: %+v", gens[0])
	}
	if len(gens[0].Hosts) != 1 || gens[0].Hosts[0] != "api.github.com" {
		t.Fatalf("host scope wrong: %+v", gens[0])
	}
}

// A pin must never be able to sever the control plane that would let an
// operator recover the box. Under ebpf/both the on-box resolver answers for
// root too, and root's destinations barely reach the census — so without this
// floor, the first pin NXDOMAINs .amazonaws.com, SSM dies, and with no un-pin
// verb the only recovery is terminating the instance.
func TestPinsAllow_PlatformCarveOutSurvivesEveryPin(t *testing.T) {
	// A pin as tight as it gets: one unrelated host, nothing else.
	gens := []netpolicy.PinGeneration{gen("example.test"), gen("example.test")}

	for _, h := range []string{
		"ssm.us-east-1.amazonaws.com",
		"ssmmessages.us-east-1.amazonaws.com",
		"s3.amazonaws.com",
		"amazonaws.com",
	} {
		if !netpolicy.PinsAllowDNS(h, gens) {
			t.Errorf("pin severed DNS for %q — the box would be unrecoverable", h)
		}
		if !netpolicy.PinsAllowHost(h, gens) {
			t.Errorf("pin severed host access for %q — the box would be unrecoverable", h)
		}
	}
	// The floor is a fuse, not a hole: everything else still pins out.
	if netpolicy.PinsAllow("evil.example", gens) {
		t.Error("the carve-out must not widen anything beyond its own suffixes")
	}
	// And it must not swallow lookalikes.
	if netpolicy.PinsAllow("amazonaws.com.evil.example", gens) {
		t.Error("a lookalike must not inherit the platform carve-out")
	}
}

// The floor covers pins only. A deny is an explicit, named act and must still
// win — otherwise the carve-out would be a hole in the deny list too.
func TestPlatformCarveOut_DoesNotOverrideDenies(t *testing.T) {
	d := netpolicy.NewDenier([]string{".amazonaws.com"}, nil)
	if !d.IsDenied("ssm.us-east-1.amazonaws.com") {
		t.Error("an explicit deny on a platform-essential host must still block")
	}
}

func TestPinStore_ParsesGenerationBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allow.pins")
	body := "# pin 1 2026-08-28T14:02:11Z\n" +
		".github.com\n" +
		".pypi.org\n" +
		"# pin 2 2026-08-28T15:30:00Z\n" +
		".github.com\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	gens := netpolicy.NewPinStore(path, 0).Generations()
	if len(gens) != 2 {
		t.Fatalf("want 2 generations, got %d: %+v", len(gens), gens)
	}
	if len(gens[0].DNS) != 2 || len(gens[1].DNS) != 1 {
		t.Fatalf("generation contents wrong: %+v", gens)
	}
}

func TestPinStore_AbsentFileIsUnpinned(t *testing.T) {
	gens := netpolicy.NewPinStore(filepath.Join(t.TempDir(), "nope"), 0).Generations()
	if len(gens) != 0 {
		t.Fatalf("absent pin file must read as unpinned, got %+v", gens)
	}
}

func TestPinStore_RoundTripsFormatPinBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allow.pins")
	block := netpolicy.FormatPinBlock(1, time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC),
		[]string{".github.com"}, []string{"api.github.com"})
	if !strings.Contains(block, "dns:.github.com") || !strings.Contains(block, "host:api.github.com") {
		t.Fatalf("FormatPinBlock must tag scopes:\n%s", block)
	}
	if err := os.WriteFile(path, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	gens := netpolicy.NewPinStore(path, 0).Generations()
	if len(gens) != 1 {
		t.Fatalf("FormatPinBlock output must parse back: %+v", gens)
	}
	if len(gens[0].DNS) != 1 || gens[0].DNS[0] != ".github.com" {
		t.Fatalf("dns scope did not round-trip: %+v", gens[0])
	}
	if len(gens[0].Hosts) != 1 || gens[0].Hosts[0] != "api.github.com" {
		t.Fatalf("host scope did not round-trip: %+v", gens[0])
	}
}

func TestMatchAllow_IsNarrowerThanMatch(t *testing.T) {
	// The deny matcher deliberately covers subdomains from a bare entry. The
	// allow matcher must NOT: on the allow side that would grant more than the
	// operator wrote.
	if !netpolicy.Match("api.evil.example", []string{"evil.example"}) {
		t.Error("deny matcher should cover subdomains")
	}
	if netpolicy.MatchAllow("api.evil.example", []string{"evil.example"}) {
		t.Error("allow matcher must not cover subdomains from a bare entry")
	}
}

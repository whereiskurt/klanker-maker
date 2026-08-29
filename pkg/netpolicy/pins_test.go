package netpolicy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

func TestPinsAllow_NoPinsAllowsEverything(t *testing.T) {
	// An unpinned box must behave exactly as it did before pins existed.
	if !netpolicy.PinsAllow("anything.example", nil) {
		t.Error("no pins must not narrow anything")
	}
}

func TestPinsAllow_SingleGenerationIsMembership(t *testing.T) {
	gens := [][]string{{".github.com", "pypi.org"}}
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
	gens := [][]string{
		{".github.com", ".pypi.org"},
		{".github.com"},
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
	if netpolicy.PinsAllow("anything.example", [][]string{{}}) {
		t.Error("an empty generation must deny everything")
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
	if len(gens[0]) != 2 || len(gens[1]) != 1 {
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
	block := netpolicy.FormatPinBlock(1, time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC), []string{".github.com"})
	if err := os.WriteFile(path, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	gens := netpolicy.NewPinStore(path, 0).Generations()
	if len(gens) != 1 || len(gens[0]) != 1 || gens[0][0] != ".github.com" {
		t.Fatalf("FormatPinBlock output must parse back: %+v", gens)
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

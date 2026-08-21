package netpolicy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

func TestMatch(t *testing.T) {
	patterns := []string{"evil.example.com", ".tracker.net"}

	cases := []struct {
		host string
		want bool
	}{
		{"evil.example.com", true},
		{"EVIL.example.COM", true},
		{"evil.example.com.", true},
		{"evil.example.com:443", true},
		{"api.evil.example.com", true},
		{"tracker.net", true},
		{"cdn.tracker.net", true},
		{"github.com", false},
		{"evil.example.community", false},
		{"notevil.example.com.attacker.io", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := netpolicy.Match(tc.host, patterns); got != tc.want {
			t.Errorf("Match(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestMatch_EmptyPatternsMatchNothing(t *testing.T) {
	if netpolicy.Match("anything.example.com", nil) {
		t.Error("empty pattern list must match nothing")
	}
}

func TestMatch_Star(t *testing.T) {
	if !netpolicy.Match("anything.example.com", []string{"*"}) {
		t.Error(`"*" must match everything`)
	}
}

func writeDenyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deny.list")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write deny file: %v", err)
	}
	return path
}

func TestStore_LoadsEntries(t *testing.T) {
	path := writeDenyFile(t, "# header\nevil.example.com\n\n.tracker.net\n")
	s := netpolicy.NewStore(path, 0)

	got := s.Entries()
	if len(got) != 2 || got[0] != "evil.example.com" || got[1] != ".tracker.net" {
		t.Errorf("Entries() = %v, want [evil.example.com .tracker.net]", got)
	}
}

// A missing file is the normal state on a sandbox that has never narrowed
// itself. It must read as "no runtime denies", not as an error and certainly
// not as a reason to fail open elsewhere.
func TestStore_MissingFileIsEmpty(t *testing.T) {
	s := netpolicy.NewStore(filepath.Join(t.TempDir(), "absent.list"), 0)
	if got := s.Entries(); len(got) != 0 {
		t.Errorf("Entries() on a missing file = %v, want empty", got)
	}
}

// The whole point of the runtime path is that an append takes effect without
// restarting the proxy.
func TestStore_PicksUpAppendedEntry(t *testing.T) {
	path := writeDenyFile(t, "evil.example.com\n")
	s := netpolicy.NewStore(path, 0)

	if got := s.Entries(); len(got) != 1 {
		t.Fatalf("initial Entries() = %v, want 1 entry", got)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open for append: %v", err)
	}
	if _, err := f.WriteString("later.example.net\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	got := s.Entries()
	if len(got) != 2 || got[1] != "later.example.net" {
		t.Errorf("after append Entries() = %v, want the appended entry", got)
	}
}

// Malformed lines must be dropped and counted, never abort the load — a single
// bad append from an agent cannot be allowed to blind the whole deny list.
func TestStore_SkipsMalformedLinesWithoutLosingGoodOnes(t *testing.T) {
	path := writeDenyFile(t, "evil.example.com\nhttps://bad\ngood.example.net\n*.glob.example\n")
	s := netpolicy.NewStore(path, 0)

	got := s.Entries()
	if len(got) != 2 || got[0] != "evil.example.com" || got[1] != "good.example.net" {
		t.Errorf("Entries() = %v, want the two well-formed entries", got)
	}
	if s.Dropped() != 2 {
		t.Errorf("Dropped() = %d, want 2", s.Dropped())
	}
}

// The proxies call this on every decision, so repeated reads inside the cache
// window must not re-stat the file.
func TestStore_CachesWithinInterval(t *testing.T) {
	path := writeDenyFile(t, "evil.example.com\n")
	s := netpolicy.NewStore(path, time.Hour)

	_ = s.Entries()

	if err := os.WriteFile(path, []byte("evil.example.com\nsneaky.example.com\n"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}

	if got := s.Entries(); len(got) != 1 {
		t.Errorf("Entries() within the cache window = %v, want the cached single entry", got)
	}
	if got := s.Reload(); len(got) != 2 {
		t.Errorf("Reload() = %v, want both entries", got)
	}
}

func TestDenier_UnionOfStaticAndRuntime(t *testing.T) {
	path := writeDenyFile(t, "runtime.example.net\n")
	d := netpolicy.NewDenier([]string{"static.example.com"}, netpolicy.NewStore(path, 0))

	if !d.IsDenied("static.example.com") {
		t.Error("static deny not enforced")
	}
	if !d.IsDenied("runtime.example.net") {
		t.Error("runtime deny not enforced")
	}
	if !d.IsDenied("api.runtime.example.net") {
		t.Error("runtime deny must cover subdomains")
	}
	if d.IsDenied("github.com") {
		t.Error("unrelated host denied")
	}
}

// A sandbox with runtimeDeny off has no store at all; the static list must still
// work and nothing may panic on the nil.
func TestDenier_NilStoreIsStaticOnly(t *testing.T) {
	d := netpolicy.NewDenier([]string{"static.example.com"}, nil)

	if !d.IsDenied("static.example.com") {
		t.Error("static deny not enforced with a nil store")
	}
	if d.IsDenied("anything.example.net") {
		t.Error("nil store must contribute no denies")
	}
}

func TestDenier_EmptyIsInert(t *testing.T) {
	d := netpolicy.NewDenier(nil, nil)
	if d.IsDenied("anything.example.com") {
		t.Error("a denier with no sources must deny nothing")
	}
	if d.Any() {
		t.Error("Any() must be false when there are no sources")
	}
}

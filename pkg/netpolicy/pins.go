package netpolicy

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// DefaultPinPath is where the runtime allow-pin file lives on a sandbox.
//
// It sits beside deny.list, under /var/lib for the same reason: a reboot that
// dropped accumulated pins would WIDEN the policy, which the design forbids.
const DefaultPinPath = "/var/lib/km/netpolicy/allow.pins"

// pinHeaderPrefix marks the start of a generation block.
const pinHeaderPrefix = "# pin "

// PinStore is a lazily-reloading view of the allow-pin file.
//
// It is the intersection counterpart to Store. Where Store's entries union into
// an ever-larger deny set, a PinStore's generations intersect into an
// ever-smaller allow set. Both directions narrow; neither has a removal verb.
//
// Like Store it never reports an error: an absent or unreadable file reads as
// "unpinned", which is the correct default for the overwhelmingly common case.
//
// Safe for concurrent use.
type PinStore struct {
	path     string
	interval time.Duration

	mu        sync.RWMutex
	gens      [][]string
	lastMod   time.Time
	lastSize  int64
	lastCheck time.Time
	loaded    bool
}

// NewPinStore returns a PinStore reading path, re-checking at most once per
// interval. An interval of 0 checks on every call.
func NewPinStore(path string, interval time.Duration) *PinStore {
	return &PinStore{path: path, interval: interval}
}

// Path returns the file this store reads.
func (s *PinStore) Path() string { return s.path }

// Generations returns the pinned sets, oldest first, reloading if the cached
// view has expired and the file changed.
func (s *PinStore) Generations() [][]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	if s.loaded && s.interval > 0 && time.Since(s.lastCheck) < s.interval {
		out := s.gens
		s.mu.RUnlock()
		return out
	}
	s.mu.RUnlock()
	return s.Reload()
}

// Reload re-reads the file unconditionally.
func (s *PinStore) Reload() [][]string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastCheck = time.Now()

	fi, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.gens, s.loaded = nil, true
			s.lastMod, s.lastSize = time.Time{}, 0
			return nil
		}
		s.loaded = true
		return s.gens
	}
	if s.loaded && fi.ModTime().Equal(s.lastMod) && fi.Size() == s.lastSize {
		return s.gens
	}
	body, err := os.ReadFile(s.path)
	if err != nil {
		s.loaded = true
		return s.gens
	}

	s.gens = ParsePinBlocks(string(body))
	s.lastMod, s.lastSize, s.loaded = fi.ModTime(), fi.Size(), true
	return s.gens
}

// ParsePinBlocks splits a pin-file body into generations.
//
// A generation starts at a "# pin N <timestamp>" header and runs to the next
// one. Patterns are validated with ParseLine, so a malformed entry is dropped
// exactly as it is in a deny list. A generation whose entries were ALL dropped
// is retained as an empty generation rather than discarded — dropping it would
// silently widen the intersection back out.
func ParsePinBlocks(body string) [][]string {
	var gens [][]string
	cur := -1
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, pinHeaderPrefix) {
			gens = append(gens, []string{})
			cur = len(gens) - 1
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if cur < 0 {
			// Entries before any header belong to an implicit first generation.
			gens = append(gens, []string{})
			cur = 0
		}
		if p, ok := ParseLine(trimmed); ok {
			gens[cur] = append(gens[cur], p)
		}
	}
	return gens
}

// FormatPinBlock renders one generation for appending to the pin file.
func FormatPinBlock(n int, at time.Time, patterns []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d %s\n", pinHeaderPrefix, n, at.UTC().Format(time.RFC3339))
	for _, p := range patterns {
		b.WriteString(p)
		b.WriteString("\n")
	}
	return b.String()
}

// Pinner answers the allow-side question every egress decision needs once pins
// exist: does this host survive every pin generation?
//
// It is the intersection counterpart to Denier. A nil Pinner, or one over an
// absent file, allows everything — so a box that has never pinned behaves
// exactly as it did before pins existed.
//
// Like Denier it is consulted per decision rather than snapshotted, which is
// what makes a pin taken at 10:00 effective on the next request without a
// restart.
type Pinner struct {
	store *PinStore
}

// NewPinner wraps a PinStore. A nil store yields a Pinner that allows all.
func NewPinner(store *PinStore) *Pinner { return &Pinner{store: store} }

// Allows reports whether host survives every pinned generation.
func (p *Pinner) Allows(host string) bool {
	if p == nil || p.store == nil {
		return true
	}
	return PinsAllow(host, p.store.Generations())
}

// PinsAllow reports whether host survives every pinned generation.
//
// No generations means unpinned, so everything passes and a box behaves exactly
// as it did before pins existed. Otherwise host must match in EVERY generation:
// the effective allow set is the intersection, so each pin can only ever shrink
// it. That monotonicity is what makes "pin never widens" a property of the data
// structure rather than a rule anyone has to trust.
func PinsAllow(host string, gens [][]string) bool {
	for _, g := range gens {
		if !MatchAllow(host, g) {
			return false
		}
	}
	return true
}

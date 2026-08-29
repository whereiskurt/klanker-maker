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

// Scope prefixes inside a pin block.
//
// A pin narrows two different matchers: the DNS allowlist (which decides
// whether a NAME resolves) and the HTTP host allowlist (which decides whether a
// CONNECT or request to a HOST proceeds). They are genuinely different
// questions — an exact-host pin has to leave DNS collapsed to eTLD+1 or the box
// stops resolving almost immediately — so the file tags which matcher an entry
// belongs to. An untagged entry applies to BOTH, which is what a hand-written
// file means and what the collapsed default amounts to.
const (
	pinScopeDNS  = "dns:"
	pinScopeHost = "host:"
)

// platformEssentialSuffixes is the safety floor a pin can never cut through.
//
// This is the ONE deliberate exception to "a pin narrows everything it is
// consulted for", and it is a fuse, not a policy knob: it is compiled in and
// there is no way for a profile or an operator to widen, narrow, or disable it.
//
// The reason is recoverability. Under ebpf/both enforcement /etc/resolv.conf
// points at the on-box resolver and DNS is neither uid- nor cgroup-scoped, so
// the resolver answers for ROOT services too — amazon-ssm-agent above all.
// Root's destinations can barely appear in the census (the audit consumer only
// watches the sandbox cgroup), so they would be pinned out by construction. A
// pin that NXDOMAINs .amazonaws.com kills the SSM control plane, and there is
// no un-pin verb, no `km shell`, and no remote destroy left to fix it with —
// the only recovery is terminating the instance.
//
// A pin must never be able to sever the control plane that would let an
// operator recover the box. Deliberately minimal: this is not "hosts we think
// are useful", it is "the channel through which a mistake can still be undone".
// Everything else a sandbox reaches remains fully pinnable.
//
// Note this floor does NOT apply to denies. `km-netpolicy deny .amazonaws.com`
// still works exactly as before: a deny is an explicit, named act, where a pin
// denies everything nobody named.
var platformEssentialSuffixes = []string{".amazonaws.com"}

// PlatformEssentialSuffixes returns the compiled-in suffixes no pin can remove.
// A copy, so a caller printing them cannot alter the floor.
func PlatformEssentialSuffixes() []string {
	out := make([]string, len(platformEssentialSuffixes))
	copy(out, platformEssentialSuffixes)
	return out
}

// IsPlatformEssential reports whether host is covered by the compiled-in
// carve-out that pins can never remove.
func IsPlatformEssential(host string) bool {
	return MatchAllow(host, platformEssentialSuffixes)
}

// PinGeneration is one appended pin block, split by the matcher each entry
// narrows.
//
// An EMPTY scope list denies everything in that scope. That is deliberate and
// load-bearing: ParsePinBlocks retains a generation whose entries were all
// dropped as malformed rather than discarding it, and discarding — or reading
// an empty list as "unconstrained" — would silently widen the intersection back
// out. `km-netpolicy pin` always writes both scopes, so an empty scope only
// arises from a corrupt or hand-edited file, where deny-all is the safe reading.
type PinGeneration struct {
	// DNS narrows name resolution: the DNS proxy and the eBPF resolver.
	DNS []string
	// Hosts narrows host decisions: the HTTP proxy's CONNECT and request paths.
	Hosts []string
}

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
	gens      []PinGeneration
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
func (s *PinStore) Generations() []PinGeneration {
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
func (s *PinStore) Reload() []PinGeneration {
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
// one. An entry may carry a "dns:" or "host:" scope prefix; an untagged entry
// applies to both scopes. Patterns are validated with ParseLine after the
// prefix is stripped, so a malformed entry is dropped exactly as it is in a
// deny list. A generation whose entries were ALL dropped is retained as an
// empty generation rather than discarded — dropping it would silently widen the
// intersection back out.
func ParsePinBlocks(body string) []PinGeneration {
	var gens []PinGeneration
	cur := -1
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, pinHeaderPrefix) {
			gens = append(gens, PinGeneration{})
			cur = len(gens) - 1
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if cur < 0 {
			// Entries before any header belong to an implicit first generation.
			gens = append(gens, PinGeneration{})
			cur = 0
		}

		entry, dns, host := trimmed, true, true
		switch {
		case strings.HasPrefix(trimmed, pinScopeDNS):
			entry, host = strings.TrimPrefix(trimmed, pinScopeDNS), false
		case strings.HasPrefix(trimmed, pinScopeHost):
			entry, dns = strings.TrimPrefix(trimmed, pinScopeHost), false
		}

		p, ok := ParseLine(entry)
		if !ok {
			continue
		}
		if dns {
			gens[cur].DNS = append(gens[cur].DNS, p)
		}
		if host {
			gens[cur].Hosts = append(gens[cur].Hosts, p)
		}
	}
	return gens
}

// FormatPinBlock renders one generation for appending to the pin file.
//
// Both scopes are always written, and always tagged. The two lists are equal in
// the collapsed default; they diverge under --exact, where DNS stays collapsed
// to eTLD+1 (an exact-match DNS allowlist stops resolving almost immediately)
// while hosts hold the literal observed names. Writing one flat untagged list
// is what made --exact inert: a collapsed ".github.com" entry consulted by the
// host matcher matches every subdomain, so the literal hosts beside it narrowed
// nothing.
func FormatPinBlock(n int, at time.Time, dns, hosts []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%d %s\n", pinHeaderPrefix, n, at.UTC().Format(time.RFC3339))
	for _, p := range dns {
		b.WriteString(pinScopeDNS)
		b.WriteString(p)
		b.WriteString("\n")
	}
	for _, p := range hosts {
		b.WriteString(pinScopeHost)
		b.WriteString(p)
		b.WriteString("\n")
	}
	return b.String()
}

// Pinner answers the allow-side question every egress decision needs once pins
// exist: does this destination survive every pin generation?
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

// AllowsDNS reports whether name survives every generation's DNS scope. This is
// the question the DNS proxy and the eBPF resolver ask.
func (p *Pinner) AllowsDNS(name string) bool {
	if p == nil || p.store == nil {
		return true
	}
	return PinsAllowDNS(name, p.store.Generations())
}

// AllowsHost reports whether host survives every generation's host scope. This
// is the question the HTTP proxy asks.
func (p *Pinner) AllowsHost(host string) bool {
	if p == nil || p.store == nil {
		return true
	}
	return PinsAllowHost(host, p.store.Generations())
}

// Allows reports whether host survives BOTH scopes.
//
// This is the conservative answer, for a decision that grants reachability
// through both layers at once — the boot-time BPF pre-seed loop, which puts an
// IP straight into the allow trie and so bypasses the per-query checks the two
// scoped matchers perform.
func (p *Pinner) Allows(host string) bool {
	return p.AllowsDNS(host) && p.AllowsHost(host)
}

// PinsAllowDNS reports whether name survives the DNS scope of every generation.
func PinsAllowDNS(name string, gens []PinGeneration) bool {
	return pinsAllow(name, gens, func(g PinGeneration) []string { return g.DNS })
}

// PinsAllowHost reports whether host survives the host scope of every
// generation.
func PinsAllowHost(host string, gens []PinGeneration) bool {
	return pinsAllow(host, gens, func(g PinGeneration) []string { return g.Hosts })
}

// PinsAllow reports whether host survives BOTH scopes of every generation.
func PinsAllow(host string, gens []PinGeneration) bool {
	return PinsAllowDNS(host, gens) && PinsAllowHost(host, gens)
}

// pinsAllow is the shared intersection walk.
//
// No generations means unpinned, so everything passes and a box behaves exactly
// as it did before pins existed. Otherwise host must match in EVERY generation:
// the effective allow set is the intersection, so each pin can only ever shrink
// it. That monotonicity is what makes "pin never widens" a property of the data
// structure rather than a rule anyone has to trust.
//
// The platform carve-out is the single exception — see platformEssentialSuffixes
// for why a pin must not be able to sever the control plane that would let an
// operator recover the box.
func pinsAllow(host string, gens []PinGeneration, scope func(PinGeneration) []string) bool {
	if len(gens) == 0 {
		return true
	}
	if IsPlatformEssential(host) {
		return true
	}
	for _, g := range gens {
		if !MatchAllow(host, scope(g)) {
			return false
		}
	}
	return true
}

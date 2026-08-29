package flowlog

import (
	"net"
	"sort"

	"github.com/whereiskurt/klanker-maker/pkg/allowlistgen"
)

// PinCandidates turns a census into the two lists a pin writes: DNS suffixes
// and hosts.
//
// Only Census.Allowed is consulted. A denied destination was never in the allow
// set, so it cannot appear in an intersection of allow sets — excluding it is
// what stops the census being a route to widening policy.
//
// Address-only observations are skipped. An allowlist is expressed in names,
// and a bare address is not a name; pinning one would produce an entry no
// matcher on either side would ever hit.
//
// DNS suffixes are ALWAYS collapsed to eTLD+1, regardless of exact. Names under
// a domain vary per request, so an exact-match DNS allowlist stops resolving
// almost immediately. CollapseToDNSSuffixes also skips anything that cannot
// reduce to an eTLD+1, so a pin can never generate a bare ".com".
//
// Hosts collapse by default and stay literal under exact. The default sits on
// the loose side deliberately: pin is irreversible, so too-loose costs a
// follow-up pin while too-tight costs a destroy/create. The two lists gate two
// different matchers (see netpolicy.PinGeneration), which is what makes exact
// narrow anything at all.
//
// IP literals never reach CollapseToDNSSuffixes. publicsuffix does not error on
// "1.2.3.4" — it returns "3.4" under the unmanaged-TLD rule — so a direct-to-IP
// HTTPS connection recorded by the HTTP proxy would otherwise put a nonsense
// ".3.4" suffix into the pin file and into any generated SandboxProfile. They
// are carried through to the host list verbatim instead, where the host matcher
// compares them exactly and reachability is preserved.
func PinCandidates(c Census, exact bool) (suffixes []string, hosts []string) {
	var names, literals []string
	for _, d := range c.Allowed {
		if d.Host == "" {
			continue
		}
		if net.ParseIP(d.Host) != nil {
			literals = append(literals, d.Host)
			continue
		}
		names = append(names, d.Host)
	}

	suffixes = allowlistgen.CollapseToDNSSuffixes(names)
	if !exact {
		return suffixes, sortedUnique(append(append([]string{}, suffixes...), literals...))
	}
	return suffixes, sortedUnique(append(append([]string{}, names...), literals...))
}

// sortedUnique deduplicates and sorts, so a pin block is deterministic and an
// operator diffing two dry-runs sees only real changes.
func sortedUnique(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

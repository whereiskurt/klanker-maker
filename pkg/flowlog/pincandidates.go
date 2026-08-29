package flowlog

import (
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
// follow-up pin while too-tight costs a destroy/create.
func PinCandidates(c Census, exact bool) (suffixes []string, hosts []string) {
	var names []string
	for _, d := range c.Allowed {
		if d.Host == "" {
			continue
		}
		names = append(names, d.Host)
	}

	suffixes = allowlistgen.CollapseToDNSSuffixes(names)
	if !exact {
		return suffixes, suffixes
	}

	seen := map[string]struct{}{}
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		hosts = append(hosts, n)
	}
	sort.Strings(hosts)
	return suffixes, hosts
}

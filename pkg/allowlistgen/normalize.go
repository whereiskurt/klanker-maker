package allowlistgen

import (
	"sort"

	"golang.org/x/net/publicsuffix"
)

// CollapseToDNSSuffixes converts a list of FQDNs into a deduplicated, sorted
// list of eTLD+1 DNS suffixes prefixed with ".".
//
// For example, ["api.github.com", "github.com"] → [".github.com"].
//
// Domains that cannot be reduced to an eTLD+1 (e.g. bare TLDs like "com")
// are skipped to prevent over-permissive suffixes.
func CollapseToDNSSuffixes(domains []string) []string {
	seen := make(map[string]struct{})

	for _, domain := range domains {
		etld1, err := publicsuffix.EffectiveTLDPlusOne(domain)
		if err != nil {
			// Bare TLD or invalid — skip to avoid over-permissive suffix.
			continue
		}
		suffix := "." + etld1
		seen[suffix] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

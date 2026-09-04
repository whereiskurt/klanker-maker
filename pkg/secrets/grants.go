package secrets

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnknownConsumer is returned when a caller claims an identity that the
// profile's grants map does not define. Returning the full bundle instead
// would make --as silently meaningless, which is worse than a refusal.
var ErrUnknownConsumer = errors.New("secrets: unknown consumer")

// Resolve computes the effective key set for one unseal.
//
// Precedence, narrowing only at every step:
//
//	base = available                     when grants is empty, or as is ""
//	base = available ∩ grants[as]        when as names a granted consumer
//	base = error                         when grants is non-empty and as is not in it
//	result = base ∩ only                 when only is non-empty
//
// `only` intersects and never widens: naming a key outside the identity's
// grant does not obtain it. This is the same disposition as km-netpolicy deny
// and pin — every runtime lever in this platform tightens and none loosens.
//
// The result is sorted so audit events are diffable across unseals.
func Resolve(available []string, grants Grants, as string, only []string) ([]string, error) {
	have := make(map[string]bool, len(available))
	for _, k := range available {
		have[k] = true
	}

	base := available
	if len(grants) > 0 && as != "" {
		granted, ok := grants[as]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownConsumer, as)
		}
		base = granted
	}

	allowed := make(map[string]bool, len(base))
	for _, k := range base {
		if have[k] { // a grant may name a key a later bundle revision dropped
			allowed[k] = true
		}
	}

	if len(only) > 0 {
		narrowed := make(map[string]bool, len(only))
		for _, k := range only {
			if allowed[k] {
				narrowed[k] = true
			}
		}
		allowed = narrowed
	}

	out := make([]string, 0, len(allowed))
	for k := range allowed {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

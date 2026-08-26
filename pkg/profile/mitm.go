package profile

import "reflect"

// InterceptEnabled reports whether an intercept is enabled. A nil Enabled
// pointer means enabled (the field's YAML default is true — see
// MITMIntercept.Enabled's doc comment for why it must be a pointer rather
// than a plain bool).
func InterceptEnabled(ic MITMIntercept) bool {
	return ic.Enabled == nil || *ic.Enabled
}

// CollapseIntercepts resolves a list of intercepts by name, last-wins,
// WHOLE ENTRY.
//
// Phase 117's deepMerge unions lists (concatDedup keeps the first occurrence
// of each distinct element and appends anything new), so without this
// post-merge step a base fragment's intercept and a leaf's same-named
// override would both survive a Resolve() as two separate list entries —
// making "enabled: false" on the leaf dead weight, since the base's enabled
// copy would still be present.
//
// The override replaces the inherited entry OUTRIGHT rather than merging
// field-by-field. Consequence: a leaf can switch a rule off with just
// `name` + `enabled: false` (there is nothing left to inherit once the
// entry is dropped), but a leaf that wants to keep a rule ENABLED while
// changing anything about it must restate `hosts` and `action` in full —
// there is nothing to inherit them from once the whole entry is replaced.
// Field-level merge was considered and rejected: it would re-import the
// Phase 117 list-union narrowing limitation into `hosts` (a leaf could
// never shrink an inherited host list, only add to it), which defeats the
// entire point of an override mechanism.
//
// Ordering rule: the output keeps each name at the index of its FIRST
// appearance, but carries the body of its LAST appearance. This matters
// because the sidecar matches first-match across the rule list — allowing a
// leaf to silently reorder a base's rules just by overriding one of them
// would be a surprising, hard-to-review side effect of an unrelated change.
//
// CollapseIntercepts is nil-safe and idempotent: calling it twice, or
// calling it on an already-collapsed list, returns an equivalent list.
func CollapseIntercepts(in []MITMIntercept) []MITMIntercept {
	if len(in) == 0 {
		return in
	}
	order := make([]string, 0, len(in))
	byName := make(map[string]MITMIntercept, len(in))
	for _, ic := range in {
		if _, seen := byName[ic.Name]; !seen {
			order = append(order, ic.Name)
		}
		byName[ic.Name] = ic // last-wins
	}
	out := make([]MITMIntercept, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}

// EnabledIntercepts collapses in (by name, last-wins) and then filters to
// only the entries that are enabled. This is the accessor the compiler
// (plan 03) uses to decide what actually reaches the box.
//
// It always collapses first, even though fromMap already collapses on every
// Resolve(): this keeps EnabledIntercepts correct and nil-safe for a
// profile that was never passed through Resolve() (e.g. a profile with no
// extends: at all, or one constructed directly in a test), rather than
// silently trusting the caller to have collapsed already.
func EnabledIntercepts(in []MITMIntercept) []MITMIntercept {
	collapsed := CollapseIntercepts(in)
	out := make([]MITMIntercept, 0, len(collapsed))
	for _, ic := range collapsed {
		if InterceptEnabled(ic) {
			out = append(out, ic)
		}
	}
	return out
}

// DuplicateInterceptNames returns, in first-appearance order, every name
// that appears more than once in in with at least two entries that are NOT
// reflect.DeepEqual to each other. Identical repeats are not reported —
// only CONFLICTING content sharing a name is treated as an error (plan 04
// uses this to flag a same-file duplicate as a typo). The same name
// recurring across an extends: DAG is the intended override path and is
// legal — but by the time a caller sees the MERGED result via fromMap, any
// duplicate remaining in it can only have come from within a single file
// (see CollapseIntercepts's wiring in fromMap), which is exactly what makes
// treating it as a typo safe.
func DuplicateInterceptNames(in []MITMIntercept) []string {
	seen := make(map[string]MITMIntercept)
	order := make([]string, 0)
	conflict := make(map[string]bool)
	for _, ic := range in {
		prev, ok := seen[ic.Name]
		if !ok {
			seen[ic.Name] = ic
			order = append(order, ic.Name)
			continue
		}
		if !reflect.DeepEqual(prev, ic) {
			conflict[ic.Name] = true
		}
	}
	out := make([]string, 0, len(order))
	for _, name := range order {
		if conflict[name] {
			out = append(out, name)
		}
	}
	return out
}

// ProfileIntercepts is the nil-safe accessor for a profile's raw (not yet
// filtered by enabled/collapsed) intercept list. A nil profile, a nil
// Spec.Network.MITM, or a nil Intercepts field all return nil rather than
// panicking.
func ProfileIntercepts(p *SandboxProfile) []MITMIntercept {
	if p == nil {
		return nil
	}
	if p.Spec.Network.MITM == nil {
		return nil
	}
	return p.Spec.Network.MITM.Intercepts
}

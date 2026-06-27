// Package quota — resolve.go
// ResolveLimits merges per-profile limit overrides with install-level defaults
// for each (action, window), implementing the precedence rule:
//
//	profile value if set → install default if set → unlimited (nil)
//
// A profile that sets only perHour for an action still inherits the install
// default's lifetime and perDay for that action. OnBreach: profile wins, else
// install default, else empty string (callers should fall back to BreachWarn).
package quota

// ResolveLimits merges profile and install-default limit maps per the
// per-(action, window) precedence rule described in CONTEXT.md §4.
//
// For each action present in either map:
//   - Each window field (Lifetime, PerHour, PerDay) resolves independently:
//     profile wins if non-nil, else install default if non-nil, else nil (unlimited).
//   - OnBreach: profile wins if non-empty, else install default if non-empty, else "".
//
// Actions present in only one map are included as-is from that map.
func ResolveLimits(profileLimits, installDefaults Limits) Limits {
	resolved := make(Limits)

	// Collect the union of all action keys from both maps.
	seen := make(map[Action]bool)
	for a := range installDefaults {
		seen[a] = true
	}
	for a := range profileLimits {
		seen[a] = true
	}

	for action := range seen {
		prof, hasProfAction := profileLimits[action]
		def, hasDefAction := installDefaults[action]

		merged := ActionLimit{}

		// Lifetime: profile wins if set, else install default, else nil.
		if hasProfAction && prof.Lifetime != nil {
			v := *prof.Lifetime
			merged.Lifetime = &v
		} else if hasDefAction && def.Lifetime != nil {
			v := *def.Lifetime
			merged.Lifetime = &v
		}

		// PerHour: profile wins if set, else install default, else nil.
		if hasProfAction && prof.PerHour != nil {
			v := *prof.PerHour
			merged.PerHour = &v
		} else if hasDefAction && def.PerHour != nil {
			v := *def.PerHour
			merged.PerHour = &v
		}

		// PerDay: profile wins if set, else install default, else nil.
		if hasProfAction && prof.PerDay != nil {
			v := *prof.PerDay
			merged.PerDay = &v
		} else if hasDefAction && def.PerDay != nil {
			v := *def.PerDay
			merged.PerDay = &v
		}

		// OnBreach: profile wins if non-empty, else install default, else "".
		if hasProfAction && prof.OnBreach != "" {
			merged.OnBreach = prof.OnBreach
		} else if hasDefAction && def.OnBreach != "" {
			merged.OnBreach = def.OnBreach
		}

		resolved[action] = merged
	}

	return resolved
}

package cmd

import (
	"fmt"
	"strings"
)

// isProfilePathArg reports whether a positional argument looks like a
// SandboxProfile path. Extension-only, case-insensitive — the file need not
// exist yet (a missing path is reported later by the profile loader with a
// better message than anything this function could produce).
func isProfilePathArg(arg string) bool {
	lower := strings.ToLower(arg)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

// resolveCreateArgs maps `km create`'s positional arguments onto a profile path
// and an alias.
//
// Two accepted forms:
//
//	km create <profile.yaml> [--alias <alias>]   // original, unchanged
//	km create <profile.yaml> <alias>             // order-independent
//
// In the two-argument form the profile is the argument ending in .yaml/.yml and
// the alias is the other one, so `km create orc profiles/dc34.yaml` and
// `km create profiles/dc34.yaml orc` are equivalent.
//
// Ambiguity is always an error rather than a guess: two YAML-looking arguments,
// zero YAML-looking arguments, or a positional alias combined with an explicit
// --alias all fail with a message naming the conflict. Silent precedence
// between two alias sources would be a footgun — the sandbox would be created
// under a name the operator did not type.
func resolveCreateArgs(args []string, aliasFlag string) (profilePath string, alias string, err error) {
	switch len(args) {
	case 1:
		// Single positional is always the profile, YAML-looking or not. Keeping
		// this branch extension-agnostic preserves the pre-change behaviour for
		// every path shape that used to work.
		return args[0], aliasFlag, nil

	case 2:
		first, second := isProfilePathArg(args[0]), isProfilePathArg(args[1])
		switch {
		case first && second:
			return "", "", fmt.Errorf(
				"ambiguous arguments: %q and %q both look like profile paths — pass one profile and one alias, or use --alias",
				args[0], args[1])
		case !first && !second:
			return "", "", fmt.Errorf(
				"no profile given: neither %q nor %q ends in .yaml or .yml — usage: km create <profile.yaml> [alias]",
				args[0], args[1])
		}

		if aliasFlag != "" {
			return "", "", fmt.Errorf(
				"alias given twice: positional alias and --alias %q — pass only one", aliasFlag)
		}

		if first {
			return args[0], args[1], nil
		}
		return args[1], args[0], nil

	default:
		return "", "", fmt.Errorf("expected a profile path: km create <profile.yaml> [alias]")
	}
}

package cmd

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// ValidateLaunchAccountLink is the config-aware half of the Phase 126
// spec.runtime.launchAccount validation. It lives here (not in
// pkg/profile.ValidateSemantic) because it needs *config.Config to resolve the
// link name, and pkg/profile importing internal/app/config would be a layering
// violation (126-RESEARCH.md Pitfall 4). The profile-only mutual-exclusion check
// (launchAccount + privateSubnet) needs no config and lives in ValidateSemantic.
//
// Dormant when p.Spec.Runtime.LaunchAccount is empty: no config lookup is
// performed at all, so a profile that omits launchAccount is byte-identical to
// Phase 125 regardless of what launch_accounts: contains.
func ValidateLaunchAccountLink(p *profile.SandboxProfile, cfg *config.Config) []profile.ValidationError {
	name := p.Spec.Runtime.LaunchAccount
	if name == "" {
		return nil
	}

	link, ok := cfg.GetLaunchAccount(name)
	if !ok {
		known := make([]string, 0, len(cfg.GetLaunchAccounts()))
		for k := range cfg.GetLaunchAccounts() {
			known = append(known, k)
		}
		sort.Strings(known)

		knownMsg := "none configured"
		if len(known) > 0 {
			knownMsg = strings.Join(known, ", ")
		}

		return []profile.ValidationError{{
			Path: "spec.runtime.launchAccount",
			Message: fmt.Sprintf(
				"launchAccount %q is not a registered link in km-config.yaml launch_accounts (known links: %s) — register it with `km account register %s` or fix the typo",
				name, knownMsg, name,
			),
		}}
	}

	// Warning, not an error: the launcher role's own IAM policy will reject the
	// launch at request time, but an operator authoring locally may not have
	// synced the latest allowlist yet — don't hard-block km validate on it.
	if len(link.InstanceTypes) > 0 && !slices.Contains(link.InstanceTypes, p.Spec.Runtime.InstanceType) {
		return []profile.ValidationError{{
			Path: "spec.runtime.launchAccount",
			Message: fmt.Sprintf(
				"instanceType %q is not in launch_accounts.%s's instance_types allowlist (%s) — the launcher role's own IAM policy will reject this launch",
				p.Spec.Runtime.InstanceType, name, strings.Join(link.InstanceTypes, ", "),
			),
			IsWarning: true,
		}}
	}

	return nil
}

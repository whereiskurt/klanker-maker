package compiler

import (
	"fmt"
	"time"

	"github.com/whereiskurt/klankrmkr/pkg/profile"
)

// compileSGRules returns the baseline SG egress rules compiled from the profile.
// Phase 2 emits two baseline rules: HTTPS (TCP 443) for SSM agent / HTTPS traffic
// and DNS (UDP 53) for name resolution. Phase 3 will tighten these when proxy
// sidecars handle per-host filtering.
func compileSGRules(p *profile.SandboxProfile) []SGRule {
	return []SGRule{
		{
			FromPort:    443,
			ToPort:      443,
			Protocol:    "tcp",
			CIDRBlocks:  []string{"0.0.0.0/0"},
			Description: "HTTPS egress for SSM agent and outbound API traffic",
		},
		{
			FromPort:    53,
			ToPort:      53,
			Protocol:    "udp",
			CIDRBlocks:  []string{"0.0.0.0/0"},
			Description: "DNS egress for name resolution",
		},
	}
}

// compileIAMPolicy parses the profile's identity spec and returns an IAMSessionPolicy.
// roleSessionDuration is parsed as a Go duration string (e.g. "1h", "2h30m").
// Default is 3600 seconds (1 hour) if not set or unparseable.
func compileIAMPolicy(p *profile.SandboxProfile) *IAMSessionPolicy {
	maxDuration := 3600 // default 1 hour

	if d := p.Spec.Identity.RoleSessionDuration; d != "" {
		if parsed, err := time.ParseDuration(d); err == nil {
			maxDuration = int(parsed.Seconds())
		}
	}

	regions := make([]string, len(p.Spec.Identity.AllowedRegions))
	copy(regions, p.Spec.Identity.AllowedRegions)

	return &IAMSessionPolicy{
		MaxSessionDuration: maxDuration,
		AllowedRegions:     regions,
	}
}

// compileSecrets builds the list of SSM parameter paths to inject at boot.
// It reads identity.allowedSecretPaths from the profile and appends
// /km/github/app-token if sourceAccess.github is configured.
func compileSecrets(p *profile.SandboxProfile) []string {
	var paths []string

	// Add profile-defined secret paths
	paths = append(paths, p.Spec.Identity.AllowedSecretPaths...)

	// Add GitHub App token path if GitHub access is configured
	if p.Spec.SourceAccess.GitHub != nil {
		paths = append(paths, "/km/github/app-token")
	}

	return paths
}

// sgRuleToHCL serializes a single SGRule into an HCL object literal string
// suitable for embedding in a list within a service.hcl locals block.
func sgRuleToHCL(r SGRule) string {
	cidrs := ""
	for i, c := range r.CIDRBlocks {
		if i > 0 {
			cidrs += ", "
		}
		cidrs += fmt.Sprintf("%q", c)
	}
	return fmt.Sprintf(
		"{ from_port = %d, to_port = %d, protocol = %q, cidr_blocks = [%s], description = %q }",
		r.FromPort, r.ToPort, r.Protocol, cidrs, r.Description,
	)
}

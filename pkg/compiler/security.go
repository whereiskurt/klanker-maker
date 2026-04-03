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
	rules := []SGRule{
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
	// EFS mount requires NFS egress (port 2049) to reach mount targets in the VPC.
	if p.Spec.Runtime.MountEFS {
		rules = append(rules, SGRule{
			FromPort:    2049,
			ToPort:      2049,
			Protocol:    "tcp",
			CIDRBlocks:  []string{"0.0.0.0/0"},
			Description: "NFS egress for EFS shared filesystem",
		})
	}
	return rules
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
// It reads identity.allowedSecretPaths from the profile.
// Note: The GitHub token is NOT injected via SecretPaths — it is stored per-sandbox
// at /sandbox/{sandbox-id}/github-token and read at git-operation time by the
// GIT_ASKPASS credential helper script installed in section 4 of userdata.go.
func compileSecrets(p *profile.SandboxProfile) []string {
	var paths []string

	// Add profile-defined secret paths
	paths = append(paths, p.Spec.Identity.AllowedSecretPaths...)

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

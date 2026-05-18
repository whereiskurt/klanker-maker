package cmd_test

// bootstrap_scp_test.go — Phase 84.4.1 Plan 01 rewrite of SCP builder tests.
//
// Phase 84.4.1: SCP cross-install composition — trusted ARN slots now use
// *-* suffix patterns (account + prefix both wildcarded) so that multiple
// installs sharing an application account do not deny each other's roles.
//
// Previous behavior (Phase 84.4): prefix "km" → "km-ecs-spot-handler" etc.
// New behavior (Phase 84.4.1): any prefix → "*-ecs-spot-handler" etc.
// resourcePrefix and applicationAccountID accepted for signature compat but
// no longer used in trust slot construction.
//
// Security trade-off: an attacker-deployed role named evil-create-handler
// would pass the SCP pattern. Mitigations: operator-only IAM:CreateRole in
// the application account; SCP is one defense-in-depth layer; deployed roles
// still need cross-account assume-role grants. Documented in OPERATOR-GUIDE.md
// (Wave 4 plan 84.4.1-06).

import (
	"strings"
	"testing"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

func TestBuildSCPPolicySize(t *testing.T) {
	// Phase 84.4.1: all prefixes produce IDENTICAL output because prefix is ignored
	// in the slot values. The parameterization is kept defensively — confirms that
	// prefix truly does not appear in the trust slots for any input prefix.
	cases := []struct {
		prefix   string
		maxBytes int
	}{
		{"km", 5000},
		{"kph", 5000},
		{"whereiskurt", 5000}, // 11-char prefix — upper bound case
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			got := cmd.BuildSCPPolicyFromPrefix(tc.prefix, "123456789012", "us-east-1")
			if len(got) > tc.maxBytes {
				t.Errorf("policy size %d bytes exceeds threshold %d for prefix %q", len(got), tc.maxBytes, tc.prefix)
			}
			// Phase 84.4.1: assert *-* pattern (prefix-agnostic)
			expectedRoleSubstring := ":role/*-ecs-spot-handler"
			if !strings.Contains(got, expectedRoleSubstring) {
				t.Errorf("expected policy to contain %q, got: %s", expectedRoleSubstring, got)
			}
			// Phase 84.4.1: negative assertion — no prefix-bound substring must appear (catches regressions).
			if strings.Contains(got, ":role/"+tc.prefix+"-ecs-spot-handler") {
				t.Errorf("policy[%s] contains prefix-bound substring; expected *-pattern", tc.prefix)
			}
		})
	}
}

func TestBuildSCPPolicyPrefix(t *testing.T) {
	// Phase 84.4.1: patterns are prefix-agnostic; verify *-* substrings present.
	got := cmd.BuildSCPPolicyFromPrefix("km", "123456789012", "us-east-1")

	// Positive assertions: *-* patterns must be present.
	for _, want := range []string{
		"*-ecs-spot-handler",
		"*-budget-enforcer-*",
		"*-ec2spot-ssm-*",
		"*-github-token-refresher-*",
		"*-ttl-handler",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from rendered policy", want)
		}
	}

	// Negative assertions: prefix-bound role names MUST NOT appear (catches re-prefixing regressions).
	for _, notWanted := range []string{
		"km-ecs-spot-handler",
		"km-budget-enforcer-*",
		"km-ec2spot-ssm-*",
		"km-github-token-refresher-*",
		"km-ttl-handler",
	} {
		if strings.Contains(got, notWanted) {
			t.Errorf("policy contains prefix-bound %q; expected *-pattern", notWanted)
		}
	}
}

package cmd_test

// bootstrap_scp_test.go — Phase 84.4 Plan 02 unit tests for BuildSCPPolicyFromPrefix.
//
// These tests verify that the simplified per-prefix SCP builder:
//   - renders policy JSON under the 5,000-byte Go-side safety threshold
//   - templates role names from the prefix (no km- for non-km prefixes)
//   - preserves backward compat: prefix "km" → byte-identical role names to v1.0.0
//
// The existing BuildSCPPolicy (accepting pre-computed ARN arrays) is tested in
// bootstrap_test.go; this file tests the new prefix-based wrapper.

import (
	"strings"
	"testing"

	cmd "github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

func TestBuildSCPPolicySize(t *testing.T) {
	cases := []struct {
		prefix   string
		maxBytes int
		wantNoKM bool // for non-km prefixes, assert ":role/km-" doesn't appear
	}{
		{"km", 5000, false},
		{"kph", 5000, true},
		{"whereiskurt", 5000, true}, // 11-char prefix — upper bound case
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			got := cmd.BuildSCPPolicyFromPrefix(tc.prefix, "123456789012", "us-east-1")
			if len(got) > tc.maxBytes {
				t.Errorf("policy size %d bytes exceeds threshold %d for prefix %q", len(got), tc.maxBytes, tc.prefix)
			}
			if tc.wantNoKM && strings.Contains(got, ":role/km-") {
				t.Errorf("non-km prefix %q produced policy still containing km- role names: %s", tc.prefix, got)
			}
			expectedRoleSubstring := ":role/" + tc.prefix + "-ecs-spot-handler"
			if !strings.Contains(got, expectedRoleSubstring) {
				t.Errorf("expected policy to contain %q, got: %s", expectedRoleSubstring, got)
			}
		})
	}
}

func TestBuildSCPPolicyPrefix(t *testing.T) {
	// Backward compat: BuildSCPPolicyFromPrefix("km", ...) renders identical role patterns to pre-84.4
	got := cmd.BuildSCPPolicyFromPrefix("km", "123456789012", "us-east-1")
	for _, want := range []string{
		"km-ecs-spot-handler",
		"km-budget-enforcer-*",
		"km-ec2spot-ssm-*",
		"km-github-token-refresher-*",
		"km-ttl-handler",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("km-default policy missing expected role name %q", want)
		}
	}
}

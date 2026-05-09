package cmd

import "testing"

func TestNameContainsSandboxIDToken(t *testing.T) {
	cases := []struct {
		name, sbID string
		want       bool
	}{
		// Suffix matches (IAM/KMS happy path).
		{"km-budget-enforcer-lrn2-ee9499b5", "lrn2-ee9499b5", true},
		{"alias/km-github-token-sb-aabbccdd", "sb-aabbccdd", true},
		// Middle-token matches (schedules, regional KMS aliases).
		{"km-at-destroy-lrn2-ee9499b5-Mar15", "lrn2-ee9499b5", true},
		{"alias/km-docker-sb-aabbccdd-use1", "sb-aabbccdd", true},
		// False-positives the old strings.Contains would have hit.
		{"km-debug-thing-lrn2-ee9499b5xtra", "lrn2-ee9499b5", false},
		{"km-superlrn2-ee9499b5", "lrn2-ee9499b5", false},
		// Different sandbox IDs — no match.
		{"km-budget-enforcer-lrn2-aabbccdd", "lrn2-ee9499b5", false},
		// Empty inputs.
		{"", "sb-aabbccdd", false},
		{"km-foo", "", false},
		// Sandbox ID at start of string (rare but possible).
		{"sb-aabbccdd", "sb-aabbccdd", true},
		{"sb-aabbccdd-suffix", "sb-aabbccdd", true},
		{"sb-aabbccddextra", "sb-aabbccdd", false},
	}
	for _, tc := range cases {
		if got := nameContainsSandboxIDToken(tc.name, tc.sbID); got != tc.want {
			t.Errorf("nameContainsSandboxIDToken(%q, %q) = %v, want %v",
				tc.name, tc.sbID, got, tc.want)
		}
	}
}

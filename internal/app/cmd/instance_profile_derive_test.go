package cmd

import "testing"

// TestInstanceProfileNameFromRoleARN pins the derivation that lets a cross-account
// sandbox reuse the linked account's box instance profile without threading a
// fourth field through the link record, the handoff fragment, `km account register`
// and km-config.yaml. infra/modules/gpu-launcher-account names the role and its
// instance profile identically, so the profile name is the role ARN's last path
// segment.
func TestInstanceProfileNameFromRoleARN(t *testing.T) {
	cases := []struct {
		name string
		arn  string
		want string
	}{
		{"real box role", "arn:aws:iam::481723467561:role/km-gpu-box", "km-gpu-box"},
		{"custom resource prefix", "arn:aws:iam::481723467561:role/acme-gpu-box", "acme-gpu-box"},
		{"pathed role keeps only the final segment", "arn:aws:iam::1:role/some/path/km-gpu-box", "km-gpu-box"},
		// Empty and malformed both fall back to "", which leaves ec2spot creating
		// its own profile — the pre-126 behaviour, and the safe direction to fail.
		{"empty", "", ""},
		{"no slash", "not-an-arn", ""},
		{"trailing slash", "arn:aws:iam::1:role/", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := instanceProfileNameFromRoleARN(tc.arn); got != tc.want {
				t.Errorf("instanceProfileNameFromRoleARN(%q) = %q, want %q", tc.arn, got, tc.want)
			}
		})
	}
}

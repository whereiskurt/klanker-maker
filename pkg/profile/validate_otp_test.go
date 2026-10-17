package profile_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// spec.otp.secrets buys ssm:GetParameter + DeleteParameter + KMS on exactly
// those paths (ec2spot v1.8.0). The grant is exact-path, so a wildcard or a
// stray space in an entry is granted nothing and the boot-time read fails.
// Struct-level on purpose: spec.otp is not in the JSON schema, so no YAML
// fixture can reach this rule through km validate today.
func TestValidateSemantic_OTPSecretsWarnOnWildcardOrWhitespace(t *testing.T) {
	cases := []struct {
		name     string
		entry    string
		wantWarn bool
	}{
		{"exact path", "/km/sandbox/sb-1/otp/github-token", false},
		{"no leading slash", "km/otp/x", false},
		{"star", "/km/otp/*", true},
		{"star mid", "/km/*/x", true},
		{"space", "/km/otp/a b", true},
		{"trailing newline", "/km/otp/a\n", true},
		{"tab", "\t/km/otp/a", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := otpBaseProfile(t)
			p.Spec.OTP = &profile.OTPSpec{Secrets: []string{"/km/otp/ok", c.entry}}
			var warn, hard bool
			for _, e := range profile.ValidateSemantic(p) {
				if e.Path == "spec.otp.secrets[1]" {
					if e.IsWarning {
						warn = true
					} else {
						hard = true
					}
				}
				if e.Path == "spec.otp.secrets[0]" {
					t.Errorf("exact entry [0] must not be flagged: %s", e.Message)
				}
			}
			if hard {
				t.Error("an OTP shape finding must be a WARN, never a hard error")
			}
			if warn != c.wantWarn {
				t.Errorf("warn at spec.otp.secrets[1] = %v, want %v", warn, c.wantWarn)
			}
		})
	}
}

func otpBaseProfile(t *testing.T) *profile.SandboxProfile {
	t.Helper()
	p, err := profile.Parse(maxLifetimeProfile(t, lifeOK))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func TestEC2ServiceHCL_EmitsEmailDomainAndOTPPaths(t *testing.T) {
	p := baseEC2Profile()
	p.Spec.OTP = &profile.OTPSpec{Secrets: []string{"/km/sandbox/sb-1/otp/github-token"}}
	network := baseEC2Network()
	network.EmailDomain = "sandboxes.example.com"
	out, err := generateEC2ServiceHCL(p, "sb-1", true, nil, &IAMSessionPolicy{MaxSessionDuration: 3600}, "", network, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`email_domain                      = "sandboxes.example.com"`,
		`otp_secret_paths = ["/km/sandbox/sb-1/otp/github-token"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("service.hcl missing %s\n%s", want, out)
		}
	}
}

func TestEC2ServiceHCL_OTPOmittedWhenAbsent_EmailDomainDefaults(t *testing.T) {
	p := baseEC2Profile()
	p.Spec.OTP = nil
	network := baseEC2Network()
	network.EmailDomain = ""
	out, err := generateEC2ServiceHCL(p, "sb-1", true, nil, &IAMSessionPolicy{MaxSessionDuration: 3600}, "", network, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "otp_secret_paths") {
		t.Error("otp_secret_paths must be omitted when the profile has no OTP secrets")
	}
	if !strings.Contains(out, `"`+defaultEmailDomain+`"`) || !strings.Contains(out, "email_domain") {
		t.Errorf("email_domain must always be emitted with the default:\n%s", out)
	}
}

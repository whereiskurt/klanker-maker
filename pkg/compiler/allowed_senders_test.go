// Package compiler — allowed_senders_test.go
//
// Inbound email sender allowlist defaults.
//
// Background: km-recv and the mail poller both wrapped their allowlist gate in
// `if [ -n "${KM_ALLOWED_SENDERS:-}" ]`, so an empty value meant "no gate" —
// unset failed OPEN. joinAllowedSenders returned "" whenever a profile omitted
// spec.email.allowedSenders, and profiles/base/platform.yaml (which nearly
// every profile extends) shipped ["*"]. Net effect: anyone on the internet
// could mail {sandbox-id}@<domain> and have the text delivered to the agent.
package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

const (
	testEmailDomain   = "sandboxes.example.com"
	testOperatorEmail = "operator-human@example.org"
)

// A profile that declares no email block must still get a closed default, not
// the empty string that disables the gate entirely.
func TestJoinAllowedSenders_DefaultsClosedWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *profile.SandboxProfile
	}{
		{name: "nil email block", p: &profile.SandboxProfile{}},
		{
			name: "email block with no allowedSenders",
			p:    &profile.SandboxProfile{Spec: profile.Spec{Email: &profile.EmailSpec{Signing: "required"}}},
		},
		{
			name: "email block with an empty allowedSenders list",
			p: &profile.SandboxProfile{Spec: profile.Spec{
				Email: &profile.EmailSpec{AllowedSenders: []string{}},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := joinAllowedSenders(tc.p, testEmailDomain, testOperatorEmail)

			if got == "" {
				t.Fatal("empty result re-opens the gate — both shell gates treat an empty " +
					"KM_ALLOWED_SENDERS as 'allow everything'")
			}
			if strings.Contains(got, "*:") || got == "*" || strings.HasSuffix(got, ":*") {
				t.Errorf("default must not be the allow-all wildcard, got %q", got)
			}

			parts := strings.Split(got, ":")
			want := map[string]bool{
				"self":                 false,
				"*@" + testEmailDomain: false,
				testOperatorEmail:      false,
			}
			for _, p := range parts {
				if _, ok := want[p]; ok {
					want[p] = true
				}
			}
			for k, seen := range want {
				if !seen {
					t.Errorf("default allowlist %q is missing %q", got, k)
				}
			}
		})
	}
}

// An operator who has not configured an operator email must not produce a
// trailing empty pattern — an empty element would match nothing but makes the
// value ambiguous to read and to test.
func TestJoinAllowedSenders_OmitsEmptyOperatorEmail(t *testing.T) {
	got := joinAllowedSenders(&profile.SandboxProfile{}, testEmailDomain, "")

	for _, part := range strings.Split(got, ":") {
		if strings.TrimSpace(part) == "" {
			t.Errorf("result %q contains an empty pattern", got)
		}
	}
	if !strings.Contains(got, "self") || !strings.Contains(got, "*@"+testEmailDomain) {
		t.Errorf("result %q lost the self / same-domain defaults", got)
	}
}

// An explicit allowlist is honoured verbatim — including "*". Narrowing the
// DEFAULT must not take away an operator's ability to deliberately open a
// sandbox up.
func TestJoinAllowedSenders_ExplicitListIsVerbatim(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want string
	}{
		{name: "explicit wildcard is preserved", in: []string{"*"}, want: "*"},
		{name: "explicit self only", in: []string{"self"}, want: "self"},
		{
			name: "explicit multi-pattern",
			in:   []string{"self", "build.*", "ops@partner.example"},
			want: "self:build.*:ops@partner.example",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &profile.SandboxProfile{Spec: profile.Spec{
				Email: &profile.EmailSpec{AllowedSenders: tc.in},
			}}
			if got := joinAllowedSenders(p, testEmailDomain, testOperatorEmail); got != tc.want {
				t.Errorf("joinAllowedSenders = %q, want %q", got, tc.want)
			}
		})
	}
}

// Both shell gates must enforce unconditionally. The `if [ -n ... ]` wrapper
// they used to carry made an empty value mean "allow everything", which is the
// wrong direction to fail for an ingress that feeds an autonomous agent.
func TestUserdata_AllowlistGatesDoNotFailOpen(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-allowlist", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	if strings.Contains(out, `if [ -n "${KM_ALLOWED_SENDERS:-}" ]; then`) {
		t.Error("an allowlist gate is still wrapped in a non-empty check — an empty " +
			"KM_ALLOWED_SENDERS would allow every sender")
	}

	// Both consumers must still be present; the gate was removed, not the check.
	if n := strings.Count(out, "KM_ALLOWED_SENDERS"); n < 2 {
		t.Errorf("expected KM_ALLOWED_SENDERS referenced by both the poller and km-recv, found %d references", n)
	}
}

// The emitted env var carries the closed default for a profile that declares no
// allowedSenders — the end-to-end version of the unit test above.
func TestUserdata_EmitsClosedDefaultAllowlist(t *testing.T) {
	p := baseProfile()
	p.Spec.Email = nil

	out, err := generateUserData(p, "sb-default", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if !strings.Contains(out, `export KM_ALLOWED_SENDERS="self:`) {
		t.Errorf("expected a self-prefixed default allowlist in the emitted userdata")
	}
	if strings.Contains(out, `export KM_ALLOWED_SENDERS=""`) {
		t.Error("emitted an empty allowlist — that disables the gate")
	}
}

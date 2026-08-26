package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// mitmBoolPtr is a tiny local helper — *bool literals can't be taken inline.
func mitmBoolPtr(b bool) *bool { return &b }

// mitmProfile builds the smallest SandboxProfile that exercises
// ValidateSemantic's MITM rules directly (no schema round-trip). Callers
// mutate the returned profile's Spec.Network fields (Egress, Enforcement)
// for the overlap-warning tests added in task 2.
func mitmProfile(intercepts []profile.MITMIntercept) *profile.SandboxProfile {
	return &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha2",
		Kind:       "SandboxProfile",
		Metadata:   profile.Metadata{Name: "mitm-test"},
		Spec: profile.Spec{
			Network: profile.NetworkSpec{
				MITM: &profile.NetworkMITMSpec{Intercepts: intercepts},
			},
		},
	}
}

func hasMITMError(errs []profile.ValidationError, substr string) bool {
	for _, e := range errs {
		if !e.IsWarning && strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// redirectIntercept builds a minimal valid enabled redirect intercept for use
// as a base that individual tests mutate to trigger one specific error.
func redirectIntercept(name string, hosts []string) profile.MITMIntercept {
	return profile.MITMIntercept{
		Name:  name,
		Hosts: hosts,
		Action: &profile.MITMAction{
			Redirect: "https://example.com/redirected",
		},
	}
}

// ─── Task 1: the five locked errors ───────────────────────────────────────

func TestValidateMITM_EmptyHosts_Error(t *testing.T) {
	ic := redirectIntercept("rule1", nil)
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "at least one host") {
		t.Errorf("expected an empty-hosts error, got: %v", errs)
	}
}

func TestValidateMITM_NoAction_Error(t *testing.T) {
	ic := profile.MITMIntercept{Name: "rule1", Hosts: []string{"api.example.com"}}
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "declare exactly one action") {
		t.Errorf("expected a no-action error, got: %v", errs)
	}
}

func TestValidateMITM_BothRedirectAndRespond_Error(t *testing.T) {
	ic := profile.MITMIntercept{
		Name:  "rule1",
		Hosts: []string{"api.example.com"},
		Action: &profile.MITMAction{
			Redirect: "https://example.com/redirected",
			Respond:  &profile.MITMRespond{Status: 503},
		},
	}
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "not both") {
		t.Errorf("expected a both-actions error, got: %v", errs)
	}
}

func TestValidateMITM_NeitherRedirectNorRespond_Error(t *testing.T) {
	ic := profile.MITMIntercept{
		Name:   "rule1",
		Hosts:  []string{"api.example.com"},
		Action: &profile.MITMAction{},
	}
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "declare exactly one of redirect or respond") {
		t.Errorf("expected a zero-actions error, got: %v", errs)
	}
}

func TestValidateMITM_Redirect_RelativePathRejected(t *testing.T) {
	ic := redirectIntercept("rule1", []string{"api.example.com"})
	ic.Action.Redirect = "/just/a/path"
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "absolute URL") {
		t.Errorf("expected a relative-path redirect error, got: %v", errs)
	}
}

func TestValidateMITM_Redirect_BareHostRejected(t *testing.T) {
	ic := redirectIntercept("rule1", []string{"api.example.com"})
	ic.Action.Redirect = "example.com"
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "absolute URL") {
		t.Errorf("expected a bare-host redirect error, got: %v", errs)
	}
}

func TestValidateMITM_Redirect_NonHTTPSchemeRejected(t *testing.T) {
	ic := redirectIntercept("rule1", []string{"api.example.com"})
	ic.Action.Redirect = "ftp://example.com/file"
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "absolute URL") {
		t.Errorf("expected a non-http-scheme redirect error, got: %v", errs)
	}
}

func TestValidateMITM_Redirect_HTTPAndHTTPSAccepted(t *testing.T) {
	for _, u := range []string{"http://example.com/x", "https://example.com/x"} {
		ic := redirectIntercept("rule1", []string{"api.example.com"})
		ic.Action.Redirect = u
		errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
		if hasMITMError(errs, "absolute URL") {
			t.Errorf("expected %q to be accepted, got: %v", u, errs)
		}
	}
}

func TestValidateMITM_RespondStatus_ZeroIsError(t *testing.T) {
	ic := profile.MITMIntercept{
		Name:   "rule1",
		Hosts:  []string{"api.example.com"},
		Action: &profile.MITMAction{Respond: &profile.MITMRespond{Status: 0}},
	}
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "between 100 and 599") {
		t.Errorf("expected a status-0 error, got: %v", errs)
	}
}

func TestValidateMITM_RespondStatus_599IsAccepted(t *testing.T) {
	ic := profile.MITMIntercept{
		Name:   "rule1",
		Hosts:  []string{"api.example.com"},
		Action: &profile.MITMAction{Respond: &profile.MITMRespond{Status: 599}},
	}
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if hasMITMError(errs, "between 100 and 599") {
		t.Errorf("expected status 599 to be accepted, got: %v", errs)
	}
}

func TestValidateMITM_RespondStatus_ValidValuesAccepted(t *testing.T) {
	for _, s := range []int{100, 301, 503, 599} {
		ic := profile.MITMIntercept{
			Name:   "rule1",
			Hosts:  []string{"api.example.com"},
			Action: &profile.MITMAction{Respond: &profile.MITMRespond{Status: s}},
		}
		errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
		if hasMITMError(errs, "between 100 and 599") {
			t.Errorf("expected status %d to be accepted, got: %v", s, errs)
		}
	}
}

func TestValidateMITM_RespondStatus_OutOfRangeRejected(t *testing.T) {
	for _, s := range []int{99, 600} {
		ic := profile.MITMIntercept{
			Name:   "rule1",
			Hosts:  []string{"api.example.com"},
			Action: &profile.MITMAction{Respond: &profile.MITMRespond{Status: s}},
		}
		errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
		if !hasMITMError(errs, "between 100 and 599") {
			t.Errorf("expected status %d to be rejected, got: %v", s, errs)
		}
	}
}

func TestValidateMITM_EmptyName_Error(t *testing.T) {
	ic := redirectIntercept("", []string{"api.example.com"})
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "must be non-empty") {
		t.Errorf("expected an empty-name error, got: %v", errs)
	}
}

func TestValidateMITM_MalformedName_Error(t *testing.T) {
	ic := redirectIntercept("Not_Valid!", []string{"api.example.com"})
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "must be non-empty") {
		t.Errorf("expected a malformed-name error, got: %v", errs)
	}
}

func TestValidateMITM_DisabledMalformedName_StillErrors(t *testing.T) {
	ic := profile.MITMIntercept{Name: "Bad Name", Enabled: mitmBoolPtr(false)}
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMError(errs, "must be non-empty") {
		t.Errorf("expected a malformed-name error even when disabled, got: %v", errs)
	}
}

func TestValidateMITM_DuplicateNameConflictingBodies_Error(t *testing.T) {
	a := redirectIntercept("dup", []string{"a.example.com"})
	b := redirectIntercept("dup", []string{"b.example.com"})
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{a, b}))
	if !hasMITMError(errs, "can only be a typo") {
		t.Errorf("expected a duplicate-name error, got: %v", errs)
	}
}

func TestValidateMITM_DuplicateNameIdenticalBodies_NoError(t *testing.T) {
	a := redirectIntercept("dup", []string{"a.example.com"})
	b := redirectIntercept("dup", []string{"a.example.com"})
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{a, b}))
	if hasMITMError(errs, "can only be a typo") {
		t.Errorf("expected no duplicate-name error for identical bodies, got: %v", errs)
	}
}

func TestValidateMITM_DisabledEntry_NoHostsNoAction_NoError(t *testing.T) {
	ic := profile.MITMIntercept{Name: "override", Enabled: mitmBoolPtr(false)}
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if len(errs) != 0 {
		t.Errorf("expected zero errors for a disable-only override, got: %v", errs)
	}
}

func TestValidateMITM_NoMITMBlock_NoError(t *testing.T) {
	p := &profile.SandboxProfile{
		APIVersion: "klankermaker.ai/v1alpha2",
		Kind:       "SandboxProfile",
		Metadata:   profile.Metadata{Name: "no-mitm"},
	}
	errs := profile.ValidateSemantic(p)
	if len(errs) != 0 {
		t.Errorf("expected zero errors/warnings for a profile with no mitm block, got: %v", errs)
	}
}

// TestValidateMITM_ThroughPublicEntryPoint proves the wiring reaches
// Validate(raw), not just ValidateSemantic, for at least one error case
// (empty hosts) per the plan's acceptance criteria.
func TestValidateMITM_ThroughPublicEntryPoint(t *testing.T) {
	data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      intercepts:
        - name: rule1
          action:
            redirect: https://example.com/redirected
`)
	errs := profile.Validate(data)
	if !hasMITMError(errs, "at least one host") {
		t.Errorf("expected the empty-hosts error through profile.Validate(raw), got: %v", errs)
	}
}

func TestValidateMITM_ThroughPublicEntryPoint_RespondStatusOutOfRange(t *testing.T) {
	data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      intercepts:
        - name: rule1
          hosts: ["api.example.com"]
          action:
            respond:
              status: 700
`)
	errs := profile.Validate(data)
	if !hasMITMError(errs, "between 100 and 599") {
		t.Errorf("expected the out-of-range status error through profile.Validate(raw), got: %v", errs)
	}
}

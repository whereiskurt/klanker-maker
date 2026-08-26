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
// for the overlap-warning tests.
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

func hasMITMWarning(errs []profile.ValidationError, substr string) bool {
	for _, e := range errs {
		if e.IsWarning && strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// anyErrorContains reports whether any error (warning or not) contains substr.
func anyErrorContains(errs []profile.ValidationError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Message, substr) {
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

// ─── Task 2: the three locked warnings ────────────────────────────────────

func TestValidateMITM_ReservedHost_Anthropic_Warning(t *testing.T) {
	ic := redirectIntercept("rule1", []string{"api.anthropic.com"})
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMWarning(errs, "platform-reserved") {
		t.Errorf("expected a platform-reserved warning for api.anthropic.com, got: %v", errs)
	}
	if hasMITMError(errs, "platform-reserved") {
		t.Errorf("platform-reserved overlap must never be an error, got: %v", errs)
	}
}

func TestValidateMITM_ReservedHost_OpenAI_Warning(t *testing.T) {
	ic := redirectIntercept("rule1", []string{"api.openai.com"})
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if !hasMITMWarning(errs, "platform-reserved") {
		t.Errorf("expected a platform-reserved warning for api.openai.com, got: %v", errs)
	}
}

func TestValidateMITM_ReservedHost_GitHub_Warning(t *testing.T) {
	for _, h := range []string{"github.com", "api.github.com", "raw.githubusercontent.com", "codeload.githubusercontent.com"} {
		ic := redirectIntercept("rule1", []string{h})
		errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
		if !hasMITMWarning(errs, "platform-reserved") {
			t.Errorf("expected a platform-reserved warning for %s, got: %v", h, errs)
		}
	}
}

func TestValidateMITM_ReservedHost_Bedrock_AnyRegion_Warning(t *testing.T) {
	for _, h := range []string{"bedrock-runtime.us-east-1.amazonaws.com", "bedrock-runtime.eu-west-2.amazonaws.com"} {
		ic := redirectIntercept("rule1", []string{h})
		errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
		if !hasMITMWarning(errs, "platform-reserved") {
			t.Errorf("expected a platform-reserved warning for %s, got: %v", h, errs)
		}
	}
}

func TestValidateMITM_ReservedHost_AllWarningsAreIsWarningTrue(t *testing.T) {
	hosts := []string{
		"api.anthropic.com", "api.openai.com", "github.com", "api.github.com",
		"raw.githubusercontent.com", "codeload.githubusercontent.com",
		"bedrock-runtime.us-east-1.amazonaws.com",
	}
	for _, h := range hosts {
		ic := redirectIntercept("rule1", []string{h})
		errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "platform-reserved") {
				found = true
				if !e.IsWarning {
					t.Errorf("expected platform-reserved finding for %s to have IsWarning=true, got %+v", h, e)
				}
			}
		}
		if !found {
			t.Errorf("expected a platform-reserved finding for %s, got: %v", h, errs)
		}
	}
}

func TestValidateMITM_DeniedHosts_Overlap_Warning(t *testing.T) {
	p := mitmProfile([]profile.MITMIntercept{redirectIntercept("rule1", []string{"blocked.example.com"})})
	p.Spec.Network.Egress.DeniedHosts = []string{"blocked.example.com"}
	errs := profile.ValidateSemantic(p)
	if !hasMITMWarning(errs, "deny gate answers first") {
		t.Errorf("expected a denied-host overlap warning, got: %v", errs)
	}
	if hasMITMError(errs, "deny gate answers first") {
		t.Errorf("denied-host overlap must never be an error, got: %v", errs)
	}
}

func TestValidateMITM_DeniedDNSSuffixes_Overlap_Warning(t *testing.T) {
	p := mitmProfile([]profile.MITMIntercept{redirectIntercept("rule1", []string{"api.blocked.example.com"})})
	p.Spec.Network.Egress.DeniedDNSSuffixes = []string{"blocked.example.com"}
	errs := profile.ValidateSemantic(p)
	if !hasMITMWarning(errs, "deny gate answers first") {
		t.Errorf("expected a denied-DNS-suffix overlap warning, got: %v", errs)
	}
}

func TestValidateMITM_DNSReachability_EBPF_Warning(t *testing.T) {
	p := mitmProfile([]profile.MITMIntercept{redirectIntercept("rule1", []string{"unresolvable.example.com"})})
	p.Spec.Network.Enforcement = "ebpf"
	p.Spec.Network.Egress.AllowedDNSSuffixes = []string{"other.example.com"}
	errs := profile.ValidateSemantic(p)
	if !hasMITMWarning(errs, "allowedDNSSuffixes") {
		t.Errorf("expected a DNS-reachability warning under ebpf enforcement, got: %v", errs)
	}
}

func TestValidateMITM_DNSReachability_Both_Warning(t *testing.T) {
	p := mitmProfile([]profile.MITMIntercept{redirectIntercept("rule1", []string{"unresolvable.example.com"})})
	p.Spec.Network.Enforcement = "both"
	p.Spec.Network.Egress.AllowedDNSSuffixes = []string{"other.example.com"}
	errs := profile.ValidateSemantic(p)
	if !hasMITMWarning(errs, "allowedDNSSuffixes") {
		t.Errorf("expected a DNS-reachability warning under both enforcement, got: %v", errs)
	}
}

func TestValidateMITM_DNSReachability_DefaultProxyEnforcement_NoWarning(t *testing.T) {
	p := mitmProfile([]profile.MITMIntercept{redirectIntercept("rule1", []string{"unresolvable.example.com"})})
	// Enforcement left at "" (default proxy); AllowedDNSSuffixes deliberately
	// does not cover the intercept host.
	p.Spec.Network.Egress.AllowedDNSSuffixes = []string{"other.example.com"}
	errs := profile.ValidateSemantic(p)
	if hasMITMWarning(errs, "allowedDNSSuffixes") {
		t.Errorf("expected no DNS-reachability warning under default (proxy) enforcement, got: %v", errs)
	}
}

func TestValidateMITM_DNSReachability_CoveredHost_NoWarning(t *testing.T) {
	p := mitmProfile([]profile.MITMIntercept{redirectIntercept("rule1", []string{"api.example.com"})})
	p.Spec.Network.Enforcement = "ebpf"
	p.Spec.Network.Egress.AllowedDNSSuffixes = []string{".example.com"}
	errs := profile.ValidateSemantic(p)
	if hasMITMWarning(errs, "allowedDNSSuffixes") {
		t.Errorf("expected no DNS-reachability warning for a host covered by allowedDNSSuffixes, got: %v", errs)
	}
}

func TestValidateMITM_DNSReachability_WildcardAllowedDNSSuffixes_NoWarning(t *testing.T) {
	p := mitmProfile([]profile.MITMIntercept{redirectIntercept("rule1", []string{"anything.example.com"})})
	p.Spec.Network.Enforcement = "ebpf"
	p.Spec.Network.Egress.AllowedDNSSuffixes = []string{"*"}
	errs := profile.ValidateSemantic(p)
	if hasMITMWarning(errs, "allowedDNSSuffixes") {
		t.Errorf("expected no DNS-reachability warning when allowedDNSSuffixes contains '*', got: %v", errs)
	}
}

func TestValidateMITM_Warnings_NeverFailExitCode(t *testing.T) {
	// A profile whose ONLY MITM problem is a reserved-host overlap warning
	// must validate as valid-with-warnings: every finding is IsWarning=true.
	ic := redirectIntercept("rule1", []string{"api.anthropic.com"})
	errs := profile.ValidateSemantic(mitmProfile([]profile.MITMIntercept{ic}))
	if len(errs) == 0 {
		t.Fatalf("expected at least the platform-reserved warning, got none")
	}
	for _, e := range errs {
		if !e.IsWarning {
			t.Errorf("expected every finding to be a warning, got a hard error: %+v", e)
		}
	}
}

func TestValidateMITM_DisabledIntercept_NoWarnings(t *testing.T) {
	ic := redirectIntercept("rule1", []string{"api.anthropic.com"})
	ic.Enabled = mitmBoolPtr(false)
	p := mitmProfile([]profile.MITMIntercept{ic})
	p.Spec.Network.Enforcement = "ebpf" // would also trip the DNS warning if not gated on enabled
	errs := profile.ValidateSemantic(p)
	if len(errs) != 0 {
		t.Errorf("expected zero warnings for a disabled intercept, got: %v", errs)
	}
}

// TestValidateMITM_ThroughPublicEntryPoint_ReservedHostWarning proves the
// reserved-host warning also reaches the public Validate(raw) entry point.
func TestValidateMITM_ThroughPublicEntryPoint_ReservedHostWarning(t *testing.T) {
	data := minimalProfileWithNetworkYAML(`    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      intercepts:
        - name: rule1
          hosts: ["api.anthropic.com"]
          action:
            redirect: https://example.com/redirected
`)
	errs := profile.Validate(data)
	if !hasMITMWarning(errs, "platform-reserved") {
		t.Errorf("expected the platform-reserved warning through profile.Validate(raw), got: %v", errs)
	}
	if !anyErrorContains(errs, "platform-reserved") {
		t.Errorf("expected a platform-reserved finding at all, got: %v", errs)
	}
}

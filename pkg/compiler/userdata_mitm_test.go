package compiler

// Tests for Phase 127 Plan 03 — the compiler half of the declarative MITM
// intercepts contract: buildMITMInterceptsB64, the guarded 7.3c template
// section, and buildL7ProxyHosts's intercept-host threading.
//
// Behaviour under test:
//  1. A profile with an enabled intercept renders a KM_MITM_INTERCEPTS line
//     inside a mitm.conf drop-in, base64-decoding to the flat wire shape.
//  2. A profile with no mitm block, or with only disabled intercepts, renders
//     neither the drop-in nor the env var — and the two frozen byte-identity
//     goldens stay untouched.
//  3. Intercept hosts reach --proxy-hosts (buildL7ProxyHosts) without
//     widening --allowed-dns.
//  4. A contract test round-trips real rendered user-data through the real
//     sidecar's httpproxy.ParseIntercepts, proving the two independently
//     authored ends of the wire agree.

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
	"github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy"
)

// mitmProfileWithIntercepts returns a baseProfile() with the given intercepts
// set on spec.network.mitm.intercepts.
func mitmProfileWithIntercepts(ics ...profile.MITMIntercept) *profile.SandboxProfile {
	p := baseProfile()
	p.Spec.Network.MITM = &profile.NetworkMITMSpec{Intercepts: ics}
	return p
}

// extractEnvValue finds "KEY=" in out and returns the rest of that line,
// trimmed. Mirrors the extraction pattern in userdata_quota_test.go.
func extractEnvValue(t *testing.T, out, key string) string {
	t.Helper()
	idx := strings.Index(out, key+"=")
	if idx < 0 {
		t.Fatalf("key %q not found in rendered user-data", key)
	}
	rest := out[idx+len(key)+1:]
	if end := strings.IndexByte(rest, '\n'); end >= 0 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// extractQuotedFlagValue finds `flag "value"` (a km ebpf-attach ExecStart
// argument) in out and returns value.
func extractQuotedFlagValue(t *testing.T, out, flag string) string {
	t.Helper()
	needle := flag + " \""
	idx := strings.Index(out, needle)
	if idx < 0 {
		t.Fatalf("flag %q not found in rendered user-data", flag)
	}
	rest := out[idx+len(needle):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		t.Fatalf("unterminated quoted value for flag %q", flag)
	}
	return rest[:end]
}

// ─── Task 1: emission + dormancy ──────────────────────────────────────────

func TestMITMInterceptsEmission_RedirectRule(t *testing.T) {
	p := mitmProfileWithIntercepts(profile.MITMIntercept{
		Name:  "rickroll",
		Hosts: []string{".example-egg.test"},
		Action: &profile.MITMAction{
			Redirect: "https://example.com/redirected",
		},
	})

	out, err := generateUserData(p, "sb-mitm-01", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, "km-http-proxy.service.d/mitm.conf") {
		t.Errorf("expected mitm.conf drop-in path in user-data\n%s", abbreviateUD(out))
	}
	if !strings.Contains(out, "KM_MITM_INTERCEPTS=") {
		t.Fatalf("expected KM_MITM_INTERCEPTS in user-data\n%s", abbreviateUD(out))
	}

	val := extractEnvValue(t, out, "KM_MITM_INTERCEPTS")
	decoded, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		t.Fatalf("KM_MITM_INTERCEPTS value is not valid base64: %v", err)
	}

	var raw []map[string]any
	if err := json.Unmarshal(decoded, &raw); err != nil {
		t.Fatalf("decoded value is not valid JSON: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("want 1 intercept, got %d: %v", len(raw), raw)
	}
	entry := raw[0]
	if entry["name"] != "rickroll" {
		t.Errorf("name: got %v, want rickroll", entry["name"])
	}
	hosts, ok := entry["hosts"].([]any)
	if !ok || len(hosts) != 1 || hosts[0] != ".example-egg.test" {
		t.Errorf("hosts not preserved: %v", entry["hosts"])
	}
	if entry["redirect"] != "https://example.com/redirected" {
		t.Errorf("redirect: got %v, want https://example.com/redirected", entry["redirect"])
	}
	if _, hasAction := entry["action"]; hasAction {
		t.Error("emitted JSON must not carry a nested 'action' key")
	}
	if _, hasEnabled := entry["enabled"]; hasEnabled {
		t.Error("emitted JSON must not carry an 'enabled' key")
	}
}

func TestMITMInterceptsEmission_RespondBodyWithSpaceAndNewlineSurvives(t *testing.T) {
	body := "line one has spaces\nline two also has spaces"
	p := mitmProfileWithIntercepts(profile.MITMIntercept{
		Name:  "chaos",
		Hosts: []string{"api.example-mitm.test"},
		Action: &profile.MITMAction{
			Respond: &profile.MITMRespond{
				Status:      503,
				ContentType: "text/plain",
				Body:        body,
			},
		},
	})

	out, err := generateUserData(p, "sb-mitm-02", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	val := extractEnvValue(t, out, "KM_MITM_INTERCEPTS")
	decoded, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(decoded, &raw); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("want 1 intercept, got %d", len(raw))
	}
	respond, ok := raw[0]["respond"].(map[string]any)
	if !ok {
		t.Fatalf("respond block missing: %v", raw[0])
	}
	if respond["body"] != body {
		t.Errorf("body round-trip failed: got %q, want %q", respond["body"], body)
	}
}

func TestMITMInterceptsDormant_NoMITMBlock(t *testing.T) {
	p := baseProfile() // no spec.network.mitm at all

	out, err := generateUserData(p, "sb-mitm-03", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "KM_MITM_INTERCEPTS") {
		t.Error("expected no KM_MITM_INTERCEPTS when profile has no mitm block")
	}
	if strings.Contains(out, "mitm.conf") {
		t.Error("expected no mitm.conf drop-in when profile has no mitm block")
	}
}

func TestMITMInterceptsDormant_OnlyDisabledIntercept(t *testing.T) {
	p := mitmProfileWithIntercepts(profile.MITMIntercept{
		Name:    "rickroll",
		Enabled: boolPtr(false),
		Hosts:   []string{".example-egg.test"},
		Action:  &profile.MITMAction{Redirect: "https://example.com/redirected"},
	})

	out, err := generateUserData(p, "sb-mitm-04", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	if strings.Contains(out, "KM_MITM_INTERCEPTS") {
		t.Error("expected no KM_MITM_INTERCEPTS when the only intercept is disabled")
	}
	if strings.Contains(out, "mitm.conf") {
		t.Error("expected no mitm.conf drop-in when the only intercept is disabled")
	}
}

func TestMITMInterceptsEmission_OneEnabledOneDisabled(t *testing.T) {
	p := mitmProfileWithIntercepts(
		profile.MITMIntercept{
			Name:    "rickroll",
			Enabled: boolPtr(false),
			Hosts:   []string{".example-egg.test"},
			Action:  &profile.MITMAction{Redirect: "https://example.com/redirected"},
		},
		profile.MITMIntercept{
			Name:  "chaos",
			Hosts: []string{"api.example-mitm.test"},
			Action: &profile.MITMAction{
				Respond: &profile.MITMRespond{Status: 503, Body: "maintenance window"},
			},
		},
	)

	out, err := generateUserData(p, "sb-mitm-05", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	val := extractEnvValue(t, out, "KM_MITM_INTERCEPTS")
	decoded, err := base64.StdEncoding.DecodeString(val)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(decoded, &raw); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("want exactly 1 (only the enabled) intercept, got %d: %v", len(raw), raw)
	}
	if raw[0]["name"] != "chaos" {
		t.Errorf("want the enabled rule 'chaos' to survive, got %v", raw[0]["name"])
	}
}

// TestMITMInterceptsByteIdentityGoldensStillPass re-runs the two frozen
// byte-identity tests to document, at this test's own callsite, that this
// plan must never require them to be re-captured. See userdata_phase92_byte_identity_test.go
// and userdata_h1_byte_identity_test.go for the actual assertions; this is a
// convenience wrapper so `go test -run MITM` alone still exercises them.
func TestMITMInterceptsByteIdentityGoldensStillPass(t *testing.T) {
	t.Run("phase92", func(t *testing.T) {
		TestUserdataLearnV2Phase92ByteIdentity(t)
	})
	t.Run("h1", func(t *testing.T) {
		TestUserdataH1ByteIdentity(t)
	})
}

// ─── Task 2: buildL7ProxyHosts threading ──────────────────────────────────

func TestBuildL7ProxyHosts_IntersectHostPresent(t *testing.T) {
	p := mitmProfileWithIntercepts(profile.MITMIntercept{
		Name:  "chaos",
		Hosts: []string{"api.example-mitm.test"},
		Action: &profile.MITMAction{
			Respond: &profile.MITMRespond{Status: 503, Body: "maintenance window"},
		},
	})
	got := buildL7ProxyHosts(p)
	if !strings.Contains(got, "api.example-mitm.test") {
		t.Errorf("expected intercept host in --proxy-hosts value, got %q", got)
	}
}

func TestBuildL7ProxyHosts_DisabledInterceptHostAbsent(t *testing.T) {
	p := mitmProfileWithIntercepts(profile.MITMIntercept{
		Name:    "chaos",
		Enabled: boolPtr(false),
		Hosts:   []string{"api.example-mitm.test"},
		Action: &profile.MITMAction{
			Respond: &profile.MITMRespond{Status: 503, Body: "maintenance window"},
		},
	})
	got := buildL7ProxyHosts(p)
	if strings.Contains(got, "api.example-mitm.test") {
		t.Errorf("disabled intercept host must not appear in --proxy-hosts, got %q", got)
	}
}

func TestBuildL7ProxyHosts_OrderIsGithubBedrockOpenAIThenIntercepts(t *testing.T) {
	p := mitmProfileWithIntercepts(profile.MITMIntercept{
		Name:  "chaos",
		Hosts: []string{"api.example-mitm.test"},
		Action: &profile.MITMAction{
			Respond: &profile.MITMRespond{Status: 503, Body: "maintenance window"},
		},
	})
	p.Spec.SourceAccess.GitHub = &profile.GitHubAccess{AllowedRepos: []string{"org/repo"}}
	p.Spec.Execution.UseBedrock = true

	got := buildL7ProxyHosts(p)
	ghIdx := strings.Index(got, "github.com")
	bedrockIdx := strings.Index(got, "api.anthropic.com")
	interceptIdx := strings.Index(got, "api.example-mitm.test")
	if ghIdx < 0 || bedrockIdx < 0 || interceptIdx < 0 {
		t.Fatalf("expected all three groups present, got %q", got)
	}
	if !(ghIdx < bedrockIdx && bedrockIdx < interceptIdx) {
		t.Errorf("expected order github < bedrock/anthropic < intercepts, got %q", got)
	}
}

func TestBuildL7ProxyHosts_DuplicateHostNotEmittedTwice(t *testing.T) {
	p := mitmProfileWithIntercepts(profile.MITMIntercept{
		Name:  "shadow-anthropic",
		Hosts: []string{"api.anthropic.com"},
		Action: &profile.MITMAction{
			Respond: &profile.MITMRespond{Status: 200, Body: "ok"},
		},
	})
	p.Spec.Execution.UseBedrock = true

	got := buildL7ProxyHosts(p)
	count := strings.Count(got, "api.anthropic.com")
	if count != 1 {
		t.Errorf("expected api.anthropic.com exactly once, got %d occurrences in %q", count, got)
	}
}

func TestMITMIntercept_DoesNotWidenAllowedDNS(t *testing.T) {
	// --allowed-dns is only rendered on the km ebpf-attach ExecStart line,
	// which itself is guarded on enforcement: ebpf|both — set it explicitly
	// so the flag is present to compare.
	without := baseProfile()
	without.Spec.Network.Enforcement = "ebpf"
	withIntercept := mitmProfileWithIntercepts(profile.MITMIntercept{
		Name:  "chaos",
		Hosts: []string{"api.example-mitm.test"},
		Action: &profile.MITMAction{
			Respond: &profile.MITMRespond{Status: 503, Body: "maintenance window"},
		},
	})
	withIntercept.Spec.Network.Enforcement = "ebpf"

	outWithout, err := generateUserData(without, "sb-mitm-dns-01", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData (without) failed: %v", err)
	}
	outWith, err := generateUserData(withIntercept, "sb-mitm-dns-02", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData (with) failed: %v", err)
	}

	dnsWithout := extractQuotedFlagValue(t, outWithout, "--allowed-dns")
	dnsWith := extractQuotedFlagValue(t, outWith, "--allowed-dns")
	if dnsWithout != dnsWith {
		t.Errorf("--allowed-dns changed when declaring an intercept: without=%q with=%q", dnsWithout, dnsWith)
	}
	if strings.Contains(dnsWith, "api.example-mitm.test") {
		t.Error("intercept host must not appear in --allowed-dns")
	}
}

// ─── Task 3: compiler-to-sidecar contract ─────────────────────────────────

// TestMITMWireContract_CompilerAndSidecarAgree renders real user-data for a
// profile declaring one redirect rule and one respond rule, extracts the
// KM_MITM_INTERCEPTS value, and decodes it with the REAL sidecar parser
// (httpproxy.ParseIntercepts) — the single test that keeps the two
// independently-authored ends of the wire contract from drifting. A field
// rename on either side fails HERE, in CI, rather than at boot on a live
// sandbox.
func TestMITMWireContract_CompilerAndSidecarAgree(t *testing.T) {
	respondBody := "maintenance window\nplease check back later"
	p := mitmProfileWithIntercepts(
		profile.MITMIntercept{
			Name:  "rickroll",
			Hosts: []string{".example-egg.test"},
			Action: &profile.MITMAction{
				Redirect: "https://example.com/redirected",
			},
		},
		profile.MITMIntercept{
			Name:  "chaos",
			Hosts: []string{"api.example-mitm.test"},
			Action: &profile.MITMAction{
				Respond: &profile.MITMRespond{
					Status:      503,
					ContentType: "text/plain",
					Body:        respondBody,
				},
			},
		},
	)

	out, err := generateUserData(p, "sb-mitm-contract", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	val := extractEnvValue(t, out, "KM_MITM_INTERCEPTS")

	parsed, err := httpproxy.ParseIntercepts(val)
	if err != nil {
		t.Fatalf("httpproxy.ParseIntercepts failed on compiler-rendered value: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("want 2 parsed intercepts, got %d: %+v", len(parsed), parsed)
	}

	byName := make(map[string]httpproxy.Intercept, 2)
	for _, ic := range parsed {
		byName[ic.Name] = ic
	}

	rickroll, ok := byName["rickroll"]
	if !ok {
		t.Fatalf("rickroll intercept missing from parsed result: %+v", parsed)
	}
	if rickroll.Redirect != "https://example.com/redirected" {
		t.Errorf("rickroll.Redirect: got %q, want https://example.com/redirected", rickroll.Redirect)
	}
	if rickroll.Respond != nil {
		t.Errorf("rickroll.Respond should be nil, got %+v", rickroll.Respond)
	}
	if !httpproxy.MatchesHost(".example-egg.test", rickroll.Hosts) {
		t.Errorf("MatchesHost failed for rickroll's own declared host: %v", rickroll.Hosts)
	}
	if !httpproxy.MatchesHost("sub.example-egg.test", rickroll.Hosts) {
		t.Errorf("MatchesHost failed for a subdomain of rickroll's leading-dot host: %v", rickroll.Hosts)
	}

	chaos, ok := byName["chaos"]
	if !ok {
		t.Fatalf("chaos intercept missing from parsed result: %+v", parsed)
	}
	if chaos.Redirect != "" {
		t.Errorf("chaos.Redirect should be empty, got %q", chaos.Redirect)
	}
	if chaos.Respond == nil {
		t.Fatalf("chaos.Respond should be non-nil")
	}
	if chaos.Respond.Status != 503 {
		t.Errorf("chaos.Respond.Status: got %d, want 503", chaos.Respond.Status)
	}
	if chaos.Respond.ContentType != "text/plain" {
		t.Errorf("chaos.Respond.ContentType: got %q, want text/plain", chaos.Respond.ContentType)
	}
	if chaos.Respond.Body != respondBody {
		t.Errorf("chaos.Respond.Body round-trip failed: got %q, want %q", chaos.Respond.Body, respondBody)
	}
	if !httpproxy.MatchesHost("api.example-mitm.test", chaos.Hosts) {
		t.Errorf("MatchesHost failed for chaos's own declared host: %v", chaos.Hosts)
	}
}

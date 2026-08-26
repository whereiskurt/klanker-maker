package profile

import (
	"testing"

	goyaml "github.com/goccy/go-yaml"
)

func boolPtr(b bool) *bool { return &b }

// ─── Round-trip tests (Task 1) ────────────────────────────────────────────

func TestMITM_RoundTrip_RedirectAndRespond(t *testing.T) {
	doc := `
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: x
spec:
  network:
    egress:
      allowedDNSSuffixes: []
      allowedHosts: []
    mitm:
      intercepts:
        - name: rickroll
          hosts: [".google.com"]
          action:
            redirect: https://www.youtube.com/watch?v=dQw4w9WgXcQ
        - name: chaos
          hosts: ["api.example.com"]
          action:
            respond:
              status: 503
              contentType: text/plain
              body: "maintenance window"
`
	var p SandboxProfile
	if err := goyaml.Unmarshal([]byte(doc), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Spec.Network.MITM == nil {
		t.Fatal("MITM should be non-nil")
	}
	ics := p.Spec.Network.MITM.Intercepts
	if len(ics) != 2 {
		t.Fatalf("want 2 intercepts, got %d", len(ics))
	}
	if ics[0].Name != "rickroll" || len(ics[0].Hosts) != 1 || ics[0].Hosts[0] != ".google.com" {
		t.Errorf("rickroll entry not preserved: %+v", ics[0])
	}
	if ics[0].Action == nil || ics[0].Action.Redirect != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("redirect action not preserved: %+v", ics[0].Action)
	}
	if ics[1].Action == nil || ics[1].Action.Respond == nil {
		t.Fatalf("respond action not preserved: %+v", ics[1].Action)
	}
	r := ics[1].Action.Respond
	if r.Status != 503 || r.ContentType != "text/plain" || r.Body != "maintenance window" {
		t.Errorf("respond fields not preserved: %+v", r)
	}
}

func TestMITM_RoundTrip_DisableOnlyOmitsHostsAndAction(t *testing.T) {
	p := SandboxProfile{
		Spec: Spec{
			Network: NetworkSpec{
				MITM: &NetworkMITMSpec{
					Intercepts: []MITMIntercept{
						{Name: "rickroll", Enabled: boolPtr(false)},
					},
				},
			},
		},
	}
	out, err := goyaml.Marshal(&p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if containsKey(s, "hosts:") {
		t.Errorf("disable-only entry must not emit a hosts: key, got:\n%s", s)
	}
	if containsKey(s, "action:") {
		t.Errorf("disable-only entry must not emit an action: key, got:\n%s", s)
	}
}

func TestMITM_RoundTrip_NoMITMBlockOmitsKey(t *testing.T) {
	p := SandboxProfile{}
	out, err := goyaml.Marshal(&p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if containsKey(string(out), "mitm:") {
		t.Errorf("profile with no mitm block must not emit a mitm: key, got:\n%s", out)
	}
}

func containsKey(doc, key string) bool {
	for _, line := range splitLines(doc) {
		if trimLeft(line) == key || hasPrefixTrimmed(line, key) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func trimLeft(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}

func hasPrefixTrimmed(line, key string) bool {
	t := trimLeft(line)
	return len(t) >= len(key) && t[:len(key)] == key
}

// ─── CollapseIntercepts / InterceptEnabled / EnabledIntercepts (Task 2) ───

func TestInterceptEnabled(t *testing.T) {
	if !InterceptEnabled(MITMIntercept{Name: "a"}) {
		t.Error("nil Enabled should be treated as enabled")
	}
	if !InterceptEnabled(MITMIntercept{Name: "a", Enabled: boolPtr(true)}) {
		t.Error("Enabled=true should be enabled")
	}
	if InterceptEnabled(MITMIntercept{Name: "a", Enabled: boolPtr(false)}) {
		t.Error("Enabled=false should be disabled")
	}
}

func TestCollapseIntercepts_NoDuplicatesUnchanged(t *testing.T) {
	in := []MITMIntercept{
		{Name: "a", Hosts: []string{"a.example.com"}},
		{Name: "b", Hosts: []string{"b.example.com"}},
	}
	got := CollapseIntercepts(in)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("expected unchanged order for no-duplicate input, got %+v", got)
	}
}

func TestCollapseIntercepts_LastWinsWholeEntry(t *testing.T) {
	base := MITMIntercept{
		Name:   "rickroll",
		Hosts:  []string{".google.com"},
		Action: &MITMAction{Redirect: "https://example.com"},
	}
	leaf := MITMIntercept{Name: "rickroll", Enabled: boolPtr(false)}
	got := CollapseIntercepts([]MITMIntercept{base, leaf})
	if len(got) != 1 {
		t.Fatalf("want 1 collapsed entry, got %d: %+v", len(got), got)
	}
	if got[0].Hosts != nil {
		t.Errorf("leaf override must drop inherited hosts, got %+v", got[0].Hosts)
	}
	if got[0].Action != nil {
		t.Errorf("leaf override must drop inherited action, got %+v", got[0].Action)
	}
	if InterceptEnabled(got[0]) {
		t.Error("collapsed entry should be disabled")
	}
}

func TestCollapseIntercepts_OrderIsFirstAppearanceIndexLastAppearanceBody(t *testing.T) {
	a := MITMIntercept{Name: "a", Hosts: []string{"a1.example.com"}}
	b := MITMIntercept{Name: "b", Hosts: []string{"b.example.com"}}
	aPrime := MITMIntercept{Name: "a", Hosts: []string{"a2.example.com"}}
	got := CollapseIntercepts([]MITMIntercept{a, b, aPrime})
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("first-appearance index must be preserved: got order %s, %s", got[0].Name, got[1].Name)
	}
	if len(got[0].Hosts) != 1 || got[0].Hosts[0] != "a2.example.com" {
		t.Errorf("last-appearance body must win for 'a': got %+v", got[0].Hosts)
	}
}

func TestEnabledIntercepts_DisabledDropped(t *testing.T) {
	base := MITMIntercept{
		Name:   "rickroll",
		Hosts:  []string{".google.com"},
		Action: &MITMAction{Redirect: "https://example.com"},
	}
	leaf := MITMIntercept{Name: "rickroll", Enabled: boolPtr(false)}
	got := EnabledIntercepts([]MITMIntercept{base, leaf})
	if len(got) != 0 {
		t.Errorf("want 0 enabled intercepts, got %d: %+v", len(got), got)
	}
}

func TestEnabledIntercepts_NilEnabledKept(t *testing.T) {
	in := []MITMIntercept{
		{Name: "a", Hosts: []string{"a.example.com"}, Action: &MITMAction{Redirect: "https://example.com"}},
		{Name: "b", Enabled: boolPtr(false)},
	}
	got := EnabledIntercepts(in)
	if len(got) != 1 || got[0].Name != "a" {
		t.Errorf("want only nil-Enabled entry 'a' kept, got %+v", got)
	}
}

// ─── DuplicateInterceptNames (Task 2) ──────────────────────────────────────

func TestDuplicateInterceptNames_ConflictingReported(t *testing.T) {
	in := []MITMIntercept{
		{Name: "a", Hosts: []string{"a1.example.com"}},
		{Name: "a", Hosts: []string{"a2.example.com"}},
	}
	got := DuplicateInterceptNames(in)
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("want ['a'], got %v", got)
	}
}

func TestDuplicateInterceptNames_IdenticalNotReported(t *testing.T) {
	entry := MITMIntercept{Name: "a", Hosts: []string{"a.example.com"}}
	in := []MITMIntercept{entry, entry}
	got := DuplicateInterceptNames(in)
	if len(got) != 0 {
		t.Errorf("want no duplicates reported for identical repeats, got %v", got)
	}
}

// ─── ProfileIntercepts nil-safety (Task 2) ─────────────────────────────────

func TestProfileIntercepts_NilSafety(t *testing.T) {
	if got := ProfileIntercepts(nil); got != nil {
		t.Errorf("nil profile should return nil, got %+v", got)
	}
	p := &SandboxProfile{}
	if got := ProfileIntercepts(p); got != nil {
		t.Errorf("nil MITM should return nil, got %+v", got)
	}
	p.Spec.Network.MITM = &NetworkMITMSpec{}
	if got := ProfileIntercepts(p); got != nil {
		t.Errorf("nil Intercepts should return nil, got %+v", got)
	}
}

// ─── Resolve-level fixture tests (Task 3) ──────────────────────────────────
//
// These prove the collapse survives a real extends: chain through Resolve(),
// not just direct calls to CollapseIntercepts.

func TestResolve_MITMFixtureDisable(t *testing.T) {
	p, err := Resolve("mitm-fixture-disable", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	ics := ProfileIntercepts(p)
	if len(ics) != 1 {
		t.Fatalf("want exactly 1 intercept named fixture-egg, got %d: %+v", len(ics), ics)
	}
	if ics[0].Name != "fixture-egg" {
		t.Errorf("want name fixture-egg, got %q", ics[0].Name)
	}
	if InterceptEnabled(ics[0]) {
		t.Error("fixture-egg should be disabled")
	}
	if got := EnabledIntercepts(ics); len(got) != 0 {
		t.Errorf("EnabledIntercepts should be empty, got %+v", got)
	}
}

func TestResolve_MITMFixtureReplace(t *testing.T) {
	p, err := Resolve("mitm-fixture-replace", []string{"../../testdata/profiles"})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	ics := ProfileIntercepts(p)
	if len(ics) != 1 {
		t.Fatalf("want exactly 1 intercept named fixture-egg, got %d: %+v", len(ics), ics)
	}
	ic := ics[0]
	if ic.Name != "fixture-egg" {
		t.Errorf("want name fixture-egg, got %q", ic.Name)
	}
	// Exact-set comparison: the base's host must NOT appear, proving the
	// collapse beat the Phase 117 list union rather than the leaf's host
	// merely being unioned alongside the base's.
	wantHosts := []string{"leaf-only.example.com"}
	if len(ic.Hosts) != len(wantHosts) {
		t.Fatalf("want hosts %v, got %v", wantHosts, ic.Hosts)
	}
	for i, h := range wantHosts {
		if ic.Hosts[i] != h {
			t.Errorf("want hosts %v, got %v", wantHosts, ic.Hosts)
			break
		}
	}
	if ic.Action == nil || ic.Action.Respond == nil {
		t.Fatalf("want a respond action (the leaf's replacement), got %+v", ic.Action)
	}
	if ic.Action.Redirect != "" {
		t.Errorf("want no redirect (base action must not survive), got %q", ic.Action.Redirect)
	}
	if ic.Action.Respond.Status != 503 || ic.Action.Respond.Body != "replaced by leaf" {
		t.Errorf("respond action not the leaf's: %+v", ic.Action.Respond)
	}
}

func TestResolve_MITMFixtureBase_NeverDuplicated(t *testing.T) {
	for _, name := range []string{"mitm-fixture-disable", "mitm-fixture-replace"} {
		p, err := Resolve(name, []string{"../../testdata/profiles"})
		if err != nil {
			t.Fatalf("resolve %s failed: %v", name, err)
		}
		ics := ProfileIntercepts(p)
		count := 0
		for _, ic := range ics {
			if ic.Name == "fixture-egg" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("%s: want fixture-egg exactly once, got %d entries: %+v", name, count, ics)
		}
	}
}

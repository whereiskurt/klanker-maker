package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// phase92LearnV2UserdataGolden is the on-disk path (relative to this test file)
// of the pre-Phase-92 userdata baseline captured from profiles/learn.v2.yaml.
const phase92LearnV2UserdataGolden = "userdata_learn_v2_pre92_baseline.golden.sh"

// generateLearnV2Userdata loads profiles/learn.v2.yaml and runs the current
// compiler's userdata generator with the same fixed inputs used to capture the
// Wave 0 baseline. Centralizing the call site guarantees the capture path
// (TestCapturePre92Userdata) and the verification path
// (TestUserdataLearnV2Phase92ByteIdentity) drive identical inputs — otherwise a
// byte-identity comparison would be meaningless.
func generateLearnV2Userdata(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	// pkg/compiler/<thisfile> -> repo root is two dirs up.
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	profilesDir := filepath.Join(repoRoot, "testdata", "profiles")

	// Resolve the full extends DAG so that once learn.v2.yaml gains `extends:`,
	// the merged spec (not just the partial leaf) is compiled — byte-identical to
	// the frozen pre-Phase-92 baseline.
	p, err := profile.Resolve("learn.v2", []string{profilesDir})
	if err != nil {
		t.Fatalf("resolve profile learn.v2: %v", err)
	}

	// Fixed inputs mirror the existing golden-test convention
	// (TestUserdataAdditionalVolumeOnly_GoldenByteIdentical): deterministic
	// sandbox ID + bucket, no spot, nil network. learn.v2's own spec drives the
	// rest of the rendered output.
	got, err := generateUserData(p, "sb-phase92-baseline", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return got
}

// goldenPath92 resolves a testdata-relative golden filename to an absolute path.
func goldenPath92(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	return filepath.Join(filepath.Dir(thisFile), "testdata", name)
}

// TestCapturePre92Userdata writes the pre-Phase-92 userdata baseline golden.
// It is a CAPTURE helper, not an assertion: it only runs when
// CAPTURE_PRE92_BASELINE=1 is set, so normal `go test` runs skip it. Capture once
// on pre-Phase-92 main, commit the golden, then never run it again — Wave 1-5
// must keep the byte-identity test (below) green against the captured baseline.
//
//	CAPTURE_PRE92_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePre92Userdata
func TestCapturePre92Userdata(t *testing.T) {
	if os.Getenv("CAPTURE_PRE92_BASELINE") != "1" {
		t.Skip("set CAPTURE_PRE92_BASELINE=1 to (re)capture the pre-Phase-92 userdata baseline")
	}
	got := generateLearnV2Userdata(t)
	out := goldenPath92(t, phase92LearnV2UserdataGolden)
	if err := os.WriteFile(out, []byte(got), 0o644); err != nil {
		t.Fatalf("write golden %s: %v", out, err)
	}
	t.Logf("captured pre-Phase-92 userdata baseline (%d bytes) -> %s", len(got), out)
}

// TestUserdataLearnV2Phase92ByteIdentity verifies that the userdata generated for
// profiles/learn.v2.yaml is byte-identical to the pre-Phase-92 baseline captured
// in Wave 0 — EXCEPT for the synthesized ~/.claude/settings.json content blob,
// which Wave 5 intentionally migrates from the legacy {"autoApprove": [...]} form
// to the canonical {"permissions": {"allow": [...]}} form (Wave 0 research
// Option B — see .planning/research/codex-config-toml.md §1b and
// docs/agent-tool-gating.md).
//
// VC-3 RECONCILIATION (Wave 5 locked decision):
//   - The byte-identity contract proves the spec restructure pipeline is
//     SEMANTICALLY TRANSPARENT, not that every byte is frozen forever.
//   - For everything OUTSIDE the settings.json blob (codex config.toml, all of
//     the rest of the userdata) we keep STRICT byte-identity: both the pre-92
//     baseline and the Wave-5 output have their settings.json blob replaced with
//     a sentinel, then compared byte-for-byte.
//   - For the settings.json blob itself we assert SEMANTIC EQUIVALENCE: the old
//     legacy form and the new canonical form must yield the same effective
//     auto-approved tool set, the same trustedDirectories, and the same km-notify
//     hooks. A tool silently gaining or losing auto-approval is a FAILURE.
//
// On pre-Phase-92 main: PASS (golden matches generated output verbatim).
// After Waves 1-5 land: PASS via the reconciled comparison above.
//
// VC-3
func TestUserdataLearnV2Phase92ByteIdentity(t *testing.T) {
	golden := goldenPath92(t, phase92LearnV2UserdataGolden)
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (Wave 0 baseline capture was not committed)", golden, err)
	}

	// Strip the deliberate post-baseline SubagentStop script additions so the REST
	// comparison still pins byte-identity to the frozen pre-92 baseline. The
	// settings.json SubagentStop hook entry is handled by the semantic check.
	got := stripSubagentStopScript(generateLearnV2Userdata(t))

	// Fast path: if the output is verbatim byte-identical (e.g. running this test
	// on pre-Phase-92 main, or if the canonical form ever matches the legacy
	// bytes), accept immediately.
	if got == string(want) {
		return
	}

	// 1. Extract the ~/.claude/settings.json heredoc blob from both.
	wantBlob, wantRest, ok1 := extractClaudeSettingsBlob(string(want))
	gotBlob, gotRest, ok2 := extractClaudeSettingsBlob(got)
	if !ok1 || !ok2 {
		t.Fatalf("could not locate ~/.claude/settings.json heredoc in baseline(%v)/generated(%v); "+
			"userdata drift is NOT confined to the settings.json blob:\n%s",
			ok1, ok2, diffStrings(string(want), got))
	}

	// 2. Strict byte-identity for everything OUTSIDE the settings.json blob.
	if wantRest != gotRest {
		t.Errorf("userdata drifted from pre-Phase-92 baseline OUTSIDE the settings.json blob "+
			"(this portion must stay byte-identical):\n%s", diffStrings(wantRest, gotRest))
	}

	// 3. Semantic equivalence of the settings.json blob: same effective tools,
	//    trustedDirectories, and km-notify hooks.
	assertClaudeSettingsSemanticEquivalence(t, wantBlob, gotBlob)
}

// extractClaudeSettingsBlob splits a rendered userdata string into the JSON body
// of the ~/.claude/settings.json heredoc and "the rest" (with the blob replaced
// by a stable sentinel so the surrounding bytes can be compared byte-for-byte).
func extractClaudeSettingsBlob(userdata string) (blob, rest string, ok bool) {
	const marker = "cat > '/home/sandbox/.claude/settings.json' << 'KM_CONFIG_EOF'\n"
	const eof = "\nKM_CONFIG_EOF\n"
	start := strings.Index(userdata, marker)
	if start < 0 {
		return "", "", false
	}
	bodyStart := start + len(marker)
	end := strings.Index(userdata[bodyStart:], eof)
	if end < 0 {
		return "", "", false
	}
	blob = userdata[bodyStart : bodyStart+end]
	rest = userdata[:bodyStart] + "<<<CLAUDE_SETTINGS_JSON>>>" + userdata[bodyStart+end:]
	return blob, rest, true
}

// assertClaudeSettingsSemanticEquivalence parses two settings.json JSON blobs
// (the pre-Phase-92 legacy form and the Wave-5 canonical form) and asserts they
// grant the same effective Claude Code behavior: identical auto-approved tool
// set, identical trustedDirectories, identical km-notify hooks.
func assertClaudeSettingsSemanticEquivalence(t *testing.T, oldBlob, newBlob string) {
	t.Helper()

	var oldS, newS map[string]any
	if err := json.Unmarshal([]byte(oldBlob), &oldS); err != nil {
		t.Fatalf("parse baseline settings.json: %v\n%s", err, oldBlob)
	}
	if err := json.Unmarshal([]byte(newBlob), &newS); err != nil {
		t.Fatalf("parse generated settings.json: %v\n%s", err, newBlob)
	}

	oldAllow := effectiveAutoApprove(oldS)
	newAllow := effectiveAutoApprove(newS)
	if !equalStringSets(oldAllow, newAllow) {
		t.Errorf("auto-approved tool set changed by the Phase 92 migration (SECURITY-CRITICAL):\n"+
			"  baseline (legacy autoApprove): %v\n  generated (permissions.allow): %v", oldAllow, newAllow)
	}

	oldDeny := effectiveDeny(oldS)
	newDeny := effectiveDeny(newS)
	if !equalStringSets(oldDeny, newDeny) {
		t.Errorf("denied tool set changed by the Phase 92 migration (SECURITY-CRITICAL):\n"+
			"  baseline: %v\n  generated: %v", oldDeny, newDeny)
	}

	if !reflect.DeepEqual(oldS["trustedDirectories"], newS["trustedDirectories"]) {
		t.Errorf("trustedDirectories changed by the Phase 92 migration:\n  baseline: %v\n  generated: %v",
			oldS["trustedDirectories"], newS["trustedDirectories"])
	}

	// SubagentStop is a DELIBERATE post-baseline hook addition (a new feature, not
	// part of the Phase 92 migration). Assert it is present + correct, then strip it
	// so the migration guard below still pins that the migration left the
	// Notification/Stop/PostToolUse hooks byte-identical to the pre-92 baseline.
	if h, ok := newS["hooks"].(map[string]any); ok {
		assertSingleHookCmd(t, h, "SubagentStop", "/opt/km/bin/km-notify-hook SubagentStop")
		delete(h, "SubagentStop")
	}

	if !reflect.DeepEqual(oldS["hooks"], newS["hooks"]) {
		t.Errorf("km-notify hooks changed by the Phase 92 migration:\n  baseline: %v\n  generated: %v",
			oldS["hooks"], newS["hooks"])
	}
}

// assertSingleHookCmd verifies hooks[event] is a one-entry array whose single
// command equals want. Used to pin a deliberately-added hook (e.g. SubagentStop).
func assertSingleHookCmd(t *testing.T, hooks map[string]any, event, want string) {
	t.Helper()
	arr, ok := hooks[event].([]any)
	if !ok || len(arr) == 0 {
		t.Errorf("expected hooks[%q] to be a non-empty array, got %#v", event, hooks[event])
		return
	}
	entry, _ := arr[len(arr)-1].(map[string]any)
	inner, _ := entry["hooks"].([]any)
	if len(inner) == 0 {
		t.Errorf("hooks[%q] last entry has no inner hooks: %#v", event, entry)
		return
	}
	cmd, _ := inner[0].(map[string]any)
	if got, _ := cmd["command"].(string); got != want {
		t.Errorf("hooks[%q] command = %q, want %q", event, got, want)
	}
}

// stripSubagentStopScript removes the deliberate post-baseline SubagentStop
// additions from a rendered km-notify-hook script so the Phase 92 / km-prefix
// migration guards can compare the REST of the userdata byte-for-byte against the
// frozen pre-92 baseline. It reverts the gate-case line and excises the "# 4b."
// handler block (anchored on the stable "# 4b. SubagentStop" / "# 5. Build
// subject" comment markers). The settings.json SubagentStop hook ENTRY lives in
// the settings blob and is handled separately by assertClaudeSettingsSemanticEquivalence.
func stripSubagentStopScript(userdata string) string {
	out := strings.Replace(userdata, "  PostToolUse|SubagentStop)\n", "  PostToolUse)\n", 1)
	const blockStart = "# 4b. SubagentStop:"
	const blockEnd = "# 5. Build subject + body for the email/slack-root path."
	start := strings.Index(out, blockStart)
	end := strings.Index(out, blockEnd)
	if start >= 0 && end > start {
		out = out[:start] + out[end:]
	}
	return out
}

// effectiveAutoApprove returns the auto-approved tool set from either the legacy
// top-level "autoApprove" key or the canonical "permissions.allow" array.
func effectiveAutoApprove(s map[string]any) []string {
	if v, ok := s["autoApprove"].([]any); ok {
		return toStringSlice(v)
	}
	if perms, ok := s["permissions"].(map[string]any); ok {
		if v, ok := perms["allow"].([]any); ok {
			return toStringSlice(v)
		}
	}
	return nil
}

// effectiveDeny returns the denied tool set from either a legacy "disallowedTools"
// key (none of our fixtures use it) or the canonical "permissions.deny" array.
func effectiveDeny(s map[string]any) []string {
	if v, ok := s["disallowedTools"].([]any); ok {
		return toStringSlice(v)
	}
	if perms, ok := s["permissions"].(map[string]any); ok {
		if v, ok := perms["deny"].([]any); ok {
			return toStringSlice(v)
		}
	}
	return nil
}

func toStringSlice(in []any) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

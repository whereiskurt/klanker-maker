//go:build phase92_wave5
// +build phase92_wave5

package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestSynthesizeClaudeSettingsGolden verifies synthesizeClaudeSettings() output is
// byte-identical to per-fixture golden files for 4 representative profiles.
//
// RED STATE (Wave 0): this file is behind the phase92_wave5 build tag and is NOT
// compiled by the default build. It references the post-phase API
// (synthesizeClaudeSettings, profile.AgentSpec, p.Spec.Agent) that does not exist
// until Wave 5 creates pkg/compiler/agent_claude.go and Wave 4 lands the structured
// AgentSpec. Wave 5 removes the build tag after producing + committing the goldens;
// its "task done" criterion is:
//
//	go test ./pkg/compiler/ -tags phase92_wave5 -run TestSynthesizeClaudeSettingsGolden  // GREEN
//
// CONTRACT (per .planning/research/codex-config-toml.md / Claude Code 2.1.132 docs):
//   - Emit "permissions.allow" (NOT legacy "autoApprove")
//   - Emit "permissions.deny" (NOT "disallowedTools")
//   - "trustedDirectories" is a TOP-LEVEL key (NOT inside permissions)
//   - "permissions" passthrough merges agent.claude.permissions[k] into output
//   - mergeNotifyHookIntoSettings runs AFTER the synthesizer (verified in Wave 5
//     integration tests, not here)
//
// VC-5
func TestSynthesizeClaudeSettingsGolden(t *testing.T) {
	fixtures := []struct {
		name        string
		profilePath string
		goldenPath  string
	}{
		{"learn.v2", "../../profiles/learn.v2.yaml", "testdata/claude_settings_learn_v2.golden.json"},
		{"dc34", "../../profiles/dc34.yaml", "testdata/claude_settings_dc34.golden.json"},
		{"locked", "../../profiles/locked.yaml", "testdata/claude_settings_locked.golden.json"},
		{"codex", "../../profiles/codex.yaml", "testdata/claude_settings_codex.golden.json"},
	}
	for _, f := range fixtures {
		f := f
		t.Run(f.name, func(t *testing.T) {
			raw, err := os.ReadFile(f.profilePath)
			if err != nil {
				t.Fatalf("read profile %s: %v", f.profilePath, err)
			}
			p, err := profile.Parse(raw)
			if err != nil {
				t.Fatalf("parse profile %s: %v", f.profilePath, err)
			}
			got, err := synthesizeClaudeSettings(p.Spec.Agent)
			if err != nil {
				t.Fatalf("synthesizeClaudeSettings: %v", err)
			}
			gotJSON, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatalf("marshal settings: %v", err)
			}
			want, err := os.ReadFile(filepath.Clean(f.goldenPath))
			if err != nil {
				t.Fatalf("read golden %s: %v (Wave 5 must produce + commit goldens)", f.goldenPath, err)
			}
			if string(gotJSON) != string(want) {
				t.Errorf("synthesizeClaudeSettings(%s) drift:\n%s",
					f.name, diffStrings(string(want), string(gotJSON)))
			}
		})
	}
}

package compiler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestSynthesizeCodexConfigGolden verifies synthesizeCodexConfig() output is
// byte-identical to a golden config.toml for profiles/codex.yaml.
//
// RED STATE (Wave 0): behind the phase92_wave5 build tag, NOT compiled by the
// default build. References the post-phase API (synthesizeCodexConfig,
// profile.AgentSpec, p.Spec.Agent) that does not exist until Wave 5 creates
// pkg/compiler/agent_codex.go. Wave 5 removes the build tag after producing +
// committing the golden; its "task done" criterion is:
//
//	go test ./pkg/compiler/ -tags phase92_wave5 -run TestSynthesizeCodexConfigGolden  // GREEN
//
// CONTRACT (per .planning/research/codex-config-toml.md):
//   - Codex 0.133 has NO native tool allow/deny in config.toml.
//   - The synthesizer therefore emits the existing INERT hook blocks + an args
//     echo + a documented note when agent.codex tools.* fields are populated.
//   - This test verifies the EMITTED toml is byte-identical to the golden — it does
//     NOT assert that Codex actually honors any tool-gating keys (it can't).
//
// VC-5
func TestSynthesizeCodexConfigGolden(t *testing.T) {
	const (
		profilePath = "../../testdata/profiles/codex.yaml"
		goldenPath  = "testdata/codex_config_codex.golden.toml"
	)

	raw, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatalf("read profile %s: %v", profilePath, err)
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatalf("parse profile %s: %v", profilePath, err)
	}

	got, err := synthesizeCodexConfig(p.Spec.Agent)
	if err != nil {
		t.Fatalf("synthesizeCodexConfig: %v", err)
	}

	want, err := os.ReadFile(filepath.Clean(goldenPath))
	if err != nil {
		t.Fatalf("read golden %s: %v (Wave 5 must produce + commit golden)", goldenPath, err)
	}

	if got != string(want) {
		t.Errorf("synthesizeCodexConfig drift:\n%s", diffStrings(string(want), got))
	}
}

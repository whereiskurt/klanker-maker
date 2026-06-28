package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestSynthesizeCodexConfig_LocalProvider verifies that synthesizeCodexConfig
// emits a [model_providers.local] TOML block when AgentCodexSpec.LocalBaseURL
// is set, and that a nil/empty-Codex call remains dormant (no provider block).
//
// Phase 122 Wave 0: intentionally RED until Plan 03 wires the LocalBaseURL/LocalModel
// emission into synthesizeCodexConfig. The test encodes the exact TOML shape that
// Plan 03 must produce — it is the Nyquist sample for that implementation task.
//
// Expected TOML shape (per 122-CONTEXT.md gateway consolidation):
//
//	[model_providers.local]
//	base_url = "http://localhost:8001/v1"
//	wire_api  = "responses"
//	env_key   = "OPENAI_API_KEY"
//	model_provider = "local"
func TestSynthesizeCodexConfig_LocalProvider(t *testing.T) {
	t.Run("with LocalBaseURL set emits model_providers.local block", func(t *testing.T) {
		agent := &profile.AgentSpec{
			Default: "codex",
			Codex: &profile.AgentCodexSpec{
				LocalBaseURL: "http://localhost:8001/v1",
				LocalModel:   "local",
			},
		}

		got, err := synthesizeCodexConfig(agent)
		if err != nil {
			t.Fatalf("synthesizeCodexConfig returned error: %v", err)
		}

		// RED until Plan 03: the current implementation does not emit these lines.
		wants := []string{
			"[model_providers.local]",
			`base_url = "http://localhost:8001/v1"`,
			`wire_api = "responses"`,
			`env_key = "OPENAI_API_KEY"`,
			`model_provider = "local"`,
		}
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("output missing %q\ngot:\n%s", want, got)
			}
		}
	})

	t.Run("dormancy: nil Codex does not emit model_providers.local block", func(t *testing.T) {
		// Nil agent should still return the base hook block and nothing more.
		got, err := synthesizeCodexConfig(nil)
		if err != nil {
			t.Fatalf("synthesizeCodexConfig(nil) returned error: %v", err)
		}
		if strings.Contains(got, "[model_providers.local]") {
			t.Errorf("nil Codex must NOT emit [model_providers.local]; got:\n%s", got)
		}
		// Base block must still be present.
		if !strings.Contains(got, "[features]") {
			t.Errorf("nil Codex must emit base hook block with [features]; got:\n%s", got)
		}
	})

	t.Run("dormancy: empty LocalBaseURL does not emit model_providers.local block", func(t *testing.T) {
		agent := &profile.AgentSpec{
			Default: "codex",
			Codex:   &profile.AgentCodexSpec{}, // no LocalBaseURL set
		}
		got, err := synthesizeCodexConfig(agent)
		if err != nil {
			t.Fatalf("synthesizeCodexConfig returned error: %v", err)
		}
		if strings.Contains(got, "[model_providers.local]") {
			t.Errorf("empty LocalBaseURL must NOT emit [model_providers.local]; got:\n%s", got)
		}
	})
}

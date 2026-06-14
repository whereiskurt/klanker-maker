package profile

import (
	"testing"
)

// TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Allowed verifies that the
// former Phase 92 (Wave 4) VC-6 hard rejection has been LIFTED: a profile may now
// populate spec.agent.claude.tools.autoApprove AND inline a Claude settings.json
// via execution.configFiles simultaneously. The compiler's Wave-5 synthesizer
// deep-merges the typed output on top of the inlined file (typed allow/deny win;
// operator keys like enabledPlugins are preserved) rather than clobbering it, so
// the two surfaces are no longer mutually exclusive. The old rejection caused
// silent data loss and left no way to express enabledPlugins under typed gating.
//
// Coexistence must NOT produce a validation error. (Merge semantics are exercised
// at the compiler layer in pkg/compiler — see
// TestUserData_InlinedSettingsPreservedAlongsideTypedAgent.)
func TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Allowed(t *testing.T) {
	p := &SandboxProfile{
		APIVersion: "klankermaker.example.com/v1",
		Kind:       "SandboxProfile",
		Metadata:   Metadata{Name: "mixed-mode"},
		Spec: Spec{
			Agent: &AgentSpec{
				Claude: &AgentClaudeSpec{
					Tools: AgentToolsSpec{AutoApprove: []string{"Bash", "Read"}},
				},
			},
			Execution: ExecutionSpec{
				ConfigFiles: map[string]string{
					"/home/sandbox/.claude/settings.json": `{"enabledPlugins":{"superpowers@marketplace":true}}`,
				},
			},
		},
	}

	for _, e := range ValidateSemantic(p) {
		if e.Path == `spec.execution.configFiles["/home/sandbox/.claude/settings.json"]` {
			t.Errorf("mixed-mode should no longer be rejected, got error: %s", e.Error())
		}
	}
}

package profile

import (
	"strings"
	"testing"
)

// TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors verifies the locked
// decision that populating agent.claude.tools.autoApprove AND inlining
// execution.configFiles[".claude/settings.json"] simultaneously is a hard
// validation error. No merge fallback — the two configuration surfaces are
// mutually exclusive.
//
// RED STATE (Wave 0): behind the phase92_wave4 build tag, NOT compiled by the
// default build. References the post-phase API (Spec.Agent, AgentSpec,
// AgentClaudeSpec, AgentToolsSpec) plus the mixed-mode check that Wave 4 adds to
// ValidateSemantic. Wave 4 removes the build tag after the validator lands; its
// "task done" criterion is:
//
//	go test ./pkg/profile/ -tags phase92_wave4 -run TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors  // GREEN
//
// VC-6
func TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors(t *testing.T) {
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
					"/home/sandbox/.claude/settings.json": `{"autoApprove":["Bash"]}`,
				},
			},
		},
	}

	errs := ValidateSemantic(p)
	if len(errs) == 0 {
		t.Fatalf("expected validation error for mixed mode, got none")
	}

	var combined strings.Builder
	for _, e := range errs {
		combined.WriteString(e.Error())
		combined.WriteString("\n")
	}
	msg := combined.String()
	if !strings.Contains(msg, "agent.claude.tools.autoApprove") || !strings.Contains(msg, "configFiles") {
		t.Errorf("error must reference both fields by name, got: %s", msg)
	}
}

package profile

import (
	"testing"
)

// TestInheritAgent_DefaultOnlyChildInheritsParentClaude verifies the pointer-merge
// bug fix for the Phase 92 (Wave 4) agent block: a child that sets only
// agent.default must still inherit the parent's agent.claude.tools.autoApprove.
//
// Without the typed mergeAgentSpec, the child's *AgentSpec pointer would replace
// the parent's wholesale, losing the parent's claude tool gating.
func TestInheritAgent_DefaultOnlyChildInheritsParentClaude(t *testing.T) {
	parent := &AgentSpec{
		Default: "claude",
		Claude: &AgentClaudeSpec{
			Tools: AgentToolsSpec{AutoApprove: []string{"Bash", "Read"}},
		},
	}
	child := &AgentSpec{
		Default: "codex",
	}

	merged := mergeAgentSpec(parent, child)
	if merged == nil {
		t.Fatal("expected non-nil merged AgentSpec")
	}
	if merged.Default != "codex" {
		t.Errorf("expected child default 'codex' to win, got %q", merged.Default)
	}
	if merged.Claude == nil {
		t.Fatal("expected merged.Claude to be inherited from parent, got nil")
	}
	if got := merged.Claude.Tools.AutoApprove; len(got) != 2 || got[0] != "Bash" || got[1] != "Read" {
		t.Errorf("expected inherited autoApprove [Bash Read], got %v", got)
	}
}

// TestInheritAgent_ChildEmptyArgsInheritsParent verifies that a child with no
// agent.claude.args inherits the parent's args (slice-empty means "inherit").
func TestInheritAgent_ChildEmptyArgsInheritsParent(t *testing.T) {
	parent := &AgentSpec{
		Claude: &AgentClaudeSpec{
			Args: []string{"--dangerously-skip-permissions"},
		},
	}
	child := &AgentSpec{
		Claude: &AgentClaudeSpec{
			// no Args — should inherit parent's
			TrustedDirectories: []string{"/workspace"},
		},
	}

	merged := mergeAgentSpec(parent, child)
	if merged.Claude == nil {
		t.Fatal("expected merged.Claude non-nil")
	}
	if got := merged.Claude.Args; len(got) != 1 || got[0] != "--dangerously-skip-permissions" {
		t.Errorf("expected inherited args [--dangerously-skip-permissions], got %v", got)
	}
	if got := merged.Claude.TrustedDirectories; len(got) != 1 || got[0] != "/workspace" {
		t.Errorf("expected child trustedDirectories [/workspace], got %v", got)
	}
}

// TestInheritAgent_PermissionsPassthroughKeyMerge verifies the passthrough map is
// top-level key-merged: parent {a:1} + child {b:2} => {a:1, b:2}.
func TestInheritAgent_PermissionsPassthroughKeyMerge(t *testing.T) {
	parent := &AgentSpec{
		Claude: &AgentClaudeSpec{
			Permissions: map[string]any{"a": 1},
		},
	}
	child := &AgentSpec{
		Claude: &AgentClaudeSpec{
			Permissions: map[string]any{"b": 2},
		},
	}

	merged := mergeAgentSpec(parent, child)
	if merged.Claude == nil {
		t.Fatal("expected merged.Claude non-nil")
	}
	perms := merged.Claude.Permissions
	if len(perms) != 2 {
		t.Fatalf("expected 2 merged permission keys, got %d: %v", len(perms), perms)
	}
	if perms["a"] != 1 {
		t.Errorf("expected permissions[a]==1, got %v", perms["a"])
	}
	if perms["b"] != 2 {
		t.Errorf("expected permissions[b]==2, got %v", perms["b"])
	}
}

// TestInheritAgent_ChildWinsPermissionCollision verifies child wins on a key
// collision in the passthrough map.
func TestInheritAgent_ChildWinsPermissionCollision(t *testing.T) {
	parent := &AgentSpec{Claude: &AgentClaudeSpec{Permissions: map[string]any{"k": "parent"}}}
	child := &AgentSpec{Claude: &AgentClaudeSpec{Permissions: map[string]any{"k": "child"}}}

	merged := mergeAgentSpec(parent, child)
	if got := merged.Claude.Permissions["k"]; got != "child" {
		t.Errorf("expected child to win collision, got %v", got)
	}
}

// TestInheritAgent_NilParentReturnsChild and nil-child return-parent edge cases.
func TestInheritAgent_NilEdges(t *testing.T) {
	child := &AgentSpec{Default: "codex"}
	if got := mergeAgentSpec(nil, child); got != child {
		t.Errorf("nil parent should return child unchanged")
	}
	parent := &AgentSpec{Default: "claude"}
	if got := mergeAgentSpec(parent, nil); got != parent {
		t.Errorf("nil child should return parent unchanged")
	}
}

package compiler

import (
	"encoding/json"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestUserData_InlinedSettingsPreservedAlongsideTypedAgent reproduces the
// clobber bug: when a profile inlines a Claude settings.json via configFiles
// (e.g. carrying enabledPlugins) AND also sets a typed spec.agent.claude field
// (here trustedDirectories — which is NOT blocked by validateAgentClaudeNoMixedMode),
// the Wave-5 synthesizer overwrites the inlined file wholesale, silently dropping
// enabledPlugins.
func TestUserData_InlinedSettingsPreservedAlongsideTypedAgent(t *testing.T) {
	p := baseProfile()
	p.Spec.Agent = &profile.AgentSpec{
		Claude: &profile.AgentClaudeSpec{
			TrustedDirectories: []string{"/workspace"},
		},
	}
	p.Spec.Execution.ConfigFiles = map[string]string{
		"/home/sandbox/.claude/settings.json": `{
  "enabledPlugins": {"superpowers@marketplace": true},
  "model": "claude-opus-4-8"
}`,
	}

	ud, err := generateUserData(p, "sb-test-merge", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}

	body := extractHeredocBody(t, ud, "/home/sandbox/.claude/settings.json", "KM_CONFIG_EOF")
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, body)
	}

	if _, ok := got["enabledPlugins"]; !ok {
		t.Errorf("enabledPlugins clobbered by synthesizer; settings.json = %s", body)
	}
	if _, ok := got["model"]; !ok {
		t.Errorf("model key clobbered by synthesizer; settings.json = %s", body)
	}
	// Typed trustedDirectories must still be present (the synthesizer's job).
	if _, ok := got["trustedDirectories"]; !ok {
		t.Errorf("trustedDirectories missing; settings.json = %s", body)
	}
	// km hooks must still be wired.
	if drillHookCmd(t, got, "Stop") != "/opt/km/bin/km-notify-hook Stop" {
		t.Errorf("Stop hook missing after merge")
	}
}

package compiler

import (
	"regexp"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// userdataForAgentTest compiles a profile with a minimal EC2 CLI spec using the
// given agent string (empty = default claude) and returns the generated userdata.
// Mirrors the pattern from userdata_notify_test.go (generateUserData + baseProfile).
func userdataForAgentTest(t *testing.T, agentValue string) string {
	t.Helper()
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifyOnPermission: true,
		NotifyOnIdle:       true,
		Agent:              agentValue,
	}
	ud, err := generateUserData(p, "sb-test70", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return ud
}

// extractContext returns 'around' chars of s centered on the first occurrence
// of marker, or "(not found)" if marker is absent.
func extractContext(s, marker string, around int) string {
	i := strings.Index(s, marker)
	if i < 0 {
		return "(not found)"
	}
	start := i - around/2
	if start < 0 {
		start = 0
	}
	end := i + around
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}

// SC-1: ~/.codex/config.toml install heredoc emitted unconditionally with locked TOML content.
func TestUserdata_CodexConfig_Emitted(t *testing.T) {
	ud := userdataForAgentTest(t, "") // no agent set → still emitted
	musts := []string{
		"cat > /home/sandbox/.codex/config.toml",
		// Codex 0.133+ renamed the feature flag from codex_hooks to hooks.
		// We use the new name; old name emits deprecation warning every exec.
		"hooks = true",
		"[[hooks.PermissionRequest]]",
		`matcher = ".*"`,
		"/opt/km/bin/km-notify-hook PermissionRequest",
		"[[hooks.Stop]]",
		"/opt/km/bin/km-notify-hook Stop",
		"timeout = 30",
		"chown sandbox:sandbox /home/sandbox/.codex/config.toml",
	}
	for _, m := range musts {
		if !strings.Contains(ud, m) {
			t.Errorf("userdata missing required fragment: %q", m)
		}
	}
}

// SC-1: PostToolUse explicitly NOT present in Codex config.toml section (Tier 2 deferral guard).
func TestUserdata_CodexConfig_NoPostToolUse(t *testing.T) {
	ud := userdataForAgentTest(t, "")
	// Find the Codex config.toml section boundaries.
	codexStart := strings.Index(ud, "cat > /home/sandbox/.codex/config.toml")
	if codexStart < 0 {
		t.Fatal("codex config.toml heredoc not found in userdata")
	}
	codexTail := ud[codexStart:]
	codexEnd := strings.Index(codexTail, "KM_CODEX_CONFIG_EOF")
	if codexEnd < 0 {
		t.Fatal("KM_CODEX_CONFIG_EOF sentinel not found after codex heredoc start")
	}
	codexSection := codexTail[:codexEnd]
	if strings.Contains(codexSection, "[[hooks.PostToolUse]]") {
		t.Errorf("codex config.toml section contains [[hooks.PostToolUse]] — Tier 2 deferral violated")
	}
	if strings.Contains(codexSection, "PostToolUse") {
		t.Errorf("codex config.toml section contains PostToolUse reference — Tier 2 deferral violated")
	}
}

// SC-4/5/6: KM_AGENT=claude when spec.cli.agent is empty (absence ≡ claude default).
func TestUserdata_KMAgentEnv_DefaultClaude(t *testing.T) {
	ud := userdataForAgentTest(t, "") // empty agent → default claude
	if !strings.Contains(ud, "KM_AGENT=claude") {
		t.Errorf("userdata missing KM_AGENT=claude; context near KM_AGENT: %s",
			extractContext(ud, "KM_AGENT", 80))
	}
	if strings.Contains(ud, "KM_AGENT=codex") {
		t.Errorf("userdata unexpectedly has KM_AGENT=codex when agent is empty")
	}
}

// SC-4/5/6: KM_AGENT=codex when spec.cli.agent: codex.
func TestUserdata_KMAgentEnv_Codex(t *testing.T) {
	ud := userdataForAgentTest(t, "codex")
	if !strings.Contains(ud, "KM_AGENT=codex") {
		t.Errorf("userdata missing KM_AGENT=codex; context near KM_AGENT: %s",
			extractContext(ud, "KM_AGENT", 80))
	}
}

// SC-4/5/6: KM_AGENT appears in BOTH /etc/profile.d/km-notify-env.sh AND /etc/km/notify.env.
// The dual emission is critical — interactive shells source the .sh, systemd
// units read the .env. Phase 67 broke this once; the test prevents regression.
func TestUserdata_KMAgentEnv_BothFiles(t *testing.T) {
	ud := userdataForAgentTest(t, "codex")
	// /etc/profile.d/km-notify-env.sh uses `export KEY="value"`
	profileDForm := regexp.MustCompile(`export KM_AGENT="codex"`)
	// /etc/km/notify.env uses `KEY=value` (no export, no leading space)
	notifyEnvForm := regexp.MustCompile(`(?m)^KM_AGENT=codex$`)
	if !profileDForm.MatchString(ud) {
		t.Errorf("userdata missing `export KM_AGENT=\"codex\"` (/etc/profile.d/km-notify-env.sh form)")
	}
	if !notifyEnvForm.MatchString(ud) {
		t.Errorf("userdata missing bare `KM_AGENT=codex` line (/etc/km/notify.env form)")
	}
}

// TestUserdata_CrossAgentHandoff_ChownsStderrBeforeDispatch is a regression guard
// for the cross-agent switch bug: the handoff block runs as ROOT and appends
// km-slack post diagnostics to $RUN_DIR/stderr.log (steps 1/3/5/6), leaving the
// file root-owned 0644. The agent dispatch then runs as the *sandbox* user with a
// 2>$RUN_DIR/stderr.log redirect that O_TRUNCs the file -- a write the sandbox user
// cannot perform on a root-owned file. bash fails the redirect (Permission denied)
// before exec, so codex/claude never runs (exit 1, empty output.json) and the
// $NEW_SESSION-gated Slack reply + DDB write are silently skipped. Observed symptom:
// the handoff posts the new top-level message but no agent reply ever lands, and
// no DDB row with the new agent_type is written so follow-up replies also fail.
//
// The fix chowns stderr.log to the sandbox user AFTER the handoff block and BEFORE
// the dispatch fork. Ordering is the whole point -- a chown after dispatch is useless.
func TestUserdata_CrossAgentHandoff_ChownsStderrBeforeDispatch(t *testing.T) {
	out := compileInboundUserData(t, minimalSlackInboundProfile(t, true))

	const (
		handoff  = `EFFECTIVE_AGENT="$NEW_AGENT"`               // handoff step 8 (root has written stderr.log by now)
		chownFix = `chown sandbox:sandbox "$RUN_DIR/stderr.log"` // the fix
		dispatch = `if [ "$EFFECTIVE_AGENT" = "codex" ]; then`   // first occurrence == sandbox-user dispatch fork
	)

	iHandoff := strings.Index(out, handoff)
	iChown := strings.Index(out, chownFix)
	iDispatch := strings.Index(out, dispatch)

	if iHandoff < 0 {
		t.Fatalf("poller missing cross-agent handoff marker %q", handoff)
	}
	if iChown < 0 {
		t.Fatalf("poller missing stderr.log chown fix %q -- cross-agent switch dispatch will fail with Permission denied", chownFix)
	}
	if iDispatch < 0 {
		t.Fatalf("poller missing dispatch fork marker %q", dispatch)
	}
	if !(iHandoff < iChown && iChown < iDispatch) {
		t.Fatalf("stderr.log chown must come AFTER the handoff block and BEFORE the dispatch fork; got handoff=%d chown=%d dispatch=%d", iHandoff, iChown, iDispatch)
	}
}

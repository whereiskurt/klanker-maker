package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestBuildAgentRunScript_NoBedrockParity locks in the three fixes that brought
// scheduled `km at agent run` into parity with the interactive / direct
// `km agent run` execution context on a no-bedrock (direct-API) sandbox.
func TestBuildAgentRunScript_NoBedrockParity(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("post a status update"))
	script := buildAgentRunScript(b64, "my-artifacts-bucket", "20260617T120000Z", false)

	// Gap 1: OAuth token must be presented as a Bearer token via
	// CLAUDE_CODE_OAUTH_TOKEN, never as ANTHROPIC_API_KEY (which is sent as
	// x-api-key and 401s for an sk-ant-oat token).
	if !strings.Contains(script, `export CLAUDE_CODE_OAUTH_TOKEN="$OAUTH_TOKEN"`) {
		t.Errorf("script must inject the OAuth token via CLAUDE_CODE_OAUTH_TOKEN; got:\n%s", script)
	}
	if strings.Contains(script, `export ANTHROPIC_API_KEY="$OAUTH_TOKEN"`) {
		t.Errorf("script must NOT inject the OAuth token via ANTHROPIC_API_KEY (the 401 bug); got:\n%s", script)
	}

	// The OAuth injection must auto-detect no-bedrock mode at runtime so a
	// no-bedrock sandbox authenticates even without an explicit --no-bedrock flag.
	if !strings.Contains(script, `if [ -z "$CLAUDE_CODE_USE_BEDROCK" ]`) {
		t.Errorf("OAuth injection must be gated on Bedrock being inactive; got:\n%s", script)
	}

	// Gap 2: --bare suppresses plugin sync (klanker:slack) + hooks; it must be gone.
	if strings.Contains(script, "--bare") {
		t.Errorf("script must NOT pass --bare (it skips plugin sync + hooks); got:\n%s", script)
	}
	if !strings.Contains(script, `claude -p "$PROMPT" --output-format json --dangerously-skip-permissions`) {
		t.Errorf("expected the claude headless invocation; got:\n%s", script)
	}

	// Gap 3: must source the FULL profile.d set (login-shell parity) so KM_SLACK_*
	// from km-slack-runtime.sh / km-notify-env.sh are present for Slack-posting skills.
	if !strings.Contains(script, `for f in /etc/profile.d/*.sh; do source "$f" 2>/dev/null; done`) {
		t.Errorf("script must source the full /etc/profile.d set; got:\n%s", script)
	}

	// On a no-bedrock-by-default box we must NOT force-unset Bedrock env (there is none).
	if strings.Contains(script, "unset CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("noBedrock=false must not emit the explicit Bedrock unset block; got:\n%s", script)
	}

	// Plumbing sanity: prompt + bucket + runID + tmux signal wired through.
	for _, want := range []string{b64, "my-artifacts-bucket", "20260617T120000Z", "tmux wait-for -S km-done-20260617T120000Z"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q; got:\n%s", want, script)
		}
	}
}

// TestBuildAgentRunScript_ExplicitNoBedrockUnsetsBedrock verifies that an explicit
// --no-bedrock (event.NoBedrock=true) additionally force-unsets Bedrock env, for a
// Bedrock-capable sandbox that the operator wants on the direct-API path.
func TestBuildAgentRunScript_ExplicitNoBedrockUnsetsBedrock(t *testing.T) {
	script := buildAgentRunScript(base64.StdEncoding.EncodeToString([]byte("hi")), "b", "RID", true)
	for _, want := range []string{
		"unset CLAUDE_CODE_USE_BEDROCK",
		"unset ANTHROPIC_BASE_URL",
		"unset ANTHROPIC_DEFAULT_OPUS_MODEL",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("explicit no-bedrock must emit %q; got:\n%s", want, script)
		}
	}
	// Even with the explicit unset, auth still goes through CLAUDE_CODE_OAUTH_TOKEN.
	if !strings.Contains(script, `export CLAUDE_CODE_OAUTH_TOKEN="$OAUTH_TOKEN"`) {
		t.Errorf("explicit no-bedrock must still inject via CLAUDE_CODE_OAUTH_TOKEN; got:\n%s", script)
	}
}

package cmd

// Plan 68-07: --transcript-stream / --no-transcript-stream flag plumbing for
// km agent run. Mirrors the Phase 62 (HOOK-04) NotifyOnPermission pattern.
//
// These tests live in package cmd (internal) so they can reach unexported
// symbols if needed, but the helpers under test (BuildAgentShellCommands /
// AgentRunOptions) are exported and already covered by the package cmd_test
// suite. We intentionally keep these tests in the same internal package as
// the Wave 0 stubs they replace.

import (
	"strings"
	"testing"
)

// agentTranscriptBoolPtr returns a *bool for terse test setup.
func agentTranscriptBoolPtr(b bool) *bool { return &b }

// TestAgentRun_TranscriptStreamFlag verifies that opts.TranscriptStream=&true
// injects export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="1" into the generated
// agent script.
func TestAgentRun_TranscriptStreamFlag(t *testing.T) {
	cmds, _ := BuildAgentShellCommands("test prompt", AgentRunOptions{
		AgentType:        "claude",
		TranscriptStream: agentTranscriptBoolPtr(true),
		ArtifactsBucket:  "test-bucket",
	})
	script := strings.Join(cmds, "\n")
	if !strings.Contains(script, `export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="1"`) {
		t.Errorf("expected KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1 export, got:\n%s", script)
	}
}

// TestAgentRun_NoTranscriptStreamFlag verifies that opts.TranscriptStream=&false
// (the --no-transcript-stream path) injects =0.
func TestAgentRun_NoTranscriptStreamFlag(t *testing.T) {
	cmds, _ := BuildAgentShellCommands("test prompt", AgentRunOptions{
		AgentType:        "claude",
		TranscriptStream: agentTranscriptBoolPtr(false),
		ArtifactsBucket:  "test-bucket",
	})
	script := strings.Join(cmds, "\n")
	if !strings.Contains(script, `export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="0"`) {
		t.Errorf("expected KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=0 export, got:\n%s", script)
	}
}

// TestAgentRun_TranscriptStreamProfileDefault verifies that opts.TranscriptStream=nil
// (neither CLI flag set) emits NO KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED line —
// the profile-derived /etc/profile.d/km-notify-env.sh value applies instead.
func TestAgentRun_TranscriptStreamProfileDefault(t *testing.T) {
	cmds, _ := BuildAgentShellCommands("test prompt", AgentRunOptions{
		AgentType:       "claude",
		ArtifactsBucket: "test-bucket",
		// TranscriptStream left nil
	})
	script := strings.Join(cmds, "\n")
	if strings.Contains(script, "KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED") {
		t.Errorf("expected no KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED line when pointer nil, got:\n%s", script)
	}
}

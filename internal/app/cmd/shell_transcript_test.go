package cmd

// Plan 68-07: --transcript-stream / --no-transcript-stream flag plumbing for
// km shell. Mirrors the Phase 62 (HOOK-04) resolveNotifyFlags +
// buildNotifySendCommands pattern in shell.go (cmd.Flags().Changed style).
//
// These tests live in package cmd (internal) because resolveTranscriptFlag and
// buildNotifySendCommands are unexported helpers — same convention as
// shell_notify_test.go.

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// shellTranscriptBoolPtr is a helper for the transcript-stream tests.
func shellTranscriptBoolPtr(b bool) *bool { return &b }

// newTranscriptCmd registers the four flags resolveNotifyFlags + the two new
// transcript-stream flags expect, so test ParseFlags doesn't error on unknown.
func newTranscriptCmd() *cobra.Command {
	c := &cobra.Command{Use: "shell"}
	c.Flags().Bool("notify-on-permission", false, "")
	c.Flags().Bool("no-notify-on-permission", false, "")
	c.Flags().Bool("notify-on-idle", false, "")
	c.Flags().Bool("no-notify-on-idle", false, "")
	c.Flags().Bool("transcript-stream", false, "")
	c.Flags().Bool("no-transcript-stream", false, "")
	return c
}

// TestShell_TranscriptStreamFlag verifies --transcript-stream resolves to *bool(true)
// and that the resolved pointer flows through buildNotifySendCommands to emit
// export KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="1" in the write commands.
func TestShell_TranscriptStreamFlag(t *testing.T) {
	c := newTranscriptCmd()
	if err := c.ParseFlags([]string{"--transcript-stream"}); err != nil {
		t.Fatalf("ParseFlags error: %v", err)
	}
	transcript := resolveTranscriptFlag(c)
	if transcript == nil || *transcript != true {
		t.Fatalf("expected transcript=*true, got %v", transcript)
	}

	write, _ := buildNotifySendCommands(nil, nil, transcript)
	joined := strings.Join(write, "\n")
	if !strings.Contains(joined, `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="1"`) {
		t.Errorf("expected KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1 in write cmds, got:\n%s", joined)
	}
}

// TestShell_NoTranscriptStreamFlag verifies --no-transcript-stream resolves to
// *bool(false) and emits =0 (explicit force-disable for this session).
func TestShell_NoTranscriptStreamFlag(t *testing.T) {
	c := newTranscriptCmd()
	if err := c.ParseFlags([]string{"--no-transcript-stream"}); err != nil {
		t.Fatalf("ParseFlags error: %v", err)
	}
	transcript := resolveTranscriptFlag(c)
	if transcript == nil || *transcript != false {
		t.Fatalf("expected transcript=*false, got %v", transcript)
	}

	write, _ := buildNotifySendCommands(nil, nil, transcript)
	joined := strings.Join(write, "\n")
	if !strings.Contains(joined, `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED="0"`) {
		t.Errorf("expected KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=0 in write cmds, got:\n%s", joined)
	}
}

// TestShell_TranscriptStreamProfileDefault verifies that when neither flag is
// supplied resolveTranscriptFlag returns nil and buildNotifySendCommands does
// NOT emit a KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED line — the profile-derived
// /etc/profile.d/km-notify-env.sh value applies instead.
func TestShell_TranscriptStreamProfileDefault(t *testing.T) {
	c := newTranscriptCmd()
	if err := c.ParseFlags([]string{}); err != nil {
		t.Fatalf("ParseFlags error: %v", err)
	}
	transcript := resolveTranscriptFlag(c)
	if transcript != nil {
		t.Fatalf("expected nil transcript when neither flag set, got %v", transcript)
	}

	// With ALL three pointers nil, build should return nil/nil (no SSM SendCommand).
	write, cleanup := buildNotifySendCommands(nil, nil, nil)
	if write != nil || cleanup != nil {
		t.Errorf("expected nil/nil when all overrides nil, got write=%v cleanup=%v", write, cleanup)
	}

	// With perm-only set, transcript nil, no transcript line should appear.
	write2, _ := buildNotifySendCommands(shellTranscriptBoolPtr(true), nil, nil)
	joined := strings.Join(write2, "\n")
	if strings.Contains(joined, "KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED") {
		t.Errorf("expected NO KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED when transcript ptr nil, got:\n%s", joined)
	}
}

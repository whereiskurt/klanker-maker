package cmd

// Phase 62 (HOOK-04): Pure-function unit tests for notify helper functions in shell.go.
// These tests live in package cmd (internal) because resolveNotifyFlags and
// buildNotifySendCommands are unexported helpers.

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// shellTestBoolPtr returns a pointer to b. Used in notify helper tests.
func shellTestBoolPtr(b bool) *bool { return &b }

// TestBuildNotifySendCommands_BothNil verifies that (nil, nil) returns empty slices.
func TestBuildNotifySendCommands_BothNil(t *testing.T) {
	write, cleanup := buildNotifySendCommands(nil, nil, nil)
	if write != nil || cleanup != nil {
		t.Errorf("expected nil/nil, got write=%v cleanup=%v", write, cleanup)
	}
}

// TestBuildNotifySendCommands_PermissionOnly verifies that a non-nil permission
// pointer produces write commands containing KM_NOTIFY_ON_PERMISSION="1"
// but NOT KM_NOTIFY_ON_IDLE, plus a cleanup command.
func TestBuildNotifySendCommands_PermissionOnly(t *testing.T) {
	write, cleanup := buildNotifySendCommands(shellTestBoolPtr(true), nil, nil)
	if len(write) < 1 {
		t.Fatal("expected at least 1 write cmd")
	}
	joined := strings.Join(write, "\n")
	if !strings.Contains(joined, `KM_NOTIFY_ON_PERMISSION="1"`) {
		t.Errorf("missing KM_NOTIFY_ON_PERMISSION=1, got:\n%s", joined)
	}
	if strings.Contains(joined, "KM_NOTIFY_ON_IDLE") {
		t.Errorf("did not expect KM_NOTIFY_ON_IDLE (idle was nil), got:\n%s", joined)
	}
	if len(cleanup) == 0 || !strings.Contains(cleanup[0], "rm -f /etc/profile.d/zz-km-notify.sh") {
		t.Errorf("expected rm -f cleanup, got %v", cleanup)
	}
}

// TestBuildNotifySendCommands_BothExplicit verifies that both pointers set
// produces write commands containing both KM_NOTIFY_ON_PERMISSION and KM_NOTIFY_ON_IDLE.
func TestBuildNotifySendCommands_BothExplicit(t *testing.T) {
	write, cleanup := buildNotifySendCommands(shellTestBoolPtr(true), shellTestBoolPtr(false), nil)
	joined := strings.Join(write, "\n")
	if !strings.Contains(joined, `KM_NOTIFY_ON_PERMISSION="1"`) {
		t.Errorf("missing perm=1, got:\n%s", joined)
	}
	if !strings.Contains(joined, `KM_NOTIFY_ON_IDLE="0"`) {
		t.Errorf("missing idle=0, got:\n%s", joined)
	}
	if len(cleanup) == 0 {
		t.Errorf("expected cleanup cmds, got none")
	}
}

// TestResolveNotifyFlags_NoneChanged verifies that when no flags are changed,
// both return values are nil.
func TestResolveNotifyFlags_NoneChanged(t *testing.T) {
	cmd := &cobra.Command{Use: "shell"}
	cmd.Flags().Bool("notify-on-permission", false, "")
	cmd.Flags().Bool("no-notify-on-permission", false, "")
	cmd.Flags().Bool("notify-on-idle", false, "")
	cmd.Flags().Bool("no-notify-on-idle", false, "")
	perm, idle := resolveNotifyFlags(cmd)
	if perm != nil || idle != nil {
		t.Errorf("expected nil/nil, got perm=%v idle=%v", perm, idle)
	}
}

// TestResolveNotifyFlags_PositiveOnly verifies that --notify-on-permission
// sets perm to a non-nil true pointer and leaves idle nil.
func TestResolveNotifyFlags_PositiveOnly(t *testing.T) {
	cmd := &cobra.Command{Use: "shell"}
	cmd.Flags().Bool("notify-on-permission", false, "")
	cmd.Flags().Bool("no-notify-on-permission", false, "")
	cmd.Flags().Bool("notify-on-idle", false, "")
	cmd.Flags().Bool("no-notify-on-idle", false, "")
	if err := cmd.ParseFlags([]string{"--notify-on-permission"}); err != nil {
		t.Fatalf("ParseFlags error: %v", err)
	}
	perm, idle := resolveNotifyFlags(cmd)
	if perm == nil || *perm != true {
		t.Errorf("expected perm=true ptr, got %v", perm)
	}
	if idle != nil {
		t.Errorf("expected idle nil, got %v", idle)
	}
}

// TestResolveNotifyFlags_NegativeOverridesProfile verifies that --no-notify-on-idle
// sets idle to a non-nil false pointer (explicit disable overrides profile default).
func TestResolveNotifyFlags_NegativeOverridesProfile(t *testing.T) {
	cmd := &cobra.Command{Use: "shell"}
	cmd.Flags().Bool("notify-on-permission", false, "")
	cmd.Flags().Bool("no-notify-on-permission", false, "")
	cmd.Flags().Bool("notify-on-idle", false, "")
	cmd.Flags().Bool("no-notify-on-idle", false, "")
	if err := cmd.ParseFlags([]string{"--no-notify-on-idle"}); err != nil {
		t.Fatalf("ParseFlags error: %v", err)
	}
	_, idle := resolveNotifyFlags(cmd)
	if idle == nil || *idle != false {
		t.Errorf("expected idle=false ptr (explicit no-), got %v", idle)
	}
}

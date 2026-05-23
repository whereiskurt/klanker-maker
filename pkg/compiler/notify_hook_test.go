package compiler

import "testing"

// TestNotifyHook_CodexPermissionRequest_Fires — Codex PermissionRequest event
// (synonym of Claude's Notification) routes through the existing notify path
// with KM_NOTIFY_ON_PERMISSION=1.
// SC-3: Plan 70-03 Task 2 implements; Wave 0 baseline stub.
func TestNotifyHook_CodexPermissionRequest_Fires(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-03 Task 2")
}

// TestNotifyHook_CodexPermissionRequest_GatedOff — with the gate var unset/0,
// the hook exits silently. Mirrors existing Notification gate behavior.
// SC-3: Plan 70-03 Task 2 implements; Wave 0 baseline stub.
func TestNotifyHook_CodexPermissionRequest_GatedOff(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-03 Task 2")
}

// TestNotifyHook_Stop_LastAssistantMessageFastPath — Codex Stop payload with
// `last_assistant_message` set; hook reads it directly without tailing a
// transcript file.
// SC-2: Plan 70-03 Task 2 implements; Wave 0 baseline stub.
func TestNotifyHook_Stop_LastAssistantMessageFastPath(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-03 Task 2")
}

// TestNotifyHook_Stop_TranscriptFallbackWhenLastAssistantMissing — Claude-style
// payload with transcript_path set but last_assistant_message absent. Regression
// guard: the Codex fast-path must NOT break the Claude tail-read path.
// SC-2: Plan 70-03 Task 2 implements; Wave 0 baseline stub.
func TestNotifyHook_Stop_TranscriptFallbackWhenLastAssistantMissing(t *testing.T) {
	t.Skip("Wave 0 stub — Plan 70-03 Task 2")
}

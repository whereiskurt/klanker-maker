package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Phase 70 (Plan 70-03): Codex parity hook tests.
//
// These tests reuse the existing harness from notify_hook_script_test.go
// (setupHookEnv / runHook / readStubLog / argListContains) to exercise the
// new PermissionRequest event branch and the last_assistant_message Stop
// fast-path added to the km-notify-hook bash heredoc in userdata.go.
//
// SC-2: Stop hook reads last_assistant_message for Codex; falls back to
//       transcript tail for Claude.
// SC-3: PermissionRequest hook fires email + Slack with tool_name, exits 0,
//       Codex --dangerously-bypass-approvals-and-sandbox auto-approves.

// TestNotifyHook_CodexPermissionRequest_Fires pipes a Codex PermissionRequest
// JSON payload through the hook with KM_NOTIFY_ON_PERMISSION=1 and asserts the
// email-branch sentinel fires with the correct subject and tool_name in the body.
// SC-3: confirms the PermissionRequest branch reaches km-send.
func TestNotifyHook_CodexPermissionRequest_Fires(t *testing.T) {
	hookPath, logPath, _ := setupHookEnv(t)

	payload := `{"hook_event_name":"PermissionRequest","session_id":"sess-codex-1","tool_name":"apply_patch","cwd":"/workspace"}`

	code, _, stderr := runHook(t, hookPath, "PermissionRequest", map[string]string{
		"KM_NOTIFY_ON_PERMISSION": "1",
		"KM_SANDBOX_ID":           "sb-test70",
	}, payload)

	if code != 0 {
		t.Errorf("hook exited %d; expected 0 (hook must never block Codex); stderr: %s", code, stderr)
	}

	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("expected 1 km-send invocation for PermissionRequest; got %d (stderr=%s)", len(calls), stderr)
	}
	c := calls[0]

	// Subject must be "[$sandbox_id] needs permission".
	wantSubject := "[sb-test70] needs permission"
	if !argListContains(c.argList, wantSubject) {
		t.Errorf("subject not found in km-send args; argList=%v", c.argList)
	}

	// Body must contain the tool_name from the Codex payload.
	if !strings.Contains(c.bodyContent, "apply_patch") {
		t.Errorf("body missing tool_name 'apply_patch'; bodyContent=%q", c.bodyContent)
	}

	// --body flag must be present (hook sends via file, not stdin).
	if !argListContains(c.argList, "--body") {
		t.Error("km-send called without --body flag")
	}
}

// TestNotifyHook_CodexPermissionRequest_GatedOff pipes a Codex PermissionRequest
// payload with KM_NOTIFY_ON_PERMISSION=0 and asserts the hook exits 0 with no
// km-send invocation (gate-off behavior mirrors existing Notification gate).
// SC-3: confirms Codex PermissionRequest does not spam when gate is off.
func TestNotifyHook_CodexPermissionRequest_GatedOff(t *testing.T) {
	hookPath, logPath, _ := setupHookEnv(t)

	payload := `{"hook_event_name":"PermissionRequest","session_id":"sess-codex-1","tool_name":"apply_patch","cwd":"/workspace"}`

	code, _, stderr := runHook(t, hookPath, "PermissionRequest", map[string]string{
		"KM_NOTIFY_ON_PERMISSION": "0",
		"KM_SANDBOX_ID":           "sb-test70",
	}, payload)

	if code != 0 {
		t.Errorf("hook exited %d; expected 0 (hook must never block Codex); stderr: %s", code, stderr)
	}

	// No km-send invocation when gate is off.
	info, statErr := os.Stat(logPath)
	if statErr == nil && info.Size() > 0 {
		b, _ := os.ReadFile(logPath)
		t.Errorf("expected zero km-send invocations when KM_NOTIFY_ON_PERMISSION=0; log:\n%s", string(b))
	}
}

// TestNotifyHook_Stop_LastAssistantMessageFastPath pipes a Codex Stop payload
// that includes a last_assistant_message field. The hook should read it directly
// without falling through to transcript-tail logic.
//
// Verification: the transcript_path in the payload points to a non-existent
// file. If the hook attempted to tail the transcript (the old Claude path), it
// would fall through to "(no recent assistant text)". The presence of km-send
// with a non-fallback body proves the fast-path fired.
// SC-2: Codex Stop fast-path.
func TestNotifyHook_Stop_LastAssistantMessageFastPath(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)

	// transcript_path points to a file that does NOT exist in tmpdir.
	// If the hook tries to tail it, body_text will be empty → falls back to
	// "(no recent assistant text)". That would fail the assertion below.
	nonexistentTranscript := filepath.Join(tmpdir, "nonexistent-codex.jsonl")

	payload := `{"hook_event_name":"Stop","session_id":"sess-codex-2","last_assistant_message":"The answer is 4.","transcript_path":"` + nonexistentTranscript + `"}`

	code, _, stderr := runHook(t, hookPath, "Stop", map[string]string{
		"KM_NOTIFY_ON_IDLE": "1",
		"KM_SANDBOX_ID":     "sb-test70",
	}, payload)

	if code != 0 {
		t.Errorf("hook exited %d; expected 0; stderr: %s", code, stderr)
	}

	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("expected 1 km-send invocation for Codex Stop; got %d (stderr=%s)", len(calls), stderr)
	}
	c := calls[0]

	// Subject must be "[$sandbox_id] idle".
	wantSubject := "[sb-test70] idle"
	if !argListContains(c.argList, wantSubject) {
		t.Errorf("subject not found in km-send args; argList=%v", c.argList)
	}

	// Body must contain the last_assistant_message text (NOT the fallback).
	if strings.Contains(c.bodyContent, "(no recent assistant text)") {
		t.Errorf("body contains fallback text — hook did NOT read last_assistant_message; bodyContent=%q", c.bodyContent)
	}
	if !strings.Contains(c.bodyContent, "The answer is 4.") {
		t.Errorf("body missing last_assistant_message value; bodyContent=%q", c.bodyContent)
	}
}

// TestNotifyHook_Stop_TranscriptFallbackWhenLastAssistantMissing pipes a
// Claude-style Stop payload (no last_assistant_message) with a valid
// transcript_path pointing to a real JSONL fixture. The hook should fall back
// to the transcript-tail path and include the assistant text in the body.
//
// This is a regression guard: the Codex fast-path must not break the existing
// Claude transcript-tail path.
// SC-2: Claude Stop fallback path regression guard.
func TestNotifyHook_Stop_TranscriptFallbackWhenLastAssistantMissing(t *testing.T) {
	hookPath, logPath, tmpdir := setupHookEnv(t)

	// Write a minimal transcript JSONL with an assistant turn.
	transcriptPath := filepath.Join(tmpdir, "transcript.jsonl")
	transcriptBody := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from Claude tail."}]}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcriptBody), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	// Claude-style payload: has transcript_path, NO last_assistant_message.
	payload := `{"hook_event_name":"Stop","session_id":"sess-claude-1","transcript_path":"` + transcriptPath + `","cwd":"/workspace"}`

	code, _, stderr := runHook(t, hookPath, "Stop", map[string]string{
		"KM_NOTIFY_ON_IDLE": "1",
		"KM_SANDBOX_ID":     "sb-test70",
	}, payload)

	if code != 0 {
		t.Errorf("hook exited %d; expected 0; stderr: %s", code, stderr)
	}

	calls := readStubLog(t, logPath)
	if len(calls) != 1 {
		t.Fatalf("expected 1 km-send invocation for Claude Stop; got %d (stderr=%s)", len(calls), stderr)
	}
	c := calls[0]

	// Subject must be "[$sandbox_id] idle".
	wantSubject := "[sb-test70] idle"
	if !argListContains(c.argList, wantSubject) {
		t.Errorf("subject not found in km-send args; argList=%v", c.argList)
	}

	// Body must contain text from the transcript (transcript-tail path fired).
	// The existing jq pipeline extracts .message.content[].text from assistant turns.
	if !strings.Contains(c.bodyContent, "Hello from Claude tail.") {
		t.Errorf("body missing transcript assistant text (fallback path did not fire); bodyContent=%q", c.bodyContent)
	}
}

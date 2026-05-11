package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// =============================================================================
// Signal 1: Login shells
// =============================================================================

func TestSignal_LoginShell_Positive(t *testing.T) {
	r := &fakeRunner{responses: map[string][]byte{"who": []byte("sandbox pts/0 2026-05-10 12:00\n")}}
	if !checkLoginShells(r) {
		t.Fatalf("expected positive when 'who' returns non-empty output")
	}
}

func TestSignal_LoginShell_Negative(t *testing.T) {
	r := &fakeRunner{responses: map[string][]byte{"who": []byte("")}}
	if checkLoginShells(r) {
		t.Fatalf("expected negative when 'who' returns empty output")
	}
}

// =============================================================================
// Signal 2: tmux clients
// =============================================================================

func TestSignal_TmuxClients_Positive(t *testing.T) {
	// tmux list-clients returns at least one line → attached client present.
	r := &fakeRunner{
		responses: map[string][]byte{
			"runuser -u sandbox -- tmux list-clients -t ": []byte("/dev/pts/0: session 0\n"),
		},
	}
	if !checkTmuxClients(r) {
		t.Fatalf("expected positive when tmux list-clients returns non-empty output")
	}
}

func TestSignal_TmuxClients_NegativeNoServer(t *testing.T) {
	// When tmux server is not running, list-clients exits with code 1 (empty output + error).
	// Signal must return false — not crash.
	r := &fakeRunner{
		responses: map[string][]byte{},
		errors:    map[string]error{"runuser -u sandbox -- tmux list-clients -t ": errExit1},
	}
	if checkTmuxClients(r) {
		t.Fatalf("expected negative when tmux list-clients returns exit code 1 (no server)")
	}
}

// =============================================================================
// Signal 3: Recent inbound email
// =============================================================================

func TestSignal_Email_Positive(t *testing.T) {
	dir := t.TempDir()
	stampPath := filepath.Join(dir, ".presence-last-tick")
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Create stamp first, then a newer mail file.
	if err := os.WriteFile(stampPath, nil, 0o644); err != nil {
		t.Fatalf("create stamp: %v", err)
	}
	time.Sleep(10 * time.Millisecond) // ensure mtime ordering
	if err := os.WriteFile(filepath.Join(mailDir, "msg1"), []byte("body"), 0o644); err != nil {
		t.Fatalf("create mail: %v", err)
	}
	if !checkInboundEmail(mailDir, stampPath) {
		t.Fatalf("expected positive when mail file is newer than stamp")
	}
}

func TestSignal_Email_NegativeNoNewerFile(t *testing.T) {
	dir := t.TempDir()
	stampPath := filepath.Join(dir, ".presence-last-tick")
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Create mail file first, then stamp (stamp is newer → no new email).
	if err := os.WriteFile(filepath.Join(mailDir, "msg1"), []byte("old"), 0o644); err != nil {
		t.Fatalf("create mail: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(stampPath, nil, 0o644); err != nil {
		t.Fatalf("create stamp: %v", err)
	}
	if checkInboundEmail(mailDir, stampPath) {
		t.Fatalf("expected negative when no mail file is newer than stamp")
	}
}

// =============================================================================
// Signal 4: Recent inbound Slack
// =============================================================================

func TestSignal_Slack_Positive(t *testing.T) {
	dir := t.TempDir()
	presenceStamp := filepath.Join(dir, ".presence-last-tick")
	slackStamp := filepath.Join(dir, "last-slack-inbound")
	// Create presence stamp first, then slack stamp (Slack message more recent).
	if err := os.WriteFile(presenceStamp, nil, 0o644); err != nil {
		t.Fatalf("create presence stamp: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(slackStamp, nil, 0o644); err != nil {
		t.Fatalf("create slack stamp: %v", err)
	}
	if !checkInboundSlack(slackStamp, presenceStamp) {
		t.Fatalf("expected positive when slack stamp is newer than presence stamp")
	}
}

func TestSignal_Slack_NegativeStampMissing(t *testing.T) {
	dir := t.TempDir()
	presenceStamp := filepath.Join(dir, ".presence-last-tick")
	slackStamp := filepath.Join(dir, "last-slack-inbound") // does not exist
	if err := os.WriteFile(presenceStamp, nil, 0o644); err != nil {
		t.Fatalf("create presence stamp: %v", err)
	}
	if checkInboundSlack(slackStamp, presenceStamp) {
		t.Fatalf("expected negative when slack stamp file is missing")
	}
}

// =============================================================================
// Signal 5: Headless agent process
// =============================================================================

func TestSignal_AgentProcess_Positive(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{
			"pgrep -af (^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\\.sh": []byte("1234 /usr/local/bin/claude -p do task\n"),
		},
	}
	if !checkAgentProcess(r) {
		t.Fatalf("expected positive when pgrep returns matching PIDs")
	}
}

func TestSignal_AgentProcess_NegativeEmpty(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{},
		errors:    map[string]error{"pgrep -af (^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\\.sh": errExit1},
	}
	if checkAgentProcess(r) {
		t.Fatalf("expected negative when pgrep returns no matches (exit 1)")
	}
}

// =============================================================================
// Tick + emit logic
// =============================================================================

func TestTick_NoEmitWhenAllNegative(t *testing.T) {
	dir := t.TempDir()
	// All signals will be negative: empty runner, non-existent slack stamp,
	// no mail newer than presence stamp.
	r := &fakeRunner{}
	slackStamp := filepath.Join(dir, "last-slack-inbound")
	presenceStamp := filepath.Join(dir, ".presence-last-tick")
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	active, emitted := tick(r, "sb-test123", mailDir, slackStamp, presenceStamp)
	if active {
		t.Fatalf("expected no active signals when all checks return false")
	}
	if emitted {
		t.Fatalf("expected no heartbeat emitted when all signals are negative")
	}
}

func TestTick_EmitWhenAnyPositive(t *testing.T) {
	dir := t.TempDir()
	// Signal 1: login shell present.
	r := &fakeRunner{responses: map[string][]byte{"who": []byte("sandbox pts/0\n")}}
	slackStamp := filepath.Join(dir, "last-slack-inbound")
	presenceStamp := filepath.Join(dir, ".presence-last-tick")
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	active, emitted := tick(r, "sb-test123", mailDir, slackStamp, presenceStamp)
	if !active {
		t.Fatalf("expected at least one active signal when login shell is present")
	}
	if !emitted {
		t.Fatalf("expected heartbeat emitted when a signal is active")
	}
}

func TestTick_StampAlwaysTouched(t *testing.T) {
	// The presence stamp must be touched at the end of every tick,
	// regardless of whether any signal was active.
	dir := t.TempDir()
	r := &fakeRunner{}
	slackStamp := filepath.Join(dir, "last-slack-inbound")
	presenceStamp := filepath.Join(dir, ".presence-last-tick")
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	before := time.Now().Add(-time.Second) // sentinel: stamp must not exist pre-tick

	tick(r, "sb-test123", mailDir, slackStamp, presenceStamp)

	fi, err := os.Stat(presenceStamp)
	if err != nil {
		t.Fatalf("presence stamp not created after tick: %v", err)
	}
	if !fi.ModTime().After(before) {
		t.Fatalf("presence stamp mtime %v not after before sentinel %v", fi.ModTime(), before)
	}
}

// =============================================================================
// Shared test helpers
// =============================================================================

// errExit1 simulates a process that exits with code 1 (no matches / no server).
var errExit1 = &exitError{code: 1}

type exitError struct{ code int }

func (e *exitError) Error() string { return "exit status 1" }

package main

import (
	"bytes"
	"errors"
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
	// No -t flag: list-clients without target lists clients across all sessions on
	// the default socket (see resolved open question 2 and runner.go comment).
	r := &fakeRunner{
		responses: map[string][]byte{
			"runuser -u sandbox -- tmux list-clients": []byte("/dev/pts/0: session 0\n"),
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
		errors:    map[string]error{"runuser -u sandbox -- tmux list-clients": errExit1},
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
	// Decision: pgrep -afE for ERE alternation (AL2023's pgrep defaults to BRE).
	// The -E flag is required for | alternation in the regex.
	r := &fakeRunner{
		responses: map[string][]byte{
			`pgrep -afE (^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\.sh`: []byte("1234 /usr/local/bin/claude -p do task\n"),
		},
	}
	if !checkAgentProcess(r) {
		t.Fatalf("expected positive when pgrep returns matching PIDs")
	}
}

func TestSignal_AgentProcess_NegativeEmpty(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{},
		errors:    map[string]error{`pgrep -afE (^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\.sh`: errExit1},
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

	// Intercept emitFn to detect unexpected calls.
	called := false
	orig := emitFn
	emitFn = func(_ string) error { called = true; return nil }
	defer func() { emitFn = orig }()

	active, emitted := tick(r, "sb-test123", mailDir, slackStamp, presenceStamp, t.TempDir())
	if active {
		t.Fatalf("expected no active signals when all checks return false")
	}
	if emitted {
		t.Fatalf("expected no heartbeat emitted when all signals are negative")
	}
	if called {
		t.Fatalf("expected emitFn not to be called when all signals are negative")
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

	// Intercept emitFn to verify it is called exactly once and succeeds.
	callCount := 0
	orig := emitFn
	emitFn = func(id string) error {
		callCount++
		if id != "sb-test123" {
			t.Errorf("emitFn called with unexpected sandbox ID %q", id)
		}
		return nil
	}
	defer func() { emitFn = orig }()

	active, emitted := tick(r, "sb-test123", mailDir, slackStamp, presenceStamp, t.TempDir())
	if !active {
		t.Fatalf("expected at least one active signal when login shell is present")
	}
	if !emitted {
		t.Fatalf("expected heartbeat emitted when a signal is active")
	}
	if callCount != 1 {
		t.Fatalf("expected emitFn called once, got %d", callCount)
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

	tick(r, "sb-test123", mailDir, slackStamp, presenceStamp, t.TempDir())

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

// =============================================================================
// Signal 6: VNC clients (KasmVNC viewers attached via the SSM tunnel)
// =============================================================================

// realistic `ss -tnHp state established` output captured from a live desktop
// sandbox (desk-1374e38f). The Xvnc line is the server side of an attached
// viewer; the ssm-session-wor line is the peer side of the same tunnel.
const ssWithViewer = `0      0       127.0.0.1:8444     127.0.0.1:42320 users:(("Xvnc",pid=1709,fd=38))
0      0       127.0.0.1:42320    127.0.0.1:8444  users:(("ssm-session-wor",pid=2712,fd=21))
0      0      10.0.1.111:47250   3.236.94.152:443 users:(("km-audit-log",pid=740,fd=5))
`

// captured from the same box with no viewer attached: sidecar traffic only,
// no Xvnc socket at all. This is the true idle baseline.
const ssNoViewer = `0      0      10.0.1.111:47250   3.236.94.152:443 users:(("km-audit-log",pid=740,fd=5))
0      0      10.0.1.111:33614   98.90.63.189:443 users:(("ssm-agent-worke",pid=1832,fd=18))
`

func TestSignal_VNCClients_Positive(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{
			"ss -tnHp state established": []byte(ssWithViewer),
		},
	}
	if !checkVNCClients(r) {
		t.Fatalf("expected positive when an Xvnc socket is established")
	}
}

func TestSignal_VNCClients_NegativeNoViewer(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{
			"ss -tnHp state established": []byte(ssNoViewer),
		},
	}
	if checkVNCClients(r) {
		t.Fatalf("expected negative when no Xvnc socket is established")
	}
}

// The idle path is the one that matters: a check that cannot go negative would
// silently disable idle teardown on every desktop sandbox. Empty ss output and
// a missing/erroring ss must both read as idle.
func TestSignal_VNCClients_NegativeEmpty(t *testing.T) {
	r := &fakeRunner{responses: map[string][]byte{"ss -tnHp state established": []byte("")}}
	if checkVNCClients(r) {
		t.Fatalf("expected negative when ss returns no established sockets")
	}
}

func TestSignal_VNCClients_NegativeSSMissing(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{},
		errors:    map[string]error{"ss -tnHp state established": errExit1},
	}
	if checkVNCClients(r) {
		t.Fatalf("expected negative (fail-idle) when ss is absent or exits non-zero")
	}
}

// Guard against a substring match that would fire on any process whose name or
// path merely contains the token — the match must be anchored to the ss
// users:(("<name>", …)) field.
func TestSignal_VNCClients_NegativeSimilarProcessName(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{
			"ss -tnHp state established": []byte(
				`0 0 10.0.1.111:5000 1.2.3.4:443 users:(("XvncHelper",pid=99,fd=3))` + "\n" +
					`0 0 10.0.1.111:5001 1.2.3.4:443 users:(("notXvnc",pid=98,fd=3))` + "\n",
			),
		},
	}
	if checkVNCClients(r) {
		t.Fatalf("expected negative: XvncHelper/notXvnc are not the Xvnc server process")
	}
}

// A VNC viewer alone — no shell, no tmux, no agent, no mail, no Slack — must
// keep the sandbox alive. This is the exact regression that stopped
// desk-1374e38f out from under an attached user.
func TestTick_EmitWhenOnlyVNCActive(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{
		responses: map[string][]byte{
			"ss -tnHp state established": []byte(ssWithViewer),
		},
	}
	slackStamp := filepath.Join(dir, "last-slack-inbound")
	presenceStamp := filepath.Join(dir, ".presence-last-tick")
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	called := false
	orig := emitFn
	emitFn = func(_ string) error { called = true; return nil }
	defer func() { emitFn = orig }()

	active, emitted := tick(r, "sb-test123", mailDir, slackStamp, presenceStamp, t.TempDir())
	if !active {
		t.Fatalf("expected active when only the VNC signal is positive")
	}
	if !emitted || !called {
		t.Fatalf("expected heartbeat emitted for a VNC-only session (active=%v emitted=%v called=%v)", active, emitted, called)
	}
}

// =============================================================================
// Signal 7: SSH sessions (VS Code Remote-SSH / km vscode tunnels)
// =============================================================================

// captured from desk-1374e38f (OpenSSH 9.6p1) with a non-PTY Remote-SSH-style
// exec channel open. Note the socket lists two sshd pids (privsep parent +
// session child) and `who` reported 0 for this exact state — signal 1 is blind
// to it, which is the whole reason this signal exists.
const ssWithSSHSession = `0      0       127.0.0.1:38982      127.0.0.1:22    users:(("ssm-session-wor",pid=11888,fd=19))
0      0       127.0.0.1:22         127.0.0.1:38982 users:(("sshd",pid=12017,fd=4),("sshd",pid=11896,fd=4))
0      0      10.0.1.111:55676   98.87.173.16:443 users:(("ssm-agent-worke",pid=1832,fd=19))
`

// OpenSSH 9.8 split the session process out and renamed it sshd-session; the
// listener stays "sshd". A matcher anchored only on (("sshd", would silently
// stop firing on that upgrade.
const ssWithSSHSessionModern = `0      0       127.0.0.1:22         127.0.0.1:38982 users:(("sshd-session",pid=12017,fd=4))
`

func TestSignal_SSHSessions_Positive(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{"ss -tnHp state established": []byte(ssWithSSHSession)},
	}
	if !checkSSHSessions(r) {
		t.Fatalf("expected positive when an sshd socket is established")
	}
}

func TestSignal_SSHSessions_PositiveModernOpenSSH(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{"ss -tnHp state established": []byte(ssWithSSHSessionModern)},
	}
	if !checkSSHSessions(r) {
		t.Fatalf("expected positive for OpenSSH 9.8+ sshd-session process name")
	}
}

func TestSignal_SSHSessions_NegativeNoSession(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{"ss -tnHp state established": []byte(ssNoViewer)},
	}
	if checkSSHSessions(r) {
		t.Fatalf("expected negative when no sshd socket is established")
	}
}

func TestSignal_SSHSessions_NegativeSSMissing(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{},
		errors:    map[string]error{"ss -tnHp state established": errExit1},
	}
	if checkSSHSessions(r) {
		t.Fatalf("expected negative (fail-idle) when ss is absent or exits non-zero")
	}
}

// The sshd LISTEN socket is owned by sshd too. `state established` filters it
// out at the ss layer, but guard the matcher against a bare process mention so
// the signal cannot latch on a listener and pin the box awake forever.
func TestSignal_SSHSessions_NegativeListenerMentionOnly(t *testing.T) {
	r := &fakeRunner{
		responses: map[string][]byte{
			"ss -tnHp state established": []byte(
				`0 0 10.0.1.111:5000 1.2.3.4:443 users:(("sshdwatch",pid=99,fd=3))` + "\n" +
					`0 0 10.0.1.111:5001 1.2.3.4:443 users:(("notsshd",pid=98,fd=3))` + "\n",
			),
		},
	}
	if checkSSHSessions(r) {
		t.Fatalf("expected negative: sshdwatch/notsshd are not sshd session processes")
	}
}

// Signals 6 and 7 read the same ss output but must not cross-trigger: a VNC
// viewer is not an SSH session and vice versa. Keeping them independent is what
// lets an operator tell which kind of session is holding a sandbox awake.
func TestSignals_VNCAndSSH_AreIndependent(t *testing.T) {
	vncOnly := &fakeRunner{
		responses: map[string][]byte{"ss -tnHp state established": []byte(ssWithViewer)},
	}
	if !checkVNCClients(vncOnly) {
		t.Fatalf("VNC fixture: expected checkVNCClients positive")
	}
	if checkSSHSessions(vncOnly) {
		t.Fatalf("VNC fixture: expected checkSSHSessions negative (no sshd socket)")
	}

	sshOnly := &fakeRunner{
		responses: map[string][]byte{"ss -tnHp state established": []byte(ssWithSSHSession)},
	}
	if !checkSSHSessions(sshOnly) {
		t.Fatalf("SSH fixture: expected checkSSHSessions positive")
	}
	if checkVNCClients(sshOnly) {
		t.Fatalf("SSH fixture: expected checkVNCClients negative (no Xvnc socket)")
	}
}

// A VS Code Remote-SSH session with no integrated terminal open — the exact
// state that reads who=0 — must keep the sandbox alive.
func TestTick_EmitWhenOnlySSHActive(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{
		responses: map[string][]byte{"ss -tnHp state established": []byte(ssWithSSHSession)},
	}
	slackStamp := filepath.Join(dir, "last-slack-inbound")
	presenceStamp := filepath.Join(dir, ".presence-last-tick")
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	called := false
	orig := emitFn
	emitFn = func(_ string) error { called = true; return nil }
	defer func() { emitFn = orig }()

	active, emitted := tick(r, "sb-test123", mailDir, slackStamp, presenceStamp, t.TempDir())
	if !active {
		t.Fatalf("expected active when only the SSH signal is positive")
	}
	if !emitted || !called {
		t.Fatalf("expected heartbeat for an SSH-only session (active=%v emitted=%v called=%v)", active, emitted, called)
	}
}

// =============================================================================
// Signal 8: Herdr pane busy
// =============================================================================

func herdrListCmd(sock string) string {
	return `runuser -u sandbox -- bash -lc HERDR_SOCKET_PATH="` + sock + `" herdr pane list`
}

func herdrInfoCmd(sock, pane string) string {
	return `runuser -u sandbox -- bash -lc HERDR_SOCKET_PATH="` + sock + `" herdr pane process-info --pane ` + pane
}

// TestHerdrPaneIsBusy_TrapIdleShellIsNotBusy is the single most important test in
// this task.
//
// `foreground_processes` is non-empty even when a pane sits at a bare shell — it
// contains that shell. An earlier draft of this signal checked
// `len(foreground_processes) > 0`, which is therefore ALWAYS true: signal 8 would
// have been permanently positive, silently disabling idle teardown on every box
// that ever ran herdr and leaking instances forever. That is the exact
// `pgrep vscode-server` trap the design set out to avoid.
//
// The real discriminator is foreground_process_group_id != shell_pid.
func TestHerdrPaneIsBusy_TrapIdleShellIsNotBusy(t *testing.T) {
	raw, err := os.ReadFile("testdata/herdr_process_info_idle.json")
	if err != nil {
		t.Fatalf("read idle fixture: %v", err)
	}
	if herdrPaneIsBusy(raw) {
		t.Fatal("idle pane reported BUSY — signal 8 can never go negative, which " +
			"disables idle teardown fleet-wide")
	}
	// Guard the guard: the fixture must actually contain a foreground process,
	// otherwise this test passes for the wrong reason.
	if !bytes.Contains(raw, []byte(`"foreground_processes"`)) ||
		!bytes.Contains(raw, []byte(`"/bin/sh"`)) {
		t.Fatal("idle fixture no longer contains a foreground shell; this test is " +
			"no longer exercising the trap it exists for")
	}
}

func TestHerdrPaneIsBusy_BusyPaneIsBusy(t *testing.T) {
	raw, err := os.ReadFile("testdata/herdr_process_info_busy.json")
	if err != nil {
		t.Fatalf("read busy fixture: %v", err)
	}
	if !herdrPaneIsBusy(raw) {
		t.Fatal("pane running `sleep 900` reported IDLE")
	}
}

func TestHerdrPaneIsBusy_MalformedIsIdle(t *testing.T) {
	for _, in := range [][]byte{[]byte("not json"), []byte("{}"), nil, []byte(`{"result":{}}`)} {
		if herdrPaneIsBusy(in) {
			t.Fatalf("malformed input %q reported BUSY; must fail idle", in)
		}
	}
}

// TestHerdrPaneIsBusy_ZeroPidsAreIdle covers a response where both ids are absent
// or zero — equal, but not meaningfully so. Treating 0 == 0 as "idle" is correct;
// treating it as busy would latch the signal on.
func TestHerdrPaneIsBusy_ZeroPidsAreIdle(t *testing.T) {
	raw := []byte(`{"result":{"process_info":{"foreground_process_group_id":0,"shell_pid":0}}}`)
	if herdrPaneIsBusy(raw) {
		t.Fatal("zero pids reported BUSY")
	}
}

func TestHerdrPaneIDs_ParsesLiveFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/herdr_pane_list.json")
	if err != nil {
		t.Fatalf("read pane list fixture: %v", err)
	}
	got := herdrPaneIDs(raw)
	if len(got) != 1 || got[0] != "w1:p1" {
		t.Fatalf("herdrPaneIDs = %v; want [w1:p1]", got)
	}
}

func TestHerdrPaneIDs_MalformedIsEmpty(t *testing.T) {
	if got := herdrPaneIDs([]byte("not json")); len(got) != 0 {
		t.Fatalf("malformed pane list returned %v; want empty", got)
	}
}

func TestSignal_HerdrPaneBusy_Positive(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	list, _ := os.ReadFile("testdata/herdr_pane_list.json")
	busy, _ := os.ReadFile("testdata/herdr_process_info_busy.json")
	r := &fakeRunner{responses: map[string][]byte{
		herdrListCmd(sock):          list,
		herdrInfoCmd(sock, "w1:p1"): busy,
	}}
	if !checkHerdrPaneBusy(r, dir) {
		t.Fatal("expected positive when a pane is running a foreground job")
	}
}

// TestSignal_HerdrPaneBusy_NegativeAllIdle is load-bearing: it is what keeps idle
// teardown working.
func TestSignal_HerdrPaneBusy_NegativeAllIdle(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	list, _ := os.ReadFile("testdata/herdr_pane_list.json")
	idle, _ := os.ReadFile("testdata/herdr_process_info_idle.json")
	r := &fakeRunner{responses: map[string][]byte{
		herdrListCmd(sock):          list,
		herdrInfoCmd(sock, "w1:p1"): idle,
	}}
	if checkHerdrPaneBusy(r, dir) {
		t.Fatal("expected negative when every pane sits at a bare shell — a signal " +
			"that cannot go negative disables idle teardown fleet-wide")
	}
}

func TestSignal_HerdrPaneBusy_NegativeNoSocket(t *testing.T) {
	if checkHerdrPaneBusy(&fakeRunner{}, t.TempDir()) {
		t.Fatal("expected negative when no herdr socket exists")
	}
}

// TestSignal_HerdrPaneBusy_NegativeBinaryMissing simulates herdr being absent or
// unresolvable — the runner returns an error. This is NOT hypothetical: herdr is
// installed to /home/sandbox/.local/bin and is invisible to root, which is why the
// invocation uses a login shell.
func TestSignal_HerdrPaneBusy_NegativeBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{
		responses: map[string][]byte{},
		errors:    map[string]error{herdrListCmd(sock): errors.New("exit status 127")},
	}
	if checkHerdrPaneBusy(r, dir) {
		t.Fatal("expected negative (fail idle) when herdr cannot be run")
	}
}

func TestHerdrSocketPaths_FindsDefaultAndNamed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "herdr.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// herdr also creates herdr-client.sock beside it; it must NOT be treated as an
	// API socket.
	if err := os.WriteFile(filepath.Join(dir, "herdr-client.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(dir, "sessions", "agents")
	if err := os.MkdirAll(named, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(named, "herdr.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := herdrSocketPaths(dir)
	if len(got) != 2 {
		t.Fatalf("herdrSocketPaths returned %d paths, want 2 (client socket must be excluded): %v", len(got), got)
	}
}

func TestSignal_HerdrPaneBusy_IndependentOfSSHAndVNC(t *testing.T) {
	r := &fakeRunner{responses: map[string][]byte{
		"ss -tnHp state established": []byte(`ESTAB 0 0 10.0.0.1:22 10.0.0.2:5000 users:(("sshd",pid=1,fd=3))`),
	}}
	if checkHerdrPaneBusy(r, t.TempDir()) {
		t.Fatal("signal 8 fired from signal 7's input")
	}
}

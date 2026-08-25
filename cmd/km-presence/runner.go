package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// commandRunner is the injectable subprocess seam used by all signal checks.
// Tests pass a fakeRunner; production uses realRunner.
type commandRunner interface {
	Output(name string, args ...string) ([]byte, error)
}

// realRunner executes subprocesses via os/exec.
type realRunner struct{}

// Output runs name with args and returns its combined stdout, or an error if
// the process exits non-zero.
func (realRunner) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// emitFn is the injectable emit function for tests. Tests can swap this to
// avoid writing to /run/km/audit-pipe. Production uses emit().
var emitFn = emit

// =============================================================================
// Signal checks
// =============================================================================

// checkLoginShells returns true when at least one login shell is attached to
// the sandbox (reads /var/run/utmp via `who`). Signal 1.
func checkLoginShells(r commandRunner) bool {
	out, _ := r.Output("who")
	return len(bytes.TrimSpace(out)) > 0
}

// checkTmuxClients returns true when at least one tmux client is attached to
// any tmux server for the sandbox user. Signal 2.
// No -t flag — list-clients without target lists clients across all sessions on
// default socket. Convention from internal/app/cmd/agent.go:423.
func checkTmuxClients(r commandRunner) bool {
	out, err := r.Output("runuser", "-u", "sandbox", "--", "tmux", "list-clients")
	if err != nil {
		// No tmux server == 0 clients; this is not an error condition.
		return false
	}
	return len(bytes.TrimSpace(out)) > 0
}

// checkInboundEmail returns true when a file in mailDir is newer than the
// file at stampPath (i.e., new email arrived since the last tick). Signal 3.
// If stampPath does not exist, stampMtime is zero (every existing mail file counts as newer).
// If mailDir does not exist, returns false (initial-tick safe default).
func checkInboundEmail(mailDir, stampPath string) bool {
	var stampMtime time.Time
	if stampInfo, err := os.Stat(stampPath); err == nil {
		stampMtime = stampInfo.ModTime()
	}
	// If stamp missing, stampMtime remains zero — every file is newer.

	entries, err := os.ReadDir(mailDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := os.Stat(filepath.Join(mailDir, entry.Name()))
		if err != nil {
			continue
		}
		if info.ModTime().After(stampMtime) {
			return true
		}
	}
	return false
}

// checkInboundSlack returns true when slackStampPath (touched by the
// km-slack-inbound-poller after each SQS dispatch) is newer than
// presenceStampPath (the daemon's own last-tick stamp). Signal 4.
// If slackStampPath does not exist, returns false (no Slack activity ever recorded).
// If presenceStampPath does not exist, treats its mtime as zero (first tick) so
// any existing slackStampPath is considered newer.
func checkInboundSlack(slackStampPath, presenceStampPath string) bool {
	slackInfo, err := os.Stat(slackStampPath)
	if err != nil {
		return false // no slack activity ever recorded
	}

	presenceInfo, err := os.Stat(presenceStampPath)
	if err != nil {
		// First tick: no presence stamp yet; slack stamp is newer.
		return true
	}

	return slackInfo.ModTime().After(presenceInfo.ModTime())
}

// checkAgentProcess returns true when a headless Claude / Codex / km-agent-run
// process is found via pgrep. Signal 5.
// Decision: pgrep -E for ERE alternation. AL2023's pgrep defaults to BRE and
// would not match | in the regex without -E. Single subprocess call (vs three
// separate pgrep -af invocations) keeps the loop body simple.
func checkAgentProcess(r commandRunner) bool {
	out, err := r.Output("pgrep", "-afE", `(^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\.sh`)
	if err != nil {
		// exit 1 = no matches; this is not an error condition.
		return false
	}
	return len(bytes.TrimSpace(out)) > 0
}

// checkVNCClients returns true when at least one VNC viewer is attached to the
// KasmVNC session (spec.runtime.desktop). Signal 6.
//
// KasmVNC runs as a systemd service, not a login session, so an attached viewer
// writes no utmp record and is invisible to checkLoginShells — a desktop
// sandbox with a user actively working in it otherwise reads as idle on all
// five original signals and gets reaped by the idle timer.
//
// Detection is by owning process rather than port: the profile sets
// network.websocket_port: auto, so the listener port is not fixed (it resolves
// to 8444 in practice, but that is not a contract). Xvnc holds the server side
// of every viewer socket, so an established socket owned by Xvnc means a viewer
// is attached. With no viewer, Xvnc owns no established socket at all.
//
// Runs as root (km-presence.service User=root), so ss -p can resolve process
// names for sockets owned by the sandbox user.
//
// Fails idle: a missing or erroring ss returns false. Idle detection reaping a
// live desktop is a recoverable annoyance (km resume); a signal that can never
// go negative would silently disable idle teardown on every desktop sandbox and
// leak instances indefinitely.
func checkVNCClients(r commandRunner) bool {
	out, err := r.Output("ss", "-tnHp", "state", "established")
	if err != nil {
		// ss absent or non-zero exit == no evidence of a viewer.
		return false
	}
	// Anchor on the ss users:(("<name>", field so a process merely containing
	// the token (XvncHelper, notXvnc) does not count as an attached viewer.
	return bytes.Contains(out, []byte(`(("Xvnc",`))
}

// checkSSHSessions returns true when at least one SSH session is established.
// Signal 7 — covers VS Code Remote-SSH (km vscode start) and any direct SSH
// through the SSM tunnel.
//
// Signal 1 (who / utmp) does NOT cover this. sshd writes a utmp record only for
// sessions that allocate a PTY, and VS Code Remote-SSH runs vscode-server over a
// non-PTY exec channel (pgrep shows it as "sshd: sandbox@notty"). Measured on a
// live sandbox: a non-PTY session reports who=0 while an interactive one reports
// who=1. Without this signal, editing files in VS Code is invisible and the idle
// timer reaps the box — but opening VS Code's integrated terminal allocates a PTY
// and makes it visible, so the bug presents intermittently.
//
// Detected via the live socket rather than the vscode-server process on purpose:
// VS Code deliberately leaves vscode-server running after a client disconnects so
// reconnects are fast, so pgrep'ing it would latch the sandbox awake indefinitely.
// The SSH connection itself drops on disconnect and is the honest liveness signal.
//
// Both process names are matched: OpenSSH <=9.7 owns session sockets as "sshd",
// while 9.8+ split the session process out and renamed it "sshd-session". The
// listener socket is also owned by sshd but is excluded by `state established`.
//
// Fails idle for the same reason as signal 6 (see checkVNCClients): a signal that
// can never go negative would silently disable idle teardown fleet-wide.
//
// Runs its own ss rather than sharing signal 6's output, keeping every signal
// independently testable; the extra ~4ms per 60s tick is not worth the coupling.
func checkSSHSessions(r commandRunner) bool {
	out, err := r.Output("ss", "-tnHp", "state", "established")
	if err != nil {
		return false
	}
	return bytes.Contains(out, []byte(`(("sshd",`)) ||
		bytes.Contains(out, []byte(`(("sshd-session",`))
}

// =============================================================================
// Daemon helpers
// =============================================================================

// tick runs one iteration of the presence loop. Returns (signalsActive, emitted).
// signalsActive is true when any of the seven signals is positive; emitted is true
// when a heartbeat event was written to the audit pipe.
// The presence stamp at presenceStampPath is ALWAYS touched at end of tick,
// even if no signal is active or emit fails.
func tick(r commandRunner, sandboxID, mailDir, slackStampPath, presenceStampPath string) (bool, bool) {
	s1 := checkLoginShells(r)
	s2 := checkTmuxClients(r)
	s3 := checkInboundEmail(mailDir, presenceStampPath)
	s4 := checkInboundSlack(slackStampPath, presenceStampPath)
	s5 := checkAgentProcess(r)
	s6 := checkVNCClients(r)
	s7 := checkSSHSessions(r)

	active := s1 || s2 || s3 || s4 || s5 || s6 || s7

	emitted := false
	if active {
		if err := emitFn(sandboxID); err == nil {
			emitted = true
		}
		// If emit returns an error, log (in main) but proceed to touch stamp.
	}

	// Always touch the stamp unconditionally.
	touchStamp(presenceStampPath)

	return active, emitted
}

// touchStamp updates the mtime of path (creating it if it does not exist).
// Uses os.OpenFile + os.Chtimes for portability and testability (no subprocess).
func touchStamp(path string) {
	now := time.Now()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	f.Close()
	_ = os.Chtimes(path, now, now)
}

// emit writes the heartbeat JSON to /run/km/audit-pipe via timeout-tee.
// The write is fire-and-forget: if the pipe is not being drained the timeout
// ensures the daemon does not block. Single-quote-escaping prevents shell injection.
func emit(sandboxID string) error {
	payload := fmt.Sprintf(
		`{"timestamp":"%s","sandbox_id":"%s","event_type":"heartbeat","source":"presence","detail":{}}`,
		time.Now().UTC().Format(time.RFC3339), sandboxID,
	) + "\n"

	// Single-quote-escape so the payload is safely embedded in the bash printf argument.
	escaped := strings.ReplaceAll(payload, "'", `'\''`)
	cmd := exec.Command("bash", "-c",
		fmt.Sprintf("printf '%%s' '%s' | timeout 0.1 tee /run/km/audit-pipe > /dev/null 2>/dev/null || true", escaped),
	)
	return cmd.Run()
}

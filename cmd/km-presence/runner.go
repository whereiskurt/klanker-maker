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

// =============================================================================
// Daemon helpers
// =============================================================================

// tick runs one iteration of the presence loop. Returns (signalsActive, emitted).
// signalsActive is true when any of the five signals is positive; emitted is true
// when a heartbeat event was written to the audit pipe.
// The presence stamp at presenceStampPath is ALWAYS touched at end of tick,
// even if no signal is active or emit fails.
func tick(r commandRunner, sandboxID, mailDir, slackStampPath, presenceStampPath string) (bool, bool) {
	s1 := checkLoginShells(r)
	s2 := checkTmuxClients(r)
	s3 := checkInboundEmail(mailDir, presenceStampPath)
	s4 := checkInboundSlack(slackStampPath, presenceStampPath)
	s5 := checkAgentProcess(r)

	active := s1 || s2 || s3 || s4 || s5

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

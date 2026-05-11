package main

import "os/exec"

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

// =============================================================================
// Signal checks — stubs returning false/nil; Plan 79-01 fills in the bodies.
// =============================================================================

// checkLoginShells returns true when at least one login shell is attached to
// the sandbox (reads /var/run/utmp via `who`). Signal 1.
func checkLoginShells(r commandRunner) bool {
	return false
}

// checkTmuxClients returns true when at least one tmux client is attached to
// any tmux server for the sandbox user. Signal 2.
func checkTmuxClients(r commandRunner) bool {
	return false
}

// checkInboundEmail returns true when a file in mailDir is newer than the
// file at stampPath (i.e., new email arrived since the last tick). Signal 3.
func checkInboundEmail(mailDir, stampPath string) bool {
	return false
}

// checkInboundSlack returns true when slackStampPath (touched by the
// km-slack-inbound-poller after each SQS dispatch) is newer than
// presenceStampPath (the daemon's own last-tick stamp). Signal 4.
func checkInboundSlack(slackStampPath, presenceStampPath string) bool {
	return false
}

// checkAgentProcess returns true when a headless Claude / Codex / km-agent-run
// process is found via pgrep. Signal 5.
func checkAgentProcess(r commandRunner) bool {
	return false
}

// =============================================================================
// Daemon helpers — stubs; Plan 79-01 fills in the bodies.
// =============================================================================

// tick runs one iteration of the presence loop. Returns (signalsActive, emitted).
// signalsActive is true when any of the five signals is positive; emitted is true
// when a heartbeat event was written to the audit pipe.
func tick(r commandRunner, sandboxID, mailDir, slackStampPath, presenceStampPath string) (bool, bool) {
	return false, false
}

// emit writes the heartbeat JSON to /run/km/audit-pipe via timeout-tee.
// Stubbed for tests; Plan 79-01 provides the real implementation.
func emit(sandboxID string) error {
	return nil
}

package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const exit127Start = "# --- km-exit-127 ---"
const exit127End = "# --- end km-exit-127 ---"

// runExit127 runs the Slack poller's failure arm under bash with a fake aws
// that records its argv, and returns the recorded calls.
func runExit127(t *testing.T, runExit string) []string {
	calls, _ := runExit127WithHeartbeat(t, runExit)
	return calls
}

// runExit127WithHeartbeat also starts a real background "heartbeat" (sleep 300)
// as HB_PID and reports whether it is still alive after the arm runs.
func runExit127WithHeartbeat(t *testing.T, runExit string) ([]string, bool) {
	t.Helper()
	block := slackPollerBlock(t)
	s := strings.Index(block, exit127Start)
	e := strings.Index(block, exit127End)
	if s < 0 || e < 0 || e < s {
		t.Fatal("exit-127 markers not found in the slack poller")
	}
	dir := t.TempDir()
	rec := filepath.Join(dir, "calls")
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte("#!/bin/bash\necho \"$*\" >> "+shellQuote(rec)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "set -euo pipefail\nPATH=" + shellQuote(dir) + ":$PATH\nRUN_EXIT=" + runExit + "\nQUEUE_URL=q\nRECEIPT=r\nREGION=us-east-1\n" +
		"sleep 300 &\nHB_PID=$!\nHB_ORIG=$HB_PID\n" + block[s:e] +
		"\nif kill -0 \"$HB_ORIG\" 2>/dev/null; then echo HB_ALIVE; kill \"$HB_ORIG\"; else echo HB_GONE; fi\n" +
		"echo \"HB_PID=[$HB_PID]\"\n" + trailingHeartbeatKill(t, block)
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("failure arm failed under bash: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(rec)
	var calls []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			calls = append(calls, l)
		}
	}
	if runExit == "127" && !strings.Contains(string(out), "HB_PID=[]") {
		t.Fatalf("the exit-127 arm must blank HB_PID so the later kill cannot hit a recycled pid:\n%s", out)
	}
	return calls, strings.Contains(string(out), "HB_ALIVE")
}

// trailingHeartbeatKill returns the poller's unconditional post-turn heartbeat
// kill, so the test runs it after the arm exactly as the box does.
func trailingHeartbeatKill(t *testing.T, block string) string {
	t.Helper()
	const anchor = "# Phase 119 (P119-E): kill the heartbeat ticker now that the turn is done."
	s := strings.Index(block, anchor)
	if s < 0 {
		t.Fatal("post-turn heartbeat kill not found in the slack poller")
	}
	w := strings.Index(block[s:], "wait ")
	if w < 0 {
		t.Fatal("post-turn heartbeat wait not found")
	}
	nl := strings.Index(block[s+w:], "\n")
	return block[s:s+w+nl] + "\n"
}

func TestExit127_ShortensVisibilityTo60(t *testing.T) {
	calls := runExit127(t, "127")
	if len(calls) != 1 || !strings.Contains(calls[0], "change-message-visibility") || !strings.Contains(calls[0], "--visibility-timeout 60") || !strings.Contains(calls[0], "--receipt-handle r") || !strings.Contains(calls[0], "--queue-url q") {
		t.Fatalf("exit 127 must shorten the visibility to 60 s once: %v", calls)
	}
}

func TestExitOther_LeavesVisibilityAlone(t *testing.T) {
	if calls := runExit127(t, "1"); len(calls) != 0 {
		t.Fatalf("a non-127 failure must keep today's behaviour (no visibility change): %v", calls)
	}
}

// A heartbeat tick after the 60 s set would re-extend the message, so the 127
// arm must stop the heartbeat before shortening the visibility.
func TestExit127_KillsHeartbeatFirst(t *testing.T) {
	calls, alive := runExit127WithHeartbeat(t, "127")
	if alive {
		t.Fatal("the heartbeat must be gone after the exit-127 arm runs")
	}
	if len(calls) != 1 || !strings.Contains(calls[0], "--visibility-timeout 60") {
		t.Fatalf("exactly one change-message-visibility call expected: %v", calls)
	}
}

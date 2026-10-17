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
	script := "set -euo pipefail\nPATH=" + shellQuote(dir) + ":$PATH\nRUN_EXIT=" + runExit + "\nQUEUE_URL=q\nRECEIPT=r\nREGION=us-east-1\n" + block[s:e]
	if out, err := exec.Command("bash", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("failure arm failed under bash: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(rec)
	var calls []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			calls = append(calls, l)
		}
	}
	return calls
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

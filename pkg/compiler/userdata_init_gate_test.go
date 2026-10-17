package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const initGateStart = "# --- km-init-gate ---"
const initGateEnd = "# --- end km-init-gate ---"

// runInitGate runs the Slack poller's gate snippet under real bash with the
// marker path and the bound rewritten, and returns combined output + elapsed.
func runInitGate(t *testing.T, markerDir string, bound int, agentOnPath bool) (string, time.Duration) {
	t.Helper()
	block := slackPollerBlock(t)
	s := strings.Index(block, initGateStart)
	e := strings.Index(block, initGateEnd)
	if s < 0 || e < 0 || e < s {
		t.Fatal("init gate markers not found in the slack poller")
	}
	snippet := block[s:e]
	if !strings.Contains(snippet, "/var/lib/km/init-done") || !strings.Contains(snippet, "1200") {
		t.Fatalf("gate no longer uses the shared marker path or the 1200 s bound:\n%s", snippet)
	}
	snippet = strings.ReplaceAll(snippet, "/var/lib/km/init-done", filepath.Join(markerDir, "init-done"))
	snippet = strings.ReplaceAll(snippet, "-lt 1200", "-lt "+strconv.Itoa(bound))
	snippet = strings.ReplaceAll(snippet, "sleep 5", "sleep 1")
	snippet = strings.ReplaceAll(snippet, "_km_init_wait+5", "_km_init_wait+1")
	agent := "definitely-not-on-path-xyz"
	if agentOnPath {
		agent = "true"
	}
	script := "set -euo pipefail\nAGENT=" + agent + "\n" + snippet
	start := time.Now()
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("gate failed under bash: %v\n%s", err, out)
	}
	return string(out), time.Since(start)
}

func TestInitGate_MarkerPresent_NoWait(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "init-done"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, el := runInitGate(t, dir, 10, true)
	if strings.Contains(out, "waiting for profile init") || el > 2*time.Second {
		t.Fatalf("must not wait when the marker exists: %s (%v)", out, el)
	}
}

func TestInitGate_MarkerAppears_Proceeds(t *testing.T) {
	dir := t.TempDir()
	go func() {
		time.Sleep(2 * time.Second)
		_ = os.WriteFile(filepath.Join(dir, "init-done"), nil, 0o644)
	}()
	out, el := runInitGate(t, dir, 10, true)
	if !strings.Contains(out, "waiting for profile init") || !strings.Contains(out, "profile init done after") || el > 6*time.Second {
		t.Fatalf("must wait then proceed: %s (%v)", out, el)
	}
}

func TestInitGate_Timeout_ProceedsWithWarning(t *testing.T) {
	dir := t.TempDir()
	out, el := runInitGate(t, dir, 3, false)
	if !strings.Contains(out, "marker not seen after") || el < 2*time.Second || el > 8*time.Second {
		t.Fatalf("must give up at the bound with a WARN: %s (%v)", out, el)
	}
	if !strings.Contains(out, "not on PATH") {
		t.Fatalf("the agent check must run AFTER the gate (and report a truly missing agent): %s", out)
	}
}

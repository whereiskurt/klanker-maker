package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// One reply per turn. km-slack touches /tmp/km-slack-posted.<thread_ts> after a
// successful threaded post/reply; the poller's km-slack-final-post block skips
// its own RESULT_TEXT post when that marker exists. These tests execute the
// rendered block under real bash with a fake km-slack recording its argv.

const slackFinalPostStart = "# --- km-slack-final-post ---"
const slackFinalPostEnd = "# --- end km-slack-final-post ---"

// runFinalPost runs the marker-delimited final-post block. The block's paths
// are literal (/opt/km/bin/km-slack, /tmp/km-slack-posted.$THREAD_TS) because
// they must match the Go binary's default, so the test rewrites exactly those
// two literals into a temp dir. It returns the recorded km-slack argv lines
// and the marker path.
func runFinalPost(t *testing.T, threadTS string, markerPresent bool) (calls []string, marker string) {
	t.Helper()
	block := slackPollerBlock(t)
	start := strings.Index(block, slackFinalPostStart)
	end := strings.Index(block, slackFinalPostEnd)
	if start == -1 || end == -1 || end < start {
		t.Fatalf("final-post markers not found in slack poller block")
	}
	snippet := block[start:end]
	if !strings.Contains(snippet, "/tmp/km-slack-posted.$THREAD_TS") || !strings.Contains(snippet, "/opt/km/bin/km-slack post") {
		t.Fatalf("final-post block no longer uses the shared literal paths:\n%s", snippet)
	}

	dir := t.TempDir()
	bin := filepath.Join(dir, "km-slack")
	rec := filepath.Join(dir, "calls")
	// Record argv plus the marker opt-out the poller must set on its OWN post:
	// as root it would otherwise leave a root-owned marker in sticky /tmp that the
	// sandbox user's km-slack cannot replace on a later post into this thread.
	fake := "#!/bin/bash\necho \"$* marker=${KM_SLACK_POSTED_MARKER:-unset}\" >> " + shellQuote(rec) + "\n"
	if err := os.WriteFile(bin, []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	markerDir := filepath.Join(dir, "markers")
	if err := os.Mkdir(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker = filepath.Join(markerDir, "km-slack-posted."+threadTS)
	if markerPresent {
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runDir := filepath.Join(dir, "run")
	if err := os.Mkdir(runDir, 0o755); err != nil {
		t.Fatal(err)
	}

	snippet = strings.ReplaceAll(snippet, "/opt/km/bin/km-slack", bin)
	snippet = strings.ReplaceAll(snippet, "/tmp/km-slack-posted.", markerDir+"/km-slack-posted.")
	script := "set -euo pipefail\n" +
		"THREAD_TS=" + shellQuote(threadTS) + "\n" +
		"REPLY_CHANNEL=C1\n" +
		"RESULT_TEXT='Reply sent to <@U0KURT> in the thread.'\n" +
		"RUN_DIR=" + shellQuote(runDir) + "\n" +
		snippet
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("final-post block failed under bash: %v\n%s", err, out)
	}
	if b, err := os.ReadFile(rec); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l != "" {
				calls = append(calls, l)
			}
		}
	}
	return calls, marker
}

func TestUserdata_SlackInbound_FinalPostSkippedWhenAgentAlreadyPosted(t *testing.T) {
	calls, marker := runFinalPost(t, "1700000000.000100", true)
	if len(calls) != 0 {
		t.Fatalf("agent already posted, but the poller still called km-slack: %v", calls)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker must be consumed after the skip (stat err=%v)", err)
	}
}

func TestUserdata_SlackInbound_FinalPostPostsWhenNoMarker(t *testing.T) {
	calls, _ := runFinalPost(t, "1700000000.000200", false)
	if len(calls) != 1 {
		t.Fatalf("expected exactly one km-slack post, got %d: %v", len(calls), calls)
	}
	if !strings.HasPrefix(calls[0], "post ") || !strings.Contains(calls[0], "--thread 1700000000.000200") {
		t.Fatalf("final post not threaded to the turn: %q", calls[0])
	}
	if !strings.Contains(calls[0], "marker=0") {
		t.Fatalf("the poller's own final post must run with KM_SLACK_POSTED_MARKER=0 so it leaves no root-owned marker: %q", calls[0])
	}
}

// A stale marker from an earlier turn must be cleared before the agent is
// dispatched, or it would suppress this turn's reply.
func TestUserdata_SlackInbound_StaleMarkerClearedBeforeDispatch(t *testing.T) {
	block := slackPollerBlock(t)
	clear := strings.Index(block, `rm -f "/tmp/km-slack-posted.$THREAD_TS"`)
	firstDispatch := strings.Index(block, `dispatch_as_sandbox "`)
	if clear == -1 || firstDispatch == -1 || clear > firstDispatch {
		t.Fatalf("stale-marker rm must precede the first dispatch (rm at %d, dispatch at %d)", clear, firstDispatch)
	}
	if strings.Index(block, `export KM_SLACK_THREAD_TS="$THREAD_TS"`) > clear {
		t.Fatal("stale-marker rm must run after THREAD_TS is final (the cross-agent handoff reassigns it)")
	}
}

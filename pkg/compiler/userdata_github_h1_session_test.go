package compiler

import (
	"strings"
	"testing"
)

// pollerBlock returns the template text from startMarker up to endMarker.
func pollerBlock(t *testing.T, startMarker, endMarker string) string {
	t.Helper()
	a := strings.Index(userDataTemplate, startMarker)
	if a < 0 {
		t.Fatalf("marker %q not found", startMarker)
	}
	b := strings.Index(userDataTemplate[a:], endMarker)
	if b < 0 {
		t.Fatalf("marker %q not found after %q", endMarker, startMarker)
	}
	return userDataTemplate[a : a+b]
}

// The three pollers carry one identical helper block.
func TestSessionHelpers_IdenticalInEveryPoller(t *testing.T) {
	var blocks []string
	rest := userDataTemplate
	for {
		a := strings.Index(rest, "# --- km-session-helpers ---")
		if a < 0 {
			break
		}
		b := strings.Index(rest[a:], "# --- end km-session-helpers ---")
		if b < 0 {
			t.Fatal("unterminated km-session-helpers block")
		}
		blocks = append(blocks, rest[a:a+b])
		rest = rest[a+b:]
	}
	if len(blocks) != 3 {
		t.Fatalf("want the helper block in exactly 3 pollers, found %d", len(blocks))
	}
	for i, b := range blocks[1:] {
		if b != blocks[0] {
			t.Fatalf("helper block %d drifted from the Slack poller's copy", i+2)
		}
	}
}

// Each helper block sits inside the poller it serves.
func TestSessionHelpers_PresentInGitHubAndH1Pollers(t *testing.T) {
	for name, block := range map[string]string{
		"github": pollerBlock(t, "# km-github-inbound-poller:", "chmod +x /opt/km/bin/km-github-inbound-poller"),
		"h1":     pollerBlock(t, "# km-h1-inbound-poller:", "chmod +x /opt/km/bin/km-h1-inbound-poller"),
	} {
		if !strings.Contains(block, "# --- km-session-helpers ---") {
			t.Errorf("%s poller lacks the km-session-helpers block", name)
		}
	}
}

func TestGitHubPoller_SessionViaLocalFileAndBridge(t *testing.T) {
	gh := pollerBlock(t, "# km-github-inbound-poller:", "chmod +x /opt/km/bin/km-github-inbound-poller")
	for _, want := range []string{
		`SESSION_FILE=$(km_session_file github "${REPO}_${NUMBER}")`,
		`km_session_read "$SESSION_FILE"`,
		`GITHUB_SESSION="$S_SESSION_ID"; THREAD_AGENT_TYPE="$S_AGENT_TYPE"`,
		`km_session_write "$SESSION_FILE" "" "$EFFECTIVE_AGENT" "" ""`,
		`km_session_write "$SESSION_FILE" "$NEW_GITHUB_SESSION" "$EFFECTIVE_AGENT" "" ""`,
		`km-slack session --source github --repo "$REPO" --number "$NUMBER" --session-id "$NEW_GITHUB_SESSION" --agent "$EFFECTIVE_AGENT"`,
		`km-slack session --source github --repo "$REPO" --number "$NUMBER" --clear --agent "$EFFECTIVE_AGENT"`,
	} {
		if !strings.Contains(gh, want) {
			t.Errorf("github poller lacks %q", want)
		}
	}
	if strings.Contains(gh, "aws dynamodb") {
		t.Error("github poller still calls dynamodb")
	}
	// Event-router turns carry NUMBER 0 and have no thread: the bridge would
	// answer 400, so every session record stays behind the NUMBER != 0 gate.
	for _, needle := range []string{"--session-id \"$NEW_GITHUB_SESSION\"", "--clear --agent"} {
		i := strings.Index(gh, needle)
		if i < 0 {
			continue
		}
		before := gh[:i]
		gate := strings.LastIndex(before, `[ "$NUMBER" != "0" ]`)
		if gate < 0 || strings.Count(before[gate:], "\n  fi\n") > 0 {
			t.Errorf("github %q is not inside the NUMBER != 0 gate", needle)
		}
	}
}

func TestH1Poller_SessionViaLocalFileAndBridge(t *testing.T) {
	h1 := pollerBlock(t, "# km-h1-inbound-poller:", "chmod +x /opt/km/bin/km-h1-inbound-poller")
	for _, want := range []string{
		`SESSION_FILE=$(km_session_file h1 "${REPORT_ID}_${TARGET}")`,
		`km_session_read "$SESSION_FILE"`,
		`H1_SESSION="$S_SESSION_ID"; THREAD_AGENT_TYPE="$S_AGENT_TYPE"`,
		`km_session_write "$SESSION_FILE" "" "$EFFECTIVE_AGENT" "" ""`,
		`km_session_write "$SESSION_FILE" "$NEW_H1_SESSION" "$EFFECTIVE_AGENT" "" ""`,
		`km-slack session --source h1 --report "$REPORT_ID" --target "$TARGET" --session-id "$NEW_H1_SESSION" --agent "$EFFECTIVE_AGENT"`,
		`km-slack session --source h1 --report "$REPORT_ID" --target "$TARGET" --clear --agent "$EFFECTIVE_AGENT"`,
	} {
		if !strings.Contains(h1, want) {
			t.Errorf("h1 poller lacks %q", want)
		}
	}
	if strings.Contains(h1, "aws dynamodb") {
		t.Error("h1 poller still calls dynamodb")
	}
}

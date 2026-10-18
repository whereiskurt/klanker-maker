package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
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

// The GitHub and H1 pollers' units load only /etc/km/notify.env, which never
// carries KM_SLACK_BRIDGE_URL, so each poller must resolve it from SSM before
// its first km-slack session call, or every session record fails.
func TestGitHubH1Pollers_ResolveBridgeURLBeforeSession(t *testing.T) {
	for name, block := range map[string]string{
		"github": pollerBlock(t, "# km-github-inbound-poller:", "chmod +x /opt/km/bin/km-github-inbound-poller"),
		"h1":     pollerBlock(t, "# km-h1-inbound-poller:", "chmod +x /opt/km/bin/km-h1-inbound-poller"),
	} {
		r := strings.Index(block, "# --- km-bridge-url-resolve ---")
		if r < 0 {
			t.Errorf("%s poller lacks the km-bridge-url-resolve block", name)
			continue
		}
		if !strings.Contains(block[r:], `--name "{{ .SsmPrefix }}slack/bridge-url"`) || !strings.Contains(block[r:], "export KM_SLACK_BRIDGE_URL") {
			t.Errorf("%s resolve block does not read slack/bridge-url and export it", name)
		}
		s := strings.Index(block, "km-slack session")
		if s < 0 || s < r {
			t.Errorf("%s poller: resolve block (at %d) must precede the first km-slack session (at %d)", name, r, s)
		}
	}
}

// Runs the lifted resolve block under real bash with a fake aws on PATH.
func TestBridgeURLResolveBlock_ExportsURLFromSSM(t *testing.T) {
	gh := pollerBlock(t, "# km-github-inbound-poller:", "chmod +x /opt/km/bin/km-github-inbound-poller")
	a := strings.Index(gh, "# --- km-bridge-url-resolve ---")
	if a < 0 {
		t.Fatal("resolve block not found")
	}
	b := strings.Index(gh[a:], "# --- end km-bridge-url-resolve ---")
	if b < 0 {
		t.Fatal("unterminated resolve block")
	}
	resolve := strings.ReplaceAll(gh[a:a+b], "{{ .SsmPrefix }}", "/km/")

	dir := t.TempDir()
	fakeAWS := "#!/bin/sh\necho \"$*\" >> \"" + filepath.Join(dir, "aws.log") + "\"\necho https://bridge.example/\n"
	if err := os.WriteFile(filepath.Join(dir, "aws"), []byte(fakeAWS), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "set -euo pipefail\nREGION=us-east-1\nunset KM_SLACK_BRIDGE_URL\n" + resolve + "\nbash -c 'printf %s \"$KM_SLACK_BRIDGE_URL\"'\n"
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve block failed: %v\n%s", err, out)
	}
	if string(out) != "https://bridge.example/" {
		t.Fatalf("child saw KM_SLACK_BRIDGE_URL=%q, want https://bridge.example/", out)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "aws.log"))
	if !strings.Contains(string(log), "--name /km/slack/bridge-url") {
		t.Errorf("aws not asked for /km/slack/bridge-url: %q", log)
	}

	// An already-set URL is kept and SSM is not consulted.
	cmd = exec.Command("bash", "-c", "set -euo pipefail\nREGION=us-east-1\n"+resolve+"\nprintf %s \"$KM_SLACK_BRIDGE_URL\"\n")
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "KM_SLACK_BRIDGE_URL=https://preset/")
	_ = os.Remove(filepath.Join(dir, "aws.log"))
	out, err = cmd.CombinedOutput()
	if err != nil || string(out) != "https://preset/" {
		t.Fatalf("preset URL not kept: %v %q", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "aws.log")); err == nil {
		t.Error("aws consulted although KM_SLACK_BRIDGE_URL was preset")
	}
}

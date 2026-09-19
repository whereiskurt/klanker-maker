package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// The inbound poller used to drop the SQS body's `.user` on the floor, so the
// agent never learned WHO sent a turn — "can you @ me back" was unanswerable,
// and an agent that guessed wrote "@Kurt", which Slack renders as plain text.
// The bridge already writes InboundQueueBody.User; these tests pin that the
// poller surfaces it as a `[Slack] From: <@U…>` prompt preamble and as
// KM_SLACK_SENDER_ID in every dispatched turn's environment.

const slackSenderPreambleStart = "# --- km-slack-sender-preamble ---"
const slackSenderPreambleEnd = "# --- end km-slack-sender-preamble ---"

func slackPollerBlock(t *testing.T) string {
	t.Helper()
	p := minimalSlackInboundProfile(t, true)
	out := compileInboundUserData(t, p)
	return heredocBlock(t, out, "cat > /opt/km/bin/km-slack-inbound-poller << 'SLACKINBOUND'", "SLACKINBOUND")
}

// runSenderPreamble executes the marker-delimited preamble snippet under real
// bash with the given SQS body and prompt-file contents, returning the prompt
// file afterwards. The snippet must be self-contained (it derives SENDER_ID
// from $BODY itself) so this test runs what the box runs, not a paraphrase.
func runSenderPreamble(t *testing.T, block, body, prompt string) string {
	t.Helper()
	start := strings.Index(block, slackSenderPreambleStart)
	end := strings.Index(block, slackSenderPreambleEnd)
	if start == -1 || end == -1 || end < start {
		t.Fatalf("sender preamble markers not found in slack poller block")
	}
	snippet := block[start:end]
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt")
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "set -euo pipefail\nBODY=" + shellQuote(body) + "\nPROMPT_FILE=" + shellQuote(promptFile) + "\n" + snippet
	cmd := exec.Command("bash", "-c", script)
	if outb, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("preamble snippet failed under bash: %v\n%s\n--- snippet ---\n%s", err, outb, snippet)
	}
	got, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestUserdata_SlackInbound_SenderPreambleIsPrepended(t *testing.T) {
	block := slackPollerBlock(t)
	got := runSenderPreamble(t, block, `{"channel":"C1","thread_ts":"1.2","text":"hi <@U0RON>","user":"U0KURT"}`, "hi <@U0RON>")
	if !strings.HasPrefix(got, "[Slack] From: <@U0KURT>") {
		t.Fatalf("prompt does not start with the sender preamble:\n%s", got)
	}
	if !strings.HasSuffix(got, "hi <@U0RON>") {
		t.Fatalf("original prompt text not preserved after the preamble:\n%s", got)
	}
	// The preamble must tell the agent HOW to mention — the literal token form
	// and the --mention flag — because "@Kurt" is the mistake it will otherwise make.
	for _, want := range []string{"<@U0KURT>", "--mention U0KURT"} {
		if !strings.Contains(got, want) {
			t.Errorf("preamble missing %q:\n%s", want, got)
		}
	}
}

// A body with no .user (bot-authored or malformed) must leave the prompt untouched.
func TestUserdata_SlackInbound_NoSenderLeavesPromptUnchanged(t *testing.T) {
	block := slackPollerBlock(t)
	got := runSenderPreamble(t, block, `{"channel":"C1","thread_ts":"1.2","text":"hi"}`, "hi")
	if got != "hi" {
		t.Fatalf("prompt altered without a sender: %q", got)
	}
}

// Every dispatched turn — codex resume, codex first, claude — must export
// KM_SLACK_SENDER_ID inline (root-side exports do not cross runuser). Counted
// against the dispatch sites so a fourth site added later is covered.
func TestUserdata_SlackInbound_SenderIDExportedInEveryDispatch(t *testing.T) {
	block := slackPollerBlock(t)
	dispatches := strings.Count(block, `dispatch_as_sandbox "`)
	exports := strings.Count(block, `export KM_SLACK_SENDER_ID='$SENDER_ID'`)
	if dispatches == 0 {
		t.Fatal("no dispatch_as_sandbox sites found in slack poller")
	}
	if exports != dispatches {
		t.Fatalf("KM_SLACK_SENDER_ID exported in %d of %d dispatch sites", exports, dispatches)
	}
}

// The other three pollers do not carry a Slack sender; nothing here leaks into them.
func TestUserdata_SenderPreambleIsSlackOnly(t *testing.T) {
	p := baseProfile()
	enabled := true
	p.Spec.Notification = &profile.NotificationSpec{
		Github: &profile.NotificationGitHubSpec{Inbound: &profile.NotificationGitHubInboundSpec{Enabled: &enabled}},
		H1:     &profile.NotificationH1Spec{Inbound: &profile.NotificationH1InboundSpec{Enabled: &enabled}},
	}
	out, err := generateUserData(p, "sb-sender-scope", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "KM_SLACK_SENDER_ID") || strings.Contains(out, slackSenderPreambleStart) {
		t.Fatal("sender preamble/env leaked into a profile with no Slack inbound")
	}
}

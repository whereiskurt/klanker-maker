package compiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sessionHelpersBlock lifts the helper definitions out of the rendered Slack
// poller (between the markers the template carries).
func sessionHelpersBlock(t *testing.T) string {
	t.Helper()
	p := pollerWithAgentClaude(t)
	a, b := strings.Index(p, "# --- km-session-helpers ---"), strings.Index(p, "# --- end km-session-helpers ---")
	if a < 0 || b < a {
		t.Fatal("session helper markers missing from the Slack poller")
	}
	return p[a:b]
}

func TestSessionHelpers_WriteThenReadRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	dir := t.TempDir()
	block := strings.ReplaceAll(sessionHelpersBlock(t), "/var/lib/km/sessions", dir)
	msg := filepath.Join(dir, "msg.txt")
	os.WriteFile(msg, []byte("say \"hi\"\nback\\slash"), 0o644)
	script := "set -euo pipefail\n" + block + `
F=$(km_session_file slack "C1_1700.1")
km_session_write "$F" sess-1 codex "` + msg + `" 1700.2
km_session_read "$F"
printf '%s|%s|%s|%s\n' "$S_SESSION_ID" "$S_AGENT_TYPE" "$S_LAST_EVENT" "$S_LAST_MSG"
km_session_read "$(km_session_file slack nope)"; printf 'absent=[%s|%s]\n' "$S_SESSION_ID" "$S_AGENT_TYPE"
D=$(dirname "$F"); printf 'mode=%s\n' "$(stat -c '%a' "$D" 2>/dev/null || stat -f '%Lp' "$D")"`
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("helpers failed: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "sess-1|codex|1700.2|say \"hi\"\nback\\slash") {
		t.Fatalf("round trip lost data:\n%s", s)
	}
	if !strings.Contains(s, "absent=[|]") {
		t.Fatalf("absent file must read as empty:\n%s", s)
	}
	if !strings.Contains(s, "\nmode=700\n") {
		t.Fatalf("sessions dir must be 0700:\n%s", s)
	}
}

func TestSessionHelpers_WriteIsAtomic(t *testing.T) {
	block := sessionHelpersBlock(t)
	if !strings.Contains(block, "mktemp") || !strings.Contains(block, "mv -f") {
		t.Fatalf("km_session_write must write to a temp file and mv it into place:\n%s", block)
	}
}

// The template must not reach any threads table from a box any more.
func TestUserdata_NoDynamoDBAgainstThreadsTables(t *testing.T) {
	for i, line := range strings.Split(userDataTemplate, "\n") {
		if strings.Contains(line, "THREADS_TABLE") {
			t.Errorf("line %d still names a threads table variable: %s", i+1, strings.TrimSpace(line))
		}
	}
	rest := userDataTemplate
	for {
		i := strings.Index(rest, "aws dynamodb ")
		if i < 0 {
			break
		}
		block := rest[i:]
		if end := strings.Index(block, "\n\n"); end > 0 {
			block = block[:end]
		}
		if strings.Contains(block, "threads") {
			t.Errorf("a dynamodb call still targets a threads table:\n%s", block)
		}
		rest = rest[i+len("aws dynamodb "):]
	}
}

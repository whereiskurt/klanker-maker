package cmd

import (
	"os/exec"
	"strings"
	"testing"
)

// TestFormatInitCommandLines_QuoteEscaping is a regression guard for a bug that
// silently corrupted the km-init.sh script generated for sandbox creation.
//
// Bug: the echo line was emitted as `echo '[km-init] <cmd>'`. When <cmd>
// contained a literal `'` (e.g. `su - sandbox -c 'nvm install 22'`), bash
// parsed the line as three separate tokens — `echo 'TEXT '`, then `nvm install
// 22` as a bare command, then `''`. The "echo" line silently ran the inner
// command as root. With `set -e` at the top of km-init.sh, the side-effect
// invocation often failed (no $HOME, no nvm on PATH, etc.), halting the entire
// script before the real `git clone` lines ever ran.
//
// Fix: escape `'` → `'\''` (close quote, literal quote, reopen quote).
func TestFormatInitCommandLines_QuoteEscaping(t *testing.T) {
	cases := []struct {
		name        string
		cmd         string
		wantInEcho  string
		wantSafeRun bool // verify by running through bash that no side-effects fire
	}{
		{
			name:        "plain command no quotes",
			cmd:         "yum install -y git",
			wantInEcho:  "[km-init] yum install -y git",
			wantSafeRun: true,
		},
		{
			name:        "single inner-quoted command",
			cmd:         "su - sandbox -c 'nvm install 22'",
			wantInEcho:  `[km-init] su - sandbox -c 'nvm install 22'`,
			wantSafeRun: true,
		},
		{
			name:        "inner command with quoted args",
			cmd:         `su - sandbox -c 'git config --global user.email "sandbox@klankermaker.ai"'`,
			wantInEcho:  `[km-init] su - sandbox -c 'git config --global user.email "sandbox@klankermaker.ai"'`,
			wantSafeRun: true,
		},
		{
			name:        "command ending with embedded quote dance",
			cmd:         `bash -c 'echo "hi" && touch /tmp/should-not-run'`,
			wantInEcho:  `[km-init] bash -c 'echo "hi" && touch /tmp/should-not-run'`,
			wantSafeRun: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatInitCommandLines(tc.cmd)

			// First line is the echo, second line is the cmd, then trailing newline.
			lines := strings.Split(got, "\n")
			if len(lines) < 3 {
				t.Fatalf("expected at least 3 lines (echo, cmd, trailing newline), got %d:\n%s", len(lines), got)
			}

			// The actual cmd line must be the cmd verbatim — no quoting applied to it.
			if lines[1] != tc.cmd {
				t.Errorf("cmd line not preserved verbatim:\n  want: %q\n  got:  %q", tc.cmd, lines[1])
			}

			// Behavioral check: run the echo line through bash, capture stdout,
			// confirm it equals exactly the expected `[km-init] <cmd>` string and
			// nothing else (no side-effect command got picked up).
			if !tc.wantSafeRun {
				return
			}

			// Use a sentinel file to detect side-effect execution. If the echo line
			// is broken, `touch /tmp/should-not-run` would actually execute when
			// bash interprets the line. We don't really need the sentinel for the
			// quote-correctness test — the stdout comparison catches the bug
			// directly — but it doubles as a guard for the most pernicious case.
			cmd := exec.Command("bash", "-c", lines[0])
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("running echo line via bash failed: %v\nline: %s", err, lines[0])
			}
			gotStdout := strings.TrimRight(string(out), "\n")
			if gotStdout != tc.wantInEcho {
				t.Errorf("echo line produced wrong output:\n  want: %q\n  got:  %q\n  line: %s",
					tc.wantInEcho, gotStdout, lines[0])
			}
		})
	}
}

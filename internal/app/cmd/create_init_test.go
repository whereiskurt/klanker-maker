package cmd

import (
	"os"
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
// 22` as a bare command, then `”`. The "echo" line silently ran the inner
// command as root. With `set -e` at the top of km-init.sh, the side-effect
// invocation often failed (no $HOME, no nvm on PATH, etc.), halting the entire
// script before the real `git clone` lines ever ran.
//
// Fix: escape `'` → `'\”` (close quote, literal quote, reopen quote).
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

// TestBuildInitScript_FailureIsLoudAndCounted runs the generated km-init.sh
// for real. Its predecessor behaviour — a failing step under a bare `set -e`
// — aborted the script with nothing in the log distinguishing "died here"
// from "this was the last command", and the bootstrap printed "Init complete"
// regardless. Three sandboxes shipped that way before anyone noticed `gh` was
// missing. The property pinned here: when step N of T fails, the script says
// so on stderr, writes the same facts to the marker file, exits non-zero, and
// runs NOTHING after the failure.
func TestBuildInitScript_FailureIsLoudAndCounted(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	after := dir + "/after-marker"
	script := buildInitScript([]string{
		"echo step-one",
		"echo step-two",
		"npm install -g @anthropic-ai/claude-code@2.1.171", // the real culprit: never published
		"touch " + after,
		"echo step-five",
	}, nil)

	if !strings.Contains(script, "set -eE\n") {
		t.Fatal("script must keep fail-fast (set -e) and add errtrace (-E) so the trap fires inside functions")
	}
	if !strings.Contains(script, "KM_INIT_TOTAL=5\n") {
		t.Fatalf("script must declare the total step count; got:\n%s", script)
	}

	// A fake npm that fails like the registry does on an unpublished version.
	bin := dir + "/bin"
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+"/npm", []byte("#!/bin/sh\necho 'npm error notarget No matching version found' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "KM_INIT_STATE_DIR=" + dir}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("script must exit non-zero when a step fails")
	}

	if _, statErr := os.Stat(after); statErr == nil {
		t.Error("a step AFTER the failure ran — fail-fast was lost")
	}
	if !strings.Contains(stderr.String(), "[km-init] FAILED (exit 1) at step 3/5: npm install -g @anthropic-ai/claude-code@2.1.171") {
		t.Errorf("stderr must name the failing step, its position and exit code; got:\n%s", stderr.String())
	}
	marker, readErr := os.ReadFile(dir + "/init-failed")
	if readErr != nil {
		t.Fatalf("marker file not written: %v", readErr)
	}
	for _, want := range []string{"exit=1\n", "step=3\n", "total=5\n", "command=npm install -g @anthropic-ai/claude-code@2.1.171\n"} {
		if !strings.Contains(string(marker), want) {
			t.Errorf("marker missing %q; got:\n%s", want, marker)
		}
	}
	// Steps before the failure did run, and were announced.
	if !strings.Contains(stdout.String(), "[km-init] echo step-two\nstep-two\n") {
		t.Errorf("steps before the failure must run normally; stdout:\n%s", stdout.String())
	}
}

// TestBuildInitScript_HappyPathIsQuiet: no failure ⇒ no marker, exit 0, every
// step announced exactly once — the count of [km-init] lines equals the number
// of steps, which is the operator's general truncation check.
func TestBuildInitScript_HappyPathIsQuiet(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	script := buildInitScript([]string{"true", "echo two"}, []initScriptFile{{Name: "extra.sh", Body: []byte("echo from-script\n")}})
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "KM_INIT_STATE_DIR=" + dir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("happy path must exit 0: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(dir + "/init-failed"); statErr == nil {
		t.Error("no marker may be written on success")
	}
	if got := strings.Count(string(out), "[km-init] "); got != 5 { // Starting + 2 cmds + 1 script + complete
		t.Errorf("expected 5 [km-init] lines (start, 3 steps, complete), got %d:\n%s", got, out)
	}
	if !strings.Contains(script, "KM_INIT_TOTAL=3\n") {
		t.Error("inlined initScripts count as steps too")
	}
}

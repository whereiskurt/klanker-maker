package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A killed SSM session never lets the remote program (Claude Code, vim, less)
// emit its own teardown sequences, so whatever it turned ON stays on in the
// operator's LOCAL terminal. The reported symptom — "four digits and a
// semicolon on every scroll step" — is SGR extended mouse reporting (DECSET
// 1006) still active with a tracking mode, printing e.g. "[<64;45;12M".

func TestTerminalRestoreSequence_DisablesEveryMouseReportingMode(t *testing.T) {
	got := terminalRestoreSequence()

	// Each tracking mode and each encoding must be turned off independently:
	// disabling SGR encoding (1006) while 1002 tracking stays on still leaks,
	// just in the older encoding.
	for _, mode := range []string{"1000", "1002", "1003", "1005", "1006", "1015"} {
		want := "\x1b[?" + mode + "l"
		if !strings.Contains(got, want) {
			t.Errorf("missing mouse-mode reset %q (DECRST ?%s) in %q", want, mode, got)
		}
	}
}

func TestTerminalRestoreSequence_RestoresPasteCursorAndScreenState(t *testing.T) {
	got := terminalRestoreSequence()

	cases := map[string]string{
		"\x1b[?1004l": "focus reporting (leaks [I / [O on window focus)",
		"\x1b[?2004l": "bracketed paste (leaks 200~ / 201~ around pastes)",
		"\x1b[?1049l": "alternate screen buffer",
		"\x1b[?7h":    "autowrap",
		"\x1b[?25h":   "cursor visibility",
		"\x1b[0m":     "SGR attributes",
	}
	for seq, what := range cases {
		if !strings.Contains(got, seq) {
			t.Errorf("missing reset for %s: expected %q in %q", what, seq, got)
		}
	}
}

// The user asked for a *gentle* reset. A hard reset would fix the symptom and
// destroy the operator's scrollback along with it.
func TestTerminalRestoreSequence_IsNonDestructive(t *testing.T) {
	got := terminalRestoreSequence()

	destructive := map[string]string{
		"\x1bc":   "RIS full reset",
		"\x1b[2J": "erase display",
		"\x1b[3J": "erase scrollback",
		"\x1b[H":  "cursor home",
	}
	for seq, what := range destructive {
		if strings.Contains(got, seq) {
			t.Errorf("sequence contains %s (%q) — that clears the operator's screen/scrollback", what, seq)
		}
	}
}

// Writing escape bytes into a redirected stdout would corrupt piped output
// (`km shell ... > file`, CI logs). Only a character device gets them.
func TestRestoreTerminal_SkipsWriterThatIsNotATerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdout.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	restoreTerminal(f)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("wrote %q to a regular file; expected nothing", data)
	}
}

func TestRestoreTerminal_NilWriterDoesNotPanic(t *testing.T) {
	restoreTerminal(nil) // a *exec.Cmd may carry a nil Stdout
}

// The chokepoint must clean up on the way OUT (the disconnect the operator
// actually hit) and on the way IN (so a session dirtied by an older km build,
// or by the plugin dying outside km, does not stay broken forever).
func TestRunSSMInteractiveSubprocess_RestoresTerminalAroundTheSession(t *testing.T) {
	origCheck := isInteractiveTerminal
	isInteractiveTerminal = func(io.Writer) bool { return true }
	defer func() { isInteractiveTerminal = origCheck }()

	var buf bytes.Buffer
	c := &exec.Cmd{}
	c.Stdout = &buf

	_ = runSSMInteractiveSubprocess(func(*exec.Cmd) error {
		buf.WriteString("SESSION-OUTPUT")
		return nil
	}, c)

	restore := terminalRestoreSequence()
	if n := strings.Count(buf.String(), restore); n != 2 {
		t.Fatalf("restore sequence written %d times, want 2 (before and after the session); got %q", n, buf.String())
	}
	before, after, _ := strings.Cut(buf.String(), "SESSION-OUTPUT")
	if !strings.Contains(before, restore) {
		t.Error("no restore sequence written before the session started")
	}
	if !strings.Contains(after, restore) {
		t.Error("no restore sequence written after the session exited")
	}
}

// km tunnel drops the operator into an interactive ssh shell that its own error
// text describes as expected to drop ("tunnel dropped, re-run km tunnel"), which
// is the same disconnect that latches mouse mode on in km shell. It must not go
// through runSSMInteractiveSubprocess, though: that also suppresses SIGINT for
// the session-manager-plugin, and ssh manages its own signal disposition.
func TestRunInteractiveWithTerminalRestore_RestoresAroundTheSubprocess(t *testing.T) {
	origCheck := isInteractiveTerminal
	isInteractiveTerminal = func(io.Writer) bool { return true }
	defer func() { isInteractiveTerminal = origCheck }()

	var buf bytes.Buffer
	c := &exec.Cmd{}
	c.Stdout = &buf

	_ = runInteractiveWithTerminalRestore(func(*exec.Cmd) error {
		buf.WriteString("SSH-OUTPUT")
		return nil
	}, c)

	restore := terminalRestoreSequence()
	if n := strings.Count(buf.String(), restore); n != 2 {
		t.Fatalf("restore sequence written %d times, want 2; got %q", n, buf.String())
	}
	before, after, _ := strings.Cut(buf.String(), "SSH-OUTPUT")
	if !strings.Contains(before, restore) {
		t.Error("no restore sequence written before the subprocess started")
	}
	if !strings.Contains(after, restore) {
		t.Error("no restore sequence written after the subprocess exited")
	}
}

// The subprocess's own error must survive the wrapper — km tunnel wraps it into
// the "re-run km tunnel" message the operator actually sees.
func TestRunInteractiveWithTerminalRestore_PropagatesSubprocessError(t *testing.T) {
	origCheck := isInteractiveTerminal
	isInteractiveTerminal = func(io.Writer) bool { return true }
	defer func() { isInteractiveTerminal = origCheck }()

	want := errors.New("ssh: connection closed by remote host")
	c := &exec.Cmd{}
	c.Stdout = &bytes.Buffer{}

	got := runInteractiveWithTerminalRestore(func(*exec.Cmd) error { return want }, c)
	if !errors.Is(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

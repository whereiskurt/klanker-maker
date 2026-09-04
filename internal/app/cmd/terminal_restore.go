package cmd

import (
	"io"
	"os"
	"os/exec"
	"strings"
)

// terminalRestoreSequence returns the escape bytes that put a local terminal
// back into a sane input mode after an SSM session ends.
//
// Why this is needed: interactive programs on the sandbox (Claude Code, vim,
// less, tmux) switch the operator's terminal into modes that only THEY know how
// to undo — mouse reporting, bracketed paste, the alternate screen. They undo
// them on clean exit. When SSM tears the session down underneath them (the
// 60-minute idleSessionTimeout on {prefix}-Sandbox-Session, a websocket drop, a
// laptop suspend) they never get the chance, and the modes stay latched on in
// the LOCAL terminal, which is now talking to a shell that does not understand
// them. The visible symptom is mouse-tracking reports printing as literal text:
// with SGR encoding (DECSET 1006) every scroll step emits "\x1b[<64;45;12M",
// i.e. digits and semicolons splattered across the prompt.
//
// Deliberately NOT a hard reset. RIS (ESC c) and ED-3 (ESC [ 3 J) would also fix
// it, and would throw away the operator's scrollback — usually the thing they
// reconnected to go read. Every sequence here is a targeted DECRST/DECSET of one
// mode, so a terminal that was already clean is unchanged.
func terminalRestoreSequence() string {
	var b strings.Builder

	// Mouse tracking. Each tracking mode and each wire encoding latches
	// independently, so all six are cleared: turning off SGR encoding (1006)
	// while 1002 tracking is still live just leaks in the older encoding.
	b.WriteString("\x1b[?1000l") // X11 button tracking
	b.WriteString("\x1b[?1002l") // button-event (drag) tracking
	b.WriteString("\x1b[?1003l") // any-event (motion) tracking
	b.WriteString("\x1b[?1005l") // UTF-8 extended coordinates
	b.WriteString("\x1b[?1006l") // SGR extended coordinates
	b.WriteString("\x1b[?1015l") // urxvt extended coordinates

	b.WriteString("\x1b[?1004l") // focus reporting — otherwise leaks [I / [O
	b.WriteString("\x1b[?2004l") // bracketed paste — otherwise leaks 200~ / 201~

	b.WriteString("\x1b[?1l") // DECCKM: normal (not application) cursor keys
	b.WriteString("\x1b>")    // DECKPNM: numeric (not application) keypad

	b.WriteString("\x1b[?1049l") // leave alternate screen, restoring the saved cursor
	b.WriteString("\x1b[?7h")    // autowrap back on
	b.WriteString("\x1b[?25h")   // cursor visible again (full-screen apps hide it)
	b.WriteString("\x1b[0m")     // drop any lingering colour/bold attributes

	return b.String()
}

// isInteractiveTerminal reports whether w is a character device, i.e. a real
// terminal rather than a pipe or a file. Package var so tests can force the
// decision; production never reassigns it.
var isInteractiveTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// restoreTerminal writes the restore sequence to w, but only when w is a real
// terminal. Writing escape bytes into a redirected stdout would corrupt the
// captured output of `km shell ... > file` or a CI log.
func restoreTerminal(w io.Writer) {
	if w == nil || !isInteractiveTerminal(w) {
		return
	}
	_, _ = io.WriteString(w, terminalRestoreSequence())
}

// runInteractiveWithTerminalRestore runs an interactive subprocess, clearing
// latched terminal modes before and after it.
//
// Used for the ssh sessions behind `km tunnel k8s` and `km tunnel socks`, which
// are explicitly expected to drop ("tunnel dropped, re-run km tunnel") and leave
// the same latched mouse/paste state behind as a killed SSM session.
//
// Deliberately NOT runSSMInteractiveSubprocess: that also suppresses SIGINT /
// SIGQUIT / SIGTSTP so the session-manager-plugin can forward them, whereas ssh
// manages its own signal disposition and inherits the terminal's process group.
func runInteractiveWithTerminalRestore(execFn ShellExecFunc, c *exec.Cmd) error {
	restoreTerminal(c.Stdout)
	defer restoreTerminal(c.Stdout)
	return execFn(c)
}

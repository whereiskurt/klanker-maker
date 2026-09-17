package cmd

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A port-forward carries no input. Handing session-manager-plugin our stdin
// (a tty) let it change the terminal's modes, and when the liveness watcher
// killed a hung plugin those modes were never restored — echo and canonical
// mode off, "keyboard dead", fixed only by killing the terminal. The next
// reconnect inherited the same broken stdin, so it never self-healed.
// Observed live 2026-09-17 under Ghostty with `km herdr start`.
//
// Pin that the forward's stdin is NOT the process's stdin. exec.Cmd with a
// nil Stdin gives the child /dev/null, which can never touch a tty.
func TestRunReconnectingPortForward_DoesNotHandThePluginStdin(t *testing.T) {
	var got []*os.File
	execFn := func(c *exec.Cmd) error {
		if f, ok := c.Stdin.(*os.File); ok {
			got = append(got, f)
		} else if c.Stdin != nil {
			t.Errorf("stdin is %T, want nil", c.Stdin)
		}
		return nil
	}
	build := func(ctx context.Context) *exec.Cmd { return exec.CommandContext(ctx, "true") }
	if err := runReconnectingPortForward(context.Background(), execFn, build, nil, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f == os.Stdin {
			t.Fatal("the port-forward was handed os.Stdin; a port-forward needs no input and must not be able to change tty modes")
		}
	}
}

// Belt to the braces above: even if a child does leave the terminal in raw
// mode, the forward loop must put it back before it returns or reconnects.
// Driven through a real pty so term.Restore has something to restore.
func TestRunReconnectingPortForward_RestoresTerminalModes(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("no `script` to allocate a pty")
	}
	// Re-exec this test binary under a pty; the inner run does the real work.
	if os.Getenv("KM_PTY_INNER") != "1" {
		cmd := exec.Command("script", "-q", "/dev/null", os.Args[0], "-test.run", "^TestRunReconnectingPortForward_RestoresTerminalModes$", "-test.v")
		cmd.Env = append(os.Environ(), "KM_PTY_INNER=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("inner run failed: %v\n%s", err, out)
		}
		if !containsBytes(out, []byte("--- PASS")) {
			t.Fatalf("inner run did not pass:\n%s", out)
		}
		return
	}

	before := sttyState(t)
	// A "plugin" that puts the terminal into raw mode and exits without
	// restoring it — the shape a killed session-manager-plugin leaves behind.
	build := func(ctx context.Context) *exec.Cmd {
		c := exec.CommandContext(ctx, "sh", "-c", "stty raw -echo < /dev/tty")
		return c
	}
	execFn := func(c *exec.Cmd) error { return c.Run() }
	if err := runReconnectingPortForward(context.Background(), execFn, build, nil, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	after := sttyState(t)
	// The keyboard-affecting modes are what matter: echo, canonical input,
	// signal chars. The full stty -g string is not stable across a
	// tcsetattr round-trip on macOS (one platform-private lflag bit flips),
	// so compare the modes a human would notice, not the opaque blob.
	for _, mode := range []string{"echo", "icanon", "isig"} {
		if !hasMode(t, mode) {
			t.Fatalf("terminal mode %q not restored after the forward exited\nbefore: %s\nafter:  %s", mode, before, after)
		}
	}
}

// hasMode reports whether the controlling tty currently has mode set
// (i.e. `stty -a` shows "echo", not "-echo").
func hasMode(t *testing.T, mode string) bool {
	t.Helper()
	c := exec.Command("stty", "-a")
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no controlling tty: %v", err)
	}
	defer tty.Close()
	c.Stdin = tty
	out, err := c.Output()
	if err != nil {
		t.Fatalf("stty -a: %v", err)
	}
	// stty -a lists whole-word flags separated by spaces/semicolons: "-echo"
	// is off, "echo" is on. Match whole words so "-echoprt" cannot be read as
	// "-echo".
	for _, w := range strings.FieldsFunc(string(out), func(r rune) bool { return r == ' ' || r == ';' || r == '\n' || r == '\t' }) {
		switch w {
		case mode:
			return true
		case "-" + mode:
			return false
		}
	}
	t.Fatalf("stty -a did not mention %q:\n%s", mode, out)
	return false
}

func sttyState(t *testing.T) string {
	t.Helper()
	c := exec.Command("stty", "-g")
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no controlling tty: %v", err)
	}
	defer tty.Close()
	c.Stdin = tty
	out, err := c.Output()
	if err != nil {
		t.Fatalf("stty -g: %v", err)
	}
	return string(out)
}

func containsBytes(b, sub []byte) bool {
	return len(sub) == 0 || (len(b) >= len(sub) && indexBytes(b, sub) >= 0)
}

func indexBytes(b, sub []byte) int {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == string(sub) {
			return i
		}
	}
	return -1
}

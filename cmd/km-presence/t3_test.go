package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// withProcRoot points procRoot at a temp dir holding <pid>/cgroup files.
func withProcRoot(t *testing.T, cgroups map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for pid, cg := range cgroups {
		if err := os.MkdirAll(filepath.Join(dir, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, pid, "cgroup"), []byte(cg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	orig := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = orig })
}

const agentPgrepKey = `pgrep -af (^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\.sh`

// TestSignal5_ExcludesT3UnitChildren pins the fix for t3's long-lived provider
// children: t3 keeps a claude child alive >=30 min after a turn, so counting it
// by name would hold the box awake long after the work ended. Signal 9 owns it.
func TestSignal5_ExcludesT3UnitChildren(t *testing.T) {
	withProcRoot(t, map[string]string{
		"101": "0::/system.slice/km-t3code.service\n",
	})
	r := &fakeRunner{responses: map[string][]byte{
		agentPgrepKey: []byte("101 /usr/bin/claude --output-format stream-json\n"),
	}}
	if checkAgentProcess(r) {
		t.Fatal("signal 5 counted a claude inside km-t3code.service; signal 9 must own t3's agents")
	}
}

func TestSignal5_CountsAgentsOutsideT3Unit(t *testing.T) {
	withProcRoot(t, map[string]string{
		"101": "0::/system.slice/km-t3code.service\n",
		"202": "0::/user.slice/user-1001.slice/session-3.scope\n",
	})
	r := &fakeRunner{responses: map[string][]byte{
		agentPgrepKey: []byte("101 /usr/bin/claude --output-format stream-json\n202 claude -p hi\n"),
	}}
	if !checkAgentProcess(r) {
		t.Fatal("signal 5 dropped a claude outside km-t3code.service")
	}
}

// TestSignal5_UnreadableCgroupStillCounts: an unknown process keeps today's
// behaviour. Excluding on a read error would let a vanished /proc entry or a
// permission quirk silently turn signal 5 off.
func TestSignal5_UnreadableCgroupStillCounts(t *testing.T) {
	withProcRoot(t, nil)
	r := &fakeRunner{responses: map[string][]byte{
		agentPgrepKey: []byte("303 codex exec hi\n"),
	}}
	if !checkAgentProcess(r) {
		t.Fatal("signal 5 dropped an agent whose cgroup could not be read")
	}
}

func TestSignal5_NoMatchesStillIdle(t *testing.T) {
	withProcRoot(t, nil)
	r := &fakeRunner{errors: map[string]error{agentPgrepKey: errors.New("exit status 1")}}
	if checkAgentProcess(r) {
		t.Fatal("pgrep no-match must stay idle")
	}
}

// =============================================================================
// Signal 9: t3 in use
// =============================================================================

const t3SSKey = "ss -tnH state established ( sport = :3773 )"

const (
	t3IdleShell = `{"schemaVersion":2,"threads":[{"id":"t1","status":"completed","activeRunId":null,"pendingBackgroundTasks":[]}],"archivedThreads":[],"projects":[]}`
	t3BusyShell = `{"schemaVersion":2,"threads":[{"id":"t1","status":"running","activeRunId":"r1","pendingBackgroundTasks":[]}]}`
)

func writeToken(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "presence.token")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestT3ThreadsBusy(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"no threads", `{"threads":[]}`, false},
		// The load-bearing negative: t3 keeps provider sessions (and their
		// claude/codex children) alive long after a turn. An idle thread must
		// read idle no matter what processes exist.
		{"idle long-lived session", `{"threads":[{"activeRunId":null,"pendingBackgroundTasks":[]}]}`, false},
		{"active run", `{"threads":[{"activeRunId":"r1","pendingBackgroundTasks":[]}]}`, true},
		{"background task pending", `{"threads":[{"activeRunId":null,"pendingBackgroundTasks":[{"id":"b1"}]}]}`, true},
		{"archived thread ignored", `{"threads":[],"archivedThreads":[{"activeRunId":"r9"}]}`, false},
		{"malformed", `{not json`, false},
		{"empty", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := t3ThreadsBusy([]byte(c.raw)); got != c.want {
				t.Fatalf("t3ThreadsBusy(%s) = %v; want %v", c.raw, got, c.want)
			}
		})
	}
}

// TestSignal9_ClientSocketWins: a browser attached through the SSM forward is
// enough on its own, and the HTTP probe must not run — its own connection is
// exactly the kind of socket this check counts.
func TestSignal9_ClientSocketWins(t *testing.T) {
	r := &fakeRunner{responses: map[string][]byte{
		t3SSKey: []byte("0 0 127.0.0.1:3773 127.0.0.1:44308\n"),
	}}
	get := func(url, token string) ([]byte, int, error) {
		t.Fatal("HTTP probe ran although a client socket was already found")
		return nil, 0, nil
	}
	if !checkT3InUse(r, get, writeToken(t, "tok\n")) {
		t.Fatal("an established client socket on :3773 must count as in use")
	}
}

func TestSignal9_BusyTurnNoClient(t *testing.T) {
	r := &fakeRunner{}
	get := func(url, token string) ([]byte, int, error) {
		if url != "http://127.0.0.1:3773/api/orchestration/shell" {
			t.Errorf("url = %q", url)
		}
		if token != "tok" {
			t.Errorf("token = %q; want trimmed %q", token, "tok")
		}
		return []byte(t3BusyShell), 200, nil
	}
	if !checkT3InUse(r, get, writeToken(t, "tok\n")) {
		t.Fatal("a running turn with no client attached must count as in use")
	}
}

func TestSignal9_IdleServerIsIdle(t *testing.T) {
	r := &fakeRunner{}
	get := func(string, string) ([]byte, int, error) { return []byte(t3IdleShell), 200, nil }
	if checkT3InUse(r, get, writeToken(t, "tok")) {
		t.Fatal("a started server with no client and no turn must read idle")
	}
}

func TestSignal9_FailsIdle(t *testing.T) {
	busy := func(string, string) ([]byte, int, error) { return []byte(t3BusyShell), 200, nil }
	cases := []struct {
		name  string
		r     *fakeRunner
		get   t3Getter
		token string // "" = no file
	}{
		{"token absent", &fakeRunner{}, busy, ""},
		{"token empty", &fakeRunner{}, busy, " \n"},
		{"server down", &fakeRunner{}, func(string, string) ([]byte, int, error) {
			return nil, 0, errors.New("connect: connection refused")
		}, "tok"},
		{"unauthorized", &fakeRunner{}, func(string, string) ([]byte, int, error) {
			return []byte(t3BusyShell), 401, nil
		}, "tok"},
		{"ss error", &fakeRunner{errors: map[string]error{t3SSKey: errors.New("exit 1")}}, func(string, string) ([]byte, int, error) {
			return []byte(t3IdleShell), 200, nil
		}, "tok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "absent")
			if c.token != "" {
				path = writeToken(t, c.token)
			}
			if checkT3InUse(c.r, c.get, path) {
				t.Fatal("signal 9 must fail idle")
			}
		})
	}
}

func TestTick_EmitWhenOnlyT3Active(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{responses: map[string][]byte{
		t3SSKey: []byte("0 0 127.0.0.1:3773 127.0.0.1:44308\n"),
	}}
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatal(err)
	}
	called := false
	orig := emitFn
	emitFn = func(_ string) error { called = true; return nil }
	defer func() { emitFn = orig }()

	active, emitted := tick(r, "sb-test123", mailDir, filepath.Join(dir, "slack"), filepath.Join(dir, "pres"), t.TempDir())
	if !active || !emitted || !called {
		t.Fatalf("expected a heartbeat when only signal 9 is positive (active=%v emitted=%v called=%v)", active, emitted, called)
	}
}

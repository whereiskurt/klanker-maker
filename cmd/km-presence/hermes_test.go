package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps every tick() in this package off the real network. Without
// it the production getter would probe 127.0.0.1:8787 on the machine running
// the tests — where a local Hermes WebUI may well be listening — and an
// "all signals negative" test would turn flaky on that machine only.
func TestMain(m *testing.M) {
	hermesGetFn = func(string) ([]byte, int, error) {
		return nil, 0, errors.New("hermes probe disabled in tests")
	}
	os.Exit(m.Run())
}

// =============================================================================
// Signal 5: km-hermes.service exclusion
// =============================================================================

// TestSignal5_ExcludesHermesUnitChildren: Hermes can spawn claude/codex as
// subagents from inside its own unit, and those must not latch the box awake
// by name. Signal 10 owns Hermes's work.
func TestSignal5_ExcludesHermesUnitChildren(t *testing.T) {
	withProcRoot(t, map[string]string{
		"111": "0::/system.slice/km-hermes.service\n",
	})
	r := &fakeRunner{responses: map[string][]byte{
		agentPgrepKey: []byte("111 /usr/bin/claude -p subagent\n"),
	}}
	if checkAgentProcess(r) {
		t.Fatal("signal 5 counted a claude inside km-hermes.service; signal 10 must own Hermes's agents")
	}
}

func TestSignal5_ExcludesBothUnitsCountsOthers(t *testing.T) {
	withProcRoot(t, map[string]string{
		"101": "0::/system.slice/km-t3code.service\n",
		"111": "0::/system.slice/km-hermes.service\n",
		"222": "0::/system.slice/km-slack-inbound-poller.service\n",
	})
	excluded := &fakeRunner{responses: map[string][]byte{
		agentPgrepKey: []byte("101 /usr/bin/claude\n111 codex exec hi\n"),
	}}
	if checkAgentProcess(excluded) {
		t.Fatal("signal 5 counted an agent inside one of the excluded units")
	}
	mixed := &fakeRunner{responses: map[string][]byte{
		agentPgrepKey: []byte("101 /usr/bin/claude\n111 codex exec hi\n222 claude -p turn\n"),
	}}
	if !checkAgentProcess(mixed) {
		t.Fatal("signal 5 dropped an agent outside both excluded units")
	}
}

// =============================================================================
// Signal 10: Hermes in use
// =============================================================================

const (
	hermesSSKey     = "ss -tnH state established ( sport = :8787 )"
	hermesHealthURL = "http://127.0.0.1:8787/health"
)

func hermesBody(body string, status int) httpGetter {
	return func(string) ([]byte, int, error) { return []byte(body), status, nil }
}

// TestSignal10_ClientSocketWins: a browser tab through the SSM forward holds an
// established socket on :8787, which is enough on its own. The HTTP probe must
// not run — its own connection is exactly the kind of socket this counts.
func TestSignal10_ClientSocketWins(t *testing.T) {
	r := &fakeRunner{responses: map[string][]byte{
		hermesSSKey: []byte("0 0 127.0.0.1:8787 127.0.0.1:51022\n"),
	}}
	get := func(string) ([]byte, int, error) {
		t.Fatal("HTTP probe ran although a client socket was already found")
		return nil, 0, nil
	}
	if !checkHermesInUse(r, get) {
		t.Fatal("an established client socket on :8787 must count as in use")
	}
}

func TestSignal10_BusyNoClient(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"active run", `{"status":"ok","active_runs":1,"active_streams":0}`, 200},
		{"active stream", `{"status":"ok","active_runs":0,"active_streams":2}`, 200},
		// Degraded but busy is still busy: a 503 whose body reports work in
		// progress must not let the box be reaped mid-run.
		{"503 with busy body", `{"status":"degraded","active_runs":1,"active_streams":1}`, 503},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &fakeRunner{}
			get := func(url string) ([]byte, int, error) {
				if url != hermesHealthURL {
					t.Errorf("url = %q; want %q", url, hermesHealthURL)
				}
				return []byte(c.body), c.status, nil
			}
			if !checkHermesInUse(r, get) {
				t.Fatalf("%s with no client attached must count as in use", c.name)
			}
		})
	}
}

// TestSignal10_IdleServerIsIdle is the load-bearing negative: a started server
// nobody is using must let the box idle out. A signal that reads "server is up"
// as "in use" would latch every box that ever ran `km hermes start` awake.
func TestSignal10_IdleServerIsIdle(t *testing.T) {
	r := &fakeRunner{}
	if checkHermesInUse(r, hermesBody(`{"status":"ok","active_runs":0,"active_streams":0}`, 200)) {
		t.Fatal("a started server with no client, no run and no stream must read idle")
	}
	if checkHermesInUse(r, hermesBody(`{"status":"ok"}`, 200)) {
		t.Fatal("a health body without the counters must read idle")
	}
}

func TestSignal10_FailsIdle(t *testing.T) {
	cases := []struct {
		name string
		r    *fakeRunner
		get  httpGetter
	}{
		{"server down", &fakeRunner{}, func(string) ([]byte, int, error) {
			return nil, 0, errors.New("connect: connection refused")
		}},
		{"timeout", &fakeRunner{}, func(string) ([]byte, int, error) {
			return nil, 0, errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)")
		}},
		{"non-JSON body", &fakeRunner{}, hermesBody(`<html>Bad Gateway</html>`, 502)},
		{"empty body", &fakeRunner{}, hermesBody(``, 200)},
		{"ss missing", &fakeRunner{errors: map[string]error{
			hermesSSKey: errors.New(`exec: "ss": executable file not found in $PATH`),
		}}, hermesBody(`{"active_runs":0,"active_streams":0}`, 200)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if checkHermesInUse(c.r, c.get) {
				t.Fatal("signal 10 must fail idle")
			}
		})
	}
}

// TestSignal10_SSMissingStillProbes: a failed ss must not hide a running turn —
// the HTTP half is independent of the socket half.
func TestSignal10_SSMissingStillProbes(t *testing.T) {
	r := &fakeRunner{errors: map[string]error{hermesSSKey: errors.New("exit 1")}}
	if !checkHermesInUse(r, hermesBody(`{"active_runs":1,"active_streams":0}`, 200)) {
		t.Fatal("a run in progress must count even when ss fails")
	}
}

func TestTick_EmitWhenOnlyHermesActive(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{}
	mailDir := filepath.Join(dir, "new")
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origGet := hermesGetFn
	hermesGetFn = hermesBody(`{"active_runs":1,"active_streams":0}`, 200)
	defer func() { hermesGetFn = origGet }()

	called := false
	orig := emitFn
	emitFn = func(_ string) error { called = true; return nil }
	defer func() { emitFn = orig }()

	active, emitted := tick(r, "sb-test123", mailDir, filepath.Join(dir, "slack"), filepath.Join(dir, "pres"), t.TempDir())
	if !active || !emitted || !called {
		t.Fatalf("expected a heartbeat when only signal 10 is positive (active=%v emitted=%v called=%v)", active, emitted, called)
	}
}

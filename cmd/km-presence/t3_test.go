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

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// t3Port is the loopback port the km-owned T3 Code server listens on. km sets
// it in the unit `km t3 start` writes, so matching on it is a contract, not an
// observation. Pinned equal to internal/app/cmd's t3RemotePort by
// TestT3PortPinnedAcrossSites.
const t3Port = 3773

// t3TokenPath holds the read-only (orchestration:read) bearer token
// `km t3 start` mints for this probe. Root-only; absent on boxes where t3 was
// never started, which reads as idle.
const t3TokenPath = "/etc/km/t3/presence.token"

// t3UnitCgroup is the cgroup path fragment of the km-owned T3 Code server unit
// that `km t3 start` writes. t3's agent children stay inside it because the
// unit unsets XDG_RUNTIME_DIR, which stops t3 from wrapping them in
// `systemd-run --user` scopes.
const t3UnitCgroup = "/km-t3code.service"

// procRoot is the procfs mount, swapped by tests.
var procRoot = "/proc"

// inT3Unit reports whether pid runs inside km-t3code.service. A read error
// reports false, so an unknown process keeps signal 5's existing behaviour
// rather than silently dropping out of it.
func inT3Unit(pid string) bool {
	b, err := os.ReadFile(filepath.Join(procRoot, pid, "cgroup"))
	if err != nil {
		return false
	}
	return strings.Contains(string(b), t3UnitCgroup)
}

// t3Getter performs the authenticated GET against the t3 server and returns
// the body and HTTP status. Injectable for tests.
type t3Getter func(url, token string) ([]byte, int, error)

// t3GetFn is the production getter, swapped by tests that drive tick().
var t3GetFn t3Getter = t3HTTPGet

// t3HTTPGet is short and keep-alive-free on purpose: a pooled idle connection
// to :3773 would still be ESTABLISHED on the next tick and read as a client.
func t3HTTPGet(url, token string) ([]byte, int, error) {
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-t3-orchestration-protocol", "2")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return body, resp.StatusCode, err
}

// t3Shell is the slice of GET /api/orchestration/shell this signal reads.
// archivedThreads is deliberately not decoded.
type t3Shell struct {
	Threads []struct {
		ActiveRunID            *string           `json:"activeRunId"`
		PendingBackgroundTasks []json.RawMessage `json:"pendingBackgroundTasks"`
	} `json:"threads"`
}

// t3ThreadsBusy reports whether any live thread has a run in progress or
// background work (subagents) pending. Malformed input reads as idle.
func t3ThreadsBusy(raw []byte) bool {
	var s t3Shell
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	for _, th := range s.Threads {
		if (th.ActiveRunID != nil && *th.ActiveRunID != "") || len(th.PendingBackgroundTasks) > 0 {
			return true
		}
	}
	return false
}

// checkT3InUse returns true when a T3 Code client is connected or an agent turn
// is running. Signal 9.
//
// Detects the USE, never the server: a started server nobody is using must let
// the box idle out, which is the whole point of `km t3 stop` being optional.
// A server merely existing is the `pgrep vscode-server` trap.
//
//  1. Client: an established socket whose local port is 3773. Matched by port,
//     not process name — the server's comm is "node-MainThread", not "t3"
//     (measured live). An idle SSM forward holds no socket here; an attached
//     browser holds exactly one (both measured live).
//  2. Turn: GET /api/orchestration/shell with the presence token, busy iff a
//     thread has an activeRunId or pending background tasks. Process presence
//     is useless here: t3 keeps claude/codex children alive >=30 min after a
//     turn, which is also why signal 5 excludes them (checkAgentProcess).
//
// The socket check runs first and short-circuits, so the probe's own
// connection is never counted. Fails idle on every error path.
func checkT3InUse(r commandRunner, get t3Getter, tokenPath string) bool {
	out, err := r.Output("ss", "-tnH", "state", "established", "(", "sport", "=", ":"+strconv.Itoa(t3Port), ")")
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return true
	}

	b, err := os.ReadFile(tokenPath)
	if err != nil {
		return false
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return false
	}
	body, status, err := get(fmt.Sprintf("http://127.0.0.1:%d/api/orchestration/shell", t3Port), token)
	if err != nil || status != http.StatusOK {
		return false
	}
	return t3ThreadsBusy(body)
}

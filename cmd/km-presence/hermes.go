package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// hermesPort is the loopback port the km-owned Hermes WebUI listens on. km sets
// it in the unit `km hermes start` writes, so matching on it is a contract, not
// an observation. It must equal internal/app/cmd/hermes.go's hermesRemotePort;
// a cross-site pin test there keeps the two in step.
const hermesPort = 8787

// hermesUnitCgroup is the cgroup path fragment of the km-owned Hermes unit.
const hermesUnitCgroup = "/km-hermes.service"

// httpGetter performs an unauthenticated GET and returns the body and HTTP
// status. Injectable for tests.
type httpGetter func(url string) ([]byte, int, error)

// hermesGetFn is the production getter, swapped by tests that drive tick().
var hermesGetFn httpGetter = plainHTTPGet

// plainHTTPGet is short and keep-alive-free for the same reason as t3HTTPGet: a
// pooled idle connection to :8787 would still be ESTABLISHED on the next tick
// and read as a client.
func plainHTTPGet(url string) ([]byte, int, error) {
	client := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}

// hermesHealth is the slice of GET /health this signal reads.
type hermesHealth struct {
	ActiveRuns    int `json:"active_runs"`
	ActiveStreams int `json:"active_streams"`
}

// checkHermesInUse returns true when a Hermes WebUI client is connected or a
// run/stream is in progress. Signal 10.
//
// Detects the USE, never the server, exactly as signal 9 does for t3: a started
// server nobody is using must let the box idle out.
//
//  1. Client: an established socket whose local port is 8787 — an open browser
//     tab through the SSM forward. Short-circuits, so the probe's own
//     connection is never counted.
//  2. Work: GET /health, busy iff active_runs > 0 || active_streams > 0. The
//     body is parsed whatever the status, because the server answers 503 when
//     degraded and a degraded server mid-run is still mid-run.
//
// Fails idle on every error path. Not covered, by design: Hermes cron jobs and
// the messaging gateway do not hold the box awake.
func checkHermesInUse(r commandRunner, get httpGetter) bool {
	out, err := r.Output("ss", "-tnH", "state", "established", "(", "sport", "=", ":"+strconv.Itoa(hermesPort), ")")
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return true
	}

	body, _, err := get(fmt.Sprintf("http://127.0.0.1:%d/health", hermesPort))
	if err != nil {
		return false
	}
	var h hermesHealth
	if err := json.Unmarshal(body, &h); err != nil {
		return false
	}
	return h.ActiveRuns > 0 || h.ActiveStreams > 0
}

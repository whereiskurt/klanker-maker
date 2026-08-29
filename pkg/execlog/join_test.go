package execlog_test

import (
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func at(min int) time.Time {
	return time.Date(2026, 8, 29, 12, min, 0, 0, time.UTC)
}

func execAt(min, pid int, comm string, args ...string) execlog.Record {
	return execlog.Record{TS: at(min), Kind: execlog.KindExec, PID: pid, Comm: comm, Args: args}
}

func exitAt(min, pid int) execlog.Record {
	return execlog.Record{TS: at(min), Kind: execlog.KindExit, PID: pid}
}

func TestIndexAt_MatchesTheExecLiveAtThatMoment(t *testing.T) {
	ix := execlog.NewIndex([]execlog.Record{execAt(0, 100, "curl", "curl", "https://x")})
	got, ok := ix.At(100, at(5))
	if !ok {
		t.Fatal("want a match for a pid that had exec'd and not exited")
	}
	if got.Comm != "curl" {
		t.Errorf("want curl, got %q", got.Comm)
	}
}

func TestIndexAt_PIDReuseDoesNotMisattribute(t *testing.T) {
	// This is the case the whole design of the exit tracepoint exists for.
	// pid 100 runs curl, exits, and the number is reused by python. A flow at
	// minute 30 belongs to python and must NEVER be reported as curl.
	ix := execlog.NewIndex([]execlog.Record{
		execAt(0, 100, "curl", "curl", "https://first"),
		exitAt(10, 100),
		execAt(20, 100, "python3", "python3", "fetch.py"),
	})
	got, ok := ix.At(100, at(30))
	if !ok {
		t.Fatal("want a match against the second exec")
	}
	if got.Comm != "python3" {
		t.Fatalf("pid reuse misattributed the flow to %q", got.Comm)
	}
}

func TestIndexAt_NoMatchAfterExitOrBeforeExec(t *testing.T) {
	ix := execlog.NewIndex([]execlog.Record{
		execAt(10, 100, "curl", "curl"),
		exitAt(20, 100),
	})
	// The process was dead by minute 30 and nothing re-used the pid. Reporting
	// curl here would be a confident lie.
	if _, ok := ix.At(100, at(30)); ok {
		t.Error("must not attribute a flow to a process that had already exited")
	}
	// Nothing had exec'd on that pid yet at minute 5.
	if _, ok := ix.At(100, at(5)); ok {
		t.Error("must not attribute a flow to an exec that had not happened yet")
	}
	// A pid never seen at all.
	if _, ok := ix.At(999, at(15)); ok {
		t.Error("must not invent a match for an unknown pid")
	}
}

func TestIndexAt_LatestExecWinsForReExec(t *testing.T) {
	// A shell that exec's a binary keeps its pid. The later exec is the truth.
	ix := execlog.NewIndex([]execlog.Record{
		execAt(0, 100, "bash", "bash", "-c", "curl https://x"),
		execAt(1, 100, "curl", "curl", "https://x"),
	})
	got, _ := ix.At(100, at(2))
	if got.Comm != "curl" {
		t.Errorf("want the most recent exec, got %q", got.Comm)
	}
}

func TestWho_AttributesMatchingFlows(t *testing.T) {
	execs := []execlog.Record{execAt(0, 100, "curl", "curl", "https://api.github.com")}
	flows := []flowlog.Record{
		{TS: at(5), Src: flowlog.SrcEBPF, Verdict: flowlog.VerdictAllow, Host: "api.github.com", PID: 100},
		{TS: at(6), Src: flowlog.SrcEBPF, Verdict: flowlog.VerdictAllow, Host: "example.com", PID: 100},
	}
	got := execlog.Who(execs, flows, "api.github.com")
	if len(got) != 1 {
		t.Fatalf("want 1 attribution, got %d", len(got))
	}
	if !got[0].Found || got[0].Exec.Comm != "curl" {
		t.Errorf("want the curl exec, got %+v", got[0])
	}
}

func TestWho_FlowWithoutPIDIsReportedUnattributed(t *testing.T) {
	// Under proxy enforcement the DNS and HTTP proxies record no pid. The flow
	// still happened and must still be listed — silently dropping it would make
	// a real destination look like it was never reached.
	execs := []execlog.Record{execAt(0, 100, "curl", "curl")}
	flows := []flowlog.Record{
		{TS: at(5), Src: flowlog.SrcHTTP, Verdict: flowlog.VerdictAllow, Host: "api.github.com"},
	}
	got := execlog.Who(execs, flows, "api.github.com")
	if len(got) != 1 {
		t.Fatalf("want the flow listed anyway, got %d", len(got))
	}
	if got[0].Found {
		t.Error("a flow with no pid must not be attributed to anything")
	}
}

func TestHostMatches_SubdomainAndCaseAndNonMatch(t *testing.T) {
	cases := []struct {
		flowHost, query string
		want            bool
	}{
		{"api.github.com", "api.github.com", true},
		{"api.github.com", "github.com", true},  // subdomain of the query
		{"API.GitHub.com", "github.com", true},  // case-insensitive
		{"github.com", "api.github.com", false}, // parent is not a match for a child
		{"github.com.evil.example", "github.com", false}, // the lookalike Phase 129 fixed
		{"", "github.com", false},
	}
	for _, c := range cases {
		if got := execlog.HostMatches(c.flowHost, c.query); got != c.want {
			t.Errorf("HostMatches(%q, %q) = %v, want %v", c.flowHost, c.query, got, c.want)
		}
	}
}

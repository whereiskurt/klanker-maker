package execlog

import (
	"sort"
	"strings"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// Index answers "what was pid P running at time T" over a trace.
//
// It exists because joining a flow to an exec on pid ALONE is wrong: pids are
// reused, so a flow can land on a number whose original owner is long dead.
// Exit records bound each process's lifetime, which turns the join from a
// plausible guess into a fact — and a wrong attribution here is worse than no
// attribution, because it names an innocent process with full confidence.
type Index struct {
	byPID map[int][]Record
}

// NewIndex builds the per-pid timeline. Records may arrive in any order.
func NewIndex(recs []Record) *Index {
	ix := &Index{byPID: map[int][]Record{}}
	for _, r := range recs {
		if r.Kind != KindExec && r.Kind != KindExit {
			continue
		}
		ix.byPID[r.PID] = append(ix.byPID[r.PID], r)
	}
	for pid := range ix.byPID {
		rs := ix.byPID[pid]
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].TS.Before(rs[j].TS) })
		ix.byPID[pid] = rs
	}
	return ix
}

// At returns the exec that pid was running at time t, if any.
//
// It walks the pid's timeline forward, holding the most recent exec and
// clearing it on an exit. Whatever is held when the walk passes t is the
// answer; nothing held means the pid had either not exec'd yet or had already
// exited, and in both cases the honest answer is "no match".
func (ix *Index) At(pid int, t time.Time) (Record, bool) {
	var cur Record
	var live bool
	for _, r := range ix.byPID[pid] {
		if r.TS.After(t) {
			break
		}
		switch r.Kind {
		case KindExec:
			cur, live = r, true
		case KindExit:
			cur, live = Record{}, false
		}
	}
	return cur, live
}

// Attribution pairs one flow with the process that produced it.
//
// Found is false when the flow carries no pid (the DNS and HTTP proxies record
// none) or when no process owned that pid at that moment. The flow is still
// returned either way: dropping it would make a destination the sandbox really
// reached look like it was never reached at all.
type Attribution struct {
	Flow  flowlog.Record
	Exec  Record
	Found bool
}

// Who returns every flow to host, each paired with the process behind it.
//
// An empty host matches nothing — without this guard, HostMatches fails
// closed on an empty query but strings.EqualFold(f.Addr, "") is true for
// every flow with no recorded address, so Who(execs, flows, "") would return
// that incoherent subset instead of the empty result an absent query implies.
func Who(execs []Record, flows []flowlog.Record, host string) []Attribution {
	if host == "" {
		return nil
	}
	ix := NewIndex(execs)
	var out []Attribution
	for _, f := range flows {
		if !HostMatches(f.Host, host) && !strings.EqualFold(f.Addr, host) {
			continue
		}
		a := Attribution{Flow: f}
		if f.PID != 0 {
			a.Exec, a.Found = ix.At(f.PID, f.TS)
		}
		out = append(out, a)
	}
	return out
}

// HostMatches reports whether a flow's host is query or a subdomain of it.
//
// Deliberately the same shape as the allowlist matcher: case-insensitive, and a
// leading-dot boundary rather than a bare suffix, so "github.com.evil.example"
// does not match "github.com" — the lookalike Phase 129's matcher narrowing
// fixed. A forensics query that quietly matched the attacker's domain would be
// worse than one that matched nothing.
func HostMatches(flowHost, query string) bool {
	if flowHost == "" || query == "" {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(flowHost, "."))
	q := strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(query, "."), "."))
	// Re-check after normalization: a bare "." trims away to "", which the
	// pre-normalization guard above never sees, and "" == "" would otherwise
	// make HostMatches(".", ".") true.
	if h == "" || q == "" {
		return false
	}
	return h == q || strings.HasSuffix(h, "."+q)
}

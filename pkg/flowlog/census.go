package flowlog

import (
	"sort"
	"time"
)

// Dest is one destination in the census, with how often it was seen and when
// it was last seen.
type Dest struct {
	Host  string
	Addr  string
	Count int
	Last  time.Time
}

// Key is what a destination is deduplicated by: its name when one was
// observed, otherwise its address.
func (d Dest) Key() string {
	if d.Host != "" {
		return d.Host
	}
	return d.Addr
}

// Census is the deduplicated view of where a sandbox has been.
//
// Allowed and Denied are kept strictly apart. Allowed is the set pin intersects
// against, so a destination that was never permitted must not appear in it —
// pinning a denied host would be an attempt to widen policy through the census.
type Census struct {
	Allowed []Dest
	Denied  []Dest
}

// Summarize collapses records into a census, sorted by descending count so the
// destinations that dominate a session appear first.
//
// VerdictRedirect counts as allowed: MITM interception is how an allowed host
// is reached (Bedrock, Anthropic, OpenAI metering), not a block.
func Summarize(recs []Record) Census {
	allowed := map[string]*Dest{}
	denied := map[string]*Dest{}

	for _, r := range recs {
		bucket := allowed
		if r.Verdict == VerdictDeny {
			bucket = denied
		}
		d := Dest{Host: r.Host, Addr: r.Addr}
		key := d.Key()
		if key == "" {
			continue
		}
		cur, ok := bucket[key]
		if !ok {
			cur = &Dest{Host: r.Host, Addr: r.Addr}
			bucket[key] = cur
		}
		// A later record may carry a name for a destination first seen as bare
		// address; keep the richer view.
		if cur.Host == "" && r.Host != "" {
			cur.Host = r.Host
		}
		if cur.Addr == "" && r.Addr != "" {
			cur.Addr = r.Addr
		}
		cur.Count++
		if r.TS.After(cur.Last) {
			cur.Last = r.TS
		}
	}

	return Census{Allowed: flatten(allowed), Denied: flatten(denied)}
}

// flatten turns the accumulator map into a slice sorted by descending count,
// then by key so output is deterministic for equal counts.
func flatten(m map[string]*Dest) []Dest {
	out := make([]Dest, 0, len(m))
	for _, d := range m {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Key() < out[j].Key()
	})
	return out
}

// NameFn resolves an address to the name that was looked up to reach it.
//
// The eBPF enforcer supplies one backed by the resolver's own map, and nothing
// else does. There is deliberately no reverse-DNS lookup and no heuristic
// inference behind this: a wrong name on a flow record is worse than an absent
// one, because the operator pins against this data and would be pinning a
// destination the box never actually reached. A nil NameFn, or one that returns
// "", leaves the record IP-only — which is the honest answer.
type NameFn func(addr string) string

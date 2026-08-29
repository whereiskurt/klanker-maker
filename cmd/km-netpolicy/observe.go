package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// runObserved prints the deduplicated census: where this sandbox has been.
//
// Allowed and denied are printed under separate headings and never merged.
// Allowed is the set `pin` intersects against, so folding a blocked
// destination into it would turn the census into a way to widen policy.
func runObserved(o opts) int {
	recs, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return 1
	}
	c := flowlog.Summarize(recs)

	fmt.Fprintf(o.stdout, "allowed (%d):\n", len(c.Allowed))
	printDests(o.stdout, c.Allowed)
	fmt.Fprintf(o.stdout, "\ndenied (%d):\n", len(c.Denied))
	printDests(o.stdout, c.Denied)
	return 0
}

// printDests renders one census section, or an explicit empty marker. An
// unadorned blank section reads as a broken command rather than a quiet box.
func printDests(w io.Writer, ds []flowlog.Dest) {
	if len(ds) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, d := range ds {
		label := d.Host
		if label == "" {
			label = d.Addr
		} else if d.Addr != "" {
			label = fmt.Sprintf("%s (%s)", d.Host, d.Addr)
		}
		fmt.Fprintf(w, "  %-48s %5d conns   last %s\n", label, d.Count, humanAgo(d.Last))
	}
}

// humanAgo renders a coarse age. Precision beyond a minute is noise for a
// census an operator reads to decide whether a session is finished.
func humanAgo(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// runFlows prints raw per-connection records.
func runFlows(args []string, o opts) int {
	var (
		since    time.Duration
		onlyDeny bool
		asJSON   bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--denied":
			onlyDeny = true
		case "--json":
			asJSON = true
		case "--since":
			if i+1 >= len(args) {
				fmt.Fprintf(o.stderr, "%s flows: --since needs a duration (e.g. 10m)\n", prog)
				return 2
			}
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil {
				fmt.Fprintf(o.stderr, "%s flows: %q is not a duration: %v\n", prog, args[i], err)
				return 2
			}
			since = d
		default:
			fmt.Fprintf(o.stderr, "%s flows: unknown flag %q\n", prog, args[i])
			return 2
		}
	}

	recs, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return 1
	}

	cutoff := time.Time{}
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}
	shown := 0
	for _, r := range recs {
		if onlyDeny && r.Verdict != flowlog.VerdictDeny {
			continue
		}
		if !cutoff.IsZero() && r.TS.Before(cutoff) {
			continue
		}
		shown++
		if asJSON {
			b, err := json.Marshal(r)
			if err != nil {
				continue
			}
			fmt.Fprintln(o.stdout, string(b))
			continue
		}
		fmt.Fprintf(o.stdout, "%s  %-8s %-8s %-40s %s\n",
			r.TS.UTC().Format(time.RFC3339), r.Src, r.Verdict, target(r), procOf(r))
	}
	if shown == 0 && !asJSON {
		fmt.Fprintln(o.stdout, "(no matching flows)")
	}
	return 0
}

// target renders the destination of a record, preferring the name.
func target(r flowlog.Record) string {
	host := r.Host
	if host == "" {
		host = r.Addr
	}
	if r.Port != 0 {
		return fmt.Sprintf("%s:%d", host, r.Port)
	}
	return host
}

// procOf renders the originating process when the producer observed one. Only
// the eBPF path does; DNS and HTTP records legitimately have none.
func procOf(r flowlog.Record) string {
	if r.Comm == "" && r.PID == 0 {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%s[%d]", r.Comm, r.PID))
}

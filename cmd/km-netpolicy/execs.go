package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

// runExecs lists what the sandbox executed.
func runExecs(o opts, args []string) error {
	// `execs save` is a sub-verb rather than a flag: it writes to S3, and a
	// flag on a read-only listing verb is too easy to trip by accident.
	if len(args) > 0 && args[0] == "save" {
		return runExecsSave(o)
	}

	var since time.Duration
	uid := -1
	var failed, asJSON bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--failed":
			failed = true
		case "--json":
			asJSON = true
		case "--since":
			if i+1 >= len(args) {
				fmt.Fprintf(o.stderr, "%s execs: --since needs a duration (e.g. 10m)\n", prog)
				return errors.New("--since needs a value")
			}
			i++
			d, err := time.ParseDuration(args[i])
			if err != nil {
				fmt.Fprintf(o.stderr, "%s execs: %q is not a duration: %v\n", prog, args[i], err)
				return err
			}
			since = d
		case "--uid":
			if i+1 >= len(args) {
				fmt.Fprintf(o.stderr, "%s execs: --uid needs a value\n", prog)
				return errors.New("--uid needs a value")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(o.stderr, "%s execs: %q is not an integer: %v\n", prog, args[i], err)
				return err
			}
			if n < 0 {
				fmt.Fprintf(o.stderr, "%s execs: --uid %q must not be negative\n", prog, args[i])
				return fmt.Errorf("--uid %q must not be negative", args[i])
			}
			uid = n
		default:
			fmt.Fprintf(o.stderr, "%s execs: unknown flag %q\n", prog, args[i])
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	recs, err := execlog.ReadDir(o.execDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read exec store %s: %v\n", prog, o.execDir, err)
		return err
	}

	cutoff := time.Time{}
	if since > 0 {
		cutoff = time.Now().Add(-since)
	}

	shown := 0
	for _, r := range recs {
		// Exit records exist only so the join can bound a lifetime. They are
		// not commands anybody ran, so they never appear in this listing.
		if r.Kind != execlog.KindExec {
			continue
		}
		if !cutoff.IsZero() && r.TS.Before(cutoff) {
			continue
		}
		if uid >= 0 && r.UID != uid {
			continue
		}
		if failed && r.Ret == 0 {
			continue
		}
		shown++
		if asJSON {
			fmt.Fprintln(o.stdout, jsonLine(r))
			continue
		}
		fmt.Fprintln(o.stdout, formatExec(r))
	}
	// --json means a consumer parses stdout as zero or more JSON lines; an
	// empty-marker string would corrupt that stream the same way the rotation
	// warning would if it went to stdout instead of stderr.
	if shown == 0 && !asJSON {
		fmt.Fprintln(o.stdout, "(none)")
	}
	warnIfTruncated(o)
	return nil
}

// formatExec renders one exec as a single line.
//
// A failed exec is marked explicitly rather than left to a bare non-zero
// number: it is the more interesting of the two outcomes and must not read as
// noise in a long trace.
func formatExec(r execlog.Record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  uid=%-5d pid=%-7d ppid=%-7d %s",
		r.TS.UTC().Format("15:04:05"), r.UID, r.PID, r.PPID, cmdline(r))
	if r.Truncated {
		b.WriteString(" …[argv truncated]")
	}
	if r.Ret != 0 {
		fmt.Fprintf(&b, "  [FAILED ret=%d]", r.Ret)
	}
	return b.String()
}

// cmdline joins argv for display, falling back to comm when argv was not
// captured at all.
func cmdline(r execlog.Record) string {
	if len(r.Args) == 0 {
		return r.Comm
	}
	return strings.Join(r.Args, " ")
}

// jsonLine renders a record as one JSON line, matching the on-disk form.
func jsonLine(r execlog.Record) string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

// runWho reports which process reached a host.
func runWho(o opts, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(o.stderr, "%s who: needs a host, e.g. %s who api.github.com\n", prog, prog)
		return errors.New("who requires a host")
	}
	if len(args) > 1 {
		fmt.Fprintf(o.stderr, "%s who: unexpected argument %q (want exactly one host)\n", prog, args[1])
		return fmt.Errorf("unexpected argument %q", args[1])
	}
	host := args[0]

	execs, err := execlog.ReadDir(o.execDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read exec store %s: %v\n", prog, o.execDir, err)
		return err
	}
	flows, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return err
	}

	hits := execlog.Who(execs, flows, host)
	if len(hits) == 0 {
		fmt.Fprintf(o.stdout, "no recorded flows to %s\n", host)
		return nil
	}

	unattributed := 0
	for _, a := range hits {
		ts := a.Flow.TS.UTC().Format("15:04:05")
		dest := a.Flow.Host
		if dest == "" {
			dest = a.Flow.Addr
		}
		if a.Found {
			fmt.Fprintf(o.stdout, "%s  %-8s %-9s %s  ← pid=%d %s\n",
				ts, a.Flow.Verdict, a.Flow.Src, dest, a.Exec.PID, cmdline(a.Exec))
			continue
		}
		unattributed++
		reason := "no exec recorded for that pid"
		if a.Flow.PID == 0 {
			reason = "no pid on this flow"
		}
		fmt.Fprintf(o.stdout, "%s  %-8s %-9s %s  ← (%s)\n",
			ts, a.Flow.Verdict, a.Flow.Src, dest, reason)
	}

	// Silence here would read as "nothing reached that host", which is the
	// opposite of the truth. Say which half of the join was missing.
	if unattributed == len(hits) {
		fmt.Fprintf(o.stdout,
			"\nNone of these flows could be attributed to a process.\n"+
				"Only the eBPF enforcement path records a pid alongside a flow, so pid\n"+
				"attribution needs spec.network.enforcement: ebpf or both. The exec trace\n"+
				"itself is complete regardless — see `km-netpolicy execs`.\n")
	}
	warnIfTruncated(o)
	return nil
}

// warnIfTruncated says so when rotation has already discarded part of the trace.
//
// One rotation is harmless: the previous generation is still on disk and is
// read. Two means the oldest generation was overwritten, and the trace is
// missing its earliest execs with no other signal that anything was lost.
func warnIfTruncated(o opts) {
	if n := execlog.Rotations(o.execDir); n >= 2 {
		fmt.Fprintf(o.stderr,
			"warning: the exec store has rotated %d times; the earliest execs "+
				"(typically the package-install phase) are no longer on disk\n", n)
	}
}

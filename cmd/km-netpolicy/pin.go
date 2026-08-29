package main

import (
	"fmt"
	"os"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

// runPin narrows this sandbox's allowlist to what it has actually reached.
//
// A pin appends one generation to the append-only pin file. The effective allow
// set is the intersection of every generation, so a pin can only ever shrink
// it. There is no un-pin verb and adding one would destroy the guarantee.
func runPin(args []string, o opts) int {
	var dryRun, exact, yes, allowEmpty bool
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--exact":
			exact = true
		case "--yes":
			yes = true
		case "--allow-empty":
			allowEmpty = true
		default:
			fmt.Fprintf(o.stderr, "%s pin: unknown flag %q\n", prog, a)
			return 2
		}
	}

	recs, err := flowlog.ReadDir(o.flowDir)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot read flow store %s: %v\n", prog, o.flowDir, err)
		return 1
	}
	census := flowlog.Summarize(recs)
	suffixes, hosts := flowlog.PinCandidates(census, exact)
	altSuffixes, altHosts := flowlog.PinCandidates(census, !exact)

	fmt.Fprintf(o.stdout, "pinned DNS suffixes (%d):\n", len(suffixes))
	printList(o.stdout, suffixes)
	fmt.Fprintf(o.stdout, "\npinned hosts (%d):\n", len(hosts))
	printList(o.stdout, hosts)

	if dryRun {
		mode, alt := "collapsed", "--exact"
		if exact {
			mode, alt = "exact", "default (collapsed)"
		}
		fmt.Fprintf(o.stdout, "\nmode: %s. With %s you would pin instead:\n", mode, alt)
		fmt.Fprintf(o.stdout, "  suffixes (%d): %v\n  hosts (%d): %v\n",
			len(altSuffixes), altSuffixes, len(altHosts), altHosts)
		fmt.Fprintf(o.stdout,
			"\nnothing written. Pinning is IRREVERSIBLE — there is no un-pin verb,\n"+
				"and recovering from a too-tight pin means km destroy && km create.\n"+
				"Re-run with --yes to apply.\n")
		return 0
	}

	// An empty candidate set is a legitimate reading of "allow only what I
	// observed" on a box that observed nothing — and it is deny-all. Refuse by
	// default so it is never reached by accident.
	if len(suffixes) == 0 && len(hosts) == 0 && !allowEmpty {
		fmt.Fprintf(o.stderr,
			"%s pin: the census is empty, so this pin would deny ALL egress.\n"+
				"  This is usually a sign the box has not done its work yet.\n"+
				"  Pass --allow-empty if sealing the box is genuinely what you want.\n", prog)
		return 1
	}

	if !yes {
		fmt.Fprintf(o.stderr,
			"\n%s pin: refusing without --yes.\n"+
				"  Pinning is IRREVERSIBLE. Run with --dry-run first, then --yes.\n", prog)
		return 1
	}

	// An absent file means boot never provisioned the pin file. Creating it here
	// would produce a file with no kernel append-only attribute that no proxy is
	// watching — worse than refusing, because it would look like it worked. This
	// mirrors runDeny's stance on the deny list.
	if _, err := os.Stat(o.pinFile); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(o.stderr,
				"%s: pin file %s does not exist — this sandbox predates allowlist pinning.\n"+
					"  Recreate it with km destroy && km create.\n", prog, o.pinFile)
			return 1
		}
		fmt.Fprintf(o.stderr, "%s: cannot access %s: %v\n", prog, o.pinFile, err)
		return 1
	}

	store := netpolicy.NewPinStore(o.pinFile, 0)
	gen := len(store.Generations()) + 1
	block := netpolicy.FormatPinBlock(gen, time.Now(), dedupe(append(append([]string{}, suffixes...), hosts...)))

	f, err := os.OpenFile(o.pinFile, os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot append to %s: %v\n", prog, o.pinFile, err)
		return 1
	}
	if _, err := f.WriteString(block); err != nil {
		f.Close()
		fmt.Fprintf(o.stderr, "%s: write failed: %v\n", prog, err)
		return 1
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(o.stderr, "%s: close failed: %v\n", prog, err)
		return 1
	}

	// Read back so the caller knows the pin is in force rather than merely
	// written — the same discipline runDeny applies.
	live := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	if len(live) != gen {
		fmt.Fprintf(o.stderr, "%s: pin did not survive read-back — NOT in force\n", prog)
		return 1
	}
	fmt.Fprintf(o.stdout, "\npinned. generation %d of %d now in force (takes effect within ~1s).\n", gen, len(live))
	fmt.Fprintf(o.stdout, "everything not listed above is now denied.\n")
	return 0
}

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
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
	var dryRun, exact, yes, allowEmpty, acceptTruncated bool
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
		case "--accept-truncated":
			acceptTruncated = true
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

	// The carve-out is invisible in the candidate lists, and an operator who
	// believes a pin seals EVERYTHING unnamed would be wrong about the one host
	// set that matters most. Say it here, where the decision is being made.
	fmt.Fprintf(o.stdout, "\nalways allowed regardless of any pin (platform recovery floor): %s\n",
		strings.Join(netpolicy.PlatformEssentialSuffixes(), " "))

	// One rotation is harmless — the previous generation is still on disk and
	// ReadDir reads it. Two means the earliest records are gone for good, which
	// on a long session is the package-install phase: exactly the traffic an
	// operator means to pin.
	rotated := flowlog.RotatedProducers(o.flowDir)
	truncated := flowlog.TruncatedProducers(o.flowDir)
	if len(rotated) > 0 {
		fmt.Fprintf(o.stdout, "\nnote: flow files have rotated for: %s\n", strings.Join(rotated, " "))
	}
	if len(truncated) > 0 {
		fmt.Fprintf(o.stdout,
			"WARNING: the census is INCOMPLETE — these producers have rotated more than once,\n"+
				"  so their earliest records were discarded: %s\n", strings.Join(sortedKeys(truncated), " "))
	}

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

	// A census known to be missing records feeds an irreversible operation. The
	// destinations it lost are pinned out with no other signal that they ever
	// existed, so require the operator to say they accept that.
	if len(truncated) > 0 && !acceptTruncated {
		fmt.Fprintf(o.stderr,
			"\n%s pin: refusing — the census has lost records to rotation (%s).\n"+
				"  Destinations reached early in this session are missing and would be\n"+
				"  pinned out irreversibly. Pass --accept-truncated if that is understood.\n",
			prog, strings.Join(sortedKeys(truncated), " "))
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
	// The two lists are written under separate scopes, so the DNS matcher and
	// the host matcher each see only their own. Folding them into one flat list
	// is what made --exact inert: the collapsed suffixes it always needs on the
	// DNS side would match every subdomain on the host side too.
	block := netpolicy.FormatPinBlock(gen, time.Now(), dedupe(suffixes), dedupe(hosts))

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

// sortedKeys renders a producer->count map deterministically.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

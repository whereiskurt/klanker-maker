package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

const usage = `usage: km-volumes <verb> [flags]

  manifest  --mountpoint MP --bdm LETTER --device NODE --label TEXT [--from-snapshot SNAP]
            record one volume's live identity in /var/lib/km/volumes.json (first cold boot)
  mount     [--fallback MP:LETTER]...   validate and mount every manifest entry
            (policy from KM_VOLUMES_ON_MISMATCH: refuse|reboot, default refuse)
  status    [--json]                    print /var/lib/km/volumes.state
  pre-sleep                             bounded unmount ladder (always exits 0)
  post-sleep                            PCI re-probe, mount, retry, policy (always exits 0)
  repair    MP [--i-accept-data-loss]   e2fsck a refused volume against its backup superblocks
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	sys, err := newRealSystem()
	if err != nil {
		fatal(err)
	}
	verb, args := os.Args[1], os.Args[2:]
	switch verb {
	case "manifest":
		err = cmdManifest(sys, args)
	case "mount":
		err = cmdMount(sys, args)
	case "status":
		err = cmdStatus(sys, args)
	case "pre-sleep":
		alwaysZero("pre-sleep", func() { cmdPreSleep(sys) })
		return
	case "post-sleep":
		alwaysZero("post-sleep", func() { cmdPostSleep(sys) })
		return
	case "repair":
		err = cmdRepair(sys, args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "km-volumes: unknown verb %q\n%s", verb, usage)
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "km-volumes: %v\n", err)
	os.Exit(1)
}

// alwaysZero runs a sleep-hook body and exits 0 whatever happens (spec §5.3,
// §5.4, addendum §C): a panic here must never fail the systemd transition.
func alwaysZero(verb string, body func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "km-volumes %s: recovered: %v\n%s", verb, r, debug.Stack())
		}
		os.Exit(0)
	}()
	body()
}

func policyFromEnv() string {
	switch p := os.Getenv("KM_VOLUMES_ON_MISMATCH"); p {
	case "reboot":
		return "reboot"
	case "", "refuse":
		return "refuse"
	default:
		fmt.Fprintf(os.Stderr, "km-volumes: KM_VOLUMES_ON_MISMATCH=%q is not refuse|reboot; using refuse\n", p)
		return "refuse"
	}
}

func cmdManifest(sys System, args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	var o manifestOpts
	fs.StringVar(&o.Mountpoint, "mountpoint", "", "mountpoint")
	fs.StringVar(&o.BDM, "bdm", "", "AWS BDM letter (informational)")
	fs.StringVar(&o.Device, "device", "", "resolved device node (or partition)")
	fs.StringVar(&o.Label, "label", "", "human label")
	fs.StringVar(&o.FromSnapshot, "from-snapshot", "", "snapshot id the volume was created from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.Mountpoint == "" || o.Device == "" {
		return fmt.Errorf("manifest: --mountpoint and --device are required")
	}
	v, err := buildManifestEntry(sys, o)
	if err != nil {
		return err
	}
	if err := appendManifestEntry(manifestPath, v); err != nil {
		return err
	}
	fmt.Printf("km-volumes: recorded %s = %s (%d sectors, %s %s)\n", v.Mountpoint, v.VolumeID, v.SizeSectors, v.FSType, v.FSUUID)
	return nil
}

// fallbackFlag collects repeatable --fallback MP:LETTER arguments.
type fallbackFlag []FallbackEntry

func (f *fallbackFlag) String() string { return fmt.Sprint([]FallbackEntry(*f)) }
func (f *fallbackFlag) Set(s string) error {
	mp, letter, ok := strings.Cut(s, ":")
	if !ok || mp == "" || letter == "" {
		return fmt.Errorf("--fallback %q: want MOUNTPOINT:LETTER", s)
	}
	*f = append(*f, FallbackEntry{Mountpoint: mp, BDM: letter})
	return nil
}

func cmdMount(sys System, args []string) error {
	fs := flag.NewFlagSet("mount", flag.ContinueOnError)
	var fb fallbackFlag
	fs.Var(&fb, "fallback", "MOUNTPOINT:LETTER for a pre-manifest box (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	st, err := runMount(context.Background(), sys, m, MountOpts{Policy: policyFromEnv(), Fallback: fb})
	if err != nil {
		return err
	}
	printState(st)
	if anyRefused(st) {
		return fmt.Errorf("one or more volumes refused; see %s", statePath)
	}
	return nil
}

func cmdStatus(sys System, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.Bool("json", true, "print JSON (always)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := readState(sys)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(st)
}

func cmdPreSleep(sys System) {
	m, err := loadManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-volumes pre-sleep: %v\n", err)
		return
	}
	printState(runPreSleep(context.Background(), sys, m))
}

func cmdPostSleep(sys System) {
	m, err := loadManifest(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-volumes post-sleep: %v\n", err)
		return
	}
	// The boot id is read BEFORE any reboot the policy may issue, so the guard
	// is keyed to this resume; the rebooted box gets a new id and a clean slate.
	resumeID := "unknown"
	if b, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil {
		resumeID = strings.TrimSpace(string(b))
	}
	printState(runPostSleep(context.Background(), sys, m, policyFromEnv(), resumeID))
}

func cmdRepair(sys System, args []string) error {
	fs := flag.NewFlagSet("repair", flag.ContinueOnError)
	accept := fs.Bool("i-accept-data-loss", false, "allow repair of a volume that is not snapshot-derived (the only copy)")
	// Accept the mountpoint before or after the flag.
	var rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			rest = append(rest, a)
		} else {
			rest = append([]string{a}, rest...)
		}
	}
	if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
		return fmt.Errorf("repair: mountpoint required")
	}
	mp := rest[0]
	if err := fs.Parse(rest[1:]); err != nil {
		return err
	}
	m, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	return runRepair(context.Background(), sys, m, mp, *accept)
}

func printState(st State) {
	for _, v := range st.Volumes {
		extra := ""
		if v.Step != "" {
			extra += " at " + v.Step
		}
		if v.Reason != "" {
			extra += ": " + v.Reason
		}
		// Log tag in the format string, as the other sidecars do — pkg/hygiene's
		// prefix guard reads a concatenated/Sprintf'd "km-…" as name construction.
		fmt.Fprintf(os.Stderr, "km-volumes: %-12s %s%s\n", v.Mountpoint, v.Outcome, extra)
	}
}

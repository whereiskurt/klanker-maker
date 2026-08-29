// Command km-netpolicy lets a sandbox tighten its own egress policy from
// user-land, after boot, without an operator round-trip.
//
// It can only ever NARROW. There is no verb that removes an entry, and the file
// it appends to is held append-only by the kernel (chattr +a, set during
// bootstrap), so truncating, unlinking, renaming, or rewriting it all fail for
// the sandbox user. "Never widens" is therefore a property of the filesystem and
// of this command's surface, not a rule that has to be trusted.
//
// Widening still requires what it always did: a new profile and a fresh
// sandbox.
//
//	km-netpolicy deny evil.example.com [more...]
//	km-netpolicy list
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/execlog"
	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

// errUnsupportedPlatform is returned by verbs whose implementation exists only
// on the sandbox's linux/amd64 build.
var errUnsupportedPlatform = errors.New("unsupported platform")

// prog is this binary's own name, used in usage and error output. It is a fixed
// brand name, not a resource identifier, so it is deliberately independent of
// the install's resource_prefix.
const prog = "km-netpolicy"

// opts carries everything run needs, so tests can drive it without touching the
// real filesystem or environment.
type opts struct {
	denyFile    string
	flowDir     string
	pinFile     string
	staticDNS   []string
	staticHosts []string
	captureSock string
	captureDir  string
	execDir     string
	stdout      io.Writer
	stderr      io.Writer
}

const usage = `km-netpolicy — narrow this sandbox's egress policy from inside the box

Usage:
  km-netpolicy deny <pattern> [pattern...]   block a host and its subdomains
  km-netpolicy list                          show the effective deny lists
  km-netpolicy observed                      show every destination reached so far
  km-netpolicy flows [--since 10m] [--denied] [--json]
                                             show raw per-connection records
  km-netpolicy profile                       emit a SandboxProfile from the census
  km-netpolicy pin [--dry-run] [--exact] [--yes]
                                             narrow the allowlist to the census
  km-netpolicy capture start [--duration 5m] [--max-size 250MB] [--port N] [--host H]
                                             start a bounded packet capture (max 60m / 2GB)
  km-netpolicy capture stop                  stop the running capture and upload it
  km-netpolicy capture status                show whether a capture is running
  km-netpolicy capture list                  list finished captures

A pattern is a bare hostname, optionally with a leading dot. It blocks the apex
AND every subdomain: "evil.example.com" also blocks "api.evil.example.com".
"*" blocks all egress.

Narrowing is one-way. There is no verb to remove a deny or widen the policy;
that requires a new profile and a fresh sandbox.
`

// DefaultEnvFile is written at boot alongside the deny list. The profile-baked
// denies otherwise live only inside the two proxy units' Environment blocks,
// which a sandbox shell never sees — so without this, `list` would report
// "(none)" for profile-baked denies on a box that actually has them.
const DefaultEnvFile = "/etc/km/netpolicy.env"

func main() {
	os.Exit(run(os.Args[1:], buildOpts(os.Getenv, DefaultEnvFile)))
}

// buildOpts resolves configuration with real environment variables taking
// precedence over the boot-written env file, which in turn beats the
// compiled-in default.
func buildOpts(getenv func(string) string, envFile string) opts {
	fileVals := parseEnvFile(envFile)

	pick := func(key string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fileVals[key]
	}

	denyFile := pick("KM_NETPOLICY_FILE")
	if denyFile == "" {
		denyFile = netpolicy.DefaultPath
	}

	flowDir := pick("KM_FLOWLOG_DIR")
	if flowDir == "" {
		flowDir = flowlog.DefaultDir
	}

	pinFile := pick("KM_NETPOLICY_PINS")
	if pinFile == "" {
		pinFile = netpolicy.DefaultPinPath
	}

	captureSock := pick("KM_CAPTURE_SOCK")
	if captureSock == "" {
		captureSock = DefaultCaptureSock
	}

	captureDir := pick("KM_CAPTURE_DIR")
	if captureDir == "" {
		captureDir = DefaultCaptureDir
	}

	execDir := pick("KM_EXEC_DIR")
	if execDir == "" {
		execDir = execlog.DefaultDir
	}

	return opts{
		denyFile:    denyFile,
		flowDir:     flowDir,
		pinFile:     pinFile,
		staticDNS:   splitCSV(pick("DENIED_SUFFIXES")),
		staticHosts: splitCSV(pick("DENIED_HOSTS")),
		captureSock: captureSock,
		captureDir:  captureDir,
		execDir:     execDir,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}
}

// parseEnvFile reads simple KEY=VALUE lines, ignoring blanks, "#" comments, and
// anything without an "=". A missing or unreadable file yields an empty map
// rather than an error — the file is absent on any sandbox without runtime
// narrowing, which is the common case.
func parseEnvFile(path string) map[string]string {
	out := map[string]string{}
	body, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}

func run(args []string, o opts) int {
	if len(args) == 0 {
		fmt.Fprint(o.stderr, usage)
		return 2
	}

	switch args[0] {
	case "deny":
		return runDeny(args[1:], o)
	case "list":
		return runList(o)
	case "observed":
		return runObserved(o)
	case "flows":
		return runFlows(args[1:], o)
	case "profile":
		return runProfileGen(o)
	case "pin":
		return runPin(args[1:], o)
	case "capture":
		return runCapture(args[1:], o)
	case "capture-daemon":
		return runCaptureDaemon(o)
	case "-h", "--help", "help":
		fmt.Fprint(o.stdout, usage)
		return 0
	default:
		fmt.Fprintf(o.stderr, "%s: unknown verb %q\n\n", prog, args[0])
		fmt.Fprint(o.stderr, usage)
		return 2
	}
}

func runDeny(patterns []string, o opts) int {
	if len(patterns) == 0 {
		fmt.Fprintf(o.stderr, "%s deny: needs at least one pattern\n\n", prog)
		fmt.Fprint(o.stderr, usage)
		return 2
	}

	// Validate everything up front. A malformed pattern part-way through a
	// multi-pattern call must not leave the file half-updated, and the append is
	// irreversible.
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		c, ok := netpolicy.ParseLine(p)
		if !ok {
			fmt.Fprintf(o.stderr,
				"%s deny: %q is not a valid pattern\n"+
					"  Expected a bare hostname, optionally dot-prefixed (e.g. evil.example.com or .tracker.net).\n"+
					"  No scheme, port, path, or glob.\n", prog, p)
			return 1
		}
		cleaned = append(cleaned, c)
	}

	// An absent file means the profile never enabled runtime narrowing. Creating
	// it here would produce a file with no kernel append-only attribute that no
	// proxy is watching — worse than refusing, because it would look like it
	// worked.
	if _, err := os.Stat(o.denyFile); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(o.stderr,
				"%s: runtime narrowing is not enabled on this sandbox (%s does not exist)\n"+
					"  Set spec.network.egress.runtimeDeny: true in the profile, then recreate the sandbox.\n",
				prog, o.denyFile)
			return 1
		}
		fmt.Fprintf(o.stderr, "%s: cannot access %s: %v\n", prog, o.denyFile, err)
		return 1
	}

	f, err := os.OpenFile(o.denyFile, os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		fmt.Fprintf(o.stderr, "%s: cannot append to %s: %v\n", prog, o.denyFile, err)
		return 1
	}
	var b strings.Builder
	for _, c := range cleaned {
		b.WriteString(c)
		b.WriteString("\n")
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		fmt.Fprintf(o.stderr, "%s: write failed: %v\n", prog, err)
		return 1
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(o.stderr, "%s: close failed: %v\n", prog, err)
		return 1
	}

	// Read back so the caller knows the deny is actually in force rather than
	// merely written.
	live := netpolicy.NewStore(o.denyFile, 0).Entries()
	for _, c := range cleaned {
		if !contains(live, c) {
			fmt.Fprintf(o.stderr, "%s: %q did not survive read-back — deny NOT in force\n", prog, c)
			return 1
		}
		fmt.Fprintf(o.stdout, "denied %s\n", c)
	}
	fmt.Fprintf(o.stdout, "%d runtime deny entr%s now in force (takes effect within ~1s)\n",
		len(live), plural(len(live)))
	return 0
}

func runList(o opts) int {
	store := netpolicy.NewStore(o.denyFile, 0)
	runtime := store.Entries()

	// A profile commonly names the same host in both deniedDNSSuffixes and
	// deniedHosts, to block it at the DNS and HTTP layers alike. Listing it twice
	// reads as duplicated config rather than one host blocked at two layers.
	fmt.Fprintln(o.stdout, "profile-baked denies (fixed at create time):")
	printList(o.stdout, dedupe(append(append([]string{}, o.staticDNS...), o.staticHosts...)))

	fmt.Fprintln(o.stdout, "\nruntime denies (appended from inside this sandbox):")
	printList(o.stdout, runtime)

	// Without this, a pinned box prints nothing about pins and reads as
	// unpinned — the same reason /etc/km/netpolicy.env exists for the deny side.
	pins := netpolicy.NewPinStore(o.pinFile, 0).Generations()
	fmt.Fprintln(o.stdout, "\nallow pins (each generation narrows further):")
	if len(pins) == 0 {
		fmt.Fprintln(o.stdout, "  (none — allowlist is as the profile declared it)")
	} else {
		for i, g := range pins {
			fmt.Fprintf(o.stdout, "  generation %d:\n", i+1)
			for _, p := range g.DNS {
				fmt.Fprintf(o.stdout, "    dns   %s\n", p)
			}
			for _, p := range g.Hosts {
				fmt.Fprintf(o.stdout, "    host  %s\n", p)
			}
		}
		// The carve-out is invisible in the file, so a pinned box would read as
		// though .amazonaws.com had been pinned out when it never can be.
		fmt.Fprintln(o.stdout, "\n  always allowed regardless of pins (platform recovery floor):")
		for _, s := range netpolicy.PlatformEssentialSuffixes() {
			fmt.Fprintf(o.stdout, "    %s\n", s)
		}
	}

	if n := store.Dropped(); n > 0 {
		fmt.Fprintf(o.stdout, "\n%d malformed line%s in %s were skipped\n",
			n, plural(n), o.denyFile)
	}
	return 0
}

// dedupe removes repeats while preserving first-seen order.
func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func printList(w io.Writer, entries []string) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, e := range entries {
		fmt.Fprintf(w, "  %s\n", e)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func splitCSV(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

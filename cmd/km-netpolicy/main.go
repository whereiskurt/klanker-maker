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
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

// prog is this binary's own name, used in usage and error output. It is a fixed
// brand name, not a resource identifier, so it is deliberately independent of
// the install's resource_prefix.
const prog = "km-netpolicy"

// opts carries everything run needs, so tests can drive it without touching the
// real filesystem or environment.
type opts struct {
	denyFile    string
	staticDNS   []string
	staticHosts []string
	stdout      io.Writer
	stderr      io.Writer
}

const usage = `km-netpolicy — narrow this sandbox's egress policy from inside the box

Usage:
  km-netpolicy deny <pattern> [pattern...]   block a host and its subdomains
  km-netpolicy list                          show the effective deny lists

A pattern is a bare hostname, optionally with a leading dot. It blocks the apex
AND every subdomain: "evil.example.com" also blocks "api.evil.example.com".
"*" blocks all egress.

Narrowing is one-way. There is no verb to remove a deny or widen the policy;
that requires a new profile and a fresh sandbox.
`

func main() {
	os.Exit(run(os.Args[1:], opts{
		denyFile:    envOr("KM_NETPOLICY_FILE", netpolicy.DefaultPath),
		staticDNS:   splitCSV(os.Getenv("DENIED_SUFFIXES")),
		staticHosts: splitCSV(os.Getenv("DENIED_HOSTS")),
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}))
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
		fmt.Fprint(o.stderr, fmt.Sprintf("%s deny: needs at least one pattern\n\n", prog))
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

	fmt.Fprintln(o.stdout, "profile-baked denies (fixed at create time):")
	printList(o.stdout, append(append([]string{}, o.staticDNS...), o.staticHosts...))

	fmt.Fprintln(o.stdout, "\nruntime denies (appended from inside this sandbox):")
	printList(o.stdout, runtime)

	if n := store.Dropped(); n > 0 {
		fmt.Fprintf(o.stdout, "\n%d malformed line%s in %s were skipped\n",
			n, plural(n), o.denyFile)
	}
	return 0
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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

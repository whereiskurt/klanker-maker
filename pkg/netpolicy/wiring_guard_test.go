package netpolicy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every consumer of the runtime DENY store must also consult the PIN store.
//
// Commit 0c7f9880 had to touch five sites to un-gate km-netpolicy, and its
// message records why a partial change is worse than none: ship the writer
// without the readers and the verb reports success while nothing enforces the
// result. The pin file has the identical shape and the identical failure mode,
// so this guard makes the pairing mechanical instead of remembered.
//
// The eBPF site is the one most likely to be missed and the most load-bearing:
// under ebpf/both the bootstrap leaves km-dns-proxy disabled and the resolver
// serves DNS, so a pin that skipped it would report success while every host
// stayed resolvable.
func TestEveryDenyConsumerAlsoReadsPins(t *testing.T) {
	repoRoot := findRepoRoot(t)
	consumers := []string{
		"sidecars/dns-proxy/dnsproxy/proxy.go",
		"sidecars/http-proxy/httpproxy/proxy.go",
		"pkg/ebpf/resolver/allowlist.go",
		// The boot-time BPF pre-seed loop. It is the site the plan actually
		// missed once: it seeds resolved IPs straight into the allow trie ahead
		// of any per-query decision, so a pin that skipped it would be undone by
		// the next reboot. Listing it here is what makes the five-site trap
		// mechanical rather than remembered.
		"internal/app/cmd/ebpf_attach.go",
	}
	for _, rel := range consumers {
		body, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		src := string(body)
		if !strings.Contains(src, "IsDenied") {
			t.Fatalf("%s no longer consults the deny store — this guard needs updating", rel)
		}
		if !strings.Contains(src, "Pinner") && !strings.Contains(src, "PinsAllow") {
			t.Errorf("%s reads denies but not pins: a pin there reports success while nothing enforces it", rel)
		}
	}
}

// findRepoRoot walks up from the test's working directory to the module root.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find repo root")
	return ""
}

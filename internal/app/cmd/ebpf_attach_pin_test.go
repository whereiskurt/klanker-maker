//go:build linux && amd64

// ebpf-attach only exists on linux/amd64, so these tests carry the same build
// tag as the command they cover and are a no-op on a macOS workstation.
package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/netpolicy"
)

// The boot-time pre-seed loop is the ONE path that bypasses the live
// resolver.IsAllowed check pins otherwise gate: it seeds a host's IPs
// straight into the BPF trie on every ebpf-attach start (boot, resume),
// ahead of any per-query decision. A wiring-guard test (pkg/netpolicy) can
// only catch total omission of the pinner — it cannot catch a pinner that is
// declared but never actually excludes a pinned-out host from being seeded.
// This test pins down the BEHAVIOUR: given a pin file that excludes host X,
// shouldSeedHost must refuse to seed X.
func TestShouldSeedHost_PinnedOutHostIsNotSeeded(t *testing.T) {
	pinPath := filepath.Join(t.TempDir(), "allow.pins")
	// A leading-dot pattern is what the pin generator emits for a host it
	// wants to cover including subdomains (MatchAllow semantics: a bare
	// entry is exact-match only, mirroring IsHostAllowed).
	body := netpolicy.FormatPinBlock(0, time.Now(), []string{".github.com"})
	if err := os.WriteFile(pinPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed pin file: %v", err)
	}

	// interval 0 forces a fresh read on every Generations() call, matching
	// what the tests below need without waiting on the real reload interval.
	pinner := netpolicy.NewPinner(netpolicy.NewPinStore(pinPath, 0))

	if shouldSeedHost("evil-not-pinned.example.com", nil, pinner) {
		t.Error("shouldSeedHost seeded a host absent from the pin generation — a reboot would re-widen the trie past what was pinned")
	}
	if !shouldSeedHost("github.com", nil, pinner) {
		t.Error("shouldSeedHost refused a host the pin generation explicitly allows")
	}
	if !shouldSeedHost("api.github.com", nil, pinner) {
		t.Error("shouldSeedHost refused a subdomain of a pinned host")
	}
}

// A nil pinner (no KM_NETPOLICY_PINS / --netpolicy-pins) must allow
// everything the deny gate doesn't already refuse — a sandbox that has never
// pinned itself is byte-identical to before pins existed.
func TestShouldSeedHost_NilPinnerAllowsEverything(t *testing.T) {
	if !shouldSeedHost("anything.example.com", nil, nil) {
		t.Error("nil denier + nil pinner must allow seeding")
	}
}

// Deny still beats a pin that would otherwise allow the host — pins narrow
// IN ADDITION to denies, they never override them.
func TestShouldSeedHost_DenyBeatsPin(t *testing.T) {
	pinPath := filepath.Join(t.TempDir(), "allow.pins")
	body := netpolicy.FormatPinBlock(0, time.Now(), []string{"evil.example.com"})
	if err := os.WriteFile(pinPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed pin file: %v", err)
	}
	pinner := netpolicy.NewPinner(netpolicy.NewPinStore(pinPath, 0))
	denier := netpolicy.NewDenier([]string{"evil.example.com"}, nil)

	if shouldSeedHost("evil.example.com", denier, pinner) {
		t.Error("a denied host was seeded despite also being pinned — deny must beat pin")
	}
}

// A regression guard for the exact defect this fix closes: with no pinner at
// all wired into the pre-seed path (the pre-fix shape), a host absent from a
// live pin generation would have been seeded anyway. This test constructs the
// same scenario the boot-time loop hits after a reboot on a pinned box and
// asserts the fixed decision function refuses it.
func TestShouldSeedHost_RebootDoesNotWidenPastPin(t *testing.T) {
	pinPath := filepath.Join(t.TempDir(), "allow.pins")
	// Sandbox pinned itself down to exactly one host before the box rebooted.
	body := netpolicy.FormatPinBlock(0, time.Now(), []string{"pinned.example.com"})
	if err := os.WriteFile(pinPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed pin file: %v", err)
	}

	// Simulate ebpf-attach re-running on reboot: fresh PinStore over the file
	// that survived under /var/lib, exactly as hostPinner constructs it.
	pinner := hostPinner(pinPath)

	// The profile's allowedHosts still lists a broader set than what was
	// pinned — this is the whole point of pinning: narrowing without editing
	// the profile.
	for _, h := range []string{"other-allowed-host.example.com", "wide-open.example.net"} {
		if shouldSeedHost(h, nil, pinner) {
			t.Errorf("post-reboot pre-seed allowed %q even though it was pinned out — the pin was silently bypassed on boot", h)
		}
	}
	if !shouldSeedHost("pinned.example.com", nil, pinner) {
		t.Error("post-reboot pre-seed refused the host the pin actually allows")
	}
}

// hostPinner itself must return a Pinner that allows everything when no pin
// file path is given — the "sandbox has never pinned" default.
func TestHostPinner_EmptyPathAllowsAll(t *testing.T) {
	pinner := hostPinner("")
	if !shouldSeedHost("anything.example.com", nil, pinner) {
		t.Error("hostPinner(\"\") must allow everything")
	}
}

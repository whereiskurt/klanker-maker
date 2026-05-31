package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// TestUserdata_PerSandboxFilesAreUnconditionalTruncatingWrites — Phase 70 follow-up.
//
// When `km ami bake` snapshots a sandbox, every per-sandbox file already on disk
// (systemd units, profile.d hooks) is baked into the AMI with the SOURCE sandbox's
// SANDBOX_ID. On the next `km create` from that AMI, cloud-init runs userdata once
// with the NEW SANDBOX_ID. Per-sandbox files MUST be re-emitted as a TRUNCATING
// `cat > /path << EOF` heredoc (not `cat >>` and not gated by `if [ ! -f /path ]`)
// so the AMI-baked stale content is overwritten with the new identity.
//
// UAT precedent: a new sandbox polled the SOURCE sandbox's SQS queue because
// /etc/systemd/system/km-slack-inbound-poller.service still had the source's
// SANDBOX_ID — silence in Slack forever. This test regression-guards the property.
func TestUserdata_PerSandboxFilesAreUnconditionalTruncatingWrites(t *testing.T) {
	// Enable every per-sandbox feature so the heredocs all render into the output.
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtr(true),
			PerSandbox: boolPtr(true),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtr(true)},
		},
	}
	// "both" enables km-ebpf-enforcer.service AND km-cgroup.sh writers.
	p.Spec.Network.Enforcement = "both"

	out, err := generateUserData(p, "sb-amitest", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// Each entry is a path that embeds {{.SandboxID}} (or runs with SANDBOX_ID env)
	// AND is observable to systemd / one-shot env consumers (where last-occurrence-wins
	// inside the file can't paper over a missing truncating write — the file must be
	// fully re-emitted with the new identity).
	//
	// Excluded: /etc/profile.d/km-profile-env.sh — its truncating write is gated by
	// {{- if .ProfileEnv }}, but the file is only ever sourced by bash (last assignment
	// wins per shell semantics), so appended OTEL_RESOURCE_ATTRIBUTES with the new
	// sandbox_id correctly shadows any stale baked-AMI line. Cosmetic file growth, not
	// a functional bug. Audit it on a future pass if rebake-growth becomes painful.
	perSandboxFiles := []string{
		// /etc/profile.d/ — shell env files sourced by all login shells.
		"/etc/profile.d/km-identity.sh",
		"/etc/profile.d/km-notify-env.sh",
		"/etc/profile.d/km-audit.sh",
		"/etc/profile.d/km-cgroup.sh",
		// /etc/km/ — systemd-native env file (no `export` prefix).
		"/etc/km/notify.env",
		// /etc/systemd/system/ — units that hard-code SANDBOX_ID via Environment=.
		"/etc/systemd/system/km-dns-proxy.service",
		"/etc/systemd/system/km-http-proxy.service",
		"/etc/systemd/system/km-audit-log.service",
		"/etc/systemd/system/km-tracing.service",
		"/etc/systemd/system/km-mail-poller.service",
		"/etc/systemd/system/km-slack-inbound-poller.service",
		"/etc/systemd/system/km-presence.service",
		"/etc/systemd/system/km-queue.service",
		"/etc/systemd/system/km-ebpf-enforcer.service",
	}

	for _, path := range perSandboxFiles {
		t.Run(path, func(t *testing.T) {
			truncate := "cat > " + path + " <<"
			idx := strings.Index(out, truncate)
			if idx < 0 {
				t.Fatalf("missing truncating write %q — file would not be regenerated on AMI-baked launches", truncate)
			}

			// Audit the few lines preceding the cat command. A skip-if-exists guard
			// (e.g. `if [ ! -f /path ]; then`) would let the stale AMI-baked file
			// survive into the new sandbox.
			lookbackChars := 400
			start := idx - lookbackChars
			if start < 0 {
				start = 0
			}
			lookback := out[start:idx]
			lines := strings.Split(lookback, "\n")
			for _, raw := range lines {
				line := strings.TrimSpace(raw)
				badPatterns := []string{
					"[ ! -f " + path + " ]",
					"[ ! -e " + path + " ]",
					"test ! -f " + path,
					"test ! -e " + path,
				}
				for _, bad := range badPatterns {
					if strings.Contains(line, bad) {
						t.Errorf("%q is preceded by skip-if-exists guard %q — AMI-baked stale content would survive into the new sandbox", path, bad)
					}
				}
			}
		})
	}
}

// TestUserdata_PerSandboxUnitsHaveNoAppendOnlyWrites — companion to the truncating-
// writes test. A `cat >> /etc/systemd/system/km-FOO.service` (append) without a
// prior truncating `cat >` would silently accumulate Environment= lines across
// AMI rebakes, with the stale source SANDBOX_ID coming FIRST in the file and
// systemd taking the FIRST occurrence. systemd unit files MUST never be appended
// to anywhere in userdata.
func TestUserdata_PerSandboxUnitsHaveNoAppendOnlyWrites(t *testing.T) {
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtr(true),
			PerSandbox: boolPtr(true),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtr(true)},
		},
	}
	p.Spec.Network.Enforcement = "both"

	out, err := generateUserData(p, "sb-amitest", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	systemdUnits := []string{
		"/etc/systemd/system/km-dns-proxy.service",
		"/etc/systemd/system/km-http-proxy.service",
		"/etc/systemd/system/km-audit-log.service",
		"/etc/systemd/system/km-tracing.service",
		"/etc/systemd/system/km-mail-poller.service",
		"/etc/systemd/system/km-slack-inbound-poller.service",
		"/etc/systemd/system/km-presence.service",
		"/etc/systemd/system/km-queue.service",
		"/etc/systemd/system/km-ebpf-enforcer.service",
	}

	for _, path := range systemdUnits {
		if strings.Contains(out, "cat >> "+path) {
			t.Errorf("systemd unit %q is appended to via `cat >>` — units must only be written with a truncating `cat >` so AMI-baked stale content is replaced, not augmented", path)
		}
	}
}

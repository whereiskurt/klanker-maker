package compiler

// Phase 79: km-presence daemon — userdata regression tests.
//
// These tests assert:
//   - The legacy bash _km_heartbeat function and its EXIT trap are GONE.
//   - The per-command _km_audit PROMPT_COMMAND hook is still PRESENT.
//   - The km-presence S3 fetch, systemd unit file, and enable/restart lines are PRESENT.
//   - The touch /run/km/last-slack-inbound stamp appears on the success path.

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// basePresenceProfile returns a minimal EC2 profile for Phase 79 presence tests.
// Uses baseProfile() from userdata_test.go (same package).
func basePresenceProfile() *profile.SandboxProfile {
	return baseProfile()
}

// renderPresenceUserdata compiles userdata for the given profile and returns the string.
func renderPresenceUserdata(t *testing.T, p *profile.SandboxProfile) string {
	t.Helper()
	out, err := generateUserData(p, "sb-phase79-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return out
}

// TestUserdata_NoBashHeartbeat asserts Phase 79 removal — the legacy bash heartbeat
// function and its EXIT trap MUST NOT appear in compiled userdata for any sandbox.
func TestUserdata_NoBashHeartbeat(t *testing.T) {
	p := basePresenceProfile()
	rendered := renderPresenceUserdata(t, p)
	forbidden := []string{
		"_km_heartbeat()",
		"_KM_HEARTBEAT_PID",
		"trap 'kill -9 $_KM_HEARTBEAT_PID",
		`"source":"shell","detail":{}}\n`,
	}
	for _, s := range forbidden {
		if strings.Contains(rendered, s) {
			t.Errorf("Phase 79 regression: rendered userdata still contains %q", s)
		}
	}
}

// TestUserdata_KmAuditHookPreserved asserts we ONLY removed the heartbeat block —
// the per-command _km_audit PROMPT_COMMAND hook MUST still be installed.
func TestUserdata_KmAuditHookPreserved(t *testing.T) {
	p := basePresenceProfile()
	rendered := renderPresenceUserdata(t, p)
	required := []string{
		"_km_audit()",
		`PROMPT_COMMAND="_km_audit`,
		`"source":"shell","detail":{"command":`,
	}
	for _, s := range required {
		if !strings.Contains(rendered, s) {
			t.Errorf("Phase 79 over-removal regression: missing required string %q", s)
		}
	}
}

// TestUserdata_PresenceSidecarInstalled asserts all four positive Phase 79 additions
// are present in compiled userdata.
func TestUserdata_PresenceSidecarInstalled(t *testing.T) {
	p := basePresenceProfile()
	rendered := renderPresenceUserdata(t, p)
	required := []string{
		"sidecars/km-presence",
		"/etc/systemd/system/km-presence.service",
		"Description=Klankrmkr presence daemon",
		"ExecStart=/opt/km/bin/km-presence",
		`echo "[km-bootstrap] km-presence.service installed"`,
	}
	for _, s := range required {
		if !strings.Contains(rendered, s) {
			t.Errorf("Phase 79 install regression: missing required string %q", s)
		}
	}
}

// TestUserdata_PresenceEnabled_BothBranches renders userdata for both enforcement
// modes and asserts km-presence appears in the systemctl enable+restart command in each.
func TestUserdata_PresenceEnabled_BothBranches(t *testing.T) {
	for _, enforcement := range []string{"proxy", "ebpf", "both"} {
		enforcement := enforcement
		t.Run(enforcement, func(t *testing.T) {
			p := basePresenceProfile()
			p.Spec.Network.Enforcement = enforcement
			rendered := renderPresenceUserdata(t, p)

			// km-presence must appear in at least one systemctl enable line.
			if !strings.Contains(rendered, "km-presence") {
				t.Errorf("enforcement=%s: km-presence not found in userdata at all", enforcement)
			}
			// Verify it appears in an enable invocation.
			enableFound := false
			for _, line := range strings.Split(rendered, "\n") {
				if strings.HasPrefix(line, "systemctl enable ") && strings.Contains(line, "km-presence") {
					enableFound = true
					break
				}
			}
			if !enableFound {
				t.Errorf("enforcement=%s: km-presence not in any systemctl enable line", enforcement)
			}
			// Verify it appears in a restart invocation.
			restartFound := false
			for _, line := range strings.Split(rendered, "\n") {
				if strings.HasPrefix(line, "systemctl restart ") && strings.Contains(line, "km-presence") {
					restartFound = true
					break
				}
			}
			if !restartFound {
				t.Errorf("enforcement=%s: km-presence not in any systemctl restart line", enforcement)
			}
		})
	}
}

// TestUserdata_SlackInboundTouchesPresenceStamp asserts the new touch line appears
// inside the success branch of the slack-inbound-poller heredoc, and comes AFTER
// the "Turn complete" log but BEFORE the "agent run failed" WARN log.
func TestUserdata_SlackInboundTouchesPresenceStamp(t *testing.T) {
	// Render with SlackInboundEnabled=true via the profile.
	p := basePresenceProfile()
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Notification = &profile.NotificationSpec{
		Slack: &profile.NotificationSlackSpec{
			Enabled:    boolPtr(true),
			PerSandbox: boolPtr(true),
			Inbound:    &profile.NotificationSlackInboundSpec{Enabled: boolPtr(true)},
		},
	}
	rendered := renderPresenceUserdata(t, p)

	if !strings.Contains(rendered, "touch /run/km/last-slack-inbound") {
		t.Fatalf("Phase 79: missing touch line in slack-inbound-poller body")
	}

	// Verify ordering: success log < touch < failure WARN.
	successIdx := strings.Index(rendered, "Turn complete — session=")
	touchIdx := strings.Index(rendered, "touch /run/km/last-slack-inbound")
	failIdx := strings.Index(rendered, "WARN: agent run failed")
	if successIdx < 0 || touchIdx < 0 || failIdx < 0 {
		t.Fatalf("expected all 3 landmarks present (success=%d touch=%d fail=%d)",
			successIdx, touchIdx, failIdx)
	}
	if !(successIdx < touchIdx && touchIdx < failIdx) {
		t.Errorf("touch line is in the wrong place: success=%d touch=%d fail=%d (want success<touch<fail)",
			successIdx, touchIdx, failIdx)
	}
}

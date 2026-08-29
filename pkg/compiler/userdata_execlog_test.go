package compiler

import (
	"strings"
	"testing"
)

// execlogUnitBlock extracts just the km-execlog.service unit body from the
// full rendered userdata, so assertions about it can't be satisfied by one of
// the OTHER units (km-capture, km-tracing, the spot handler) that also carry
// an Environment=AWS_REGION= line.
func execlogUnitBlock(t *testing.T, out string) string {
	t.Helper()
	start := strings.Index(out, "cat > /etc/systemd/system/km-execlog.service")
	if start == -1 {
		t.Fatal("km-execlog.service unit not found in rendered userdata")
	}
	rest := out[start:]
	end := strings.Index(rest, "\nUNIT")
	if end == -1 {
		t.Fatal("km-execlog.service unit has no closing UNIT heredoc terminator")
	}
	return rest[:end]
}

// Exec capture is unconditional: it only reports what already happened, so its
// presence cannot widen a policy. Gating it on enforcement mode is what made
// Phase 131's flow recording dead under the default, and this test is what
// stops that being re-introduced.
func TestUserData_ExecLogProvisionedInEveryEnforcementMode(t *testing.T) {
	for _, mode := range []string{"proxy", "ebpf", "both"} {
		t.Run(mode, func(t *testing.T) {
			p := baseProfile()
			p.Spec.Network.Enforcement = mode
			out, err := generateUserData(p, "sb-execlog-"+mode, nil, "my-bucket", false, nil)
			if err != nil {
				t.Fatalf("generateUserData: %v", err)
			}
			for _, want := range []string{
				"km-execlog.service",
				"km-netpolicy execs-daemon",
				"systemctl enable --now km-execlog",
				"/var/lib/km/execs",
			} {
				if !contains(out, want) {
					t.Errorf("mode %s: userdata missing %q", mode, want)
				}
			}
		})
	}
}

func TestUserData_ExecStoreIsRootOnly(t *testing.T) {
	// argv is recorded unredacted and includes root's. A sandbox-readable store
	// would be an information leak on exactly the unprivileged profiles that
	// are supposed to be the tighter ones.
	p := baseProfile()
	out, err := generateUserData(p, "sb-execlog-root-only", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if !contains(out, "chmod 700 /var/lib/km/execs") {
		t.Error("exec store directory must be 0700 root-only")
	}
	if contains(out, "chmod 1777 /var/lib/km/execs") || contains(out, "chmod 777 /var/lib/km/execs") {
		t.Error("exec store must not be world-writable like the flow store")
	}
}

func TestUserData_ExecLogUnitCarriesRegionAndSavesOnStop(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-execlog-region", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	// Scoped to just this unit's body — km-capture.service, km-tracing.service,
	// and the spot handler ALL already carry an Environment=AWS_REGION= line,
	// so a whole-file contains() here would pass even if this unit's own line
	// were deleted.
	unit := execlogUnitBlock(t, out)

	// Without AWS_REGION the SDK fails with "Invalid region: region was not a
	// valid DNS name", which reads as a bucket problem. That was the km-capture
	// bug fixed in 216d4664 and it is not being re-introduced here.
	if !contains(unit, "Environment=AWS_REGION=") {
		t.Error("km-execlog.service must carry AWS_REGION")
	}
	// Must be ExecStopPost, not ExecStop: for Type=simple, ExecStop runs
	// BEFORE the main process is signalled, so a plain ExecStop would upload
	// the store before the daemon's SIGTERM-triggered drain ever writes its
	// buffered tail to disk — defeating Task 5's drain fix outright.
	if !contains(unit, "ExecStopPost=/opt/km/bin/km-netpolicy execs save") {
		t.Error("a graceful stop must save the trace, AFTER the daemon has drained, without anyone remembering")
	}
	if contains(unit, "\nExecStop=") {
		t.Error("km-execlog.service must not use plain ExecStop — it races the daemon's SIGTERM drain")
	}
	// A box where the tracer genuinely cannot load should show one failed unit,
	// not restart every few seconds for the life of the sandbox. Scoped to the
	// unit body — and this also proves the directive landed under [Unit],
	// since systemd v230 silently drops it as an unknown key under [Service].
	if !contains(unit, "StartLimitBurst") {
		t.Error("km-execlog.service must give up rather than crash-loop forever")
	}
	if idx := strings.Index(unit, "[Service]"); idx != -1 && strings.Index(unit, "StartLimitBurst") > idx {
		t.Error("StartLimitBurst must be a [Unit] directive, not a [Service] one — systemd silently drops it there since v230")
	}
}

// Tracepoint attach resolves a tracepoint id through tracefs. AL2023 and the
// Ubuntu AMIs normally mount it, but a baked AMI need not, and the failure
// reads as a permissions error rather than a missing filesystem.
func TestUserData_MountsTracefsBeforeTracing(t *testing.T) {
	p := baseProfile()
	out, err := generateUserData(p, "sb-execlog-tracefs", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	if !contains(out, "/sys/kernel/tracing") {
		t.Error("userdata must ensure tracefs is mounted for tracepoint attach")
	}
}

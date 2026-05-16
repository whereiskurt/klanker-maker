package compiler

// Phase 79.1: audit-pipe FIFO recreation on resumed sandboxes — Layer 1
// (systemd-tmpfiles drop-in) userdata regression tests.
//
// These tests assert the rendered EC2 userdata contains a
// /usr/lib/tmpfiles.d/km.conf heredoc that re-creates the audit-pipe FIFO
// with correct ownership on every boot (including EC2 stop+resume), and
// that the heredoc appears BEFORE the km-audit-log.service heredoc so the
// FIFO is in place before the sidecar starts.
//
// Authoritative design: .planning/phases/79.1-.../79.1-RESEARCH.md § Q4.
//
// RED today; turns GREEN when Plan 02 lands the tmpfiles.d heredoc in
// pkg/compiler/userdata.go between the existing mkfifo block and the
// km-audit-log.service heredoc.

import (
	"regexp"
	"strings"
	"testing"
)

// TestUserdata_TmpfilesDDropInPresent: criterion L1-TMPFILES.
// Rendered userdata MUST install the tmpfiles.d drop-in with p+ and
// km-sidecar ownership.
func TestUserdata_TmpfilesDDropInPresent(t *testing.T) {
	p := basePresenceProfile()
	rendered := renderPresenceUserdata(t, p)
	required := []string{
		"/usr/lib/tmpfiles.d/km.conf",
		"p+ /run/km/audit-pipe",
		"km-sidecar km-sidecar",
	}
	for _, s := range required {
		if !strings.Contains(rendered, s) {
			t.Errorf("Phase 79.1 L1-TMPFILES: missing required substring %q in rendered userdata", s)
		}
	}
}

// TestUserdata_TmpfilesDDropInBeforeKmAuditLogUnit: criterion L1-ORDER.
// The tmpfiles.d heredoc MUST appear earlier in the rendered userdata than
// the km-audit-log.service unit-file heredoc. This guarantees /etc/systemd
// resolves the FIFO entry before the unit is registered + enabled.
func TestUserdata_TmpfilesDDropInBeforeKmAuditLogUnit(t *testing.T) {
	p := basePresenceProfile()
	rendered := renderPresenceUserdata(t, p)
	tmpfilesIdx := strings.Index(rendered, "/usr/lib/tmpfiles.d/km.conf")
	unitIdx := strings.Index(rendered, "/etc/systemd/system/km-audit-log.service")
	if tmpfilesIdx == -1 {
		t.Fatalf("Phase 79.1 L1-ORDER: tmpfiles.d heredoc not found in rendered userdata")
	}
	if unitIdx == -1 {
		t.Fatalf("Phase 79.1 L1-ORDER: km-audit-log.service heredoc not found in rendered userdata")
	}
	if tmpfilesIdx >= unitIdx {
		t.Errorf("Phase 79.1 L1-ORDER: tmpfiles.d heredoc (offset %d) MUST appear before km-audit-log.service heredoc (offset %d)", tmpfilesIdx, unitIdx)
	}
}

// TestUserdata_TmpfilesDDropInMode: criterion L1-MODE.
// The two declarative lines inside km.conf MUST have correct mode + owner +
// group columns. Tolerant of column-alignment whitespace via regex.
func TestUserdata_TmpfilesDDropInMode(t *testing.T) {
	p := basePresenceProfile()
	rendered := renderPresenceUserdata(t, p)
	// p+ line: type p+, path /run/km/audit-pipe, mode 0666, user km-sidecar, group km-sidecar.
	pipeRe := regexp.MustCompile(`(?m)^p\+\s+/run/km/audit-pipe\s+0666\s+km-sidecar\s+km-sidecar\s+-\s+-\s*$`)
	if !pipeRe.MatchString(rendered) {
		t.Errorf("Phase 79.1 L1-MODE: tmpfiles.d entry for /run/km/audit-pipe missing or has wrong columns (expected: p+ /run/km/audit-pipe 0666 km-sidecar km-sidecar - -)")
	}
	// d line: type d, path /run/km, mode 0755, user km-sidecar, group km-sidecar.
	dirRe := regexp.MustCompile(`(?m)^d\s+/run/km\s+0755\s+km-sidecar\s+km-sidecar\s+-\s+-\s*$`)
	if !dirRe.MatchString(rendered) {
		t.Errorf("Phase 79.1 L1-MODE: tmpfiles.d entry for /run/km directory missing or has wrong columns (expected: d /run/km 0755 km-sidecar km-sidecar - -)")
	}
}

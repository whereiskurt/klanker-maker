package compiler

// reply_mode: none — the poller must (a) parse the field, (b) swap the "post with
// km-h1 (REQUIRED)" preamble block for the do-NOT-post block, and (c) gate its own
// two best-effort km-h1 comment sites (codex-missing notice, Phase 106 resume hint).
// The DEFAULT rendering must keep every pre-existing line so a reply_mode-less
// envelope behaves exactly as before.
//
// These assert on executable bash, not comments (memory:
// project_userdata_template_invisible_to_go_build).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func renderH1EnabledPoller(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "testdata", h1BaselineProfile))
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	enabled := true
	if p.Spec.Notification == nil {
		p.Spec.Notification = &profile.NotificationSpec{}
	}
	p.Spec.Notification.H1 = &profile.NotificationH1Spec{
		Inbound: &profile.NotificationH1InboundSpec{Enabled: &enabled},
	}
	got, err := generateUserData(p, "sb-h1-replymode", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	start := strings.Index(got, "cat > /opt/km/bin/km-h1-inbound-poller << 'H1INBOUND'")
	end := strings.Index(got, "\nH1INBOUND\n")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("could not isolate the km-h1-inbound-poller heredoc")
	}
	return got[start:end]
}

func TestH1Poller_ReplyModeNone_IsHonoured(t *testing.T) {
	poller := renderH1EnabledPoller(t)

	mustContain := []string{
		// (a) parsed from the envelope
		`REPLY_MODE=$(echo "$BODY" | jq -r '.reply_mode // empty' 2>/dev/null || true)`,
		// (b) the swap is a real bash branch on the value
		`if [ "$REPLY_MODE" = "none" ]; then`,
		`Do NOT post anything to HackerOne for this trigger (no km-h1 comment).`,
		`A human will decide whether it reaches HackerOne.`,
		// the default branch keeps the original REQUIRED text verbatim
		`--- Posting your response (REQUIRED) ---`,
		`Do NOT only print your answer — it is discarded unless you post it with km-h1.`,
		// (c) both poller-owned comment sites are gated on the same value
		`[ "$REPLY_MODE" != "none" ]`,
	}
	for _, s := range mustContain {
		if !strings.Contains(poller, s) {
			t.Errorf("poller missing %q", s)
		}
	}

	// Every km-h1 comment invocation the poller itself makes must sit inside a
	// reply_mode gate. Count the sites and the gates.
	sites := strings.Count(poller, `/opt/km/bin/km-h1 comment --report "$REPORT_ID"`)
	gates := strings.Count(poller, `[ "$REPLY_MODE" != "none" ]`)
	if sites != 2 {
		t.Fatalf("expected exactly 2 poller-owned km-h1 comment sites (codex-missing, resume hint); found %d — add a gate for the new one and update this test", sites)
	}
	if gates < sites {
		t.Errorf("poller-owned km-h1 comment sites=%d but reply_mode gates=%d; every site must be gated", sites, gates)
	}
}

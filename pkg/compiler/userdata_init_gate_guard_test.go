package compiler

import (
	"os"
	"strings"
	"testing"
)

// Every inbound poller heredoc must carry the init gate before its first
// receive-message, with the agent-on-PATH check inside the gate, and the
// exit-127 visibility rule after its "agent run failed" line. Name-agnostic: a
// fifth poller added later is covered without an edit.
func TestEveryInboundPollerHasInitGateAnd127Rule(t *testing.T) {
	body, err := os.ReadFile("userdata.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	heredocs := []string{}
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "cat > /opt/km/bin/km-") && strings.Contains(l, "-inbound-poller << '") {
			heredocs = append(heredocs, l)
		}
	}
	if len(heredocs) < 4 {
		t.Fatalf("expected at least 4 inbound poller heredocs, found %d", len(heredocs))
	}
	for _, h := range heredocs {
		name := h[strings.Index(h, "<< '")+4 : len(h)-1]
		start := strings.Index(src, h)
		end := strings.Index(src[start:], "\n"+name+"\n")
		if end < 0 {
			t.Fatalf("%s: terminator not found", name)
		}
		poller := src[start : start+end]
		gate := strings.Index(poller, "# --- km-init-gate ---")
		gateEnd := strings.Index(poller, "# --- end km-init-gate ---")
		recv := strings.Index(poller, "receive-message")
		if gate < 0 || gateEnd < 0 || recv < 0 || gate > recv || gateEnd > recv {
			t.Errorf("%s: init gate missing or placed after the first receive-message", name)
		}
		check := strings.Index(poller, `command -v "$AGENT"`)
		if check < 0 || check < gate || check > gateEnd {
			t.Errorf("%s: the agent-on-PATH check must sit inside the init gate, after the wait", name)
		}
		failed := strings.Index(poller, "agent run failed (exit $RUN_EXIT)")
		rule := strings.Index(poller, "--visibility-timeout 60")
		if failed < 0 || rule < 0 || rule < failed {
			t.Errorf("%s: exit-127 visibility rule missing or placed before the failure line", name)
		}
	}
}

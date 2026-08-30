package flowlog_test

import (
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/flowlog"
)

func recAt(min int, verdict, host string) flowlog.Record {
	return flowlog.Record{
		TS:      time.Date(2026, 8, 28, 14, min, 0, 0, time.UTC),
		Src:     flowlog.SrcHTTP,
		Verdict: verdict,
		Host:    host,
	}
}

func TestSummarize_SeparatesAllowedFromDenied(t *testing.T) {
	c := flowlog.Summarize([]flowlog.Record{
		recAt(0, flowlog.VerdictAllow, "api.github.com"),
		recAt(1, flowlog.VerdictAllow, "api.github.com"),
		recAt(2, flowlog.VerdictDeny, "evil.example.com"),
	})

	if len(c.Allowed) != 1 || c.Allowed[0].Host != "api.github.com" {
		t.Fatalf("allowed: %+v", c.Allowed)
	}
	if c.Allowed[0].Count != 2 {
		t.Errorf("want count 2, got %d", c.Allowed[0].Count)
	}
	if len(c.Denied) != 1 || c.Denied[0].Host != "evil.example.com" {
		t.Fatalf("denied: %+v", c.Denied)
	}
}

func TestSummarize_DeniedNeverAppearsInAllowed(t *testing.T) {
	// A host both allowed and later denied must still be pinnable — it WAS
	// reachable. But a host only ever denied must never leak into Allowed,
	// because Allowed is what pin intersects against.
	c := flowlog.Summarize([]flowlog.Record{
		recAt(0, flowlog.VerdictDeny, "blocked.example"),
	})
	for _, d := range c.Allowed {
		if d.Host == "blocked.example" {
			t.Fatal("a never-allowed host leaked into the pinnable set")
		}
	}
}

func TestSummarize_RedirectCountsAsAllowed(t *testing.T) {
	// MITM interception is how an allowed host is reached, not a block.
	c := flowlog.Summarize([]flowlog.Record{
		recAt(0, flowlog.VerdictRedirect, "bedrock-runtime.us-east-1.amazonaws.com"),
	})
	if len(c.Allowed) != 1 {
		t.Fatalf("want redirect treated as allowed, got %+v", c.Allowed)
	}
}

func TestSummarize_AddressOnlyAndNamedAreDistinctDests(t *testing.T) {
	// An eBPF record whose address the resolver could not name stays IP-only.
	// It must not collapse into a named destination, because an operator
	// deciding what to pin has to see that something went somewhere unnamed.
	c := flowlog.Summarize([]flowlog.Record{
		{TS: time.Now(), Src: flowlog.SrcEBPF, Verdict: flowlog.VerdictAllow, Host: "api.github.com", Addr: "140.82.113.6"},
		{TS: time.Now(), Src: flowlog.SrcEBPF, Verdict: flowlog.VerdictAllow, Addr: "203.0.113.9"},
	})
	if len(c.Allowed) != 2 {
		t.Fatalf("want 2 distinct destinations, got %+v", c.Allowed)
	}
}

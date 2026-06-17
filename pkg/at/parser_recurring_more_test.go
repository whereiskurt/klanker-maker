package at_test

import (
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/at"
)

// TestParseRecurring_FlexibleForms covers the "smart and flexible" recurring
// inputs: bare unitary cadences, the EventBridge singular/plural rate rule, days,
// and verbatim cron()/rate() passthrough — alongside the pre-existing forms.
func TestParseRecurring_FlexibleForms(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct{ in, want string }{
		{"every hour", "rate(1 hour)"},
		{"hourly", "rate(1 hour)"},
		{"every minute", "rate(1 minute)"},
		{"every day", "rate(1 day)"},
		{"every 1 hour", "rate(1 hour)"},  // singular rule (was the buggy rate(1 hours))
		{"every 15 minutes", "rate(15 minutes)"},
		{"every 2 hours", "rate(2 hours)"},
		{"every 3 days", "rate(3 days)"},
		{"cron(0/15 * * * ? *)", "cron(0/15 * * * ? *)"}, // verbatim passthrough
		{"rate(45 minutes)", "rate(45 minutes)"},
		{"every day at 15:00", "cron(0 15 * * ? *)"},   // still routes to cron
		{"every monday at 8:30am", "cron(30 8 ? * 2 *)"},
	}
	for _, c := range cases {
		spec, err := at.Parse(c.in, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !spec.IsRecurring {
			t.Errorf("%q: expected IsRecurring=true", c.in)
		}
		if spec.Expression != c.want {
			t.Errorf("%q: Expression=%q, want %q", c.in, spec.Expression, c.want)
		}
	}
}

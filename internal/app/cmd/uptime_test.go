package cmd_test

import (
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// TestFormatUptime covers the three display bands + zero/negative guard.
func TestFormatUptime(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		createdAt time.Time
		want      string
	}{
		// < 1 hour — minutes only
		{name: "8 minutes", createdAt: now.Add(-8 * time.Minute), want: "8m"},
		{name: "0 minutes / zero", createdAt: now, want: "0m"},
		{name: "negative (future)", createdAt: now.Add(5 * time.Minute), want: "0m"},
		{name: "59 minutes", createdAt: now.Add(-59 * time.Minute), want: "59m"},

		// 1h–<1d — hours+minutes (drop "m" segment when M==0)
		{name: "3h12m", createdAt: now.Add(-(3*time.Hour + 12*time.Minute)), want: "3h12m"},
		{name: "3h exactly", createdAt: now.Add(-3 * time.Hour), want: "3h"},
		{name: "23h59m", createdAt: now.Add(-(23*time.Hour + 59*time.Minute)), want: "23h59m"},
		{name: "1h0m", createdAt: now.Add(-1 * time.Hour), want: "1h"},

		// >= 1 day — days+hours (drop "h" segment when H==0)
		{name: "2d4h", createdAt: now.Add(-(2*24*time.Hour + 4*time.Hour)), want: "2d4h"},
		{name: "1d exactly", createdAt: now.Add(-24 * time.Hour), want: "1d"},
		{name: "3d0h", createdAt: now.Add(-3 * 24 * time.Hour), want: "3d"},
		{name: "7d2h", createdAt: now.Add(-(7*24*time.Hour + 2*time.Hour)), want: "7d2h"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cmd.FormatUptime(tc.createdAt)
			if got != tc.want {
				t.Errorf("FormatUptime(%v) = %q, want %q", tc.createdAt, got, tc.want)
			}
		})
	}
}

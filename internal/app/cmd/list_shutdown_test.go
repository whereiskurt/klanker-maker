package cmd

import (
	"testing"
	"time"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// A sandbox goes away at whichever comes first: TTL expiry or the idle reaper.
// Showing TTL alone is actively misleading — a box with 19h of TTL left can be
// reaped in 2h by the idle timer, which is what the operator actually needs to
// plan around.
func TestShutdownLabel(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }

	cases := []struct {
		name string
		rec  kmaws.SandboxRecord
		want string
	}{
		{
			name: "idle sooner than ttl -> idle wins and says so",
			rec: kmaws.SandboxRecord{
				Status: "running", TTLExpiry: at(19 * time.Hour), IdleRemaining: "2h0m0s remaining",
			},
			want: "2h idle",
		},
		{
			name: "ttl sooner than idle -> ttl wins",
			rec: kmaws.SandboxRecord{
				Status: "running", TTLExpiry: at(30 * time.Minute), IdleRemaining: "2h0m0s remaining",
			},
			want: "30m ttl",
		},
		{
			name: "no idle timeout configured -> ttl alone",
			rec:  kmaws.SandboxRecord{Status: "running", TTLExpiry: at(90 * time.Minute)},
			want: "1h30m ttl",
		},
		{
			// A stopped or paused box is not being idle-reaped -- it is already
			// stopped. Only the TTL still runs.
			name: "paused -> idle does not apply",
			rec: kmaws.SandboxRecord{
				Status: "paused", TTLExpiry: at(19 * time.Hour), IdleRemaining: "2h0m0s remaining",
			},
			want: "19h ttl",
		},
		{
			name: "expired ttl",
			rec:  kmaws.SandboxRecord{Status: "running", TTLExpiry: at(-time.Minute)},
			want: "expired",
		},
		{
			name: "idle imminent",
			rec: kmaws.SandboxRecord{
				Status: "running", TTLExpiry: at(5 * time.Hour), IdleRemaining: "imminent",
			},
			want: "imminent idle",
		},
		{
			name: "nothing known",
			rec:  kmaws.SandboxRecord{Status: "running"},
			want: "-",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shutdownLabel(tc.rec); got != tc.want {
				t.Errorf("shutdownLabel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A profile with spec.lifecycle.ttl: 86000h (a "never expire" idiom) used to
// render as "85999h42m ttl" — 14 characters in an 11-wide column, pushing UP
// and AUTH off their headers on every row. Past a day the hours stop being
// information and start being noise: roll to days, and drop the trailing unit
// that is under 1% of the value, the same shape formatUptime already uses.
func TestCompactDuration_LargeValuesRollToDays(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{46 * time.Minute, "46m"},
		{90 * time.Minute, "1h30m"},
		{23*time.Hour + 59*time.Minute, "23h59m"},
		{24 * time.Hour, "1d"},
		{25*time.Hour + 30*time.Minute, "1d1h"}, // minutes dropped past a day
		{6*24*time.Hour + 23*time.Hour, "6d23h"},
		{7 * 24 * time.Hour, "7d"},
		{364 * 24 * time.Hour, "364d"}, // hours dropped past a week
		{365 * 24 * time.Hour, "1y"},
		{400 * 24 * time.Hour, "1y35d"},
		{(2*365 + 364) * 24 * time.Hour, "2y364d"}, // days kept under 3y
		{(3*365 + 200) * 24 * time.Hour, "3y"},     // days dropped from 3y on
		{86000 * time.Hour, "9y"},                  // the "never expire" idiom
		{10 * 365 * 24 * time.Hour, "10y"},
	}
	for _, tc := range cases {
		if got := compactDuration(tc.d); got != tc.want {
			t.Errorf("compactDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
		if got := compactDuration(tc.d); len(got) > 7 {
			t.Errorf("compactDuration(%v) = %q is %d chars; the SHUTDOWN column is 11 wide and needs room for \" ttl\"", tc.d, got, len(got))
		}
	}
}

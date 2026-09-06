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
			want: "2h0m idle",
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
			want: "19h0m ttl",
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

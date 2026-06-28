package cmd_test

import (
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/pkg/capacity"
)

// TestCapacityReport drives ComputeCapacityVerdict with each input scenario and asserts:
// (a) each expected verdict is produced, and
// (b) "available" is never emitted.
func TestCapacityReport(t *testing.T) {
	t.Parallel()

	now := time.Now()
	freshICE := now.Add(-5 * time.Minute)   // within 45-min window
	staleICE := now.Add(-60 * time.Minute)  // outside 45-min window
	successAt := now.Add(-2 * time.Hour)

	cases := []struct {
		name          string
		offered       bool
		isGPU         bool
		quotaHeadroom float64
		quotaAvail    bool
		entry         *capacity.CapacityEntry
		want          string
	}{
		{
			name:    "not-offered: AZ not in offerings",
			offered: false,
			isGPU:   false,
			entry:   &capacity.CapacityEntry{},
			want:    cmd.VerdictNotOffered,
		},
		{
			name:          "quota-blocked: GPU + headroom=0",
			offered:       true,
			isGPU:         true,
			quotaHeadroom: 0,
			quotaAvail:    true,
			entry:         &capacity.CapacityEntry{},
			want:          cmd.VerdictQuotaBlocked,
		},
		{
			name:          "recently-dry: GPU + fresh ICE",
			offered:       true,
			isGPU:         true,
			quotaHeadroom: 96,
			quotaAvail:    true,
			entry: &capacity.CapacityEntry{
				LastICEAt: &freshICE,
			},
			want: cmd.VerdictRecentlyDry,
		},
		{
			name:    "recently-dry: non-GPU + fresh ICE",
			offered: true,
			isGPU:   false,
			entry: &capacity.CapacityEntry{
				LastICEAt: &freshICE,
			},
			want: cmd.VerdictRecentlyDry,
		},
		{
			name:          "likely: GPU + quota OK + last-success",
			offered:       true,
			isGPU:         true,
			quotaHeadroom: 96,
			quotaAvail:    true,
			entry: &capacity.CapacityEntry{
				LastSuccessAt: &successAt,
			},
			want: cmd.VerdictLikely,
		},
		{
			name:    "likely: non-GPU + last-success",
			offered: true,
			isGPU:   false,
			entry: &capacity.CapacityEntry{
				LastSuccessAt: &successAt,
			},
			want: cmd.VerdictLikely,
		},
		{
			name:    "likely: offered + no signal at all",
			offered: true,
			isGPU:   false,
			entry:   &capacity.CapacityEntry{},
			want:    cmd.VerdictLikely,
		},
		{
			name:    "likely: offered + stale ICE (expired window)",
			offered: true,
			isGPU:   false,
			entry: &capacity.CapacityEntry{
				LastICEAt: &staleICE,
			},
			want: cmd.VerdictLikely,
		},
		{
			name:          "quota-blocked takes precedence over recently-dry",
			offered:       true,
			isGPU:         true,
			quotaHeadroom: 0,
			quotaAvail:    true,
			entry: &capacity.CapacityEntry{
				LastICEAt: &freshICE,
			},
			want: cmd.VerdictQuotaBlocked,
		},
		{
			name:          "not-offered takes precedence over quota-blocked",
			offered:       false,
			isGPU:         true,
			quotaHeadroom: 0,
			quotaAvail:    true,
			entry:         &capacity.CapacityEntry{},
			want:          cmd.VerdictNotOffered,
		},
	}

	allVerdicts := []string{
		cmd.VerdictNotOffered,
		cmd.VerdictQuotaBlocked,
		cmd.VerdictRecentlyDry,
		cmd.VerdictLikely,
		cmd.VerdictUnknown,
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			verdict := cmd.ComputeCapacityVerdict(tc.offered, tc.isGPU, tc.quotaHeadroom, tc.quotaAvail, tc.entry)

			// Assert expected verdict.
			if verdict != tc.want {
				t.Errorf("ComputeCapacityVerdict() = %q, want %q", verdict, tc.want)
			}

			// Assert "available" is NEVER emitted (key invariant).
			if strings.EqualFold(verdict, "available") {
				t.Errorf("verdict %q is forbidden — 'available' must never be emitted", verdict)
			}

			// Assert verdict is a known value.
			known := false
			for _, v := range allVerdicts {
				if verdict == v {
					known = true
					break
				}
			}
			if !known {
				t.Errorf("verdict %q is not a recognized verdict value", verdict)
			}
		})
	}
}

// TestCapacityVerdictNeverAvailable exhaustively checks that no verdict constant
// equals "available" (regression guard).
func TestCapacityVerdictNeverAvailable(t *testing.T) {
	t.Parallel()

	constants := []string{
		cmd.VerdictNotOffered,
		cmd.VerdictQuotaBlocked,
		cmd.VerdictRecentlyDry,
		cmd.VerdictLikely,
		cmd.VerdictUnknown,
	}
	for _, c := range constants {
		if strings.EqualFold(c, "available") {
			t.Errorf("verdict constant %q must not be 'available'", c)
		}
	}
}

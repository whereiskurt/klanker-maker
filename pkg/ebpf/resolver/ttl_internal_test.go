package resolver

import (
	"testing"
	"time"
)

func TestClampTTL(t *testing.T) {
	floor := 10 * time.Minute
	cases := []struct {
		name   string
		ttlSec uint32
		floor  time.Duration
		want   time.Duration
	}{
		{"below floor clamps up", 5, floor, floor},
		{"zero clamps up", 0, floor, floor},
		{"equal to floor passes through", 600, floor, floor}, // 600s == 10m
		{"above floor passes through", 1200, floor, 1200 * time.Second},
		{"custom small floor", 5, 90 * time.Second, 90 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := clampTTL(tc.ttlSec, tc.floor)
			if got != tc.want {
				t.Fatalf("clampTTL(%d, %v) = %v, want %v", tc.ttlSec, tc.floor, got, tc.want)
			}
		})
	}
}

func TestDefaultMinIPLifetime(t *testing.T) {
	if defaultMinIPLifetime != 10*time.Minute {
		t.Fatalf("defaultMinIPLifetime = %v, want 10m", defaultMinIPLifetime)
	}
}

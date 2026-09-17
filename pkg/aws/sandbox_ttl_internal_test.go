package aws

import (
	"testing"
	"time"
)

// A "never expire" ttl of 86000h rendered as "85999h42m" in km list --wide,
// --json and the operator email digest. Past a day the hours are noise; past
// a week only the days are worth a column. Sub-day values keep their existing
// shape so nothing that parses "1h23m" today changes.
func TestComputeTTLRemaining_LargeValuesRollToDays(t *testing.T) {
	now := time.Now()
	at := func(d time.Duration) *time.Time { x := now.Add(d + 500*time.Millisecond); return &x }
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Minute, "expired"},
		{45 * time.Second, "45s"},
		{5*time.Minute + 7*time.Second, "5m07s"},
		{90 * time.Minute, "1h30m"},
		{23*time.Hour + 59*time.Minute, "23h59m"},
		{24 * time.Hour, "1d"},
		{25*time.Hour + 30*time.Minute, "1d1h"},
		{6*24*time.Hour + 23*time.Hour, "6d23h"},
		{364 * 24 * time.Hour, "364d"},
		{365 * 24 * time.Hour, "1y"},
		{400 * 24 * time.Hour, "1y35d"},
		{86000 * time.Hour, "9y298d"},
	}
	for _, tc := range cases {
		if got := computeTTLRemaining(at(tc.d)); got != tc.want {
			t.Errorf("computeTTLRemaining(+%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
	if got := computeTTLRemaining(nil); got != "" {
		t.Errorf("nil expiry = %q, want empty", got)
	}
}

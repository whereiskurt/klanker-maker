package cmd

import (
	"testing"
	"time"
)

// TestSleepSeamZeroedInTests proves the package's `sleep` seam is overridden to
// a no-op for the whole test binary (via TestMain). Production code calls
// `sleep(d)` instead of `time.Sleep(d)`, so this removes real wall-clock waits
// from the cmd test suite (which was ~8 min, ~400s of it in sleeps) WITHOUT
// changing any logic — the sleeps still "happen", they just return instantly.
func TestSleepSeamZeroedInTests(t *testing.T) {
	start := time.Now()
	sleep(3 * time.Second)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("sleep seam not zeroed in tests: a 3s sleep took %v "+
			"(TestMain should set sleep to a no-op)", d)
	}
}

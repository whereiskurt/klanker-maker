package cmd

import (
	"testing"
)

// TestSpotRateStaticTableKnownTypes verifies that staticSpotRate returns non-zero rates
// for well-known instance types (BUDG-03 gap closure).
func TestSpotRateStaticTableKnownTypes(t *testing.T) {
	knownTypes := []string{
		"t3.micro",
		"t3.small",
		"t3.medium",
		"t3.large",
		"t3.xlarge",
		"c5.large",
		"c5.xlarge",
		"m5.large",
		"m5.xlarge",
		"r5.large",
	}
	for _, it := range knownTypes {
		rate := staticSpotRate(it)
		if rate <= 0 {
			t.Errorf("staticSpotRate(%q) = %f, want > 0", it, rate)
		}
	}
}

// TestSpotRateStaticTableUnknownFallback verifies that an unknown instance type
// returns a conservative non-zero minimum fallback.
func TestSpotRateStaticTableUnknownFallback(t *testing.T) {
	rate := staticSpotRate("p4d.24xlarge") // not in table
	if rate <= 0 {
		t.Errorf("staticSpotRate(unknown) = %f, want > 0 (conservative fallback)", rate)
	}
}

// TestSpotRateStaticTableOrdering verifies that larger instance types cost more than smaller ones.
func TestSpotRateStaticTableOrdering(t *testing.T) {
	micro := staticSpotRate("t3.micro")
	large := staticSpotRate("t3.large")
	if micro >= large {
		t.Errorf("expected t3.micro (%f) < t3.large (%f)", micro, large)
	}
}

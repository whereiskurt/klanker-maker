package cmd

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/capacity"
)

// TestAZSweepLoop verifies sweepDecision — the classify-and-retry decision function
// used by the km create AZ-sweep loop (runCreate, internal/app/cmd/create.go).
//
// sweepDecision(class, attempt, maxAttempts) returns:
//   - retry=true, failFast=false: iterate-class error with remaining AZ attempts
//   - retry=false, failFast=true: quota/auth/invalid — fail-fast immediately
//   - retry=false, failFast=false: iterate-class exhausted or unknown — print summary
func TestAZSweepLoop(t *testing.T) {
	t.Run("ICE_with_remaining_AZs_returns_retry", func(t *testing.T) {
		retry, failFast := sweepDecision(capacity.ClassICE, 0, 3)
		if !retry || failFast {
			t.Errorf("sweepDecision(ClassICE, 0, 3) = (retry=%v, failFast=%v), want (true, false)", retry, failFast)
		}
	})

	t.Run("SpotPrice_with_remaining_AZs_returns_retry", func(t *testing.T) {
		retry, failFast := sweepDecision(capacity.ClassSpotPrice, 1, 3)
		if !retry || failFast {
			t.Errorf("sweepDecision(ClassSpotPrice, 1, 3) = (retry=%v, failFast=%v), want (true, false)", retry, failFast)
		}
	})

	t.Run("SpotLimit_with_remaining_AZs_returns_retry", func(t *testing.T) {
		retry, failFast := sweepDecision(capacity.ClassSpotLimit, 0, 2)
		if !retry || failFast {
			t.Errorf("sweepDecision(ClassSpotLimit, 0, 2) = (retry=%v, failFast=%v), want (true, false)", retry, failFast)
		}
	})

	t.Run("WaiterTimeout_with_remaining_AZs_returns_retry", func(t *testing.T) {
		retry, failFast := sweepDecision(capacity.ClassWaiterTimeout, 0, 2)
		if !retry || failFast {
			t.Errorf("sweepDecision(ClassWaiterTimeout, 0, 2) = (retry=%v, failFast=%v), want (true, false)", retry, failFast)
		}
	})

	t.Run("ICE_on_last_AZ_no_retry_no_failfast", func(t *testing.T) {
		retry, failFast := sweepDecision(capacity.ClassICE, 2, 3)
		if retry || failFast {
			t.Errorf("sweepDecision(ClassICE, 2, 3) = (retry=%v, failFast=%v), want (false, false)", retry, failFast)
		}
	})

	t.Run("ICE_maxAttempts1_no_retry_no_failfast", func(t *testing.T) {
		// on-demand: maxAttempts=1, so attempt=0 is already the last
		retry, failFast := sweepDecision(capacity.ClassICE, 0, 1)
		if retry || failFast {
			t.Errorf("sweepDecision(ClassICE, 0, 1) = (retry=%v, failFast=%v), want (false, false)", retry, failFast)
		}
	})

	t.Run("Quota_fail_fast_regardless_of_remaining_AZs", func(t *testing.T) {
		retry, failFast := sweepDecision(capacity.ClassQuota, 0, 3)
		if retry || !failFast {
			t.Errorf("sweepDecision(ClassQuota, 0, 3) = (retry=%v, failFast=%v), want (false, true)", retry, failFast)
		}
	})

	t.Run("Auth_fail_fast_regardless_of_remaining_AZs", func(t *testing.T) {
		retry, failFast := sweepDecision(capacity.ClassAuth, 0, 3)
		if retry || !failFast {
			t.Errorf("sweepDecision(ClassAuth, 0, 3) = (retry=%v, failFast=%v), want (false, true)", retry, failFast)
		}
	})

	t.Run("Invalid_fail_fast_regardless_of_remaining_AZs", func(t *testing.T) {
		retry, failFast := sweepDecision(capacity.ClassInvalid, 0, 3)
		if retry || !failFast {
			t.Errorf("sweepDecision(ClassInvalid, 0, 3) = (retry=%v, failFast=%v), want (false, true)", retry, failFast)
		}
	})

	t.Run("Unknown_error_no_retry_no_failfast", func(t *testing.T) {
		// Unknown errors are not retriable but also not fail-fast:
		// they fall through to the generic error message path.
		retry, failFast := sweepDecision(capacity.ClassUnknown, 0, 3)
		if retry || failFast {
			t.Errorf("sweepDecision(ClassUnknown, 0, 3) = (retry=%v, failFast=%v), want (false, false)", retry, failFast)
		}
	})

	t.Run("Quota_fail_fast_even_on_first_attempt", func(t *testing.T) {
		// A quota wall must not burn AZ attempts — fail-fast on attempt 0.
		retry, failFast := sweepDecision(capacity.ClassQuota, 0, 1)
		if retry || !failFast {
			t.Errorf("sweepDecision(ClassQuota, 0, 1) = (retry=%v, failFast=%v), want (false, true)", retry, failFast)
		}
	})
}

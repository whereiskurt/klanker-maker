package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/capacity"
)

// fakeCapacityStore is a test-only CapacityStore that records which methods
// were called and with which arguments. It can be configured to return an
// error to verify best-effort behaviour (error never propagates to the caller).
type fakeCapacityStore struct {
	recordSuccessCalls []struct{ InstanceType, AZ string }
	recordICECalls     []struct{ InstanceType, AZ string }
	returnErr          error // when set, RecordSuccess and RecordICE return this
}

func (f *fakeCapacityStore) RecordSuccess(_ context.Context, instanceType, az string) error {
	f.recordSuccessCalls = append(f.recordSuccessCalls, struct{ InstanceType, AZ string }{instanceType, az})
	return f.returnErr
}

func (f *fakeCapacityStore) RecordICE(_ context.Context, instanceType, az string) error {
	f.recordICECalls = append(f.recordICECalls, struct{ InstanceType, AZ string }{instanceType, az})
	return f.returnErr
}

func (f *fakeCapacityStore) Get(_ context.Context, instanceType, az string) (*capacity.CapacityEntry, error) {
	return &capacity.CapacityEntry{InstanceType: instanceType, AZ: az}, nil
}

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

// TestCapacityWriteBack verifies bestEffortRecordCapacity — the helper wired
// into the km create AZ sweep loop that records launch outcomes to the
// capacity store (pkg/capacity). All calls are best-effort: a store error
// must never propagate to the caller, and a nil store must be a no-op.
func TestCapacityWriteBack(t *testing.T) {
	ctx := context.Background()
	const instanceType = "g6e.12xlarge"
	const az = "us-east-1a"

	t.Run("success_calls_RecordSuccess", func(t *testing.T) {
		store := &fakeCapacityStore{}
		bestEffortRecordCapacity(ctx, store, instanceType, az, capacity.ClassSuccess, true)
		if len(store.recordSuccessCalls) != 1 {
			t.Fatalf("want 1 RecordSuccess call, got %d", len(store.recordSuccessCalls))
		}
		if got := store.recordSuccessCalls[0]; got.InstanceType != instanceType || got.AZ != az {
			t.Errorf("RecordSuccess called with (%s, %s), want (%s, %s)", got.InstanceType, got.AZ, instanceType, az)
		}
		if len(store.recordICECalls) != 0 {
			t.Errorf("RecordICE must not be called on success, got %d calls", len(store.recordICECalls))
		}
	})

	t.Run("ICE_class_calls_RecordICE", func(t *testing.T) {
		store := &fakeCapacityStore{}
		bestEffortRecordCapacity(ctx, store, instanceType, az, capacity.ClassICE, false)
		if len(store.recordICECalls) != 1 {
			t.Fatalf("want 1 RecordICE call, got %d", len(store.recordICECalls))
		}
		if got := store.recordICECalls[0]; got.InstanceType != instanceType || got.AZ != az {
			t.Errorf("RecordICE called with (%s, %s), want (%s, %s)", got.InstanceType, got.AZ, instanceType, az)
		}
		if len(store.recordSuccessCalls) != 0 {
			t.Errorf("RecordSuccess must not be called on ICE, got %d calls", len(store.recordSuccessCalls))
		}
	})

	t.Run("non_ICE_failure_calls_neither", func(t *testing.T) {
		// SpotPrice/Quota/Auth/Unknown failures should not write to the store.
		for _, class := range []capacity.ErrorClass{
			capacity.ClassSpotPrice,
			capacity.ClassSpotLimit,
			capacity.ClassWaiterTimeout,
			capacity.ClassQuota,
			capacity.ClassAuth,
			capacity.ClassInvalid,
			capacity.ClassUnknown,
		} {
			store := &fakeCapacityStore{}
			bestEffortRecordCapacity(ctx, store, instanceType, az, class, false)
			if len(store.recordSuccessCalls)+len(store.recordICECalls) != 0 {
				t.Errorf("class %v: expected no store calls, got success=%d ice=%d",
					class, len(store.recordSuccessCalls), len(store.recordICECalls))
			}
		}
	})

	t.Run("nil_store_is_noop", func(t *testing.T) {
		// Docker substrate path: capacityStore is nil — must not panic.
		bestEffortRecordCapacity(ctx, nil, instanceType, az, capacity.ClassSuccess, true)
		bestEffortRecordCapacity(ctx, nil, instanceType, az, capacity.ClassICE, false)
	})

	t.Run("store_error_is_best_effort", func(t *testing.T) {
		// A store returning an error must not cause bestEffortRecordCapacity to panic
		// or return an error — the write failure is logged and silently dropped.
		store := &fakeCapacityStore{returnErr: errors.New("DDB unavailable")}
		// Neither of these must panic or fail the test.
		bestEffortRecordCapacity(ctx, store, instanceType, az, capacity.ClassSuccess, true)
		bestEffortRecordCapacity(ctx, store, instanceType, az, capacity.ClassICE, false)
		// Both were still called (best-effort = try, then drop the error).
		if len(store.recordSuccessCalls) != 1 || len(store.recordICECalls) != 1 {
			t.Errorf("want 1 call each, got success=%d ice=%d",
				len(store.recordSuccessCalls), len(store.recordICECalls))
		}
	})
}

package capacity_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/capacity"
)

// Re-export for test readability.
type ErrorClass = capacity.ErrorClass

const (
	ClassSuccess      = capacity.ClassSuccess
	ClassICE          = capacity.ClassICE
	ClassSpotPrice    = capacity.ClassSpotPrice
	ClassSpotLimit    = capacity.ClassSpotLimit
	ClassWaiterTimeout = capacity.ClassWaiterTimeout
	ClassQuota        = capacity.ClassQuota
	ClassAuth         = capacity.ClassAuth
	ClassInvalid      = capacity.ClassInvalid
	ClassUnknown      = capacity.ClassUnknown
)

func ClassifyError(stderr string, err error) capacity.ErrorClass {
	return capacity.ClassifyError(stderr, err)
}

// TestClassifyError verifies the full error taxonomy with real AWS error strings.
func TestClassifyError(t *testing.T) {
	t.Parallel()

	wrapped := func(msg string) error { return fmt.Errorf("apply failed: %w", errors.New(msg)) }
	wrappedDeadline := func() error { return fmt.Errorf("waiter timed out: %w", context.DeadlineExceeded) }

	cases := []struct {
		name   string
		stderr string
		err    error
		want   ErrorClass
	}{
		// Success
		{name: "nil error", stderr: "", err: nil, want: ClassSuccess},

		// ClassICE — iterate
		{name: "InsufficientInstanceCapacity", stderr: "Error: InsufficientInstanceCapacity", err: wrapped("apply"), want: ClassICE},
		{name: "no Spot capacity", stderr: "Error: no Spot capacity found in 'us-east-1a'", err: wrapped("apply"), want: ClassICE},
		{name: "capacity-not-available", stderr: "Error: capacity-not-available for g6e.12xlarge", err: wrapped("apply"), want: ClassICE},

		// ClassSpotPrice — iterate
		{name: "SpotMaxPriceTooLow", stderr: "Error: SpotMaxPriceTooLow: Your Spot request price of 0.10 is lower", err: wrapped("apply"), want: ClassSpotPrice},

		// ClassSpotLimit — iterate
		{name: "MaxSpotInstanceCountExceeded", stderr: "Error: MaxSpotInstanceCountExceeded: Max spot instance count exceeded", err: wrapped("apply"), want: ClassSpotLimit},

		// ClassWaiterTimeout — iterate
		{name: "context.DeadlineExceeded wrapped", stderr: "", err: wrappedDeadline(), want: ClassWaiterTimeout},
		{name: "context.DeadlineExceeded direct", stderr: "", err: context.DeadlineExceeded, want: ClassWaiterTimeout},

		// ClassQuota — fail-fast
		{name: "VcpuLimitExceeded", stderr: "Error: VcpuLimitExceeded: You have reached your maximum vCPU limit", err: wrapped("apply"), want: ClassQuota},
		{name: "InstanceLimitExceeded", stderr: "Error: InstanceLimitExceeded: You have reached your instance limit", err: wrapped("apply"), want: ClassQuota},
		{name: "vCPU limit phrase", stderr: "Error: You have reached your vCPU limit for this account", err: wrapped("apply"), want: ClassQuota},
		{name: "You have requested more vCPU capacity", stderr: "Error: You have requested more vCPU capacity than your current limit allows", err: wrapped("apply"), want: ClassQuota},

		// ClassAuth — fail-fast
		{name: "AuthFailure", stderr: "Error: AuthFailure: AWS was not able to validate the provided access credentials", err: wrapped("apply"), want: ClassAuth},
		{name: "UnauthorizedOperation", stderr: "Error: UnauthorizedOperation: You are not authorized to perform this operation", err: wrapped("apply"), want: ClassAuth},

		// ClassInvalid — fail-fast
		{name: "InvalidParameterValue", stderr: "Error: InvalidParameterValue: The parameter 'ami' is invalid", err: wrapped("apply"), want: ClassInvalid},
		{name: "UnsupportedOperation", stderr: "Error: UnsupportedOperation: The operation is not supported for the instance type", err: wrapped("apply"), want: ClassInvalid},
		{name: "Unsupported", stderr: "Error: Unsupported: The requested configuration is currently not supported", err: wrapped("apply"), want: ClassInvalid},

		// ClassUnknown
		{name: "unrecognized error", stderr: "Error: something else entirely unexpected", err: wrapped("apply"), want: ClassUnknown},
		{name: "unrecognized with non-deadline err", stderr: "", err: errors.New("network timeout"), want: ClassUnknown},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyError(tc.stderr, tc.err)
			if got != tc.want {
				t.Errorf("ClassifyError(%q, %v) = %v (%d), want %v (%d)",
					tc.stderr, tc.err, got, got, tc.want, tc.want)
			}
		})
	}
}

// TestErrorClass_ShouldIterate verifies iterate vs fail-fast taxonomy.
func TestErrorClass_ShouldIterate(t *testing.T) {
	t.Parallel()

	iterateCases := []ErrorClass{ClassICE, ClassSpotPrice, ClassSpotLimit, ClassWaiterTimeout}
	for _, c := range iterateCases {
		if !c.ShouldIterate() {
			t.Errorf("%v.ShouldIterate() = false, want true", c)
		}
	}

	failFastCases := []ErrorClass{ClassSuccess, ClassQuota, ClassAuth, ClassInvalid, ClassUnknown}
	for _, c := range failFastCases {
		if c.ShouldIterate() {
			t.Errorf("%v.ShouldIterate() = true, want false", c)
		}
	}
}

// Package capacity provides the shared error taxonomy, DynamoDB capacity store,
// and AZ ranking logic for Phase 124 capacity-aware EC2 launch failover.
package capacity

import (
	"context"
	"errors"
	"strings"
)

// ErrorClass categorises an AWS/Terraform apply failure into iterate-vs-fail-fast buckets.
type ErrorClass int

const (
	// ClassSuccess indicates no error occurred.
	ClassSuccess ErrorClass = iota
	// ClassICE indicates InsufficientInstanceCapacity or equivalent — iterate to next AZ.
	ClassICE
	// ClassSpotPrice indicates SpotMaxPriceTooLow — iterate to next AZ.
	ClassSpotPrice
	// ClassSpotLimit indicates MaxSpotInstanceCountExceeded — iterate to next AZ.
	ClassSpotLimit
	// ClassWaiterTimeout indicates a context.DeadlineExceeded from the bounded spot waiter — iterate.
	ClassWaiterTimeout
	// ClassQuota indicates VcpuLimitExceeded / InstanceLimitExceeded / vCPU limit phrases — fail-fast.
	ClassQuota
	// ClassAuth indicates AuthFailure / UnauthorizedOperation — fail-fast.
	ClassAuth
	// ClassInvalid indicates InvalidParameterValue / UnsupportedOperation / Unsupported — fail-fast.
	ClassInvalid
	// ClassUnknown covers unrecognized errors. Callers should typically fail-fast on unknown errors.
	ClassUnknown
)

// ShouldIterate returns true for error classes where retrying another AZ may succeed:
// ClassICE, ClassSpotPrice, ClassSpotLimit, ClassWaiterTimeout.
func (c ErrorClass) ShouldIterate() bool {
	switch c {
	case ClassICE, ClassSpotPrice, ClassSpotLimit, ClassWaiterTimeout:
		return true
	default:
		return false
	}
}

// ClassifyError maps an AWS apply failure to an ErrorClass using pure substring
// matching over stderr plus errors.Is for the waiter-timeout case.
//
// Priority order matters: iterate cases are checked before fail-fast cases.
// context.DeadlineExceeded is checked after the stderr-match cases because
// some ICE errors also travel via err wrapping.
func ClassifyError(stderr string, err error) ErrorClass {
	if err == nil {
		return ClassSuccess
	}

	// Iterate cases — check stderr first.
	if strings.Contains(stderr, "InsufficientInstanceCapacity") ||
		strings.Contains(stderr, "no Spot capacity") ||
		strings.Contains(stderr, "capacity-not-available") {
		return ClassICE
	}
	if strings.Contains(stderr, "SpotMaxPriceTooLow") {
		return ClassSpotPrice
	}
	if strings.Contains(stderr, "MaxSpotInstanceCountExceeded") {
		return ClassSpotLimit
	}
	// Waiter timeout: the apply context was cancelled by a spot create timeout.
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassWaiterTimeout
	}

	// Fail-fast cases.
	if strings.Contains(stderr, "VcpuLimitExceeded") ||
		strings.Contains(stderr, "InstanceLimitExceeded") ||
		strings.Contains(stderr, "vCPU limit") ||
		strings.Contains(stderr, "You have requested more vCPU capacity") {
		return ClassQuota
	}
	if strings.Contains(stderr, "AuthFailure") ||
		strings.Contains(stderr, "UnauthorizedOperation") {
		return ClassAuth
	}
	if strings.Contains(stderr, "InvalidParameterValue") ||
		strings.Contains(stderr, "UnsupportedOperation") ||
		strings.Contains(stderr, "Unsupported") {
		return ClassInvalid
	}

	return ClassUnknown
}

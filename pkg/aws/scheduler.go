// Package aws — scheduler.go
// CreateTTLSchedule and DeleteTTLSchedule wrap the EventBridge Scheduler SDK
// for TTL enforcement of km sandboxes.
package aws

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

// SchedulerAPI is the minimal EventBridge Scheduler interface required by
// CreateTTLSchedule and DeleteTTLSchedule.
// Implemented by *scheduler.Client.
type SchedulerAPI interface {
	CreateSchedule(ctx context.Context, input *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error)
	DeleteSchedule(ctx context.Context, input *scheduler.DeleteScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error)
}

// CreateTTLSchedule creates an EventBridge Scheduler one-time at() rule that fires at
// the TTL expiry time and triggers the sandbox teardown Lambda.
//
// If input is nil (TTL not configured) the function is a no-op and returns nil.
func CreateTTLSchedule(ctx context.Context, client SchedulerAPI, input *scheduler.CreateScheduleInput) error {
	if input == nil {
		// TTL not configured — no-op.
		return nil
	}
	_, err := client.CreateSchedule(ctx, input)
	return err
}

// DeleteTTLSchedule cancels the EventBridge TTL schedule for the given sandboxID.
// The schedule name is "km-ttl-<sandboxID>".
//
// This function is idempotent: if the schedule does not exist
// (ResourceNotFoundException), nil is returned so that km destroy can safely
// call it even when no TTL was configured.
func DeleteTTLSchedule(ctx context.Context, client SchedulerAPI, sandboxID string) error {
	_, err := client.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: schedulerNamePtr("km-ttl-" + sandboxID),
	})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			// Schedule does not exist — idempotent, treat as success.
			return nil
		}
		return err
	}
	return nil
}

// schedulerNamePtr is a helper that returns a pointer to a schedule name string.
// Using aws.String from the main aws-sdk-go-v2 module avoids an import cycle here.
func schedulerNamePtr(s string) *string {
	return &s
}

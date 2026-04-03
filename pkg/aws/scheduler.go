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

// SchedulerAPI is the EventBridge Scheduler interface required by schedule
// helpers. Implemented by *scheduler.Client.
type SchedulerAPI interface {
	CreateSchedule(ctx context.Context, input *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error)
	DeleteSchedule(ctx context.Context, input *scheduler.DeleteScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error)
	ListSchedules(ctx context.Context, input *scheduler.ListSchedulesInput, optFns ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error)
	GetSchedule(ctx context.Context, input *scheduler.GetScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.GetScheduleOutput, error)
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

// CreateAtSchedule creates an EventBridge Scheduler one-time at() rule for a
// deferred km at action (e.g. kill, stop). Unlike CreateTTLSchedule, the input
// is always required — km at always provides a fully-built CreateScheduleInput.
func CreateAtSchedule(ctx context.Context, client SchedulerAPI, input *scheduler.CreateScheduleInput) error {
	_, err := client.CreateSchedule(ctx, input)
	return err
}

// DeleteAtSchedule deletes an EventBridge km-at schedule by name and group.
// The schedule is identified by scheduleName within groupName.
//
// This function is idempotent: ResourceNotFoundException is treated as success
// so that km at cancel can safely be called even when the schedule has already
// fired or was never created.
func DeleteAtSchedule(ctx context.Context, client SchedulerAPI, scheduleName, groupName string) error {
	_, err := client.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name:      schedulerNamePtr(scheduleName),
		GroupName: schedulerNamePtr(groupName),
	})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
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

// Package aws — scheduler.go
// CreateTTLSchedule and DeleteTTLSchedule wrap the EventBridge Scheduler SDK
// for TTL enforcement of km sandboxes.
package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	CreateScheduleGroup(ctx context.Context, input *scheduler.CreateScheduleGroupInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleGroupOutput, error)
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
// prefix is the resource prefix (e.g. "km"); the schedule name is "{prefix}-ttl-{sandboxID}".
//
// This function is idempotent: if the schedule does not exist
// (ResourceNotFoundException), nil is returned so that km destroy can safely
// call it even when no TTL was configured.
func DeleteTTLSchedule(ctx context.Context, client SchedulerAPI, sandboxID, prefix string) error {
	_, err := client.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name: schedulerNamePtr(prefix + "-ttl-" + sandboxID),
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
//
// km at writes every schedule into a named EventBridge Scheduler group
// ("{prefix}-at"), but nothing provisions that group (it is not Terraform-
// managed). EventBridge requires a custom group to exist before CreateSchedule
// can target it, so the first km at on a fresh install — or any install whose
// group was removed by a teardown — would fail with ResourceNotFoundException.
//
// This function self-heals that gap: on ResourceNotFoundException it creates the
// group (idempotently — ConflictException means it already exists) and retries.
// New groups are eventually consistent, so the retry is bounded with a small
// backoff. This mirrors how DeleteAtSchedule swallows ResourceNotFoundException
// for idempotency.
func CreateAtSchedule(ctx context.Context, client SchedulerAPI, input *scheduler.CreateScheduleInput) error {
	_, err := client.CreateSchedule(ctx, input)
	if err == nil {
		return nil
	}

	var notFound *types.ResourceNotFoundException
	if !errors.As(err, &notFound) || input.GroupName == nil {
		// Unrelated error, or no custom group to create — return as-is.
		return err
	}

	// Group missing — create it (idempotent) before retrying.
	if _, gerr := client.CreateScheduleGroup(ctx, &scheduler.CreateScheduleGroupInput{
		Name: input.GroupName,
	}); gerr != nil {
		var conflict *types.ConflictException
		if !errors.As(gerr, &conflict) {
			// ConflictException means the group already exists, which is fine;
			// anything else is a real failure.
			return fmt.Errorf("create schedule group %q: %w", *input.GroupName, gerr)
		}
	}

	// Retry the create. A brand-new group can take a moment to become usable,
	// so retry a few times on ResourceNotFoundException with a small backoff.
	const maxRetries = 4
	for attempt := 0; ; attempt++ {
		_, err = client.CreateSchedule(ctx, input)
		if err == nil {
			return nil
		}
		if !errors.As(err, &notFound) || attempt >= maxRetries {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}
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

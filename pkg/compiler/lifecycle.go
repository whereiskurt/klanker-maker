// Package compiler — lifecycle.go
// BuildTTLScheduleInput produces an EventBridge Scheduler CreateScheduleInput
// for TTL enforcement. Returns nil if ttlExpiry is zero (TTL not configured).
package compiler

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

// BuildTTLScheduleInput constructs the CreateScheduleInput for an EventBridge one-time
// at() schedule that fires at ttlExpiry and triggers the TTL handler Lambda.
//
// Returns nil if ttlExpiry.IsZero() — callers should treat nil as "TTL not configured".
// Schedule name format: km-ttl-<sandboxID>.
// ActionAfterCompletion: DELETE so the rule self-cleans after firing.
func BuildTTLScheduleInput(sandboxID string, ttlExpiry time.Time, lambdaARN string, schedulerRoleARN string) *scheduler.CreateScheduleInput {
	if ttlExpiry.IsZero() {
		return nil
	}

	// at() expression format: at(2006-01-02T15:04:05) — UTC, no timezone suffix
	atExpr := "at(" + ttlExpiry.UTC().Format("2006-01-02T15:04:05") + ")"

	return &scheduler.CreateScheduleInput{
		Name:                       aws.String("km-ttl-" + sandboxID),
		ScheduleExpression:         aws.String(atExpr),
		ScheduleExpressionTimezone: aws.String("UTC"),
		Target: &types.Target{
			Arn:     aws.String(lambdaARN),
			RoleArn: aws.String(schedulerRoleARN),
			Input:   aws.String(`{"sandbox_id":"` + sandboxID + `"}`),
			RetryPolicy: &types.RetryPolicy{
				MaximumRetryAttempts: aws.Int32(0),
			},
		},
		FlexibleTimeWindow: &types.FlexibleTimeWindow{
			Mode: types.FlexibleTimeWindowModeOff,
		},
		ActionAfterCompletion: types.ActionAfterCompletionDelete,
	}
}

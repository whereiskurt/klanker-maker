// Package aws — CloudWatch Logs helpers for the km CLI.
// Provides EnsureLogGroup, PutLogEvents, GetLogEvents, and TailLogs.
// All functions take a narrow CWLogsAPI interface for mock-testable unit tests.
package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// CWLogsAPI is the minimal CloudWatch Logs interface required by the audit-log sidecar
// and the km status/tail commands.
// Implemented by *cloudwatchlogs.Client.
type CWLogsAPI interface {
	CreateLogGroup(ctx context.Context, params *cloudwatchlogs.CreateLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error)
	CreateLogStream(ctx context.Context, params *cloudwatchlogs.CreateLogStreamInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error)
	PutLogEvents(ctx context.Context, params *cloudwatchlogs.PutLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error)
	GetLogEvents(ctx context.Context, params *cloudwatchlogs.GetLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error)
	PutRetentionPolicy(ctx context.Context, params *cloudwatchlogs.PutRetentionPolicyInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error)
	DeleteLogGroup(ctx context.Context, params *cloudwatchlogs.DeleteLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteLogGroupOutput, error)
	CreateExportTask(ctx context.Context, params *cloudwatchlogs.CreateExportTaskInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateExportTaskOutput, error)
}

// LogEvent is a single timestamped log message for CloudWatch Logs.
type LogEvent struct {
	Timestamp int64  // Unix milliseconds
	Message   string
}

// EnsureLogGroup creates the CloudWatch log group and stream if they do not already exist.
// It ignores ResourceAlreadyExistsException for both group and stream creation
// to make the function idempotent and safe to call on every startup.
func EnsureLogGroup(ctx context.Context, client CWLogsAPI, logGroup, logStream string) error {
	_, err := client.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(logGroup),
	})
	if err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("create log group %q: %w", logGroup, err)
	}

	// Set 7-day retention on sandbox log groups — they're ephemeral.
	_, _ = client.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
		LogGroupName:    aws.String(logGroup),
		RetentionInDays: aws.Int32(7),
	})

	_, err = client.CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  aws.String(logGroup),
		LogStreamName: aws.String(logStream),
	})
	if err != nil && !isAlreadyExists(err) {
		return fmt.Errorf("create log stream %q/%q: %w", logGroup, logStream, err)
	}

	return nil
}

// DeleteSandboxLogGroup deletes the CloudWatch log group for a sandbox.
// Idempotent — ignores ResourceNotFoundException.
func DeleteSandboxLogGroup(ctx context.Context, client CWLogsAPI, sandboxID string) error {
	logGroup := "/km/sandboxes/" + sandboxID + "/"
	_, err := client.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{
		LogGroupName: aws.String(logGroup),
	})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("delete log group %q: %w", logGroup, err)
	}
	return nil
}

// ExportSandboxLogs initiates a CloudWatch Logs export task to S3 for the given sandbox.
// The export covers the last 7 days (matching the log group retention policy) up to now.
// Returns nil if the log group does not exist (ResourceNotFoundException) — no logs to export.
// The export is async (CreateExportTask is fire-and-forget); log group deletion proceeds immediately.
func ExportSandboxLogs(ctx context.Context, client CWLogsAPI, sandboxID, destBucket string) error {
	logGroup := "/km/sandboxes/" + sandboxID + "/"
	now := time.Now().UTC()
	from := now.Add(-7 * 24 * time.Hour)

	_, err := client.CreateExportTask(ctx, &cloudwatchlogs.CreateExportTaskInput{
		LogGroupName:      aws.String(logGroup),
		Destination:       aws.String(destBucket),
		DestinationPrefix: aws.String("logs/" + sandboxID),
		From:              aws.Int64(from.UnixMilli()),
		To:                aws.Int64(now.UnixMilli()),
	})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			// Log group does not exist — nothing to export.
			return nil
		}
		return fmt.Errorf("export sandbox logs for %q to s3://%s: %w", sandboxID, destBucket, err)
	}
	return nil
}

// PutLogEvents sends a batch of log events to the specified CloudWatch log group/stream.
// It batches up to 10,000 events per PutLogEvents SDK call (CloudWatch limit).
func PutLogEvents(ctx context.Context, client CWLogsAPI, logGroup, logStream string, events []LogEvent) error {
	const batchSize = 10_000

	for i := 0; i < len(events); i += batchSize {
		end := i + batchSize
		if end > len(events) {
			end = len(events)
		}
		batch := events[i:end]

		cwEvents := make([]types.InputLogEvent, 0, len(batch))
		for _, ev := range batch {
			cwEvents = append(cwEvents, types.InputLogEvent{
				Timestamp: aws.Int64(ev.Timestamp),
				Message:   aws.String(ev.Message),
			})
		}

		_, err := client.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
			LogGroupName:  aws.String(logGroup),
			LogStreamName: aws.String(logStream),
			LogEvents:     cwEvents,
		})
		if err != nil {
			return fmt.Errorf("put log events to %q/%q: %w", logGroup, logStream, err)
		}
	}

	return nil
}

// GetLogEvents retrieves up to limit log events from the specified CloudWatch log group/stream.
// Returns an empty slice (not nil) when no events are found.
func GetLogEvents(ctx context.Context, client CWLogsAPI, logGroup, logStream string, limit int32) ([]LogEvent, error) {
	output, err := client.GetLogEvents(ctx, &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  aws.String(logGroup),
		LogStreamName: aws.String(logStream),
		Limit:         aws.Int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("get log events from %q/%q: %w", logGroup, logStream, err)
	}

	result := make([]LogEvent, 0, len(output.Events))
	for _, ev := range output.Events {
		var ts int64
		if ev.Timestamp != nil {
			ts = *ev.Timestamp
		}
		var msg string
		if ev.Message != nil {
			msg = *ev.Message
		}
		result = append(result, LogEvent{Timestamp: ts, Message: msg})
	}

	return result, nil
}

// TailLogs prints log events from the specified CloudWatch log group/stream to w.
// If follow is true, TailLogs polls for new events every 2 seconds until ctx is cancelled.
// If follow is false, it fetches and prints once then returns.
func TailLogs(ctx context.Context, client CWLogsAPI, logGroup, logStream string, follow bool, w io.Writer) error {
	const pollInterval = 2 * time.Second
	const pageSize = int32(100)

	for {
		// Check context cancellation before each fetch.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		events, err := GetLogEvents(ctx, client, logGroup, logStream, pageSize)
		if err != nil {
			return err
		}

		for _, ev := range events {
			if _, werr := fmt.Fprintln(w, ev.Message); werr != nil {
				return fmt.Errorf("tail logs: write: %w", werr)
			}
		}

		if !follow {
			return nil
		}

		// Wait for next poll or context cancellation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// isAlreadyExists reports whether err is a CloudWatch ResourceAlreadyExistsException.
func isAlreadyExists(err error) bool {
	var rae *types.ResourceAlreadyExistsException
	return errors.As(err, &rae)
}

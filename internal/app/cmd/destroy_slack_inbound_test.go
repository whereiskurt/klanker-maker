package cmd

// destroy_slack_inbound_test.go — Phase 67 Plan 07 tests
//
// Exercises drainSlackInbound via local mocks — no real AWS connection.
// Covers: full drain happy path, queue-only (nil optional deps),
// thread cleanup still runs when queue delete fails, no-op when QueueURL empty.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDDB is an in-memory implementation of DDBQueryDeleteAPI for tests.
type fakeDDB struct {
	queryItems []map[string]ddbtypes.AttributeValue
	queryErr   error
	deleteCalled int
	deleteErr  error
}

func (f *fakeDDB) Query(_ context.Context, _ *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	return &dynamodb.QueryOutput{Items: f.queryItems}, f.queryErr
}

func (f *fakeDDB) DeleteItem(_ context.Context, _ *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.deleteCalled++
	return &dynamodb.DeleteItemOutput{}, f.deleteErr
}

// TestDestroy_SlackInboundDrain verifies the full happy-path drain sequence:
//   - StopPoller called once
//   - WaitForAgentRunIdle called once
//   - SQS DeleteQueue called once
//   - DDB DeleteItem called once per thread row (2 rows in test data)
func TestDestroy_SlackInboundDrain(t *testing.T) {
	ddb := &fakeDDB{
		queryItems: []map[string]ddbtypes.AttributeValue{
			{
				"channel_id": &ddbtypes.AttributeValueMemberS{Value: "C1"},
				"thread_ts":  &ddbtypes.AttributeValueMemberS{Value: "1.0"},
			},
			{
				"channel_id": &ddbtypes.AttributeValueMemberS{Value: "C1"},
				"thread_ts":  &ddbtypes.AttributeValueMemberS{Value: "2.0"},
			},
		},
	}
	sqs := &fakeSQS{}
	stopCalls := 0
	waitCalls := 0
	deps := destroyInboundDeps{
		SandboxID:             "sb-abc",
		InstanceID:            "i-0",
		QueueURL:              "https://sqs/q.fifo",
		ChannelID:             "C1",
		SQS:                   sqs,
		DDB:                   ddb,
		SlackThreadsTableName: "km-slack-threads",
		StopPoller: func(ctx context.Context, iid string) error {
			stopCalls++
			return nil
		},
		WaitForAgentRunIdle: func(ctx context.Context, iid string, dl time.Time) error {
			waitCalls++
			return nil
		},
	}
	drainSlackInbound(context.Background(), deps)
	if stopCalls != 1 {
		t.Errorf("StopPoller called %d times, want 1", stopCalls)
	}
	if waitCalls != 1 {
		t.Errorf("WaitForAgentRunIdle called %d times, want 1", waitCalls)
	}
	if sqs.deleteCalled != 1 {
		t.Errorf("SQS DeleteQueue called %d times, want 1", sqs.deleteCalled)
	}
	if ddb.deleteCalled != 2 {
		t.Errorf("DDB DeleteItem called %d times, want 2", ddb.deleteCalled)
	}
}

// TestDestroy_SlackInboundQueueDeleted verifies the minimal path:
// queue is deleted even when optional deps (StopPoller, WaitForAgentRunIdle,
// DDB) are nil.
func TestDestroy_SlackInboundQueueDeleted(t *testing.T) {
	sqs := &fakeSQS{}
	deps := destroyInboundDeps{
		QueueURL: "https://sqs/q.fifo",
		SQS:      sqs,
		// intentionally nil: StopPoller, WaitForAgentRunIdle, DDB
	}
	drainSlackInbound(context.Background(), deps)
	if sqs.deleteCalled != 1 {
		t.Fatalf("queue must be deleted even if other deps nil; deleteCalled=%d", sqs.deleteCalled)
	}
}

// TestDestroy_SlackInboundThreadsCleanedUp_OnQueueErr verifies best-effort:
// thread cleanup still runs even when the queue delete fails.
func TestDestroy_SlackInboundThreadsCleanedUp_OnQueueErr(t *testing.T) {
	ddb := &fakeDDB{
		queryItems: []map[string]ddbtypes.AttributeValue{
			{
				"channel_id": &ddbtypes.AttributeValueMemberS{Value: "C1"},
				"thread_ts":  &ddbtypes.AttributeValueMemberS{Value: "1.0"},
			},
		},
	}
	// SQS delete fails — drain must STILL clean threads (each step is best-effort).
	sqs := &fakeSQS{deleteErr: errors.New("queue gone")}
	deps := destroyInboundDeps{
		QueueURL:              "https://sqs/q.fifo",
		ChannelID:             "C1",
		SQS:                   sqs,
		DDB:                   ddb,
		SlackThreadsTableName: "km-slack-threads",
	}
	drainSlackInbound(context.Background(), deps)
	if ddb.deleteCalled != 1 {
		t.Fatalf("threads cleanup should run even if queue delete failed; deleteCalled=%d", ddb.deleteCalled)
	}
}

// TestDestroy_SlackInboundDrain_NoOp_WhenNoQueueURL verifies that when the
// sandbox has no inbound queue (QueueURL=""), drainSlackInbound is a no-op.
func TestDestroy_SlackInboundDrain_NoOp_WhenNoQueueURL(t *testing.T) {
	sqs := &fakeSQS{}
	deps := destroyInboundDeps{QueueURL: "", SQS: sqs}
	drainSlackInbound(context.Background(), deps)
	if sqs.deleteCalled != 0 {
		t.Fatalf("expected no-op when QueueURL empty; deleteCalled=%d", sqs.deleteCalled)
	}
}

package lifecycle_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	"github.com/whereiskurt/klankrmkr/pkg/lifecycle"
)

// mockCWLogsClient implements kmaws.CWLogsAPI for testing IdleDetector.
// Only GetLogEvents is used by IdleDetector; other methods are stubs.
type mockCWLogsClient struct {
	events []kmaws.LogEvent
	getErr error
}

func (m *mockCWLogsClient) CreateLogGroup(ctx context.Context, params *cloudwatchlogs.CreateLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogGroupOutput, error) {
	return &cloudwatchlogs.CreateLogGroupOutput{}, nil
}

func (m *mockCWLogsClient) CreateLogStream(ctx context.Context, params *cloudwatchlogs.CreateLogStreamInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateLogStreamOutput, error) {
	return &cloudwatchlogs.CreateLogStreamOutput{}, nil
}

func (m *mockCWLogsClient) PutLogEvents(ctx context.Context, params *cloudwatchlogs.PutLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutLogEventsOutput, error) {
	return &cloudwatchlogs.PutLogEventsOutput{}, nil
}

func (m *mockCWLogsClient) PutRetentionPolicy(ctx context.Context, params *cloudwatchlogs.PutRetentionPolicyInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error) {
	return &cloudwatchlogs.PutRetentionPolicyOutput{}, nil
}

func (m *mockCWLogsClient) DeleteLogGroup(ctx context.Context, params *cloudwatchlogs.DeleteLogGroupInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteLogGroupOutput, error) {
	return &cloudwatchlogs.DeleteLogGroupOutput{}, nil
}

func (m *mockCWLogsClient) CreateExportTask(ctx context.Context, params *cloudwatchlogs.CreateExportTaskInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateExportTaskOutput, error) {
	return &cloudwatchlogs.CreateExportTaskOutput{}, nil
}

func (m *mockCWLogsClient) GetLogEvents(ctx context.Context, params *cloudwatchlogs.GetLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	cwEvents := make([]cwtypes.OutputLogEvent, 0, len(m.events))
	for _, ev := range m.events {
		ts := ev.Timestamp
		msg := ev.Message
		cwEvents = append(cwEvents, cwtypes.OutputLogEvent{
			Timestamp: &ts,
			Message:   &msg,
		})
	}
	return &cloudwatchlogs.GetLogEventsOutput{Events: cwEvents}, nil
}

func TestIdleDetector_FiresAfterIdle(t *testing.T) {
	// Events are 3 seconds old; idleTimeout is 1 second → should fire.
	now := time.Now()
	oldEventTime := now.Add(-3 * time.Second)

	mock := &mockCWLogsClient{
		events: []kmaws.LogEvent{
			{Timestamp: oldEventTime.UnixMilli(), Message: "old event"},
		},
	}

	var firedID string
	d := &lifecycle.IdleDetector{
		SandboxID:    "sb-idle01",
		IdleTimeout:  1 * time.Second,
		PollInterval: 1 * time.Millisecond, // fast poll for test
		CWClient:     mock,
		LogGroup:     "/km/sandboxes/sb-idle01/",
		LogStream:    "audit",
		OnIdle:       func(id string) { firedID = id },
	}
	// Inject a fixed clock so the elapsed calculation is consistent.
	d.SetNowFn(func() time.Time { return now })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = d.Run(ctx)

	if firedID != "sb-idle01" {
		t.Errorf("expected OnIdle to be called with sb-idle01, got %q", firedID)
	}
}

func TestIdleDetector_DoesNotFireIfActive(t *testing.T) {
	// Events are 100ms old; idleTimeout is 5s → should NOT fire within test duration.
	now := time.Now()
	recentEventTime := now.Add(-100 * time.Millisecond)

	mock := &mockCWLogsClient{
		events: []kmaws.LogEvent{
			{Timestamp: recentEventTime.UnixMilli(), Message: "recent event"},
		},
	}

	fired := false
	d := &lifecycle.IdleDetector{
		SandboxID:    "sb-active1",
		IdleTimeout:  5 * time.Second,
		PollInterval: 10 * time.Millisecond, // fast poll for test
		CWClient:     mock,
		LogGroup:     "/km/sandboxes/sb-active1/",
		LogStream:    "audit",
		OnIdle:       func(id string) { fired = true },
	}
	d.SetNowFn(func() time.Time { return now })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_ = d.Run(ctx) // exits on ctx timeout

	if fired {
		t.Error("OnIdle should NOT have fired for a recently active sandbox")
	}
}

func TestTeardownPolicy_Destroy(t *testing.T) {
	destroyCalledWith := ""
	callbacks := lifecycle.TeardownCallbacks{
		Destroy: func(ctx context.Context, sandboxID string) error {
			destroyCalledWith = sandboxID
			return nil
		},
		Stop: func(ctx context.Context, sandboxID string) error {
			t.Error("Stop should not be called for destroy policy")
			return nil
		},
	}

	err := lifecycle.ExecuteTeardown(context.Background(), "destroy", "sb-dest01", callbacks)
	if err != nil {
		t.Fatalf("ExecuteTeardown(destroy) returned error: %v", err)
	}
	if destroyCalledWith != "sb-dest01" {
		t.Errorf("Destroy callback called with %q; want %q", destroyCalledWith, "sb-dest01")
	}
}

func TestTeardownPolicy_Retain(t *testing.T) {
	callbacks := lifecycle.TeardownCallbacks{
		Destroy: func(ctx context.Context, sandboxID string) error {
			t.Error("Destroy should not be called for retain policy")
			return nil
		},
		Stop: func(ctx context.Context, sandboxID string) error {
			t.Error("Stop should not be called for retain policy")
			return nil
		},
	}

	err := lifecycle.ExecuteTeardown(context.Background(), "retain", "sb-ret001", callbacks)
	if err != nil {
		t.Fatalf("ExecuteTeardown(retain) returned error: %v", err)
	}
}

// --------------------------------------------------------------------------
// OnIdleNotify tests
// --------------------------------------------------------------------------

// TestIdleDetector_OnIdleNotifyCalled verifies OnIdleNotify is called when idle fires.
func TestIdleDetector_OnIdleNotifyCalled(t *testing.T) {
	now := time.Now()
	oldEventTime := now.Add(-3 * time.Second)

	mock := &mockCWLogsClient{
		events: []kmaws.LogEvent{
			{Timestamp: oldEventTime.UnixMilli(), Message: "old event"},
		},
	}

	var notifiedID string
	d := &lifecycle.IdleDetector{
		SandboxID:    "sb-notify01",
		IdleTimeout:  1 * time.Second,
		PollInterval: 1 * time.Millisecond,
		CWClient:     mock,
		LogGroup:     "/km/sandboxes/sb-notify01/",
		LogStream:    "audit",
		OnIdle:       func(id string) {},
		OnIdleNotify: func(id string) { notifiedID = id },
	}
	d.SetNowFn(func() time.Time { return now })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = d.Run(ctx)

	if notifiedID != "sb-notify01" {
		t.Errorf("expected OnIdleNotify to be called with sb-notify01, got %q", notifiedID)
	}
}

// TestIdleDetector_OnIdleNotifyNilSafe verifies nil OnIdleNotify does not panic.
func TestIdleDetector_OnIdleNotifyNilSafe(t *testing.T) {
	now := time.Now()
	oldEventTime := now.Add(-3 * time.Second)

	mock := &mockCWLogsClient{
		events: []kmaws.LogEvent{
			{Timestamp: oldEventTime.UnixMilli(), Message: "old event"},
		},
	}

	d := &lifecycle.IdleDetector{
		SandboxID:    "sb-nilnotify",
		IdleTimeout:  1 * time.Second,
		PollInterval: 1 * time.Millisecond,
		CWClient:     mock,
		LogGroup:     "/km/sandboxes/sb-nilnotify/",
		LogStream:    "audit",
		OnIdle:       func(id string) {},
		OnIdleNotify: nil, // explicitly nil — must not panic
	}
	d.SetNowFn(func() time.Time { return now })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := d.Run(ctx); err != nil && err != context.DeadlineExceeded {
		t.Errorf("unexpected error with nil OnIdleNotify: %v", err)
	}
	// Test passes if no panic occurred
}

package lifecycle_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/lifecycle"
)

// mockCWLogsClient implements kmaws.CWLogsAPI for testing IdleDetector.
// Only GetLogEvents is used by IdleDetector; other methods are stubs.
type mockCWLogsClient struct {
	events []kmaws.LogEvent
	getErr error

	// degradeAfter, when > 0, makes GetLogEvents start failing (or returning an
	// empty page, per degradeEmpty) from the Nth call onwards. It models the
	// real-world shape the detector must survive: a stream that reads fine and
	// then goes bad. Zero disables the behaviour.
	degradeAfter int
	degradeEmpty bool

	// calls counts GetLogEvents invocations.
	calls int
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

func (m *mockCWLogsClient) FilterLogEvents(ctx context.Context, params *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
	return &cloudwatchlogs.FilterLogEventsOutput{}, nil
}

func (m *mockCWLogsClient) GetLogEvents(ctx context.Context, params *cloudwatchlogs.GetLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetLogEventsOutput, error) {
	m.calls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.degradeAfter > 0 && m.calls >= m.degradeAfter {
		if m.degradeEmpty {
			// GetLogEvents is documented to be able to return an empty page even
			// when the stream has events.
			return &cloudwatchlogs.GetLogEventsOutput{Events: nil}, nil
		}
		return nil, errors.New("throttled: Rate exceeded")
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
	// Detector starts at T=0, idle timeout is 1s. Events are 3s before T=0.
	// Clock advances past idle timeout on second call so detector fires.
	startTime := time.Now()
	oldEventTime := startTime.Add(-3 * time.Second)

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
	// Clock starts at startTime, then advances past idle timeout.
	callCount := 0
	d.SetNowFn(func() time.Time {
		callCount++
		if callCount <= 2 {
			return startTime // first poll: set startTime, check grace
		}
		return startTime.Add(2 * time.Second) // past idle timeout
	})

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
	startTime := time.Now()
	oldEventTime := startTime.Add(-3 * time.Second)

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
	callCount := 0
	d.SetNowFn(func() time.Time {
		callCount++
		if callCount <= 2 {
			return startTime
		}
		return startTime.Add(2 * time.Second)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = d.Run(ctx)

	if notifiedID != "sb-notify01" {
		t.Errorf("expected OnIdleNotify to be called with sb-notify01, got %q", notifiedID)
	}
}

// TestIdleDetector_OnIdleNotifyNilSafe verifies nil OnIdleNotify does not panic.
func TestIdleDetector_OnIdleNotifyNilSafe(t *testing.T) {
	startTime := time.Now()
	oldEventTime := startTime.Add(-3 * time.Second)

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
	callCount := 0
	d.SetNowFn(func() time.Time {
		callCount++
		if callCount <= 2 {
			return startTime
		}
		return startTime.Add(2 * time.Second)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := d.Run(ctx); err != nil && err != context.DeadlineExceeded {
		t.Errorf("unexpected error with nil OnIdleNotify: %v", err)
	}
	// Test passes if no panic occurred
}

// A degraded GetLogEvents read must never be mistaken for proof of inactivity.
//
// Regression: on 2026-09-02 a sandbox whose audit stream carried presence
// heartbeats up to 2 minutes earlier was stopped as idle against a 30-minute
// timeout. isIdle fell back to `now - startTime` on a failed/empty poll, so once
// the detector had been alive longer than IdleTimeout, ANY single bad poll fired
// the one-shot reap regardless of real activity. Both shapes are covered: a
// transient error, and the empty page GetLogEvents is documented to return.
func TestIdleDetector_DegradedPollDoesNotReapRecentlyActiveSandbox(t *testing.T) {
	for _, tc := range []struct {
		name  string
		empty bool
	}{
		{name: "poll_errors", empty: false},
		{name: "poll_returns_empty_page", empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Timeline, mirroring the live incident:
			//   T0      detector starts
			//   T0+53m  last heartbeat the detector successfully observes
			//   T0+55m  the poll degrades; IdleTimeout is 30m
			// now-lastSeen is 2m (not idle); now-startTime is 55m (the old,
			// wrong answer that reaped the box).
			t0 := time.Now()
			mock := &mockCWLogsClient{
				events: []kmaws.LogEvent{
					{Timestamp: t0.Add(53 * time.Minute).UnixMilli(), Message: "heartbeat"},
				},
				degradeAfter: 2,
				degradeEmpty: tc.empty,
			}

			fired := false
			d := &lifecycle.IdleDetector{
				SandboxID:    "sb-degrade",
				IdleTimeout:  30 * time.Minute,
				PollInterval: 20 * time.Millisecond,
				CWClient:     mock,
				LogGroup:     "/km/sandboxes/sb-degrade/",
				LogStream:    "audit",
				OnIdle:       func(id string) { fired = true },
			}
			// First poll establishes startTime at T0; every later read sees T0+55m.
			nowCalls := 0
			d.SetNowFn(func() time.Time {
				nowCalls++
				if nowCalls <= 2 {
					return t0
				}
				return t0.Add(55 * time.Minute)
			})

			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			_ = d.Run(ctx)

			if fired {
				t.Fatal("OnIdle fired on a degraded poll despite activity 2 minutes ago; " +
					"a failed or empty read must fall back to the last observed activity")
			}
			if mock.calls < 2 {
				t.Fatalf("expected the degraded path to be exercised, got %d GetLogEvents calls", mock.calls)
			}
		})
	}
}

// The fail-safe must not become a never-reap: once the last OBSERVED activity is
// itself older than IdleTimeout, a degraded poll still reports idle.
func TestIdleDetector_DegradedPollStillReapsWhenLastActivityIsStale(t *testing.T) {
	// Detector starts at T0 with only a 90-minute-old event on the stream, then
	// the poll degrades. At T0+31m the baseline (T0, the later of startTime and
	// the stale event) is past a 30m timeout, so the reap must still happen.
	t0 := time.Now()
	mock := &mockCWLogsClient{
		events: []kmaws.LogEvent{
			{Timestamp: t0.Add(-90 * time.Minute).UnixMilli(), Message: "old event"},
		},
		degradeAfter: 2,
	}

	var firedID string
	d := &lifecycle.IdleDetector{
		SandboxID:    "sb-stale",
		IdleTimeout:  30 * time.Minute,
		PollInterval: 20 * time.Millisecond,
		CWClient:     mock,
		LogGroup:     "/km/sandboxes/sb-stale/",
		LogStream:    "audit",
		OnIdle:       func(id string) { firedID = id },
	}
	nowCalls := 0
	d.SetNowFn(func() time.Time {
		nowCalls++
		if nowCalls <= 2 {
			return t0
		}
		return t0.Add(31 * time.Minute)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = d.Run(ctx)

	if firedID != "sb-stale" {
		t.Errorf("expected OnIdle for a sandbox whose last observed activity is 90m old, got %q", firedID)
	}
}

// A sandbox that has NEVER produced an audit event is still reaped after
// IdleTimeout measured from detector start — the original cost-leak protection,
// which the fail-safe must preserve.
func TestIdleDetector_NoEventsEverStillReapsFromStartTime(t *testing.T) {
	start := time.Now()
	mock := &mockCWLogsClient{} // no events, no error — empty stream forever

	var firedID string
	d := &lifecycle.IdleDetector{
		SandboxID:    "sb-silent",
		IdleTimeout:  30 * time.Minute,
		PollInterval: 1 * time.Millisecond,
		CWClient:     mock,
		LogGroup:     "/km/sandboxes/sb-silent/",
		LogStream:    "audit",
		OnIdle:       func(id string) { firedID = id },
	}
	callCount := 0
	d.SetNowFn(func() time.Time {
		callCount++
		if callCount <= 2 {
			return start // first poll establishes startTime
		}
		return start.Add(31 * time.Minute)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = d.Run(ctx)

	if firedID != "sb-silent" {
		t.Errorf("expected OnIdle for a sandbox that never produced an audit event, got %q", firedID)
	}
}

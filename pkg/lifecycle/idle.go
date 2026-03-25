package lifecycle

import (
	"context"
	"time"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// IdleDetector polls the CloudWatch log group for a sandbox and fires the OnIdle
// callback when no new log events have been observed for IdleTimeout.
//
// Typical usage:
//
//	d := &IdleDetector{
//	    SandboxID:   "sb-a1b2c3d4",
//	    IdleTimeout: 30 * time.Minute,
//	    CWClient:    cwlogsClient,
//	    LogGroup:    "/km/sandboxes/sb-a1b2c3d4/",
//	    LogStream:   "audit",
//	    OnIdle:      func(id string) { /* trigger teardown */ },
//	}
//	go d.Run(ctx)
type IdleDetector struct {
	// SandboxID is passed to OnIdle when idle is detected.
	SandboxID string

	// IdleTimeout is the duration of inactivity before the sandbox is considered idle.
	IdleTimeout time.Duration

	// PollInterval controls how often CloudWatch is polled. Default 30s if zero.
	PollInterval time.Duration

	// CWClient is the CloudWatch Logs client used for GetLogEvents calls.
	CWClient kmaws.CWLogsAPI

	// LogGroup is the CloudWatch log group to poll for audit activity.
	LogGroup string

	// LogStream is the log stream within LogGroup.
	LogStream string

	// OnIdle is called once when idle is detected. The sandboxID is passed as argument.
	OnIdle func(sandboxID string)

	// OnIdleNotify is called when idle is detected, to send a lifecycle notification.
	// Called with (sandboxID). If nil, no notification is sent.
	// Failure is best-effort: logged as warning. Decoupled from OnIdle for separation
	// of concerns (teardown action vs. notification).
	OnIdleNotify func(sandboxID string)

	// nowFn allows tests to inject a controlled clock. Defaults to time.Now.
	nowFn func() time.Time

	// startTime tracks when the detector started running (set on first isIdle call).
	startTime time.Time
}

// SetNowFn injects a custom clock function for testing. Not safe for concurrent use.
func (d *IdleDetector) SetNowFn(fn func() time.Time) {
	d.nowFn = fn
}

// Run polls CloudWatch at PollInterval, checking the most recent log event timestamp.
// If the most recent event is older than IdleTimeout (or no events exist), OnIdle is called.
// Run respects ctx cancellation and returns ctx.Err() on cancellation.
//
// OnIdle is called at most once — Run returns immediately after firing it.
func (d *IdleDetector) Run(ctx context.Context) error {
	interval := d.PollInterval
	if interval == 0 {
		interval = 30 * time.Second
	}

	now := d.nowFn
	if now == nil {
		now = time.Now
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if d.isIdle(ctx, now) {
			if d.OnIdle != nil {
				d.OnIdle(d.SandboxID)
			}
			if d.OnIdleNotify != nil {
				d.OnIdleNotify(d.SandboxID)
			}
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// isIdle checks whether the sandbox has been idle longer than IdleTimeout.
// Returns true only if the most recent event is older than IdleTimeout.
// Returns false when no events exist yet — the sandbox just started and hasn't
// had time to generate activity. The startTime field tracks when the detector
// began running so we can distinguish "just started" from "truly idle".
func (d *IdleDetector) isIdle(ctx context.Context, now func() time.Time) bool {
	events, err := kmaws.GetLogEvents(ctx, d.CWClient, d.LogGroup, d.LogStream, 1)
	if err != nil || len(events) == 0 {
		// No events yet — only consider idle if we've been running longer than IdleTimeout.
		// This prevents killing sandboxes that just started and haven't generated events.
		if d.startTime.IsZero() {
			d.startTime = now()
			return false
		}
		return now().Sub(d.startTime) > d.IdleTimeout
	}

	// Events are returned in ascending order; the last one is the most recent.
	lastEvent := events[len(events)-1]
	lastEventTime := time.UnixMilli(lastEvent.Timestamp)
	elapsed := now().Sub(lastEventTime)
	return elapsed > d.IdleTimeout
}

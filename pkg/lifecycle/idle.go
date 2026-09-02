package lifecycle

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
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

	// lastSeenActivity is the newest log-event timestamp observed on a SUCCESSFUL
	// poll. It is the fail-safe baseline for a later poll that errors or returns
	// an empty page, so one degraded read cannot reap a demonstrably active
	// sandbox. Zero until the first event is seen.
	lastSeenActivity time.Time
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
//
// A failed or empty poll is NEVER treated as proof of inactivity: it falls back
// to lastSeenActivity (the newest event seen on a successful poll) so a single
// transient CloudWatch error cannot reap a sandbox that was active seconds ago.
//
// For stale events (e.g. after hibernate/resume), we use the later of the last
// event time and the detector start time as the activity baseline. This prevents
// immediate idle detection when old pre-hibernate events are the only ones present.
func (d *IdleDetector) isIdle(ctx context.Context, now func() time.Time) bool {
	if d.startTime.IsZero() {
		d.startTime = now()
	}

	events, err := kmaws.GetLogEvents(ctx, d.CWClient, d.LogGroup, d.LogStream, 1)
	if err != nil || len(events) == 0 {
		// A degraded read is the ABSENCE of evidence, not evidence of absence.
		// GetLogEvents fails transiently and is documented to return an empty
		// page even for a stream that has events, so a single bad poll must
		// never be read as "nothing has happened here". Firing OnIdle is
		// one-shot and irreversible (the sandbox is stopped or destroyed), so
		// this branch falls back to the last activity actually observed.
		//
		// Only a detector that has NEVER seen an event falls back to startTime.
		// That preserves the original intent — a sandbox that generates no
		// audit activity at all is still reaped rather than leaking forever —
		// while a sandbox with a known-recent event keeps its full idle window.
		baseline := d.startTime
		if d.lastSeenActivity.After(baseline) {
			baseline = d.lastSeenActivity
		}

		switch {
		case err != nil:
			log.Warn().Err(err).
				Str("sandbox_id", d.SandboxID).
				Str("log_group", d.LogGroup).
				Time("baseline", baseline).
				Msg("idle detector: activity read failed; holding last known activity as the baseline")
		case !d.lastSeenActivity.IsZero():
			// The stream HAS produced events before and now reads empty — the
			// exact shape that used to reap an active sandbox silently.
			log.Warn().
				Str("sandbox_id", d.SandboxID).
				Str("log_group", d.LogGroup).
				Time("baseline", baseline).
				Msg("idle detector: activity read returned an empty page; holding last known activity as the baseline")
		default:
			// Cold start, no events yet — the normal case, not worth a warning.
			log.Debug().
				Str("sandbox_id", d.SandboxID).
				Msg("idle detector: no audit activity observed yet")
		}

		return now().Sub(baseline) > d.IdleTimeout
	}

	// Events are returned in ascending order; the last one is the most recent.
	lastEvent := events[len(events)-1]
	lastEventTime := time.UnixMilli(lastEvent.Timestamp)

	// Remember the newest observation so a later degraded poll has a truthful
	// baseline to fall back on.
	if lastEventTime.After(d.lastSeenActivity) {
		d.lastSeenActivity = lastEventTime
	}

	// Use the later of last event time and detector start time as the baseline.
	// After hibernate/resume, startTime will be recent even though the last event
	// is hours old — this gives the sandbox a full idle window to generate activity.
	baseline := lastEventTime
	if d.startTime.After(baseline) {
		baseline = d.startTime
	}

	elapsed := now().Sub(baseline)
	return elapsed > d.IdleTimeout
}

// pkg/slack/bridge/history_backfill.go
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

const (
	DefaultHistoryPageSize        = 200 // Slack's documented Tier 3 posture: <=999, 200 recommended
	DefaultHistoryIntervalSeconds = 2
	MaxHistoryPageSize            = 999
	MaxSQSDelaySeconds            = 900 // SQS DelaySeconds ceiling
)

// historyPacing returns the install's page size and backfill interval
// (slack.history_rate), defaults when unset. Retry-After always overrides the
// interval for the step that was throttled.
func (h *EventsHandler) historyPacing() (page, interval int) {
	page, interval = DefaultHistoryPageSize, DefaultHistoryIntervalSeconds
	if h.HistoryPageSize > 0 {
		page = h.HistoryPageSize
	}
	if h.HistoryIntervalSeconds >= 0 && h.HistoryPageSize > 0 { // an explicit 0 interval is honoured only alongside an explicit page size (env present)
		interval = h.HistoryIntervalSeconds
	}
	return
}

// HistoryReader pages a channel's history newest-first.
type HistoryReader interface {
	History(ctx context.Context, channel, oldest, latest string, limit int) ([]SlackReplyMessage, bool, error)
}

// SQSBackfillQueue enqueues backfill jobs on a standard (non-FIFO) queue,
// using DelaySeconds for pacing.
type SQSBackfillQueue struct {
	Client   SQSSendMessageAPI
	QueueURL string
}

func (q *SQSBackfillQueue) Enqueue(ctx context.Context, job BackfillJob, delaySeconds int32) error {
	if q.QueueURL == "" {
		return fmt.Errorf("history backfill: no queue url")
	}
	if delaySeconds > MaxSQSDelaySeconds {
		slog.Default().Warn("history backfill: delay exceeds SQS maximum; clamping", "requested", delaySeconds, "clamped", MaxSQSDelaySeconds, "channel", job.Channel)
		delaySeconds = MaxSQSDelaySeconds
	}
	b, _ := json.Marshal(job)
	_, err := q.Client.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(q.QueueURL), MessageBody: aws.String(string(b)), DelaySeconds: delaySeconds})
	return err
}

// BackfillStep runs one page of a channel's backfill. It is the SQS consumer
// body: idempotent (PutItem on (channel, ts)), bounded (one Slack call), and
// self-scheduling (re-enqueues with a delay). A rate limit re-enqueues the job
// with Slack's Retry-After and returns nil. Any other failure — a Slack error,
// a row write, a re-enqueue — records the state for km doctor and RETURNS the
// error, so SQS redelivers the same job (ReportBatchItemFailures) and, after
// the queue's maxReceiveCount, parks it on the dead-letter queue. A page is
// safe to redo: every write is a PutItem on (channel, ts).
func (h *EventsHandler) BackfillStep(ctx context.Context, job BackfillJob) error {
	if h.History == nil || h.HistoryReader == nil {
		return nil
	}
	meta, err := h.History.GetMeta(ctx, job.Channel)
	if err != nil {
		return err // transient store error: let SQS retry
	}
	if meta == nil {
		return nil // channel left since the job was queued
	}
	// The route as TODAY's config defines it: a glob removed (or a route that
	// stopped mirroring) since the job was queued ends the backfill.
	route := h.Routes[job.Channel]
	if route == nil {
		route = resolveMetaRoute(meta, h.Patterns)
	}
	if route == nil || route.History == nil || !route.History.Mirror {
		meta.BackfillState = "off"
		h.log().Info("events: history backfill: channel no longer mirrored; stopping", "channel", job.Channel)
		return h.History.PutMeta(ctx, *meta)
	}
	page, interval := h.historyPacing()
	limit := job.Remaining
	if limit > page {
		limit = page
	}
	if limit <= 0 {
		meta.BackfillState = "capped"
		return h.History.PutMeta(ctx, *meta)
	}
	msgs, more, err := h.HistoryReader.History(ctx, job.Channel, job.Oldest, job.Latest, limit)
	if err != nil {
		var rl *ErrSlackRateLimited
		if errors.As(err, &rl) {
			meta.BackfillState = "rate_limited"
			_ = h.History.PutMeta(ctx, *meta)
			if h.Backfill != nil {
				if eerr := h.Backfill.Enqueue(ctx, job, int32(rl.RetryAfterSeconds)); eerr != nil {
					h.log().Error("events: history backfill: re-enqueue after rate limit failed; SQS will retry", "channel", job.Channel, "err", eerr)
					return eerr
				}
			}
			return nil
		}
		h.log().Warn("events: history backfill: history fetch failed; SQS will retry", "channel", job.Channel, "err", err)
		meta.BackfillState = "error: " + err.Error()
		if perr := h.History.PutMeta(ctx, *meta); perr != nil {
			h.log().Warn("events: history backfill: meta write failed", "channel", job.Channel, "err", perr)
		}
		return err
	}
	// Newest-first. The cursor advances over every message with a ts, written or
	// skipped as a system subtype, until the first failed write; after that it
	// stays put (PutItem is idempotent, so later writes still go ahead).
	cursor := job.Latest
	failed := 0
	written := 0
	advance := func(ts string) {
		if failed == 0 && (cursor == "" || ts < cursor) {
			cursor = ts
		}
	}
	for _, m := range msgs {
		if m.TS == "" {
			continue
		}
		if historySystemSubtypes[m.Subtype] {
			advance(m.TS)
			continue
		}
		row := HistoryMessageRow{Channel: job.Channel, TS: m.TS, User: m.User, BotID: m.BotID, Text: m.Text, ThreadTS: m.ThreadTS, Subtype: m.Subtype, ExpiresAt: historyExpiry(h.now(), route.History)}
		for _, f := range m.Files {
			row.Files = append(row.Files, f.Name)
		}
		if perr := h.History.PutMessage(ctx, row); perr != nil {
			h.log().Warn("events: history backfill: row write failed", "channel", job.Channel, "ts", m.TS, "err", perr)
			failed++
			continue
		}
		written++
		advance(m.TS)
	}
	meta.BackfillCursor = cursor
	if failed > 0 {
		// The page is redone as a whole when SQS redelivers this job, so its
		// writes are not counted yet; the cursor records how far it got.
		meta.BackfillState = fmt.Sprintf("error: %d row write(s) failed; retrying", failed)
		if perr := h.History.PutMeta(ctx, *meta); perr != nil {
			h.log().Warn("events: history backfill: meta write failed", "channel", job.Channel, "err", perr)
		}
		return fmt.Errorf("history backfill %s: %d row write(s) failed", job.Channel, failed)
	}
	meta.BackfillWritten += written
	remaining := job.Remaining - len(msgs)
	switch {
	case remaining <= 0 && more:
		meta.BackfillState = "capped"
	case !more || len(msgs) == 0:
		meta.BackfillState = "done"
	default:
		meta.BackfillState = "running"
	}
	if err := h.History.PutMeta(ctx, *meta); err != nil {
		return err
	}
	if meta.BackfillState == "running" && h.Backfill != nil {
		if eerr := h.Backfill.Enqueue(ctx, BackfillJob{Channel: job.Channel, Oldest: job.Oldest, Latest: cursor, Remaining: remaining}, int32(interval)); eerr != nil {
			h.log().Error("events: history backfill: re-enqueue failed; SQS will retry the page", "channel", job.Channel, "err", eerr)
			return eerr
		}
	}
	h.log().Info("events: history backfill step", "channel", job.Channel, "written", written, "state", meta.BackfillState, "remaining", remaining)
	return nil
}

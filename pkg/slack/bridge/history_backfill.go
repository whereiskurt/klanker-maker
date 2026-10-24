// pkg/slack/bridge/history_backfill.go
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

const (
	DefaultHistoryPageSize        = 200 // Slack's documented Tier 3 posture: <=999, 200 recommended
	DefaultHistoryIntervalSeconds = 2
	MaxHistoryPageSize            = 999
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
	b, _ := json.Marshal(job)
	_, err := q.Client.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(q.QueueURL), MessageBody: aws.String(string(b)), DelaySeconds: delaySeconds})
	return err
}

// BackfillStep runs one page of a channel's backfill. It is the SQS consumer
// body: idempotent (PutItem on (channel, ts)), bounded (one Slack call), and
// self-scheduling (re-enqueues with a delay). It returns nil on every Slack
// error so SQS never retries a step that already re-enqueued itself.
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
					h.log().Error("events: history backfill: re-enqueue after rate limit failed", "channel", job.Channel, "err", eerr)
				}
			}
			return nil
		}
		h.log().Warn("events: history backfill: history fetch failed", "channel", job.Channel, "err", err)
		meta.BackfillState = "error: " + err.Error()
		_ = h.History.PutMeta(ctx, *meta)
		return nil
	}
	cursor := job.Latest
	for _, m := range msgs {
		if m.TS == "" || historySystemSubtypes[m.Subtype] {
			continue
		}
		row := HistoryMessageRow{Channel: job.Channel, TS: m.TS, User: m.User, BotID: m.BotID, Text: m.Text, ThreadTS: m.ThreadTS, Subtype: m.Subtype, ExpiresAt: historyExpiry(h.now(), meta.Route.History)}
		for _, f := range m.Files {
			row.Files = append(row.Files, f.Name)
		}
		if perr := h.History.PutMessage(ctx, row); perr != nil {
			h.log().Warn("events: history backfill: row write failed", "channel", job.Channel, "ts", m.TS, "err", perr)
			continue
		}
		meta.BackfillWritten++
		if cursor == "" || m.TS < cursor {
			cursor = m.TS
		}
	}
	remaining := job.Remaining - len(msgs)
	meta.BackfillCursor = cursor
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
			h.log().Error("events: history backfill: re-enqueue failed", "channel", job.Channel, "err", eerr)
		}
	}
	h.log().Info("events: history backfill step", "channel", job.Channel, "written", len(msgs), "state", meta.BackfillState, "remaining", remaining)
	return nil
}

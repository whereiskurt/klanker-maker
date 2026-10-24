// Package bridge — history_store.go
// DynamoDB-backed store for the Slack channel-history mirror. One table, two
// kinds of row: message rows (PK channel, SK ts) and channel meta rows (PK
// historyMetaPK, SK channel id). Message rows carry text and attachment NAMES
// only, never file bytes, and expire through the expires_at TTL attribute.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// historyMetaPK is the partition that holds channel meta rows. It can never
// collide with a Slack channel id (those start with C, G or D).
const historyMetaPK = "#meta"

const (
	historyTSFloor   = "0"                 // sorts below every Slack ts
	historyTSCeiling = "9999999999.999999" // sorts above every Slack ts
)

// HistoryMessageRow is one mirrored message.
type HistoryMessageRow struct {
	Channel, TS, User, BotID, Text, ThreadTS, Subtype, EditedTS string
	Files                                                       []string // names only
	ExpiresAt                                                   int64    // epoch seconds
}

// ChannelMeta is the per-channel bookkeeping row.
type ChannelMeta struct {
	Channel, Name   string
	Route           RouteEntry // the route the channel is bound to (alias, allow, history…)
	MatchedAt       string     // RFC3339
	BackfillOldest  string
	BackfillCursor  string
	BackfillState   string // pending|running|done|capped|rate_limited|off
	BackfillWritten int
	Announced       bool
	Overlap         string // a second glob that also matched, for km doctor
}

// HistoryStore persists the channel mirror.
type HistoryStore interface {
	PutMessage(ctx context.Context, m HistoryMessageRow) error
	UpdateText(ctx context.Context, channel, ts, text, editedTS string) error
	DeleteMessage(ctx context.Context, channel, ts string) error
	// Query returns messages in (oldest, latest] with ts < before when before != "",
	// newest first, at most limit; more reports whether older rows remain.
	Query(ctx context.Context, channel, oldest, latest, before string, limit int) (rows []HistoryMessageRow, more bool, err error)
	PutMeta(ctx context.Context, m ChannelMeta) error
	GetMeta(ctx context.Context, channel string) (*ChannelMeta, error) // nil,nil when absent
	ListMeta(ctx context.Context) ([]ChannelMeta, error)
	PurgeChannel(ctx context.Context, channel string) (deleted int, err error)
}

// DDBHistoryAPI is the DynamoDB surface the store needs; *dynamodb.Client satisfies it.
type DDBHistoryAPI interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, in *dynamodb.UpdateItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
	Query(ctx context.Context, in *dynamodb.QueryInput, opts ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

// DDBHistoryStore implements HistoryStore on the km-slack-history table.
type DDBHistoryStore struct {
	Client    DDBHistoryAPI
	TableName string
	RetryBase time.Duration // first purge-retry backoff; zero means 50ms
}

func hkeyS(v string) dynamodbtypes.AttributeValue {
	return &dynamodbtypes.AttributeValueMemberS{Value: v}
}

func historyKey(channel, ts string) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{"channel": hkeyS(channel), "ts": hkeyS(ts)}
}

func (s *DDBHistoryStore) PutMessage(ctx context.Context, m HistoryMessageRow) error {
	item := historyKey(m.Channel, m.TS)
	item["text"] = hkeyS(m.Text)
	item["expires_at"] = &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(m.ExpiresAt, 10)}
	for k, v := range map[string]string{"user": m.User, "bot_id": m.BotID, "thread_ts": m.ThreadTS, "subtype": m.Subtype, "edited_ts": m.EditedTS} {
		if v != "" {
			item[k] = hkeyS(v)
		}
	}
	if len(m.Files) > 0 {
		l := make([]dynamodbtypes.AttributeValue, 0, len(m.Files))
		for _, f := range m.Files {
			l = append(l, hkeyS(f))
		}
		item["files"] = &dynamodbtypes.AttributeValueMemberL{Value: l}
	}
	if _, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: awssdk.String(s.TableName), Item: item}); err != nil {
		return fmt.Errorf("history put: %w", err)
	}
	return nil
}

// UpdateText rewrites an edited message's text. It is conditioned on the row
// existing, so an edit of a message that expired or was never mirrored is a
// no-op rather than a half-row.
func (s *DDBHistoryStore) UpdateText(ctx context.Context, channel, ts, text, editedTS string) error {
	_, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 awssdk.String(s.TableName),
		Key:                       historyKey(channel, ts),
		UpdateExpression:          awssdk.String("SET #t = :t, edited_ts = :e"),
		ConditionExpression:       awssdk.String("attribute_exists(ts)"),
		ExpressionAttributeNames:  map[string]string{"#t": "text"},
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{":t": hkeyS(text), ":e": hkeyS(editedTS)},
	})
	var ccf *dynamodbtypes.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("history update: %w", err)
	}
	return nil
}

func (s *DDBHistoryStore) DeleteMessage(ctx context.Context, channel, ts string) error {
	if _, err := s.Client.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: awssdk.String(s.TableName), Key: historyKey(channel, ts)}); err != nil {
		return fmt.Errorf("history delete: %w", err)
	}
	return nil
}

// Query reads (oldest, latest] newest first. DynamoDB's BETWEEN is inclusive on
// both ends, so the key condition is the widest range (oldest..min(latest,
// before)) and the exclusive ends are applied here: ts == oldest and
// ts >= before are dropped. At most two rows can be dropped, but they sit at the
// edges of the range, so the loop keeps paging until limit+1 survivors exist
// (the extra one is how `more` is known) or the partition is exhausted.
func (s *DDBHistoryStore) Query(ctx context.Context, channel, oldest, latest, before string, limit int) ([]HistoryMessageRow, bool, error) {
	if limit <= 0 || channel == historyMetaPK {
		return nil, false, nil
	}
	lo, hi := historyTSFloor, historyTSCeiling
	if oldest != "" {
		lo = oldest
	}
	if latest != "" {
		hi = latest
	}
	if before != "" && before < hi {
		hi = before
	}
	if lo > hi {
		return nil, false, nil
	}
	var rows []HistoryMessageRow
	var start map[string]dynamodbtypes.AttributeValue
	for len(rows) < limit+1 {
		out, err := s.Client.Query(ctx, &dynamodb.QueryInput{
			TableName:              awssdk.String(s.TableName),
			KeyConditionExpression: awssdk.String("channel = :c AND ts BETWEEN :lo AND :hi"),
			ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
				":c": hkeyS(channel), ":lo": hkeyS(lo), ":hi": hkeyS(hi),
			},
			ScanIndexForward:  awssdk.Bool(false),
			Limit:             awssdk.Int32(int32(limit + 1)),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, false, fmt.Errorf("history query: %w", err)
		}
		for _, it := range out.Items {
			row := historyRowFromItem(it)
			if row.Channel == historyMetaPK || (oldest != "" && row.TS == oldest) || (before != "" && row.TS >= before) {
				continue
			}
			rows = append(rows, row)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	return rows, more, nil
}

func historyRowFromItem(it map[string]dynamodbtypes.AttributeValue) HistoryMessageRow {
	r := HistoryMessageRow{
		Channel: hs2(it, "channel"), TS: hs2(it, "ts"), User: hs2(it, "user"), BotID: hs2(it, "bot_id"),
		Text: hs2(it, "text"), ThreadTS: hs2(it, "thread_ts"), Subtype: hs2(it, "subtype"), EditedTS: hs2(it, "edited_ts"),
	}
	if n, ok := it["expires_at"].(*dynamodbtypes.AttributeValueMemberN); ok {
		r.ExpiresAt, _ = strconv.ParseInt(n.Value, 10, 64)
	}
	if l, ok := it["files"].(*dynamodbtypes.AttributeValueMemberL); ok {
		for _, f := range l.Value {
			if sv, ok := f.(*dynamodbtypes.AttributeValueMemberS); ok {
				r.Files = append(r.Files, sv.Value)
			}
		}
	}
	return r
}

func hs2(it map[string]dynamodbtypes.AttributeValue, k string) string {
	if v, ok := it[k].(*dynamodbtypes.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func (s *DDBHistoryStore) PutMeta(ctx context.Context, m ChannelMeta) error {
	rj, err := json.Marshal(m.Route)
	if err != nil {
		return fmt.Errorf("history meta marshal route: %w", err)
	}
	item := historyKey(historyMetaPK, m.Channel)
	item["route_json"] = hkeyS(string(rj))
	item["backfill_written"] = &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(m.BackfillWritten)}
	item["announced"] = &dynamodbtypes.AttributeValueMemberBOOL{Value: m.Announced}
	for k, v := range map[string]string{
		"name": m.Name, "matched_at": m.MatchedAt, "backfill_oldest": m.BackfillOldest,
		"backfill_cursor": m.BackfillCursor, "backfill_state": m.BackfillState, "overlap": m.Overlap,
	} {
		if v != "" {
			item[k] = hkeyS(v)
		}
	}
	if _, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: awssdk.String(s.TableName), Item: item}); err != nil {
		return fmt.Errorf("history meta put: %w", err)
	}
	return nil
}

func (s *DDBHistoryStore) GetMeta(ctx context.Context, channel string) (*ChannelMeta, error) {
	out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{TableName: awssdk.String(s.TableName), Key: historyKey(historyMetaPK, channel)})
	if err != nil {
		return nil, fmt.Errorf("history meta get: %w", err)
	}
	if len(out.Item) == 0 {
		return nil, nil
	}
	m, err := metaFromItem(out.Item)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func metaFromItem(it map[string]dynamodbtypes.AttributeValue) (ChannelMeta, error) {
	m := ChannelMeta{
		Channel: hs2(it, "ts"), Name: hs2(it, "name"), MatchedAt: hs2(it, "matched_at"),
		BackfillOldest: hs2(it, "backfill_oldest"), BackfillCursor: hs2(it, "backfill_cursor"),
		BackfillState: hs2(it, "backfill_state"), Overlap: hs2(it, "overlap"),
	}
	if n, ok := it["backfill_written"].(*dynamodbtypes.AttributeValueMemberN); ok {
		m.BackfillWritten, _ = strconv.Atoi(n.Value)
	}
	if b, ok := it["announced"].(*dynamodbtypes.AttributeValueMemberBOOL); ok {
		m.Announced = b.Value
	}
	if rj := hs2(it, "route_json"); rj != "" {
		if err := json.Unmarshal([]byte(rj), &m.Route); err != nil {
			return m, fmt.Errorf("history meta %s route_json: %w", m.Channel, err)
		}
	}
	return m, nil
}

func (s *DDBHistoryStore) ListMeta(ctx context.Context) ([]ChannelMeta, error) {
	var all []ChannelMeta
	var start map[string]dynamodbtypes.AttributeValue
	for {
		out, err := s.Client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 awssdk.String(s.TableName),
			KeyConditionExpression:    awssdk.String("channel = :c"),
			ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{":c": hkeyS(historyMetaPK)},
			ExclusiveStartKey:         start,
		})
		if err != nil {
			return nil, fmt.Errorf("history meta list: %w", err)
		}
		for _, it := range out.Items {
			m, err := metaFromItem(it)
			if err != nil {
				return nil, err
			}
			all = append(all, m)
		}
		if len(out.LastEvaluatedKey) == 0 {
			return all, nil
		}
		start = out.LastEvaluatedKey
	}
}

const (
	purgeMaxAttempts = 8
	purgeBaseBackoff = 50 * time.Millisecond
)

// drainUnprocessed retries throttled deletes with exponential backoff, bounded
// by purgeMaxAttempts and the context.
func (s *DDBHistoryStore) drainUnprocessed(ctx context.Context, left map[string][]dynamodbtypes.WriteRequest) error {
	backoff := s.retryBase()
	for attempt := 0; len(left) > 0; attempt++ {
		if attempt >= purgeMaxAttempts {
			n := 0
			for _, r := range left {
				n += len(r)
			}
			return fmt.Errorf("history purge: %d deletes still unprocessed after %d retries", n, purgeMaxAttempts)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		r, err := s.Client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: left})
		if err != nil {
			return fmt.Errorf("history purge batch retry: %w", err)
		}
		left = r.UnprocessedItems
	}
	return nil
}

func (s *DDBHistoryStore) retryBase() time.Duration {
	if s.RetryBase > 0 {
		return s.RetryBase
	}
	return purgeBaseBackoff
}

// PurgeChannel deletes every message row of a channel (keys-only paging,
// BatchWriteItem in chunks of 25) and then its meta row. It returns the number
// of message rows deleted.
func (s *DDBHistoryStore) PurgeChannel(ctx context.Context, channel string) (int, error) {
	if channel == historyMetaPK {
		return 0, fmt.Errorf("history purge: %q is the meta partition, not a channel", channel)
	}
	deleted := 0
	var start map[string]dynamodbtypes.AttributeValue
	for {
		out, err := s.Client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 awssdk.String(s.TableName),
			KeyConditionExpression:    awssdk.String("channel = :c"),
			ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{":c": hkeyS(channel)},
			ProjectionExpression:      awssdk.String("channel, ts"),
			ExclusiveStartKey:         start,
		})
		if err != nil {
			return deleted, fmt.Errorf("history purge query: %w", err)
		}
		for i := 0; i < len(out.Items); i += 25 {
			end := i + 25
			if end > len(out.Items) {
				end = len(out.Items)
			}
			reqs := make([]dynamodbtypes.WriteRequest, 0, end-i)
			for _, it := range out.Items[i:end] {
				reqs = append(reqs, dynamodbtypes.WriteRequest{DeleteRequest: &dynamodbtypes.DeleteRequest{
					Key: historyKey(channel, hs2(it, "ts")),
				}})
			}
			bo, err := s.Client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
				RequestItems: map[string][]dynamodbtypes.WriteRequest{s.TableName: reqs},
			})
			if err != nil {
				return deleted, fmt.Errorf("history purge batch: %w", err)
			}
			if err := s.drainUnprocessed(ctx, bo.UnprocessedItems); err != nil {
				return deleted, err
			}
			deleted += end - i
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	if err := s.DeleteMessage(ctx, historyMetaPK, channel); err != nil {
		return deleted, err
	}
	return deleted, nil
}

var (
	_ HistoryStore  = (*DDBHistoryStore)(nil)
	_ DDBHistoryAPI = (*dynamodb.Client)(nil)
)

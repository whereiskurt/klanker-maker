// pkg/slack/bridge/history_fakes_test.go
package bridge

import (
	"context"
	"sort"
	"sync"
)

// fakeHistoryStore is an in-memory HistoryStore that records calls.
type fakeHistoryStore struct {
	mu         sync.Mutex
	rows       map[string]HistoryMessageRow // channel|ts
	meta       map[string]ChannelMeta
	putErr     error
	getMetaErr error
	updates    []string // channel|ts|text|editedTS
	deletes    []string // channel|ts
	purges     []string
	putMetas   int

	queryRows  []HistoryMessageRow // canned Query result (newest first)
	queryMore  bool
	queryErr   error
	queryCalls int
	queryLimit int // limit of the last Query call
	getMetas   int // GetMeta calls
}

func newFakeHistoryStore() *fakeHistoryStore {
	return &fakeHistoryStore{rows: map[string]HistoryMessageRow{}, meta: map[string]ChannelMeta{}}
}

func (f *fakeHistoryStore) PutMessage(_ context.Context, m HistoryMessageRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	f.rows[m.Channel+"|"+m.TS] = m
	return nil
}

func (f *fakeHistoryStore) UpdateText(_ context.Context, channel, ts, text, editedTS string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, channel+"|"+ts+"|"+text+"|"+editedTS)
	return nil
}

func (f *fakeHistoryStore) DeleteMessage(_ context.Context, channel, ts string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, channel+"|"+ts)
	return nil
}

func (f *fakeHistoryStore) Query(_ context.Context, _, _, _, _ string, limit int) ([]HistoryMessageRow, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queryCalls++
	f.queryLimit = limit
	return f.queryRows, f.queryMore, f.queryErr
}

func (f *fakeHistoryStore) PutMeta(_ context.Context, m ChannelMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putMetas++
	f.meta[m.Channel] = m
	return nil
}

func (f *fakeHistoryStore) GetMeta(_ context.Context, channel string) (*ChannelMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getMetas++
	if f.getMetaErr != nil {
		return nil, f.getMetaErr
	}
	m, ok := f.meta[channel]
	if !ok {
		return nil, nil
	}
	return &m, nil
}

func (f *fakeHistoryStore) ListMeta(context.Context) ([]ChannelMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ChannelMeta
	for _, m := range f.meta {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out, nil
}

func (f *fakeHistoryStore) PurgeChannel(_ context.Context, channel string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purges = append(f.purges, channel)
	return 0, nil
}

// fakeChannelInfo answers conversations.info from maps.
type fakeChannelInfo struct {
	name    map[string]string
	private map[string]bool
	err     error
}

func (f *fakeChannelInfo) Info(_ context.Context, channel string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	return f.name[channel], f.private[channel], nil
}

// fakeBackfill records enqueued backfill jobs.
type fakeBackfill struct {
	jobs   []BackfillJob
	delays []int32
}

func (f *fakeBackfill) Enqueue(_ context.Context, job BackfillJob, delay int32) error {
	f.jobs = append(f.jobs, job)
	f.delays = append(f.delays, delay)
	return nil
}

// fakeHistoryReader answers conversations.history from a canned page.
type fakeHistoryReader struct {
	msgs  []SlackReplyMessage
	more  bool
	err   error
	calls []struct {
		Channel, Oldest, Latest string
		Limit                   int
	}
}

func (f *fakeHistoryReader) History(_ context.Context, channel, oldest, latest string, limit int) ([]SlackReplyMessage, bool, error) {
	f.calls = append(f.calls, struct {
		Channel, Oldest, Latest string
		Limit                   int
	}{channel, oldest, latest, limit})
	return f.msgs, f.more, f.err
}

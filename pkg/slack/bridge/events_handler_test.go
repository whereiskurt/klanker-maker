package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeSlackPoster is a test double for the SlackPoster interface.
// Records all PostMessage calls for assertion.
type fakeSlackPoster struct {
	mu   sync.Mutex
	msgs []fakeSlackMsg
	err  error
}

type fakeSlackMsg struct {
	channel, subject, body, threadTS string
}

func (f *fakeSlackPoster) PostMessage(ctx context.Context, channel, subject, body, threadTS string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, fakeSlackMsg{channel, subject, body, threadTS})
	return "1234.567", f.err
}

func (f *fakeSlackPoster) ArchiveChannel(ctx context.Context, channelID string) error {
	return nil
}

func (f *fakeSlackPoster) GetPermalink(_ context.Context, channel, messageTS string) (string, error) {
	return "https://example.slack.com/archives/" + channel + "/p" + messageTS, nil
}

func (f *fakeSlackPoster) UpdateMessage(_ context.Context, _, ts, _ string) (string, error) {
	return ts, nil
}

// slowDownloader implements FileDownloader with a configurable delay,
// used to verify that Handle returns before Download completes.
type slowDownloader struct {
	mu      sync.Mutex
	called  bool
	delay   time.Duration
	signal  chan struct{} // closed when Download is called
	atts    []Attachment
	errs    []FileError
	callErr error
}

func newSlowDownloader(delay time.Duration) *slowDownloader {
	return &slowDownloader{
		delay:  delay,
		signal: make(chan struct{}),
	}
}

func (d *slowDownloader) Download(ctx context.Context, files []SlackFile, sandboxID, threadTS string) ([]Attachment, []FileError, error) {
	d.mu.Lock()
	d.called = true
	if d.signal != nil {
		select {
		case <-d.signal:
			// already closed — noop
		default:
			close(d.signal)
		}
	}
	d.mu.Unlock()

	time.Sleep(d.delay)
	return d.atts, d.errs, d.callErr
}

func (d *slowDownloader) wasCalled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.called
}

// ---- mocks ----

type fakeSigningSecret struct {
	secret string
	err    error
}

func (f *fakeSigningSecret) Fetch(ctx context.Context) (string, error) { return f.secret, f.err }

type fakeBotUserID struct {
	uid string
	err error
}

func (f *fakeBotUserID) Fetch(ctx context.Context) (string, error) { return f.uid, f.err }

type fakeNonces struct {
	seen map[string]bool
	err  error
}

func (f *fakeNonces) CheckAndStore(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[id] {
		return true, nil
	}
	f.seen[id] = true
	return false, nil
}

type fakeSandboxes struct {
	info SandboxRoutingInfo
	err  error
}

func (f *fakeSandboxes) FetchByChannel(ctx context.Context, ch string) (SandboxRoutingInfo, error) {
	return f.info, f.err
}

type fakeThreads struct {
	upserts []struct{ chan_, ts, sb string }
	err     error
	// Phase 91.3: simulate existing thread rows for LookupSandbox.
	// Key: ch+"|"+ts → sandbox_id. Empty value = row not found.
	sandboxByThread map[string]string
}

func (f *fakeThreads) Get(ctx context.Context, ch, ts string) (string, error) { return "", nil }
func (f *fakeThreads) Upsert(ctx context.Context, ch, ts, sb string) error {
	f.upserts = append(f.upserts, struct{ chan_, ts, sb string }{ch, ts, sb})
	return f.err
}
func (f *fakeThreads) LookupSandbox(ctx context.Context, ch, ts string) (string, error) {
	if f.sandboxByThread == nil {
		return "", nil
	}
	return f.sandboxByThread[ch+"|"+ts], nil
}

type fakeSQS struct {
	sends []struct{ url, body, group, dedup string }
	err   error
}

func (f *fakeSQS) Send(ctx context.Context, url, body, group, dedup string) error {
	f.sends = append(f.sends, struct{ url, body, group, dedup string }{url, body, group, dedup})
	return f.err
}

type fakePauseHinter struct {
	mu    sync.Mutex
	calls []struct{ ch, ts string }
	err   error
}

func (f *fakePauseHinter) PostIfCooldownExpired(ctx context.Context, ch, ts string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct{ ch, ts string }{ch, ts})
	return f.err
}

func (f *fakePauseHinter) snapshot() []struct{ ch, ts string } {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]struct{ ch, ts string }, len(f.calls))
	copy(out, f.calls)
	return out
}

type reactorCall struct {
	channel, ts, emoji string
}

type fakeReactor struct {
	mu    sync.Mutex
	calls []reactorCall
	err   error
}

func (f *fakeReactor) Add(ctx context.Context, channel, ts, emoji string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, reactorCall{channel, ts, emoji})
	return f.err
}

func (f *fakeReactor) snapshot() []reactorCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]reactorCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// ---- helpers ----

const testSigningSecret = "test-signing-secret-32-chars-aaaa"

func signSlackPayload(t *testing.T, body string, ts time.Time) (tsHdr, sigHdr string) {
	t.Helper()
	tsHdr = strconv.FormatInt(ts.Unix(), 10)
	base := "v0:" + tsHdr + ":" + body
	mac := hmac.New(sha256.New, []byte(testSigningSecret))
	mac.Write([]byte(base))
	sigHdr = "v0=" + hex.EncodeToString(mac.Sum(nil))
	return
}

func newHandler(now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeNonces, *fakeSandboxes, *fakePauseHinter, *fakeReactor) {
	sqs := &fakeSQS{}
	threads := &fakeThreads{}
	nonces := &fakeNonces{}
	sandboxes := &fakeSandboxes{
		info: SandboxRoutingInfo{SandboxID: "sb-abc123", QueueURL: "https://sqs.example/queue.fifo"},
	}
	pauseHinter := &fakePauseHinter{}
	reactor := &fakeReactor{}
	return &EventsHandler{
		SigningSecret: &fakeSigningSecret{secret: testSigningSecret},
		BotUserID:     &fakeBotUserID{uid: "UBOT123"},
		Nonces:        nonces,
		Sandboxes:     sandboxes,
		Threads:       threads,
		SQS:           sqs,
		PauseHinter:   pauseHinter,
		Reactor:       reactor,
		AckEmoji:      "eyes",
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return now },
	}, sqs, threads, nonces, sandboxes, pauseHinter, reactor
}

// ---- tests ----

func TestEventsHandler_URLVerification(t *testing.T) {
	h, sqs, _, _, _, _, _ := newHandler(time.Now())
	body := `{"type":"url_verification","challenge":"abc-xyz"}`
	resp := h.Handle(context.Background(), EventsRequest{Body: body})
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body=%s", resp.StatusCode, resp.Body)
	}
	var got map[string]string
	_ = json.Unmarshal([]byte(resp.Body), &got)
	if got["challenge"] != "abc-xyz" {
		t.Fatalf("challenge echo: %s", resp.Body)
	}
	if len(sqs.sends) != 0 {
		t.Fatalf("expected no SQS write on url_verification")
	}
}

func TestEventsHandler_BadSigningSecret(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := newHandler(now)
	body := `{"type":"event_callback","event_id":"E1","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, _ := signSlackPayload(t, body, now)
	badSig := "v0=" + fmt.Sprintf("%064d", 0)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": badSig},
		Body:    body,
	})
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 0 {
		t.Fatal("no SQS on bad sig")
	}
}

func TestEventsHandler_StaleTimestamp(t *testing.T) {
	now := time.Now()
	h, _, _, _, _, _, _ := newHandler(now)
	body := `{"type":"event_callback"}`
	tsHdr, sigHdr := signSlackPayload(t, body, now.Add(-10*time.Minute)) // 600s+ old, sign with same age
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 stale, got %d", resp.StatusCode)
	}
}

func TestEventsHandler_FutureTimestamp(t *testing.T) {
	now := time.Now()
	h, _, _, _, _, _, _ := newHandler(now)
	body := `{"type":"event_callback"}`
	tsHdr, sigHdr := signSlackPayload(t, body, now.Add(10*time.Minute))
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 future-ts, got %d", resp.StatusCode)
	}
}

func TestEventsHandler_BotSelfMessageFiltered(t *testing.T) {
	cases := []struct {
		name  string
		event string
	}{
		{"bot_id_set", `{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0","bot_id":"B1"}`},
		{"subtype_bot_message", `{"type":"message","channel":"C1","subtype":"bot_message","text":"hi","ts":"1.0"}`},
		{"subtype_message_changed", `{"type":"message","channel":"C1","subtype":"message_changed","ts":"1.0"}`},
		{"subtype_message_deleted", `{"type":"message","channel":"C1","subtype":"message_deleted","ts":"1.0"}`},
		{"user_equals_bot_uid", `{"type":"message","channel":"C1","user":"UBOT123","text":"hi","ts":"1.0"}`},
		{"empty_user", `{"type":"message","channel":"C1","text":"hi","ts":"1.0"}`},
		// Phase 67-12 Gap B: extended system-subtype coverage. Allow-list semantics in
		// isBotLoop drop every subtype except "" and "thread_broadcast" — these cases
		// document a representative sample of Slack system subtypes that were previously
		// passed through to SQS by the deny-list and burned Bedrock spend on no-op turns.
		{"subtype_channel_join", `{"type":"message","channel":"C1","subtype":"channel_join","user":"U1","text":"<@U1> has joined","ts":"1.0"}`},
		{"subtype_channel_leave", `{"type":"message","channel":"C1","subtype":"channel_leave","user":"U1","ts":"1.0"}`},
		{"subtype_channel_topic", `{"type":"message","channel":"C1","subtype":"channel_topic","user":"U1","topic":"new","ts":"1.0"}`},
		{"subtype_channel_purpose", `{"type":"message","channel":"C1","subtype":"channel_purpose","user":"U1","purpose":"new","ts":"1.0"}`},
		{"subtype_channel_name", `{"type":"message","channel":"C1","subtype":"channel_name","user":"U1","old_name":"a","name":"b","ts":"1.0"}`},
		{"subtype_channel_archive", `{"type":"message","channel":"C1","subtype":"channel_archive","user":"U1","ts":"1.0"}`},
		{"subtype_channel_unarchive", `{"type":"message","channel":"C1","subtype":"channel_unarchive","user":"U1","ts":"1.0"}`},
		{"subtype_pinned_item", `{"type":"message","channel":"C1","subtype":"pinned_item","user":"U1","ts":"1.0"}`},
		{"subtype_unpinned_item", `{"type":"message","channel":"C1","subtype":"unpinned_item","user":"U1","ts":"1.0"}`},
		// subtype_file_share row removed in Phase 75: file_share is now an allowed subtype (user-initiated file uploads).
		{"subtype_me_message", `{"type":"message","channel":"C1","subtype":"me_message","user":"U1","text":"/me waves","ts":"1.0"}`},
		{"subtype_reminder_add", `{"type":"message","channel":"C1","subtype":"reminder_add","user":"U1","ts":"1.0"}`},
		{"subtype_ekm_access_denied", `{"type":"message","channel":"C1","subtype":"ekm_access_denied","ts":"1.0"}`},
		// Allow-list regression-proof guarantee: any future subtype Slack invents
		// is filtered by default until explicitly added to the allow-list.
		{"subtype_unknown_future", `{"type":"message","channel":"C1","subtype":"some_new_2027_subtype","user":"U1","text":"hi","ts":"1.0"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			h, sqs, threads, _, _, _, _ := newHandler(now)
			body := fmt.Sprintf(`{"type":"event_callback","event_id":"E-%s","event":%s}`, tc.name, tc.event)
			tsHdr, sigHdr := signSlackPayload(t, body, now)
			resp := h.Handle(context.Background(), EventsRequest{
				Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
				Body:    body,
			})
			if resp.StatusCode != 200 {
				t.Fatalf("%s: status=%d body=%s", tc.name, resp.StatusCode, resp.Body)
			}
			if len(sqs.sends) != 0 {
				t.Fatalf("%s: expected no SQS, got %+v", tc.name, sqs.sends)
			}
			if len(threads.upserts) != 0 {
				t.Fatalf("%s: expected no Threads upsert, got %+v", tc.name, threads.upserts)
			}
		})
	}
}

// TestEventsHandler_FileShareSubtype_Allowed verifies the Phase 75 positive case:
// a file_share subtype event (human uploading a file) carries user content and
// MUST pass through isBotLoop to reach SQS dispatch (allow-list extended in Phase 75).
func TestEventsHandler_FileShareSubtype_Allowed(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := newHandler(now)
	body := `{"type":"event_callback","event_id":"E-fs","event":{"type":"message","channel":"C1","subtype":"file_share","user":"U1","text":"","ts":"1.7","thread_ts":"1.0","files":[{"id":"F012345","name":"screenshot.png","mimetype":"image/png","url_private_download":"https://files.slack.com/files-pri/T0/F012345/download/screenshot.png","size":12345}]}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	// file_share with user content must reach SQS (not short-circuit on isBotLoop).
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 SQS send for file_share, got %d (isBotLoop may still be blocking it)", len(sqs.sends))
	}
	if len(threads.upserts) != 1 {
		t.Fatalf("expected 1 threads upsert for file_share, got %d", len(threads.upserts))
	}
}

// TestEventsHandler_ThreadBroadcastPasses verifies the positive case in the
// Phase 67-12 allow-list: thread_broadcast (a user replied in a thread with
// "Also send to channel" ticked) carries human content and MUST reach SQS.
func TestEventsHandler_ThreadBroadcastPasses(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := newHandler(now)
	body := `{"type":"event_callback","event_id":"E-tb","event":{"type":"message","channel":"C1","subtype":"thread_broadcast","user":"U1","text":"shouting from thread","ts":"1.5","thread_ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 SQS send for thread_broadcast, got %d", len(sqs.sends))
	}
	if len(threads.upserts) != 1 {
		t.Fatalf("expected 1 threads upsert for thread_broadcast, got %d", len(threads.upserts))
	}
}

func TestEventsHandler_ReplayedEventID(t *testing.T) {
	now := time.Now()
	h, sqs, _, nonces, _, _, _ := newHandler(now)
	nonces.seen = map[string]bool{EventNoncePrefix + "EVT-DUP": true}
	body := `{"type":"event_callback","event_id":"EVT-DUP","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(sqs.sends) != 0 {
		t.Fatalf("expected no SQS on replay, got %+v", sqs.sends)
	}
}

func TestEventsHandler_UnknownChannel(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, sandboxes, _, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{} // empty SandboxID — channel not in our DB
	body := `{"type":"event_callback","event_id":"E1","event":{"type":"message","channel":"CUNKNOWN","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(sqs.sends) != 0 {
		t.Fatal("expected no SQS for unknown channel")
	}
}

func TestEventsHandler_TopLevelPost_UsesTSAsThreadTS(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := newHandler(now)
	body := `{"type":"event_callback","event_id":"E1","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1714280400.001"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 || len(sqs.sends) != 1 {
		t.Fatalf("status=%d sends=%d", resp.StatusCode, len(sqs.sends))
	}
	var qb InboundQueueBody
	_ = json.Unmarshal([]byte(sqs.sends[0].body), &qb)
	if qb.ThreadTS != "1714280400.001" {
		t.Fatalf("expected ThreadTS=msg.TS for top-level, got %q", qb.ThreadTS)
	}
	if len(threads.upserts) != 1 || threads.upserts[0].ts != "1714280400.001" {
		t.Fatalf("expected upsert with thread_ts=1714280400.001, got %+v", threads.upserts)
	}
}

func TestEventsHandler_InThreadReply_PreservesThreadTS(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := newHandler(now)
	body := `{"type":"event_callback","event_id":"E2","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1714280400.999","thread_ts":"1714280400.001"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 || len(sqs.sends) != 1 {
		t.Fatalf("status=%d sends=%d", resp.StatusCode, len(sqs.sends))
	}
	var qb InboundQueueBody
	_ = json.Unmarshal([]byte(sqs.sends[0].body), &qb)
	if qb.ThreadTS != "1714280400.001" {
		t.Fatalf("expected preserved thread_ts, got %q", qb.ThreadTS)
	}
}

func TestEventsHandler_ValidMessage_HappyPath(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := newHandler(now)
	body := `{"type":"event_callback","event_id":"E3","event":{"type":"message","channel":"C1","user":"U1","text":"refactor the auth module","ts":"1714280400.001"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", resp.StatusCode, resp.Body)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 SQS send, got %d", len(sqs.sends))
	}
	if sqs.sends[0].group != "sb-abc123" {
		t.Fatalf("group=%s, want sb-abc123", sqs.sends[0].group)
	}
	if sqs.sends[0].dedup != "E3" {
		t.Fatalf("dedup=%s, want E3", sqs.sends[0].dedup)
	}
	if len(threads.upserts) != 1 {
		t.Fatalf("expected 1 threads upsert, got %d", len(threads.upserts))
	}
}

// ---- failure-returns-200 tests (RESEARCH.md Pitfall 2 / CONTEXT.md flow step 9) ----

func TestEventsHandler_SQSWriteFailure_Returns200(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := newHandler(now)
	sqs.err = fmt.Errorf("simulated AccessDeniedException")
	body := `{"type":"event_callback","event_id":"EFAIL","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on SQS failure (got %d) — Slack would retry 5xx with new event_id and bypass dedup", resp.StatusCode)
	}
	if resp.Body != "ok" {
		t.Fatalf("expected body \"ok\", got %q", resp.Body)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 sqs send attempt, got %d", len(sqs.sends))
	}
}

func TestEventsHandler_DDBUpsertFailure_Returns200(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := newHandler(now)
	threads.err = fmt.Errorf("simulated ProvisionedThroughputExceededException")
	body := `{"type":"event_callback","event_id":"EDDB","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on DDB upsert failure, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("expected SQS send despite DDB failure, got %d", len(sqs.sends))
	}
}

func TestEventsHandler_SandboxLookupFailure_Returns200(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, sandboxes, _, _ := newHandler(now)
	sandboxes.err = fmt.Errorf("simulated DDB Query failure")
	body := `{"type":"event_callback","event_id":"ELOOKUP","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on sandbox lookup failure, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 0 {
		t.Fatalf("expected zero SQS sends when channel routing fails, got %d", len(sqs.sends))
	}
}

func TestEventsHandler_SigningSecretFetchFailure_Returns200(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := newHandler(now)
	h.SigningSecret = &fakeSigningSecret{err: fmt.Errorf("simulated SSM throttle")}
	body := `{"type":"event_callback","event_id":"ESEC","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on signing-secret fetch failure, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 0 {
		t.Fatalf("expected zero SQS sends when secret unavailable, got %d", len(sqs.sends))
	}
}

// ---- paused-sandbox hint tests (CONTEXT.md "Edge Cases" + checker BLOCKER) ----

func TestEventsHandler_PausedSandbox_FirstMessage(t *testing.T) {
	// First message: cooldown adapter (mocked) accepts the call. Handler
	// contract: invoke PauseHinter when info.Paused=true AFTER SQS write,
	// in a goroutine, fire-and-forget.
	now := time.Now()
	h, sqs, _, _, sandboxes, hinter, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-paused", QueueURL: "https://sqs.example/q.fifo",
		Paused: true,
	}
	body := `{"type":"event_callback","event_id":"EP1","event":{"type":"message","channel":"CPAUSED","user":"U1","text":"wake up","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 on paused first-message, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("expected SQS write on paused, got %d sends", len(sqs.sends))
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(hinter.snapshot()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := hinter.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 PauseHinter call, got %d", len(calls))
	}
	if calls[0].ch != "CPAUSED" || calls[0].ts != "1.0" {
		t.Fatalf("PauseHinter args wrong: ch=%s ts=%s", calls[0].ch, calls[0].ts)
	}
}

func TestEventsHandler_PausedSandbox_WithinCooldown(t *testing.T) {
	// Handler still INVOKES PauseHinter — adapter enforces the cooldown.
	// This test verifies the handler's "always invoke when paused" contract;
	// adapter test in Plan 67-05 verifies actual cooldown skip behavior.
	now := time.Now()
	h, sqs, _, _, sandboxes, hinter, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-paused", QueueURL: "https://sqs.example/q.fifo",
		Paused: true,
	}
	hinter.err = nil // adapter returns nil on cooldown skip — handler can't tell
	body := `{"type":"event_callback","event_id":"EP2","event":{"type":"message","channel":"CPAUSED","user":"U1","text":"another msg","ts":"2.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("expected SQS write, got %d", len(sqs.sends))
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(hinter.snapshot()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(hinter.snapshot()) != 1 {
		t.Fatalf("expected handler to invoke PauseHinter, got %d calls", len(hinter.snapshot()))
	}
}

func TestEventsHandler_NotPaused_NoHint(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, hinter, _ := newHandler(now)
	body := `{"type":"event_callback","event_id":"ELIVE","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 || len(sqs.sends) != 1 {
		t.Fatalf("status=%d sends=%d", resp.StatusCode, len(sqs.sends))
	}
	time.Sleep(50 * time.Millisecond)
	if len(hinter.snapshot()) != 0 {
		t.Fatalf("PauseHinter must NOT be invoked when info.Paused=false, got %d calls", len(hinter.snapshot()))
	}
}

func TestEventsHandler_PausedSandbox_NilHinter_IsNoop(t *testing.T) {
	// nil PauseHinter must not panic; nothing to verify but absence of crash.
	now := time.Now()
	h, sqs, _, _, sandboxes, _, _ := newHandler(now)
	h.PauseHinter = nil
	sandboxes.info = SandboxRoutingInfo{
		SandboxID: "sb-paused", QueueURL: "https://sqs.example/q.fifo",
		Paused: true,
	}
	body := `{"type":"event_callback","event_id":"ENIL","event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr},
		Body:    body,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("nil PauseHinter must not affect response, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("nil PauseHinter must not block SQS write, got %d", len(sqs.sends))
	}
}

// ---- Phase 67.1: ACK reaction test helpers ----

// buildMessageEventBody builds a signed Slack event_callback body for a regular
// human message. channel, ts, threadTS (empty for top-level), user, and text are
// configurable. eventID is auto-generated from the ts to keep tests unique.
func buildMessageEventBody(t *testing.T, channel, ts, threadTS, user, text string) string {
	t.Helper()
	event := map[string]any{
		"type":    "message",
		"channel": channel,
		"user":    user,
		"text":    text,
		"ts":      ts,
	}
	if threadTS != "" {
		event["thread_ts"] = threadTS
	}
	eventBytes, _ := json.Marshal(event)
	outer := map[string]any{
		"type":     "event_callback",
		"event_id": "E-" + ts,
		"event":    json.RawMessage(eventBytes),
	}
	b, _ := json.Marshal(outer)
	return string(b)
}

// buildBotMessageEventBody builds a Slack event_callback body for a bot message
// (bot_id set). Used to exercise the isBotLoop short-circuit.
func buildBotMessageEventBody(t *testing.T, channel, ts, botID, text string) string {
	t.Helper()
	event := map[string]any{
		"type":    "message",
		"channel": channel,
		"text":    text,
		"ts":      ts,
		"bot_id":  botID,
	}
	eventBytes, _ := json.Marshal(event)
	outer := map[string]any{
		"type":     "event_callback",
		"event_id": "E-bot-" + ts,
		"event":    json.RawMessage(eventBytes),
	}
	b, _ := json.Marshal(outer)
	return string(b)
}

// ============================================================
// Phase 67.1: ACK reaction tests
// ============================================================

// TestEventsHandler_Reactor_HappyPath: valid message → Reactor.Add called once
// with (msg.Channel, msg.TS, "eyes") synchronously before Handle returns.
//
// Phase 75.2 lesson applied: the reactor runs inline, NOT in a goroutine,
// because AWS Lambda freezes the runtime when Handle returns and any
// in-flight retry's wall-clock context elapses during the freeze.
func TestEventsHandler_Reactor_HappyPath(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, reactor := newHandler(now)

	body := buildMessageEventBody(t, "C01234567", "1714280400.001", "", "U_HUMAN", "hello sandbox")
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Body: body,
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d (body=%s)", resp.StatusCode, resp.Body)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 SQS message, got %d", len(sqs.sends))
	}

	// Synchronous: reactor must already have been invoked by the time Handle returns.
	calls := reactor.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 reactor call after Handle returns, got %d", len(calls))
	}
	if calls[0].channel != "C01234567" || calls[0].ts != "1714280400.001" || calls[0].emoji != "eyes" {
		t.Errorf("unexpected reactor call: %+v", calls[0])
	}
}

// TestEventsHandler_Reactor_FailureDoesNotBlock: Reactor.Add error does NOT
// surface to caller; SQS still has the message; response is still 200.
// The reactor runs synchronously but its error is logged and swallowed.
func TestEventsHandler_Reactor_FailureDoesNotBlock(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, reactor := newHandler(now)
	reactor.err = errors.New("simulated missing_scope")

	body := buildMessageEventBody(t, "C01234567", "1714280400.002", "", "U_HUMAN", "hello")
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Body: body,
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 even on reactor failure, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("expected SQS write to succeed despite reactor error, got %d sends", len(sqs.sends))
	}
	if len(reactor.snapshot()) != 1 {
		t.Errorf("expected reactor invoked exactly once even on error path")
	}
}

// TestEventsHandler_Reactor_BotLoopSkips: a message matching isBotLoop
// (bot_id set) returns at step 4, never reaches step 10 → Reactor not called.
func TestEventsHandler_Reactor_BotLoopSkips(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, reactor := newHandler(now)

	// Build a bot-loop message: bot_id set.
	body := buildBotMessageEventBody(t, "C01234567", "1714280400.003", "B_BOT", "I am a bot")
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Body: body,
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 0 {
		t.Errorf("expected NO SQS send for bot-loop, got %d", len(sqs.sends))
	}
	if calls := reactor.snapshot(); len(calls) != 0 {
		t.Errorf("expected NO reactor calls for bot-loop, got %d: %+v", len(calls), calls)
	}
}

// TestEventsHandler_Reactor_NilReactorIsNoop: with Reactor=nil, Handle does
// not panic and SQS still receives the message. Back-compat for tests that
// don't wire a reactor.
func TestEventsHandler_Reactor_NilReactorIsNoop(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := newHandler(now)
	h.Reactor = nil // explicit nil

	body := buildMessageEventBody(t, "C01234567", "1714280400.004", "", "U_HUMAN", "hi")
	tsHdr, sigHdr := signSlackPayload(t, body, now)

	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Handle panicked with nil Reactor: %v", r)
		}
	}()

	resp := h.Handle(context.Background(), EventsRequest{
		Body: body,
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 {
		t.Errorf("expected 1 SQS send even without reactor, got %d", len(sqs.sends))
	}
}

// ============================================================
// Phase 75: File-fork goroutine tests
// ============================================================

// TestEventsHandler_WithFiles_Synchronous verifies that when a file_share event
// carries len(Files)>0 and a non-nil FileDownloader is wired:
//  1. Download is invoked synchronously within Handle.
//  2. SQS write happens before Handle returns (with attachments populated).
//  3. Handle returns 200.
//
// Phase 75.2: the original Phase 75 design spawned a goroutine to return 200
// inside Slack's 3s ack window, but AWS Lambda freezes the runtime when the
// handler returns and the in-flight HTTP request's wall-clock deadline
// elapses during the freeze. Synchronous handling is sound because the
// event_id dedup check earlier in Handle absorbs any 3s Slack retry.
func TestEventsHandler_WithFiles_Synchronous(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := newHandler(now)

	// Wire a SlackPoster so warnings (if any) can be posted; no warnings in this test.
	slackPoster := &fakeSlackPoster{}
	h.Slack = slackPoster

	// Mock downloader: returns one Attachment, no errors. No artificial delay —
	// synchronous handling is the contract; the test just asserts ordering.
	dl := newSlowDownloader(0)
	h.FileDownloader = dl

	// Build a file_share event with 1 file.
	event := map[string]any{
		"type":    "message",
		"channel": "C01234567",
		"user":    "U_HUMAN",
		"text":    "",
		"ts":      "1714280400.010",
		"subtype": "file_share",
		"files": []map[string]any{
			{
				"id":                   "F001",
				"name":                 "screenshot.png",
				"mimetype":             "image/png",
				"url_private_download": "https://files.slack.com/screenshot.png",
				"size":                 12345,
			},
		},
	}
	eventBytes, _ := json.Marshal(event)
	outer := map[string]any{
		"type":     "event_callback",
		"event_id": "E-files-test",
		"event":    json.RawMessage(eventBytes),
	}
	body, _ := json.Marshal(outer)
	bodyStr := string(body)
	tsHdr, sigHdr := signSlackPayload(t, bodyStr, now)

	resp := h.Handle(context.Background(), EventsRequest{
		Body: bodyStr,
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
	})

	// 1. Handle must return 200.
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// 2. Download must have been called inside Handle (signal was already sent).
	select {
	case <-dl.signal:
		// Good — Download was invoked.
	default:
		t.Error("Download was not called before Handle returned (synchronous contract broken)")
	}

	// 3. Exactly 1 synchronous SQS send must have happened.
	if len(sqs.sends) != 1 {
		t.Fatalf("expected 1 synchronous SQS send, got %d", len(sqs.sends))
	}
}

// fakeNoncesCounter wraps fakeNonces and counts CheckAndStore invocations.
// Used by TestEventsHandler_MentionOnly to verify skipped messages do NOT
// consume a nonce slot (must be placed before dedup, per PLAN 91-03 MUST-HAVE).
type fakeNoncesCounter struct {
	inner     fakeNonces
	callCount int
}

func (f *fakeNoncesCounter) CheckAndStore(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	f.callCount++
	return f.inner.CheckAndStore(ctx, id, ttl)
}

// TestEventsHandler_MentionOnly verifies the Phase 91 polite-bot mention-scan guard
// (step 4b in Handle). The 7 cases exercise:
//   - mention-only disabled: every message dispatched
//   - mention-only enabled: dispatch when @mention present, skip when absent
//   - edge cases: mention at start, mention at end, wrong-bot mention, fetch error
//
// Critically, skipped messages must NOT invoke Nonces.CheckAndStore (the guard sits
// BEFORE step 5 dedup so non-mention messages don't consume nonce slots).
func TestEventsHandler_MentionOnly(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		mentionOnly bool
		botUID      string
		botUIDErr   error
		messageText string
		wantSkipped bool
	}{
		{
			name:        "mention-only=false, no @mention → dispatched",
			mentionOnly: false,
			botUID:      "UBOT123",
			messageText: "hello world",
			wantSkipped: false,
		},
		{
			name:        "mention-only=true, has @mention → dispatched",
			mentionOnly: true,
			botUID:      "UBOT123",
			messageText: "hey <@UBOT123> do the thing",
			wantSkipped: false,
		},
		{
			name:        "mention-only=true, no @mention → skipped",
			mentionOnly: true,
			botUID:      "UBOT123",
			messageText: "hello world",
			wantSkipped: true,
		},
		{
			name:        "mention-only=true, different @mention → skipped",
			mentionOnly: true,
			botUID:      "UBOT123",
			messageText: "hey <@UOTHER456> do the thing",
			wantSkipped: true,
		},
		{
			name:        "mention-only=true, mention at start → dispatched",
			mentionOnly: true,
			botUID:      "UBOT123",
			messageText: "<@UBOT123> please summarise",
			wantSkipped: false,
		},
		{
			name:        "mention-only=true, mention at end → dispatched",
			mentionOnly: true,
			botUID:      "UBOT123",
			messageText: "please summarise <@UBOT123>",
			wantSkipped: false,
		},
		{
			name:        "mention-only=true, bot_uid fetch error → fail-open",
			mentionOnly: true,
			botUID:      "",
			botUIDErr:   errors.New("ssm: parameter not found"),
			messageText: "hello world",
			wantSkipped: false,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, _, _, _, _, _ := newHandler(now)

			// Replace BotUserID with a fake that returns the test's uid/err.
			h.BotUserID = &fakeBotUserID{uid: tc.botUID, err: tc.botUIDErr}

			// Replace BotUserID in isBotLoop path too — use a UID that won't
			// match the message sender "U1" so isBotLoop passes through cleanly.
			// (UBOT123 is the bot; messages are sent from "U1" — no conflict.)

			// Swap in a counting nonce store.
			nonceCounter := &fakeNoncesCounter{}
			h.Nonces = nonceCounter

			h.MentionOnly = tc.mentionOnly

			// Build a unique event to avoid cross-test nonce collisions.
			eventID := fmt.Sprintf("EMO-%d", i)
			body := fmt.Sprintf(
				`{"type":"event_callback","event_id":%q,"event":{"type":"message","channel":"C1","user":"U1","text":%q,"ts":"1.0"}}`,
				eventID, tc.messageText,
			)
			tsHdr, sigHdr := signSlackPayload(t, body, now)
			resp := h.Handle(context.Background(), EventsRequest{
				Headers: map[string]string{
					"x-slack-request-timestamp": tsHdr,
					"x-slack-signature":         sigHdr,
				},
				Body: body,
			})

			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}

			if tc.wantSkipped {
				// Skipped: no SQS write, and no nonce slot consumed (guard is before step 5).
				if len(sqs.sends) != 0 {
					t.Errorf("expected no SQS write when skipped, got %d sends", len(sqs.sends))
				}
				if nonceCounter.callCount != 0 {
					t.Errorf("expected nonce CheckAndStore NOT called when skipped (guard before dedup), got callCount=%d", nonceCounter.callCount)
				}
			} else {
				// Dispatched: SQS write happened.
				if len(sqs.sends) != 1 {
					t.Errorf("expected 1 SQS send when dispatched, got %d", len(sqs.sends))
				}
			}
		})
	}
}

// TestEventsHandler_MentionOnly_ThreadBypass verifies the Phase 91.3 thread-bypass.
// When MentionOnly=true and the message is a reply in a thread the bot already
// owns (sandbox_id row exists in km-slack-threads), the mention requirement is
// skipped. Top-level messages and replies in unknown threads still require mention.
func TestEventsHandler_MentionOnly_ThreadBypass(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		threadTS     string            // empty = top-level; non-empty = reply
		sandboxByT   map[string]string // existing thread rows keyed by ch|ts
		messageText  string
		wantSkipped  bool
		wantDispatch bool
	}{
		{
			name:         "engaged thread, no mention → dispatched (bypass)",
			threadTS:     "1.0",
			sandboxByT:   map[string]string{"C1|1.0": "sb-abc"},
			messageText:  "what about edge cases?",
			wantDispatch: true,
		},
		{
			name:         "engaged thread, with mention → dispatched (bypass + match)",
			threadTS:     "1.0",
			sandboxByT:   map[string]string{"C1|1.0": "sb-abc"},
			messageText:  "<@UBOT123> what about edge cases?",
			wantDispatch: true,
		},
		{
			name:        "thread reply but no row, no mention → skipped (no bypass)",
			threadTS:    "2.0",
			sandboxByT:  map[string]string{}, // no row for C1|2.0
			messageText: "hi",
			wantSkipped: true,
		},
		{
			name:         "top-level, no mention → skipped (bypass never applies)",
			threadTS:     "", // top-level
			sandboxByT:   map[string]string{"C1|3.0": "sb-abc"},
			messageText:  "hi",
			wantSkipped:  true,
		},
		{
			name:         "top-level, with mention → dispatched (normal mention scan)",
			threadTS:     "",
			messageText:  "<@UBOT123> kick off a turn",
			wantDispatch: true,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, threads, _, _, _, _ := newHandler(now)
			h.BotUserID = &fakeBotUserID{uid: "UBOT123"}
			h.MentionOnly = true
			threads.sandboxByThread = tc.sandboxByT

			body := fmt.Sprintf(
				`{"type":"event_callback","event_id":%q,"event":{"type":"message","channel":"C1","user":"U1","text":%q,"ts":"%d.0","thread_ts":%q}}`,
				fmt.Sprintf("ETB-%d", i), tc.messageText, 100+i, tc.threadTS,
			)
			tsHdr, sigHdr := signSlackPayload(t, body, now)
			resp := h.Handle(context.Background(), EventsRequest{
				Headers: map[string]string{
					"x-slack-request-timestamp": tsHdr,
					"x-slack-signature":         sigHdr,
				},
				Body: body,
			})
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}

			if tc.wantSkipped && len(sqs.sends) != 0 {
				t.Errorf("expected skip but got %d sends", len(sqs.sends))
			}
			if tc.wantDispatch && len(sqs.sends) != 1 {
				t.Errorf("expected dispatch but got %d sends", len(sqs.sends))
			}
		})
	}
}

// TestEventsHandler_ReactAlways verifies the Phase 91.4 first-only-react toggle
// at step 10 of Handle(). When ReactAlways=true (default), every dispatched
// message gets a 👀. When ReactAlways=false, only top-level messages
// (msg.ThreadTS == "") get a reaction; thread replies dispatch silently.
func TestEventsHandler_ReactAlways(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		reactAlways bool
		threadTS    string
		wantReact   bool
	}{
		{
			name:        "react-always=true, top-level → reacts",
			reactAlways: true,
			threadTS:    "",
			wantReact:   true,
		},
		{
			name:        "react-always=true, thread reply → reacts",
			reactAlways: true,
			threadTS:    "1.0",
			wantReact:   true,
		},
		{
			name:        "react-always=false, top-level → reacts (engagement)",
			reactAlways: false,
			threadTS:    "",
			wantReact:   true,
		},
		{
			name:        "react-always=false, thread reply → silent",
			reactAlways: false,
			threadTS:    "1.0",
			wantReact:   false,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _, threads, _, _, _, reactor := newHandler(now)
			h.ReactAlways = tc.reactAlways
			// Skip mention-only entirely so we focus the test on the reactor.
			h.MentionOnly = false
			// For thread replies, mark the thread as engaged so dispatch proceeds
			// even when (in real flow) a mention-only filter might gate it.
			if tc.threadTS != "" {
				threads.sandboxByThread = map[string]string{"C1|" + tc.threadTS: "sb-abc"}
			}

			body := fmt.Sprintf(
				`{"type":"event_callback","event_id":%q,"event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"%d.0","thread_ts":%q}}`,
				fmt.Sprintf("ERA-%d", i), 500+i, tc.threadTS,
			)
			tsHdr, sigHdr := signSlackPayload(t, body, now)
			resp := h.Handle(context.Background(), EventsRequest{
				Headers: map[string]string{
					"x-slack-request-timestamp": tsHdr,
					"x-slack-signature":         sigHdr,
				},
				Body: body,
			})
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}

			if tc.wantReact && len(reactor.calls) != 1 {
				t.Errorf("expected 1 reaction, got %d", len(reactor.calls))
			}
			if !tc.wantReact && len(reactor.calls) != 0 {
				t.Errorf("expected no reaction (first-only mode in thread), got %d", len(reactor.calls))
			}
		})
	}
}

// TestEventsHandler_ReactAlways_PerSandboxOverride — Phase 91.5. When the
// channel-lookup returns SandboxRoutingInfo with ReactAlways non-nil, the
// per-sandbox value wins over the install-level h.ReactAlways. Truth table:
//   h.ReactAlways × info.ReactAlways × msg.ThreadTS → reaction posted?
func TestEventsHandler_ReactAlways_PerSandboxOverride(t *testing.T) {
	now := time.Now()
	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name        string
		installRA   bool   // h.ReactAlways
		perSbRA     *bool  // info.ReactAlways
		threadTS    string
		wantReact   bool
	}{
		{"install=true, per-sb nil, thread → reacts (install wins)", true, nil, "1.0", true},
		{"install=false, per-sb nil, thread → silent (install wins)", false, nil, "1.0", false},
		{"install=true, per-sb=&false, thread → silent (per-sb overrides)", true, boolPtr(false), "1.0", false},
		{"install=false, per-sb=&true, thread → reacts (per-sb overrides)", false, boolPtr(true), "1.0", true},
		{"install=true, per-sb=&false, top-level → reacts (engagement always)", true, boolPtr(false), "", true},
		{"install=false, per-sb=&true, top-level → reacts (engagement)", false, boolPtr(true), "", true},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, _, threads, _, sandboxes, _, reactor := newHandler(now)
			h.ReactAlways = tc.installRA
			h.MentionOnly = false
			sandboxes.info.ReactAlways = tc.perSbRA
			if tc.threadTS != "" {
				threads.sandboxByThread = map[string]string{"C1|" + tc.threadTS: "sb-abc"}
			}

			body := fmt.Sprintf(
				`{"type":"event_callback","event_id":%q,"event":{"type":"message","channel":"C1","user":"U1","text":"hi","ts":"%d.0","thread_ts":%q}}`,
				fmt.Sprintf("EPS-%d", i), 700+i, tc.threadTS,
			)
			tsHdr, sigHdr := signSlackPayload(t, body, now)
			resp := h.Handle(context.Background(), EventsRequest{
				Headers: map[string]string{
					"x-slack-request-timestamp": tsHdr,
					"x-slack-signature":         sigHdr,
				},
				Body: body,
			})
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}
			if tc.wantReact && len(reactor.calls) != 1 {
				t.Errorf("expected 1 reaction, got %d", len(reactor.calls))
			}
			if !tc.wantReact && len(reactor.calls) != 0 {
				t.Errorf("expected no reaction, got %d", len(reactor.calls))
			}
		})
	}
}

// TestEventsHandler_MentionOnly_PerSandboxOverride verifies the per-sandbox
// mention_only override: when FetchByChannel returns SandboxRoutingInfo with
// MentionOnly non-nil, the per-sandbox value wins over the install-level
// h.MentionOnly. (Wires notification.slack.inbound.mentionOnly through to the
// bridge, mirroring the Phase 91.5 react_always override.)
func TestEventsHandler_MentionOnly_PerSandboxOverride(t *testing.T) {
	now := time.Now()
	boolPtr := func(b bool) *bool { return &b }
	const botUID = "UBOT123"

	tests := []struct {
		name        string
		installMO   bool   // h.MentionOnly
		perSbMO     *bool  // info.MentionOnly
		text        string // top-level message text
		wantSkipped bool
	}{
		{"install=false, per-sb nil, no mention → dispatched (install chatty)", false, nil, "hello", false},
		{"install=true, per-sb nil, no mention → skipped (install polite)", true, nil, "hello", true},
		{"install=true, per-sb=&false, no mention → dispatched (per-sb forces chatty)", true, boolPtr(false), "hello", false},
		{"install=false, per-sb=&true, no mention → skipped (per-sb forces polite)", false, boolPtr(true), "hello", true},
		{"install=false, per-sb=&true, WITH mention → dispatched", false, boolPtr(true), "yo <@UBOT123>", false},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, _, _, sandboxes, _, _ := newHandler(now)
			h.MentionOnly = tc.installMO
			h.BotUserID = &fakeBotUserID{uid: botUID}
			sandboxes.info.MentionOnly = tc.perSbMO

			body := fmt.Sprintf(
				`{"type":"event_callback","event_id":%q,"event":{"type":"message","channel":"C1","user":"U1","text":%q,"ts":"%d.0"}}`,
				fmt.Sprintf("EMO-%d", i), tc.text, 800+i,
			)
			tsHdr, sigHdr := signSlackPayload(t, body, now)
			resp := h.Handle(context.Background(), EventsRequest{
				Headers: map[string]string{
					"x-slack-request-timestamp": tsHdr,
					"x-slack-signature":         sigHdr,
				},
				Body: body,
			})
			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}
			if tc.wantSkipped && len(sqs.sends) != 0 {
				t.Errorf("expected skip (no SQS write), got %d sends", len(sqs.sends))
			}
			if !tc.wantSkipped && len(sqs.sends) != 1 {
				t.Errorf("expected dispatch (1 SQS write), got %d sends", len(sqs.sends))
			}
		})
	}
}

// ── Phase 95: Federated relay decision table ─────────────────────────────────

// fakePeerRelayer is a test double for PeerRelayer.
// Records all Broadcast calls for assertion and returns a configurable error.
type fakePeerRelayer struct {
	mu    sync.Mutex
	calls []fakeRelayCall
	err   error
}

type fakeRelayCall struct {
	body    string
	headers map[string]string
}

func (f *fakePeerRelayer) Broadcast(ctx context.Context, rawBody string, h map[string]string) error {
	f.mu.Lock()
	f.calls = append(f.calls, fakeRelayCall{rawBody, h})
	f.mu.Unlock()
	return f.err
}

func (f *fakePeerRelayer) snapshot() []fakeRelayCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeRelayCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestEventsHandler_FederatedRelay exercises the four-row decision table at the
// FetchByChannel miss site (Phase 95). The table:
//
//	| X-KM-Relayed? | Owns channel? | Action                               |
//	|---------------|---------------|--------------------------------------|
//	| absent        | yes           | process locally; Relayer NOT invoked |
//	| absent        | no            | Broadcast called once; returns 200   |
//	| present       | yes           | process locally; Relayer NOT invoked |
//	| present       | no            | drop (slack_relay_no_owner); Relayer NOT invoked (loop guard) |
func TestEventsHandler_FederatedRelay(t *testing.T) {
	now := time.Now()

	makeBody := func(eventID, channel string) string {
		return fmt.Sprintf(
			`{"type":"event_callback","event_id":%q,"event":{"type":"message","channel":%q,"user":"U1","text":"hello","ts":"1.0"}}`,
			eventID, channel,
		)
	}

	tests := []struct {
		name          string
		relayed       bool // true = set X-KM-Relayed: 1
		owns          bool // true = FetchByChannel returns a valid sandbox
		wantBroadcast int  // expected number of Relayer.Broadcast calls
		wantSQS       int  // expected number of SQS sends
	}{
		{
			name:          "absent+owns → process locally; relayer NOT invoked",
			relayed:       false,
			owns:          true,
			wantBroadcast: 0,
			wantSQS:       1,
		},
		{
			name:          "absent+miss → Broadcast called once; returns 200",
			relayed:       false,
			owns:          false,
			wantBroadcast: 1,
			wantSQS:       0,
		},
		{
			name:          "present+owns → process locally; relayer NOT invoked",
			relayed:       true,
			owns:          true,
			wantBroadcast: 0,
			wantSQS:       1,
		},
		{
			name:          "present+miss → drop (loop guard); relayer NOT invoked",
			relayed:       true,
			owns:          false,
			wantBroadcast: 0, // CRITICAL: relayer must NEVER be called on relayed miss
			wantSQS:       0,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, _, _, sandboxes, _, _ := newHandler(now)

			relayer := &fakePeerRelayer{}
			h.Relayer = relayer

			// Configure owns-channel via the sandboxes stub.
			if tc.owns {
				sandboxes.info = SandboxRoutingInfo{
					SandboxID: "sb-abc123",
					QueueURL:  "https://sqs.example/queue.fifo",
				}
			} else {
				sandboxes.info = SandboxRoutingInfo{} // empty = unknown channel
			}

			body := makeBody(fmt.Sprintf("EFED-%d", i), "C1")
			tsHdr, sigHdr := signSlackPayload(t, body, now)

			hdrs := map[string]string{
				"x-slack-request-timestamp": tsHdr,
				"x-slack-signature":         sigHdr,
			}
			if tc.relayed {
				hdrs["x-km-relayed"] = "1"
			}

			resp := h.Handle(context.Background(), EventsRequest{
				Headers: hdrs,
				Body:    body,
			})

			if resp.StatusCode != 200 {
				t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, resp.Body)
			}

			calls := relayer.snapshot()
			if len(calls) != tc.wantBroadcast {
				t.Errorf("Relayer.Broadcast calls: got %d, want %d", len(calls), tc.wantBroadcast)
			}

			if len(sqs.sends) != tc.wantSQS {
				t.Errorf("SQS sends: got %d, want %d", len(sqs.sends), tc.wantSQS)
			}

			// Loop-impossibility assertion: on relayed+miss, relayer is strictly NOT invoked.
			if tc.relayed && !tc.owns && len(calls) != 0 {
				t.Errorf("LOOP GUARD VIOLATED: relayer was invoked on a relayed miss (calls=%d); loops now possible", len(calls))
			}

			// When broadcast is expected, verify the original body was forwarded.
			if tc.wantBroadcast > 0 && len(calls) == 1 {
				if calls[0].body != body {
					t.Errorf("Broadcast received wrong body: got %q, want %q", calls[0].body, body)
				}
			}
		})
	}
}

// TestEventsHandler_NilRelayer_MissReturns200 is the hard nil-invariant assertion:
// when EventsHandler.Relayer is nil and FetchByChannel returns no owner (miss),
// Handle MUST return 200 without broadcasting — byte-identical to today's behavior.
func TestEventsHandler_NilRelayer_MissReturns200(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, sandboxes, _, _ := newHandler(now)

	// Explicitly nil Relayer — federation off.
	h.Relayer = nil

	// Unknown channel (miss).
	sandboxes.info = SandboxRoutingInfo{}

	body := `{"type":"event_callback","event_id":"ENILREL","event":{"type":"message","channel":"C-unknown","user":"U1","text":"hello","ts":"1.0"}}`
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{
		Headers: map[string]string{
			"x-slack-request-timestamp": tsHdr,
			"x-slack-signature":         sigHdr,
		},
		Body: body,
	})

	if resp.StatusCode != 200 {
		t.Fatalf("nil Relayer + miss: expected 200, got %d body=%s", resp.StatusCode, resp.Body)
	}
	if len(sqs.sends) != 0 {
		t.Errorf("nil Relayer + miss: expected no SQS sends, got %d", len(sqs.sends))
	}
}

// TestVerifySlackSignature_Relayed proves SLACK-FED-VERIFY: a request forwarded
// by HTTPPeerRelayer with the original body + X-Slack-Request-Timestamp +
// X-Slack-Signature passes verifySlackSignature on the receiving peer with the
// shared signing secret. This is a structural guarantee — the HMAC covers the
// (timestamp, body) pair unchanged, so forwarding them verbatim is sufficient.
func TestVerifySlackSignature_Relayed(t *testing.T) {
	const sharedSecret = "test-signing-secret-32-chars-aaaa" // same as testSigningSecret
	rawBody := `{"type":"event_callback","event":{"type":"message","text":"hello"}}`
	now := time.Now()

	// Compute the original Slack signature (as Slack would).
	tsHdr, sigHdr := signSlackPayload(t, rawBody, now)

	// Verify that forwarding (body, tsHdr, sigHdr) verbatim passes the HMAC check.
	// This is exactly what HTTPPeerRelayer does: it copies these headers onto the
	// outbound request without modification.
	if err := verifySlackSignature(sharedSecret, tsHdr, rawBody, sigHdr, now); err != nil {
		t.Fatalf("relayed request failed verifySlackSignature: %v", err)
	}

	// Sanity: mutating the body must fail (ensures we're actually checking the HMAC).
	if err := verifySlackSignature(sharedSecret, tsHdr, rawBody+"tampered", sigHdr, now); err == nil {
		t.Fatal("expected signature mismatch for tampered body; got nil error")
	}

	// Sanity: a stale timestamp (>5 min) must fail regardless of HMAC correctness.
	staleTS, staleSig := signSlackPayload(t, rawBody, now.Add(-6*time.Minute))
	if err := verifySlackSignature(sharedSecret, staleTS, rawBody, staleSig, now); err == nil {
		t.Fatal("expected stale-timestamp error; got nil")
	}
}

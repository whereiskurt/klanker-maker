package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"
)

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
}

func (f *fakeThreads) Get(ctx context.Context, ch, ts string) (string, error) { return "", nil }
func (f *fakeThreads) Upsert(ctx context.Context, ch, ts, sb string) error {
	f.upserts = append(f.upserts, struct{ chan_, ts, sb string }{ch, ts, sb})
	return f.err
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

func newHandler(now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeNonces, *fakeSandboxes, *fakePauseHinter) {
	sqs := &fakeSQS{}
	threads := &fakeThreads{}
	nonces := &fakeNonces{}
	sandboxes := &fakeSandboxes{
		info: SandboxRoutingInfo{SandboxID: "sb-abc123", QueueURL: "https://sqs.example/queue.fifo"},
	}
	pauseHinter := &fakePauseHinter{}
	return &EventsHandler{
		SigningSecret: &fakeSigningSecret{secret: testSigningSecret},
		BotUserID:     &fakeBotUserID{uid: "UBOT123"},
		Nonces:        nonces,
		Sandboxes:     sandboxes,
		Threads:       threads,
		SQS:           sqs,
		PauseHinter:   pauseHinter,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           func() time.Time { return now },
	}, sqs, threads, nonces, sandboxes, pauseHinter
}

// ---- tests ----

func TestEventsHandler_URLVerification(t *testing.T) {
	h, sqs, _, _, _, _ := newHandler(time.Now())
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
	h, sqs, _, _, _, _ := newHandler(now)
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
	h, _, _, _, _, _ := newHandler(now)
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
	h, _, _, _, _, _ := newHandler(now)
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
		{"subtype_file_share", `{"type":"message","channel":"C1","subtype":"file_share","user":"U1","text":"shared a file","ts":"1.0"}`},
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
			h, sqs, threads, _, _, _ := newHandler(now)
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

// TestEventsHandler_ThreadBroadcastPasses verifies the positive case in the
// Phase 67-12 allow-list: thread_broadcast (a user replied in a thread with
// "Also send to channel" ticked) carries human content and MUST reach SQS.
func TestEventsHandler_ThreadBroadcastPasses(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _ := newHandler(now)
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
	h, sqs, _, nonces, _, _ := newHandler(now)
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
	h, sqs, _, _, sandboxes, _ := newHandler(now)
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
	h, sqs, threads, _, _, _ := newHandler(now)
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
	h, sqs, _, _, _, _ := newHandler(now)
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
	h, sqs, threads, _, _, _ := newHandler(now)
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
	h, sqs, _, _, _, _ := newHandler(now)
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
	h, sqs, threads, _, _, _ := newHandler(now)
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
	h, sqs, _, _, sandboxes, _ := newHandler(now)
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
	h, sqs, _, _, _, _ := newHandler(now)
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
	h, sqs, _, _, sandboxes, hinter := newHandler(now)
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
	h, sqs, _, _, sandboxes, hinter := newHandler(now)
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
	h, sqs, _, _, _, hinter := newHandler(now)
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
	h, sqs, _, _, sandboxes, _ := newHandler(now)
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

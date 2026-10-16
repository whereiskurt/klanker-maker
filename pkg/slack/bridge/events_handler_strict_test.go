package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

type fakeReader struct {
	calls   int
	replies []SlackReplyMessage
	err     error
}

func (f *fakeReader) Replies(_ context.Context, _, _, _ string) ([]SlackReplyMessage, error) {
	f.calls++
	return f.replies, f.err
}

func strictEvent(t *testing.T, now time.Time, text, ts, threadTS, user, eventID string) EventsRequest {
	t.Helper()
	body := fmt.Sprintf(`{"type":"event_callback","event_id":%q,"event":{"type":"message","channel":"C1","user":%q,"text":%q,"ts":%q,"thread_ts":%q}}`, eventID, user, text, ts, threadTS)
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	return EventsRequest{Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr}, Body: body}
}

// Mapped channel, mention-only on, engaged thread: the B4.1 matrix × ThreadBypass.
func TestStrictMode_EngagedThread_Matrix(t *testing.T) {
	now := time.Now()
	f := false
	tr := true
	cases := []struct {
		name         string
		bypass       *bool
		text         string
		wantDispatch bool
		wantContext  bool
	}{
		{"nil bypass, no mention → dispatch (91.3)", nil, "no mention", true, false},
		{"true bypass, no mention → dispatch", &tr, "no mention", true, false},
		{"strict, no mention → drop", &f, "no mention", false, false},
		{"strict, mention → dispatch + context", &f, "<@UBOT123> now?", true, true},
		{"nil bypass, mention → dispatch, no context", nil, "<@UBOT123> now?", true, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, sqs, threads, nonces, sandboxes, _, reactor := newHandler(now)
			h.MentionOnly = true
			sandboxes.info.ThreadBypass = tc.bypass
			threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
			threads.boundary = map[string]string{"C1|1.0": "1.000001"}
			reader := &fakeReader{replies: []SlackReplyMessage{
				{User: "U0RON", TS: "1.000002", Text: "human one"},
				{User: "UBOT123", TS: "1.000003", Text: "bot"},
				{User: "U0KAR", TS: "1.000004", Text: "human two"},
			}}
			h.ThreadReader = reader
			resp := h.Handle(context.Background(), strictEvent(t, now, tc.text, "1.000005", "1.0", "U0KURT", fmt.Sprintf("ST-%d", i)))
			if resp.StatusCode != 200 {
				t.Fatalf("status %d", resp.StatusCode)
			}
			if !tc.wantDispatch {
				if len(sqs.sends) != 0 || len(threads.upserts) != 0 || len(reactor.snapshot()) != 0 || len(nonces.seen) != 0 || reader.calls != 0 {
					t.Fatalf("strict drop must touch nothing: sends=%d upserts=%d reactions=%d nonces=%d reader=%d",
						len(sqs.sends), len(threads.upserts), len(reactor.snapshot()), len(nonces.seen), reader.calls)
				}
				return
			}
			if len(sqs.sends) != 1 {
				t.Fatalf("want dispatch, sends=%d", len(sqs.sends))
			}
			var body InboundQueueBody
			json.Unmarshal([]byte(sqs.sends[0].body), &body)
			if tc.wantContext {
				if body.ThreadContext == nil || len(body.ThreadContext.Messages) != 2 || body.ThreadContext.Messages[0].Text != "human one" {
					t.Fatalf("context %+v", body.ThreadContext)
				}
				if reader.calls != 1 {
					t.Fatalf("reader calls %d", reader.calls)
				}
			} else if body.ThreadContext != nil || reader.calls != 0 {
				t.Fatalf("no context expected; ctx=%+v reader=%d", body.ThreadContext, reader.calls)
			}
			if len(threads.marks) != 1 || threads.marks[0].ev != "1.000005" {
				t.Fatalf("MarkDispatched after delivery; marks=%+v", threads.marks)
			}
		})
	}
}

func TestStrictMode_ContextRespectsAllowlist(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, _, sandboxes, _, _ := newHandler(now)
	h.MentionOnly = true
	h.Allow = []string{"U0KURT", "U0RON"}
	sandboxes.info.ThreadBypass = &f
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
	h.ThreadReader = &fakeReader{replies: []SlackReplyMessage{
		{User: "U0RON", TS: "1.000002", Text: "listed"},
		{User: "U0EVIL", TS: "1.000003", Text: "unlisted secret"},
	}}
	h.Handle(context.Background(), strictEvent(t, now, "<@UBOT123> go", "1.000005", "1.0", "U0KURT", "SA-1"))
	if len(sqs.sends) != 1 {
		t.Fatalf("want dispatch, sends=%d", len(sqs.sends))
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	if body.ThreadContext == nil || len(body.ThreadContext.Messages) != 1 || body.ThreadContext.OmittedNonAllow != 1 {
		t.Fatalf("context %+v", body.ThreadContext)
	}
}

func TestStrictMode_ContextRunsAfterDedup(t *testing.T) {
	now := time.Now()
	f := false
	h, _, threads, _, sandboxes, _, _ := newHandler(now)
	h.MentionOnly = true
	sandboxes.info.ThreadBypass = &f
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
	reader := &fakeReader{}
	h.ThreadReader = reader
	req := strictEvent(t, now, "<@UBOT123> go", "1.000005", "1.0", "U0KURT", "DUP-1")
	h.Handle(context.Background(), req)
	h.Handle(context.Background(), req) // Slack retry, same event_id
	if reader.calls != 1 {
		t.Fatalf("replies must be fetched once; got %d", reader.calls)
	}
}

func TestStrictMode_ReaderErrorStillDispatches(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, _, sandboxes, _, _ := newHandler(now)
	h.MentionOnly = true
	sandboxes.info.ThreadBypass = &f
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
	h.ThreadReader = &fakeReader{err: errors.New("slack error \"ratelimited\"")}
	h.Handle(context.Background(), strictEvent(t, now, "<@UBOT123> go", "1.000005", "1.0", "U0KURT", "RE-1"))
	if len(sqs.sends) != 1 {
		t.Fatalf("reader error must not block the turn; sends=%d", len(sqs.sends))
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	if body.ThreadContext == nil || body.ThreadContext.Error == "" {
		t.Fatalf("want ThreadContext.Error, got %+v", body.ThreadContext)
	}
}

// contextDownloader returns one attachment per file, or a per-call error for a
// file named "boom", and records the sandbox id each call was keyed on.
type contextDownloader struct {
	sandboxIDs []string
}

func (d *contextDownloader) Download(_ context.Context, files []SlackFile, sandboxID, _ string) ([]Attachment, []FileError, error) {
	d.sandboxIDs = append(d.sandboxIDs, sandboxID)
	if len(files) == 1 && files[0].Name == "boom" {
		return nil, nil, errors.New("token fetch failed")
	}
	var out []Attachment
	for _, f := range files {
		out = append(out, Attachment{S3Key: "k/" + f.Name, OriginalName: f.Name})
	}
	return out, nil, nil
}

// Context files land on the message that carried them, keyed on the sandbox id;
// a download failure becomes a FileError and never blocks the turn.
func TestStrictMode_ContextFiles(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, _, sandboxes, _, _ := newHandler(now)
	h.MentionOnly = true
	sandboxes.info.ThreadBypass = &f
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
	dl := &contextDownloader{}
	h.FileDownloader = dl
	h.ThreadReader = &fakeReader{replies: []SlackReplyMessage{
		{User: "U0RON", TS: "1.000002", Text: "log attached", Files: []SlackFile{{Name: "a.log", Size: 10}}},
		{User: "U0KAR", TS: "1.000003", Text: "broken", Files: []SlackFile{{Name: "boom", Size: 10}}},
	}}
	h.Handle(context.Background(), strictEvent(t, now, "<@UBOT123> go", "1.000005", "1.0", "U0KURT", "CF-1"))
	if len(sqs.sends) != 1 {
		t.Fatalf("want dispatch, sends=%d", len(sqs.sends))
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	tc := body.ThreadContext
	if tc == nil || len(tc.Messages) != 2 {
		t.Fatalf("context %+v", tc)
	}
	if len(tc.Messages[0].Attachments) != 1 || tc.Messages[0].Attachments[0].OriginalName != "a.log" {
		t.Fatalf("a.log must attach to the first message: %+v", tc.Messages)
	}
	if len(tc.Messages[1].Attachments) != 0 || len(tc.FileErrors) != 1 || tc.FileErrors[0].OriginalName != "boom" {
		t.Fatalf("boom must be a FileError: msgs=%+v errs=%+v", tc.Messages, tc.FileErrors)
	}
	for _, id := range dl.sandboxIDs {
		if id != "sb-abc123" {
			t.Fatalf("context download keyed on %q, want sb-abc123", id)
		}
	}
}

// A strict mentioned reply that itself carries a file takes the files-sync
// branch; that envelope must carry the thread context too.
func TestStrictMode_FilesSyncBranchCarriesContext(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, _, sandboxes, _, _ := newHandler(now)
	h.MentionOnly = true
	sandboxes.info.ThreadBypass = &f
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
	h.FileDownloader = &contextDownloader{}
	h.ThreadReader = &fakeReader{replies: []SlackReplyMessage{
		{User: "U0RON", TS: "1.000002", Text: "earlier"},
	}}
	ev := map[string]any{
		"type": "message", "subtype": "file_share", "channel": "C1", "user": "U0KURT",
		"text": "<@UBOT123> see file", "ts": "1.000005", "thread_ts": "1.0",
		"files": []map[string]any{{"id": "F1", "name": "trigger.png", "size": 10}},
	}
	evb, _ := json.Marshal(ev)
	raw := fmt.Sprintf(`{"type":"event_callback","event_id":"FS-1","event":%s}`, evb)
	tsHdr, sigHdr := signSlackPayload(t, raw, now)
	h.Handle(context.Background(), EventsRequest{Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr}, Body: raw})
	if len(sqs.sends) != 1 {
		t.Fatalf("want dispatch, sends=%d", len(sqs.sends))
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	if len(body.Attachments) != 1 || body.Attachments[0].OriginalName != "trigger.png" {
		t.Fatalf("want the files-sync branch (trigger attachment), got %+v", body.Attachments)
	}
	if body.ThreadContext == nil || len(body.ThreadContext.Messages) != 1 || body.ThreadContext.Messages[0].Text != "earlier" {
		t.Fatalf("files-sync envelope must carry context: %+v", body.ThreadContext)
	}
}

// Part A routed thread on a strict default sandbox.
func TestStrictMode_RoutedThread(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, alias, _, reactor, _ := routedHandler(t, now)
	nonces := h.Nonces.(*fakeNonces)
	alias.info.ThreadBypass = &f
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.info}}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	reader := &fakeReader{replies: []SlackReplyMessage{{User: "U0KURT", TS: "1.000002", Text: "earlier"}, {User: "U0OTHER", TS: "1.000003", Text: "unlisted"}}}
	h.ThreadReader = reader
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "no mention", "1.5", "1.0", "RS-1"))
	if len(sqs.sends) != 0 || reader.calls != 0 || len(nonces.seen) != 0 || len(threads.upserts) != 0 ||
		len(reactor.snapshot()) != 0 || len(threads.marks) != 0 {
		t.Fatalf("strict routed follow-up without mention must touch nothing: sends=%d reader=%d nonces=%d upserts=%d reactions=%d marks=%d",
			len(sqs.sends), reader.calls, len(nonces.seen), len(threads.upserts), len(reactor.snapshot()), len(threads.marks))
	}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> again", "1.6", "1.0", "RS-2"))
	if len(sqs.sends) != 1 {
		t.Fatal("strict routed follow-up with mention must dispatch")
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	if body.ThreadContext == nil || len(body.ThreadContext.Messages) != 1 || body.ThreadContext.OmittedNonAllow != 1 {
		t.Fatalf("routed context must use the email gate: %+v", body.ThreadContext)
	}
}

// deadlineEmails records whether each SenderEmail call carried a deadline.
type deadlineEmails struct {
	inner       *scriptedEmailsByUser
	hadDeadline map[string]bool
}

func (d *deadlineEmails) SenderEmail(ctx context.Context, u string) (string, error) {
	_, ok := ctx.Deadline()
	d.hadDeadline[u] = ok
	return d.inner.SenderEmail(ctx, u)
}

// The routed trust predicate must run under collectThreadContext's bounded
// sub-context, not Handle's, so a slow Slack API cannot outlive the turn.
func TestStrictMode_RoutedPredicateUsesBoundedContext(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, alias, _, _, emails := routedHandler(t, now)
	de := &deadlineEmails{inner: emails, hadDeadline: map[string]bool{}}
	h.SenderEmails = de
	alias.info.ThreadBypass = &f
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.info}}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.ThreadReader = &fakeReader{replies: []SlackReplyMessage{{User: "U0OTHER", TS: "1.000003", Text: "unlisted"}}}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> again", "1.6", "1.0", "RD-1"))
	if len(sqs.sends) != 1 {
		t.Fatalf("want dispatch, sends=%d", len(sqs.sends))
	}
	if !de.hadDeadline["U0OTHER"] {
		t.Fatal("context-author lookup ran without the sub-context deadline")
	}
}

// F2: collection is budgeted against the handler deadline. With too little time
// left it is skipped (no Replies call) and says so on the envelope; with a normal
// Lambda deadline it runs.
func TestStrictMode_ContextBudgetedAgainstDeadline(t *testing.T) {
	run := func(t *testing.T, d time.Duration, id string) (*fakeReader, InboundQueueBody) {
		now := time.Now()
		f := false
		h, sqs, threads, _, sandboxes, _, _ := newHandler(now)
		h.MentionOnly = true
		sandboxes.info.ThreadBypass = &f
		threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
		reader := &fakeReader{replies: []SlackReplyMessage{{User: "U0RON", TS: "1.000002", Text: "earlier"}}}
		h.ThreadReader = reader
		ctx, cancel := context.WithTimeout(context.Background(), d)
		defer cancel()
		h.Handle(ctx, strictEvent(t, now, "<@UBOT123> go", "1.000005", "1.0", "U0KURT", id))
		if len(sqs.sends) != 1 {
			t.Fatalf("context must never block the turn; sends=%d", len(sqs.sends))
		}
		var body InboundQueueBody
		json.Unmarshal([]byte(sqs.sends[0].body), &body)
		return reader, body
	}
	t.Run("10s deadline skips", func(t *testing.T) {
		reader, body := run(t, 10*time.Second, "CB-1")
		if reader.calls != 0 {
			t.Fatalf("no Replies call expected, got %d", reader.calls)
		}
		if body.ThreadContext == nil || body.ThreadContext.Error != "skipped: insufficient time before the Lambda deadline" {
			t.Fatalf("want the skip error on the envelope, got %+v", body.ThreadContext)
		}
	})
	t.Run("60s deadline collects", func(t *testing.T) {
		reader, body := run(t, 60*time.Second, "CB-2")
		if reader.calls != 1 || body.ThreadContext == nil || len(body.ThreadContext.Messages) != 1 || body.ThreadContext.Error != "" {
			t.Fatalf("collection must run; reader=%d ctx=%+v", reader.calls, body.ThreadContext)
		}
	})
}

// F2: a context budget already under the file floor records every context file
// in FilesOmitted instead of starting a download it cannot finish.
func TestStrictMode_ContextFilesStopNearDeadline(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, _, sandboxes, _, _ := newHandler(now)
	h.MentionOnly = true
	sandboxes.info.ThreadBypass = &f
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
	dl := &contextDownloader{}
	h.FileDownloader = dl
	h.ThreadReader = &fakeReader{replies: []SlackReplyMessage{
		{User: "U0RON", TS: "1.000002", Text: "log", Files: []SlackFile{{Name: "a.log", Size: 10}}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), ContextTurnReserve+ContextFileFloor-time.Second)
	defer cancel()
	h.Handle(ctx, strictEvent(t, now, "<@UBOT123> go", "1.000005", "1.0", "U0KURT", "CB-3"))
	if len(sqs.sends) != 1 {
		t.Fatalf("sends=%d", len(sqs.sends))
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	tc := body.ThreadContext
	if tc == nil || len(tc.Messages) != 1 || len(tc.FilesOmitted) != 1 || tc.FilesOmitted[0] != "a.log" || len(dl.sandboxIDs) != 0 {
		t.Fatalf("a.log must be omitted without a download; ctx=%+v downloads=%v", tc, dl.sandboxIDs)
	}
}

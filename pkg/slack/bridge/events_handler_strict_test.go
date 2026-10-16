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

// Part A routed thread on a strict default sandbox.
func TestStrictMode_RoutedThread(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, alias, _, _, _ := routedHandler(t, now)
	alias.info.ThreadBypass = &f
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.info}}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	reader := &fakeReader{replies: []SlackReplyMessage{{User: "U0KURT", TS: "1.000002", Text: "earlier"}, {User: "U0OTHER", TS: "1.000003", Text: "unlisted"}}}
	h.ThreadReader = reader
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "no mention", "1.5", "1.0", "RS-1"))
	if len(sqs.sends) != 0 || reader.calls != 0 {
		t.Fatal("strict routed follow-up without mention must drop")
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

package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---- fakes for the four default-route deps ----

type fakeByID struct {
	infos map[string]SandboxRoutingInfo
	err   error
}

func (f *fakeByID) FetchBySandboxID(_ context.Context, id string) (SandboxRoutingInfo, error) {
	return f.infos[id], f.err
}

type fakeAlias struct {
	info  SandboxRoutingInfo
	found bool
	err   error
	calls int
}

func (f *fakeAlias) ResolveAlias(_ context.Context, _ string) (SandboxRoutingInfo, bool, error) {
	f.calls++
	return f.info, f.found, f.err
}

type fakePublisher struct {
	calls []struct{ sandboxID, alias, profile, envelope string }
	err   error
}

func (f *fakePublisher) PutSandboxCreate(_ context.Context, sandboxID, alias, profile, env string) error {
	f.calls = append(f.calls, struct{ sandboxID, alias, profile, envelope string }{sandboxID, alias, profile, env})
	return f.err
}

// recordingDownloader records the sandbox id each Download was keyed on.
type recordingDownloader struct {
	sandboxIDs []string
}

func (d *recordingDownloader) Download(_ context.Context, _ []SlackFile, sandboxID, _ string) ([]Attachment, []FileError, error) {
	d.sandboxIDs = append(d.sandboxIDs, sandboxID)
	return nil, nil, nil
}

type fakeRelayer struct {
	calls   int
	results []PeerClaimResult
}

func (f *fakeRelayer) Broadcast(_ context.Context, _ string, _ map[string]string) ([]PeerClaimResult, error) {
	f.calls++
	return f.results, nil
}

// routedHandler: channel CFOREIGN is owned by nobody; alias "ir-bot" resolves to
// sb-irbot running; sender U0KURT maps to kurt@example.com (allowed), U0OTHER to
// other@example.com (denied).
func routedHandler(t *testing.T, now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeAlias, *fakePublisher, *fakeReactor, *scriptedEmailsByUser) {
	t.Helper()
	h, sqs, threads, _, sandboxes, _, reactor := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{} // every channel is a miss
	alias := &fakeAlias{info: SandboxRoutingInfo{SandboxID: "sb-irbot", QueueURL: "https://sqs.example/irbot.fifo"}, found: true}
	pub := &fakePublisher{}
	emails := &scriptedEmailsByUser{byUser: map[string]string{"U0KURT": "kurt@example.com", "U0OTHER": "other@example.com"}}
	h.DefaultRoute = NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"kurt@example.com"})
	h.AliasResolver = alias
	h.CreatePublisher = pub
	h.SenderEmails = emails
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.info}}
	return h, sqs, threads, alias, pub, reactor, emails
}

type scriptedEmailsByUser struct {
	byUser map[string]string
	err    error
	calls  int
}

func (s *scriptedEmailsByUser) SenderEmail(_ context.Context, u string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	e, ok := s.byUser[u]
	if !ok {
		return "", ErrSenderIneligible
	}
	return e, nil
}

func routedRequest(t *testing.T, now time.Time, channel, channelType, user, text, ts, threadTS, eventID string) EventsRequest {
	t.Helper()
	ev := map[string]any{"type": "message", "channel": channel, "user": user, "text": text, "ts": ts}
	if channelType != "" {
		ev["channel_type"] = channelType
	}
	if threadTS != "" {
		ev["thread_ts"] = threadTS
	}
	evb, _ := json.Marshal(ev)
	body := fmt.Sprintf(`{"type":"event_callback","event_id":%q,"event":%s}`, eventID, evb)
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	return EventsRequest{Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr}, Body: body}
}

func TestDefaultRoute_MentionFromListedSender_Dispatches(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, reactor, _ := routedHandler(t, now)
	resp := h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> do the thing", "1.0", "", "DR1"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/irbot.fifo" {
		t.Fatalf("want 1 send to irbot queue, got %+v", sqs.sends)
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	if body.Channel != "CFOREIGN" || body.ThreadTS != "1.0" || body.User != "U0KURT" {
		t.Fatalf("envelope %+v", body)
	}
	if len(threads.upserts) != 1 || threads.upserts[0].sb != "sb-irbot" || threads.upserts[0].chan_ != "CFOREIGN" {
		t.Fatalf("thread row %+v", threads.upserts)
	}
	if len(reactor.snapshot()) != 1 {
		t.Fatalf("want 👀, got %d reactions", len(reactor.snapshot()))
	}
}

func TestDefaultRoute_UnlistedSender_SilentDrop(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, pub, reactor, _ := routedHandler(t, now)
	h.Slack = &fakeSlackPoster{}
	h.DefaultRouter = true // even with the Phase 96 router on, no orphan reply
	resp := h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0OTHER", "<@UBOT123> hi", "1.0", "", "DR2"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(sqs.sends) != 0 || len(threads.upserts) != 0 || len(pub.calls) != 0 || len(reactor.snapshot()) != 0 {
		t.Fatal("unlisted sender must produce nothing")
	}
	if len(h.Slack.(*fakeSlackPoster).msgs) != 0 {
		t.Fatal("no orphan reply when a route is configured")
	}
}

func TestDefaultRoute_NoMention_Dropped_ButDMNeedsNone(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := routedHandler(t, now)
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "no mention here", "1.0", "", "DR3a"))
	if len(sqs.sends) != 0 {
		t.Fatal("channel message without mention must be dropped")
	}
	h.Handle(context.Background(), routedRequest(t, now, "DKURT", "im", "U0KURT", "no mention here", "2.0", "", "DR3b"))
	if len(sqs.sends) != 1 {
		t.Fatal("DM without mention must dispatch")
	}
	h.Handle(context.Background(), routedRequest(t, now, "GMPIM", "mpim", "U0KURT", "no mention here", "3.0", "", "DR3c"))
	if len(sqs.sends) != 1 {
		t.Fatal("group DM without mention must be dropped")
	}
}

func TestDefaultRoute_ThreadContinuity_NoMention_NoRelay(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := routedHandler(t, now)
	relay := &fakeRelayer{results: []PeerClaimResult{{Claimed: true}}} // would stop everything if consulted
	h.Relayer = relay
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "follow-up, no mention", "1.5", "1.0", "DR4"))
	if len(sqs.sends) != 1 {
		t.Fatalf("continuity reply must dispatch; sends=%d", len(sqs.sends))
	}
	if relay.calls != 0 {
		t.Fatal("continuity must be decided before the relay")
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	if body.ThreadTS != "1.0" {
		t.Fatalf("thread_ts %q", body.ThreadTS)
	}
}

func TestDefaultRoute_ThreadContinuity_StillGatedOnEmail(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := routedHandler(t, now)
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0OTHER", "me too", "1.5", "1.0", "DR5"))
	if len(sqs.sends) != 0 {
		t.Fatal("unlisted sender in an established thread must be dropped")
	}
}

func TestDefaultRoute_ThreadContinuity_RebindsWhenAliasMoved(t *testing.T) {
	now := time.Now()
	h, sqs, threads, alias, _, _, _ := routedHandler(t, now)
	alias.info = SandboxRoutingInfo{SandboxID: "sb-irbot2", QueueURL: "https://sqs.example/irbot2.fifo"}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "hello again", "1.5", "1.0", "DR6"))
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/irbot2.fifo" {
		t.Fatalf("must enqueue to the recreated box; sends=%+v", sqs.sends)
	}
	if len(threads.rebinds) != 1 || threads.rebinds[0].sb != "sb-irbot2" {
		t.Fatalf("thread row must be rebound; rebinds=%+v", threads.rebinds)
	}
}

func TestDefaultRoute_NotConfigured_Phase96Unchanged(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, sandboxes, _, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{}
	poster := &fakeSlackPoster{}
	h.Slack = poster
	h.DefaultRouter = true
	h.Relayer = &fakeRelayer{}
	h.RouterCooldown = &fakeCooldownStore{}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR7"))
	if len(sqs.sends) != 0 {
		t.Fatal("no dispatch without a route")
	}
	if len(poster.msgs) != 1 {
		t.Fatalf("Phase 96 orphan reply must still fire; got %d", len(poster.msgs))
	}
}

func TestDefaultRoute_PeerClaim_RouteNotEvaluated(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _, emails := routedHandler(t, now)
	h.Relayer = &fakeRelayer{results: []PeerClaimResult{{Claimed: true}}}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR8"))
	if len(sqs.sends) != 0 || alias.calls != 0 || emails.calls != 0 {
		t.Fatal("a peer claim must end processing before the route")
	}
}

func TestDefaultRoute_AliasAbsent_ColdCreateCarriesEnvelope(t *testing.T) {
	now := time.Now()
	h, sqs, threads, alias, pub, reactor, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR9"))
	if len(sqs.sends) != 0 {
		t.Fatal("cold-create must not enqueue (no queue exists yet)")
	}
	if len(pub.calls) != 1 || pub.calls[0].alias != "ir-bot" || pub.calls[0].profile != "profiles/ir-bot.yaml" {
		t.Fatalf("publisher calls %+v", pub.calls)
	}
	var body InboundQueueBody
	if err := json.Unmarshal([]byte(pub.calls[0].envelope), &body); err != nil || body.Channel != "CFOREIGN" || body.Text == "" {
		t.Fatalf("envelope must be an InboundQueueBody: %s err=%v", pub.calls[0].envelope, err)
	}
	if !strings.HasPrefix(pub.calls[0].sandboxID, "sl-") {
		t.Fatalf("handler must mint an sl- id for the publisher; got %q", pub.calls[0].sandboxID)
	}
	if len(threads.upserts) != 1 || threads.upserts[0].sb != pub.calls[0].sandboxID {
		t.Fatalf("thread row must name the minted sandbox id; %+v", threads.upserts)
	}
	if len(reactor.snapshot()) != 1 {
		t.Fatal("👀 on cold-create too")
	}
}

func TestDefaultRoute_AliasStopped_ResumesThenEnqueues(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _, _ := routedHandler(t, now)
	alias.info.Paused = true
	resumer := &mockSandboxResumer{}
	h.Resumer = resumer
	h.StatusWriter = &mockSandboxStatusWriter{}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> wake", "1.0", "", "DR10"))
	if len(sqs.sends) != 1 {
		t.Fatal("stopped alias must still enqueue")
	}
	if calls, id := resumer.snapshot(); calls != 1 || id != "sb-irbot" {
		t.Fatalf("resumer calls=%d id=%s", calls, id)
	}
}

func TestDefaultRoute_AliasFrozen_NoticeNoDispatch(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _, _ := routedHandler(t, now)
	alias.info.ActionFrozen = true
	alias.info.FrozenReason = "quota"
	poster := &fakeSlackPoster{}
	h.Slack = poster
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR11"))
	if len(sqs.sends) != 0 {
		t.Fatal("frozen must not dispatch")
	}
	if len(poster.msgs) != 1 || !strings.Contains(poster.msgs[0].body, "frozen") || poster.msgs[0].channel != "CFOREIGN" {
		t.Fatalf("frozen notice %+v", poster.msgs)
	}
}

func TestDefaultRoute_EmailLookupFailureIsDeny(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, emails := routedHandler(t, now)
	emails.err = errors.New("slack: ratelimited")
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR12"))
	if len(sqs.sends) != 0 {
		t.Fatal("lookup error must deny")
	}
}

func TestDefaultRoute_RelayedRequest_StaysTerminal(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _, _ := routedHandler(t, now)
	req := routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "DR13")
	req.Headers["x-km-relayed"] = "1"
	resp := h.Handle(context.Background(), req)
	if len(sqs.sends) != 0 || alias.calls != 0 {
		t.Fatal("a relayed miss must never route; peers are not front doors")
	}
	if !strings.Contains(resp.Body, `"claimed":false`) {
		t.Fatalf("body %s", resp.Body)
	}
}

func TestDefaultRoute_OrphanThread_AliasAbsent_ColdCreateRebinds(t *testing.T) {
	now := time.Now()
	h, _, threads, alias, pub, _, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-dead"}
	// Gone box + absent alias is strict (no box to inherit a bypass from), so
	// the follow-up carries a mention.
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> still there?", "1.5", "1.0", "DR14"))
	if len(pub.calls) != 1 {
		t.Fatalf("want cold-create, got %d publishes", len(pub.calls))
	}
	if !strings.HasPrefix(pub.calls[0].sandboxID, "sl-") {
		t.Fatalf("minted id %q", pub.calls[0].sandboxID)
	}
	if len(threads.rebinds) != 1 || threads.rebinds[0].sb != pub.calls[0].sandboxID {
		t.Fatalf("orphaned thread row must be rebound to the minted box; rebinds=%+v upserts=%+v", threads.rebinds, threads.upserts)
	}
}

func TestDefaultRoute_AliasAbsent_ColdCreateWithFiles_KeysOnMintedID(t *testing.T) {
	now := time.Now()
	h, _, threads, alias, pub, _, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	dl := &recordingDownloader{}
	h.FileDownloader = dl
	ev := map[string]any{
		"type": "message", "subtype": "file_share", "channel": "CFOREIGN", "channel_type": "channel",
		"user": "U0KURT", "text": "<@UBOT123> look at this", "ts": "1.0",
		"files": []map[string]any{{"id": "F1", "name": "a.txt", "url_private_download": "https://files.example/a.txt", "size": 3}},
	}
	evb, _ := json.Marshal(ev)
	body := fmt.Sprintf(`{"type":"event_callback","event_id":%q,"event":%s}`, "DR15", evb)
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	h.Handle(context.Background(), EventsRequest{Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr}, Body: body})
	if len(dl.sandboxIDs) != 1 || !strings.HasPrefix(dl.sandboxIDs[0], "sl-") {
		t.Fatalf("downloader must key on the minted sl- id; got %v", dl.sandboxIDs)
	}
	if len(pub.calls) != 1 || pub.calls[0].sandboxID != dl.sandboxIDs[0] {
		t.Fatalf("publisher id must match the download key; calls=%+v", pub.calls)
	}
	if len(threads.upserts) != 1 || threads.upserts[0].sb != dl.sandboxIDs[0] {
		t.Fatalf("thread row must name the minted id; %+v", threads.upserts)
	}
}

// ---- fix round 2: dispatch bookkeeping only after a successful delivery ----

func TestDefaultRoute_Enqueue_MarksDispatchedOnce(t *testing.T) {
	now := time.Now()
	h, _, threads, _, _, _, _ := routedHandler(t, now)
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> go", "7.0", "", "DM1"))
	if len(threads.upserts) != 1 {
		t.Fatalf("want 1 upsert, got %+v", threads.upserts)
	}
	if len(threads.marks) != 1 || threads.marks[0].ev != "7.0" || threads.marks[0].ts != "7.0" || threads.marks[0].chan_ != "CFOREIGN" {
		t.Fatalf("want one mark with ev=msg.TS, got %+v", threads.marks)
	}
}

func TestDefaultRoute_ColdCreate_MarksDispatchedOnce(t *testing.T) {
	now := time.Now()
	h, _, threads, alias, pub, _, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> go", "8.0", "", "DM2"))
	if len(pub.calls) != 1 {
		t.Fatalf("want cold-create, got %d", len(pub.calls))
	}
	if len(threads.marks) != 1 || threads.marks[0].ev != "8.0" {
		t.Fatalf("want one mark with ev=msg.TS, got %+v", threads.marks)
	}
}

func TestDefaultRoute_EnqueueFailure_NoRowNoMarkNoReaction(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, reactor, _ := routedHandler(t, now)
	sqs.err = errors.New("sqs down")
	resp := h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> go", "9.0", "", "DM3"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(threads.upserts) != 0 || len(threads.rebinds) != 0 || len(threads.marks) != 0 {
		t.Fatalf("failed send must write nothing; upserts=%+v rebinds=%+v marks=%+v", threads.upserts, threads.rebinds, threads.marks)
	}
	if len(reactor.snapshot()) != 0 {
		t.Fatal("failed send must not react")
	}
}

func TestDefaultRoute_ColdCreatePublishFailure_NoRowNoMark(t *testing.T) {
	now := time.Now()
	h, _, threads, alias, pub, _, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	pub.err = errors.New("eventbridge down")
	resp := h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> go", "10.0", "", "DM4"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(threads.upserts) != 0 || len(threads.rebinds) != 0 || len(threads.marks) != 0 {
		t.Fatalf("failed publish must write nothing; upserts=%+v rebinds=%+v marks=%+v", threads.upserts, threads.rebinds, threads.marks)
	}
}

func TestEventsHandler_OwnedEnqueueFailure_NoRowNoMark(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _, _ := newHandler(now) // owned channel: sb-abc123
	sqs.err = errors.New("sqs down")
	resp := h.Handle(context.Background(), routedRequest(t, now, "COWNED", "channel", "U0KURT", "hello", "11.0", "", "DM5"))
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(sqs.sends) != 1 {
		t.Fatalf("owned path must attempt the send; sends=%d", len(sqs.sends))
	}
	if len(threads.upserts) != 0 || len(threads.marks) != 0 {
		t.Fatalf("owned failed send must write no row; upserts=%+v marks=%+v", threads.upserts, threads.marks)
	}
}

// ---- fix round 2: a gone continuity box never lends a bypass ----

func TestDefaultRoute_ThreadContinuity_GoneBox_BypassFromCurrentBox(t *testing.T) {
	strict := false
	setup := func(t *testing.T, now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeAlias) {
		h, sqs, threads, alias, _, _, _ := routedHandler(t, now)
		alias.info = SandboxRoutingInfo{SandboxID: "sb-irbot2", QueueURL: "https://sqs.example/irbot2.fifo", ThreadBypass: &strict}
		threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-dead"} // sb-dead absent from fakeByID
		return h, sqs, threads, alias
	}

	now := time.Now()
	h, sqs, _, alias := setup(t, now)
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "follow-up, no mention", "1.5", "1.0", "DG1"))
	if len(sqs.sends) != 0 {
		t.Fatalf("strict current box: a no-mention follow-up must drop; sends=%+v", sqs.sends)
	}
	if alias.calls != 1 {
		t.Fatalf("alias must be resolved once (continuity check), got %d", alias.calls)
	}

	h, sqs, threads, alias := setup(t, now)
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> follow-up", "1.6", "1.0", "DG2"))
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/irbot2.fifo" {
		t.Fatalf("mention must dispatch to the current box; sends=%+v", sqs.sends)
	}
	if alias.calls != 1 {
		t.Fatalf("alias resolved %d times; the continuity result must be reused", alias.calls)
	}
	if len(threads.rebinds) != 1 || threads.rebinds[0].sb != "sb-irbot2" || len(threads.upserts) != 0 {
		t.Fatalf("row must be rebound to the current box; rebinds=%+v upserts=%+v", threads.rebinds, threads.upserts)
	}
}

func TestDefaultRoute_ThreadContinuity_GoneBoxAliasAbsent_Strict(t *testing.T) {
	now := time.Now()
	h, sqs, threads, alias, pub, _, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-dead"}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "no mention", "1.5", "1.0", "DG3"))
	if len(sqs.sends) != 0 || len(pub.calls) != 0 {
		t.Fatal("gone box + absent alias is strict: a no-mention follow-up must drop")
	}
}

// ---- fix round 2: the row is rewritten only after a successful delivery ----

func TestDefaultRoute_AliasMoved_FrozenOrFailedSend_NoRebind(t *testing.T) {
	now := time.Now()
	h, _, threads, alias, _, _, _ := routedHandler(t, now)
	alias.info = SandboxRoutingInfo{SandboxID: "sb-irbot2", QueueURL: "https://sqs.example/irbot2.fifo", ActionFrozen: true}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.Slack = &fakeSlackPoster{}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "hello", "1.5", "1.0", "DF1"))
	if len(threads.rebinds) != 0 {
		t.Fatalf("frozen box must not rewrite the row; rebinds=%+v", threads.rebinds)
	}

	h, sqs, threads, alias, _, _, _ := routedHandler(t, now)
	alias.info = SandboxRoutingInfo{SandboxID: "sb-irbot2", QueueURL: "https://sqs.example/irbot2.fifo"}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	sqs.err = errors.New("sqs down")
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "hello", "1.6", "1.0", "DF2"))
	if len(threads.rebinds) != 0 || len(threads.marks) != 0 {
		t.Fatalf("failed send must not rewrite the row; rebinds=%+v marks=%+v", threads.rebinds, threads.marks)
	}
}

// F2: the files-sync branch returns on a failed delivery like the plain branch,
// so an undelivered message never gets the 👀 reaction.
func TestDefaultRoute_ColdCreatePublishFailureWithFiles_NoRowNoMarkNoReaction(t *testing.T) {
	now := time.Now()
	h, _, threads, alias, pub, reactor, _ := routedHandler(t, now)
	alias.found = false
	alias.info = SandboxRoutingInfo{}
	pub.err = errors.New("eventbridge down")
	h.FileDownloader = &recordingDownloader{}
	ev := map[string]any{
		"type": "message", "subtype": "file_share", "channel": "CFOREIGN", "channel_type": "channel",
		"user": "U0KURT", "text": "<@UBOT123> look", "ts": "10.5",
		"files": []map[string]any{{"id": "F1", "name": "a.txt", "url_private_download": "https://files.example/a.txt", "size": 3}},
	}
	evb, _ := json.Marshal(ev)
	body := fmt.Sprintf(`{"type":"event_callback","event_id":%q,"event":%s}`, "DM4F", evb)
	tsHdr, sigHdr := signSlackPayload(t, body, now)
	resp := h.Handle(context.Background(), EventsRequest{Headers: map[string]string{"x-slack-request-timestamp": tsHdr, "x-slack-signature": sigHdr}, Body: body})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(pub.calls) != 1 {
		t.Fatalf("want the files-sync publish attempt; calls=%+v", pub.calls)
	}
	if len(threads.upserts) != 0 || len(threads.rebinds) != 0 || len(threads.marks) != 0 {
		t.Fatalf("failed publish must write nothing; upserts=%+v rebinds=%+v marks=%+v", threads.upserts, threads.rebinds, threads.marks)
	}
	if n := len(reactor.snapshot()); n != 0 {
		t.Fatalf("undelivered files message must not react; reactions=%d", n)
	}
}

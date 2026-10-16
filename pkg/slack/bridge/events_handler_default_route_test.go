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
	calls     []struct{ alias, profile, envelope string }
	sandboxID string
	err       error
}

func (f *fakePublisher) PutSandboxCreate(_ context.Context, alias, profile, env string) (string, error) {
	f.calls = append(f.calls, struct{ alias, profile, envelope string }{alias, profile, env})
	return f.sandboxID, f.err
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
	pub := &fakePublisher{sandboxID: "sl-cold1"}
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
	if len(threads.upserts) != 1 || threads.upserts[0].sb != "sl-cold1" {
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
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "still there?", "1.5", "1.0", "DR14"))
	if len(pub.calls) != 1 {
		t.Fatalf("want cold-create, got %d publishes", len(pub.calls))
	}
	if len(threads.rebinds) != 1 || threads.rebinds[0].sb != "sl-cold1" {
		t.Fatalf("orphaned thread row must be rebound to the minted box; rebinds=%+v upserts=%+v", threads.rebinds, threads.upserts)
	}
}

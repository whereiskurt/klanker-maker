// pkg/slack/bridge/events_handler_channel_routes_test.go
package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeAliasByName resolves per alias, so two routes can point at two boxes.
type fakeAliasByName struct {
	infos map[string]SandboxRoutingInfo
	calls []string
}

func (f *fakeAliasByName) ResolveAlias(_ context.Context, alias string) (SandboxRoutingInfo, bool, error) {
	f.calls = append(f.calls, alias)
	info, ok := f.infos[alias]
	return info, ok, nil
}

// channelRoutedHandler: CSEC → ir-bot (mention required), COPS → ops-bot
// (mention_only false, kurt only), default route → ir-bot (kurt only).
// U0KURT = kurt@example.com, U0OTHER = other@example.com.
func channelRoutedHandler(t *testing.T, now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeAliasByName, *fakePublisher, *scriptedEmailsByUser) {
	t.Helper()
	h, sqs, threads, _, sandboxes, _, _ := newHandler(now)
	sandboxes.info = SandboxRoutingInfo{}
	alias := &fakeAliasByName{infos: map[string]SandboxRoutingInfo{
		"ir-bot":  {SandboxID: "sb-irbot", QueueURL: "https://sqs.example/irbot.fifo"},
		"ops-bot": {SandboxID: "sb-ops", QueueURL: "https://sqs.example/ops.fifo"},
	}}
	pub := &fakePublisher{}
	emails := &scriptedEmailsByUser{byUser: map[string]string{"U0KURT": "kurt@example.com", "U0OTHER": "other@example.com"}}
	h.DefaultRoute = NewDefaultRoute("ir-bot", "ir-bot", []string{"kurt@example.com"}, nil)
	sec := NewDefaultRoute("ir-bot", "ir-bot", []string{"*@example.com"}, nil)
	ops := NewDefaultRoute("ops-bot", "ops", []string{"kurt@example.com"}, nil)
	ops.MentionOnly = false
	h.Routes = map[string]*DefaultRoute{"CSEC": sec, "COPS": ops}
	h.AliasResolver = alias
	h.CreatePublisher = pub
	h.SenderEmails = emails
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.infos["ir-bot"], "sb-ops": alias.infos["ops-bot"]}}
	return h, sqs, threads, alias, pub, emails
}

// req builds a signed message event: mention=true prefixes "<@UBOT123> "
// (the bot id newHandler's BotUserID fake answers). routedRequest is the
// helper events_handler_default_route_test.go already defines.
func req(t *testing.T, now time.Time, channel, channelType, user, text, ts, threadTS, eventID string, mention bool) EventsRequest {
	t.Helper()
	if mention {
		text = "<@UBOT123> " + text
	}
	return routedRequest(t, now, channel, channelType, user, text, ts, threadTS, eventID)
}

func TestChannelRoutes_MappedChannelGoesToItsAlias_UnmappedToDefault(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, _, _ := channelRoutedHandler(t, now)
	// COPS: top-level, no mention, other@ is NOT on the ops list ⇒ drop.
	resp := h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0OTHER", "do it", "1.0", "", "CR1", false))
	if resp.StatusCode != 200 || len(sqs.sends) != 0 {
		t.Fatalf("unlisted sender on a mention_only:false channel must be dropped: %+v %d", resp, len(sqs.sends))
	}
	// COPS: top-level, no mention, kurt ⇒ dispatched to sb-ops.
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "do it", "1.1", "", "CR2", false))
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/ops.fifo" {
		t.Fatalf("mapped channel must enqueue to its alias's box: %+v", sqs.sends)
	}
	// CSEC: mention required; without it, drop.
	h.Handle(context.Background(), req(t, now, "CSEC", "channel", "U0OTHER", "hi", "1.2", "", "CR3", false))
	if len(sqs.sends) != 1 {
		t.Fatal("CSEC without a mention must not dispatch")
	}
	// CSEC: with a mention, other@example.com is allowed by *@example.com.
	h.Handle(context.Background(), req(t, now, "CSEC", "channel", "U0OTHER", "hi", "1.3", "", "CR4", true))
	if len(sqs.sends) != 2 || sqs.sends[1].url != "https://sqs.example/irbot.fifo" {
		t.Fatalf("CSEC must dispatch to ir-bot: %+v", sqs.sends)
	}
	// CUNMAPPED: default route; other@ is not on the install list ⇒ drop; kurt ⇒ ir-bot.
	h.Handle(context.Background(), req(t, now, "CUNMAPPED", "channel", "U0OTHER", "hi", "1.4", "", "CR5", true))
	h.Handle(context.Background(), req(t, now, "CUNMAPPED", "channel", "U0KURT", "hi", "1.5", "", "CR6", true))
	if len(sqs.sends) != 3 || sqs.sends[2].url != "https://sqs.example/irbot.fifo" {
		t.Fatalf("unmapped channel must use the default route: %+v", sqs.sends)
	}
	if strings.Join(alias.calls, ",") == "" {
		t.Fatal("alias resolver must have been consulted")
	}
}

func TestChannelRoutes_ColdCreateCarriesTheRoutesAliasAndProfile(t *testing.T) {
	now := time.Now()
	h, _, _, alias, pub, _ := channelRoutedHandler(t, now)
	delete(alias.infos, "ops-bot")
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "first", "2.0", "", "CR7", false))
	if len(pub.calls) != 1 || pub.calls[0].alias != "ops-bot" || pub.calls[0].profile != "ops" {
		t.Fatalf("cold-create must name the route's alias and profile slug: %+v", pub.calls)
	}
}

func TestChannelRoutes_DMIgnoresTheTable(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _ := channelRoutedHandler(t, now)
	h.Routes["D0DM"] = h.Routes["COPS"] // a table entry for a D… id must not apply: a DM takes the default route
	h.Handle(context.Background(), req(t, now, "D0DM", "im", "U0OTHER", "hi", "3.0", "", "CR8", false))
	if len(sqs.sends) != 0 {
		t.Fatal("a DM must be gated by the default route (other@ is not on the install list)")
	}
}

func TestChannelRoutes_ContinuityKeepsThreadOnItsBoxAfterTableChange(t *testing.T) {
	now := time.Now()
	h, sqs, threads, _, _, _ := channelRoutedHandler(t, now)
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "start", "4.0", "", "CR9", false))
	if len(sqs.sends) != 1 {
		t.Fatal("setup: first turn must dispatch")
	}
	// The first dispatch wrote the thread row; fakeThreads.Upsert does not feed
	// LookupSandbox, so mirror the stored row into the lookup map.
	if len(threads.upserts) != 1 || threads.upserts[0].sb != "sb-ops" || threads.upserts[0].ts != "4.0" {
		t.Fatalf("setup: first turn must write the thread row for sb-ops: %+v", threads.upserts)
	}
	threads.sandboxByThread = map[string]string{"COPS|4.0": "sb-ops"}
	// Remap COPS to ir-bot; the existing thread (root ts 4.0) must still go to sb-ops.
	h.Routes["COPS"] = h.Routes["CSEC"]
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "follow-up", "4.1", "4.0", "CR10", false))
	if len(sqs.sends) != 2 || sqs.sends[1].url != "https://sqs.example/ops.fifo" {
		t.Fatalf("continuity must win over the new mapping: %+v (threads %+v)", sqs.sends, threads)
	}
}

func TestChannelRoutes_PeerClaimBeatsMapping(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _ := channelRoutedHandler(t, now)
	h.Relayer = &fakeRelayer{results: []PeerClaimResult{{Claimed: true}}}
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "hi", "5.0", "", "CR11", false))
	if len(sqs.sends) != 0 {
		t.Fatal("a peer that owns the channel must win over a mapping")
	}
}

func TestChannelRoutes_NoDefaultRouteStillRoutesMappedChannels(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _ := channelRoutedHandler(t, now)
	h.DefaultRoute = nil
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "hi", "6.0", "", "CR12", false))
	if len(sqs.sends) != 1 {
		t.Fatal("routes without a default route must still dispatch")
	}
	h.Handle(context.Background(), req(t, now, "CUNMAPPED", "channel", "U0KURT", "hi", "6.1", "", "CR13", true))
	if len(sqs.sends) != 1 {
		t.Fatal("an unmapped channel with no default route is the Phase 96 path: no dispatch")
	}
}

// mention_only: false lifts the mention for top-level posts only. A thread
// reply on a strict box (threadBypass: false) still needs one.
func TestChannelRoutes_MentionOnlyFalseKeepsStrictThreadMention(t *testing.T) {
	now := time.Now()
	h, sqs, threads, alias, _, _ := channelRoutedHandler(t, now)
	strict := false
	ops := alias.infos["ops-bot"]
	ops.ThreadBypass = &strict
	alias.infos["ops-bot"] = ops
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.infos["ir-bot"], "sb-ops": ops}}
	threads.sandboxByThread = map[string]string{"COPS|7.0": "sb-ops"}

	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "follow-up", "7.1", "7.0", "CR14", false))
	if len(sqs.sends) != 0 {
		t.Fatalf("an un-mentioned reply in a strict thread must be dropped on a mention_only:false channel: %+v", sqs.sends)
	}
	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "follow-up", "7.2", "7.0", "CR15", true))
	if len(sqs.sends) != 1 || sqs.sends[0].url != "https://sqs.example/ops.fifo" {
		t.Fatalf("a mentioned reply in a strict thread must dispatch to sb-ops: %+v", sqs.sends)
	}
}

// A route whose profile has its own allowEmails gates the cold-create with
// that list, as the default route does: a sender on the effective list but
// not on the profile's must not create the box.
func TestChannelRoutes_ProfileAllowGatesColdCreate(t *testing.T) {
	now := time.Now()
	h, sqs, _, alias, pub, _ := channelRoutedHandler(t, now)
	delete(alias.infos, "ir-bot")
	table := BuildRouteTable(&RoutesDocument{V: 1, Routes: []RouteEntry{
		{Channel: "CSEC", Alias: "ir-bot", Profile: "ir-bot", Allow: []string{"*@example.com"}, ProfileAllow: []string{"kurt@example.com"}, MentionOnly: true},
	}}, func(string, ...any) {})
	h.Routes = table
	h.Handle(context.Background(), req(t, now, "CSEC", "channel", "U0OTHER", "hi", "8.0", "", "CR16", true))
	if len(pub.calls) != 0 || len(sqs.sends) != 0 {
		t.Fatalf("a sender off the route profile's list must not cold-create: %+v", pub.calls)
	}
	h.Handle(context.Background(), req(t, now, "CSEC", "channel", "U0KURT", "hi", "8.1", "", "CR17", true))
	if len(pub.calls) != 1 || pub.calls[0].alias != "ir-bot" {
		t.Fatalf("a sender on the route profile's list must cold-create: %+v", pub.calls)
	}
}

// history_query tells the poller to advertise `km-slack history` to the agent.
// It is set only for a routed turn whose route carries history.query; an owned
// #sb- dispatch never sets it.
func TestChannelRoutes_HistoryQueryFlagOnQueueBody(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _ := channelRoutedHandler(t, now)
	h.Routes["CSEC"].History = &RouteHistory{Query: true}

	queryFlag := func(t *testing.T, i int) bool {
		t.Helper()
		if i >= len(sqs.sends) {
			t.Fatalf("expected send #%d, have %d", i, len(sqs.sends))
		}
		var b InboundQueueBody
		if err := json.Unmarshal([]byte(sqs.sends[i].body), &b); err != nil {
			t.Fatal(err)
		}
		return b.HistoryQuery
	}

	h.Handle(context.Background(), req(t, now, "CSEC", "channel", "U0OTHER", "hi", "3.0", "", "HQ1", true))
	if !queryFlag(t, 0) {
		t.Errorf("route with history.query must set history_query: %s", sqs.sends[0].body)
	}

	h.Handle(context.Background(), req(t, now, "COPS", "channel", "U0KURT", "hi", "3.1", "", "HQ2", false))
	if queryFlag(t, 1) {
		t.Errorf("route without history must not set history_query: %s", sqs.sends[1].body)
	}
	if strings.Contains(sqs.sends[1].body, "history_query") {
		t.Errorf("history_query must be omitted when false: %s", sqs.sends[1].body)
	}

	// Owned #sb- channel: plain handler, no route involved.
	h2, sqs2, _, _, _, _, _ := newHandler(now)
	h2.Handle(context.Background(), req(t, now, "C1", "channel", "U1", "hi", "3.2", "", "HQ3", true))
	if len(sqs2.sends) != 1 || strings.Contains(sqs2.sends[0].body, "history_query") {
		t.Errorf("owned dispatch must not carry history_query: %+v", sqs2.sends)
	}
}

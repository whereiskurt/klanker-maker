package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A sandbox row's allowEmails (SandboxRoutingInfo.AllowEmails) REPLACES the
// install/route email list for that sandbox, on every path: its own #sb-
// channel, routed threads (continuity, enqueue, resume) and strict context.

var allowEmailsUsers = map[string]string{
	"U0KURT":  "kurt@example.com",
	"U0OTHER": "other@example.com",
	"U0BOB":   "bob@example.com",
	"U0Z":     "z@other.example",
	"U0X":     "kurt@example.com", // a second Slack identity carrying kurt's email
}

func ownedEmailHandler(t *testing.T, now time.Time, list []string) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeSandboxes, *fakeReactor, *scriptedEmailsByUser, *bytes.Buffer) {
	t.Helper()
	h, sqs, threads, _, sandboxes, _, reactor := newHandler(now)
	sandboxes.info.AllowEmails = list
	emails := &scriptedEmailsByUser{byUser: allowEmailsUsers}
	h.SenderEmails = emails
	var buf bytes.Buffer
	h.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return h, sqs, threads, sandboxes, reactor, emails, &buf
}

func TestAllowEmails_OwnedChannel_ListedDispatches_OtherDropped(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, reactor, _, _ := ownedEmailHandler(t, now, []string{"kurt@example.com"})
	h.Handle(context.Background(), routedRequest(t, now, "C1", "channel", "U0KURT", "hi", "1.0", "", "AE-1"))
	if len(sqs.sends) != 1 || len(reactor.snapshot()) != 1 {
		t.Fatalf("listed sender must dispatch; sends=%d reactions=%d", len(sqs.sends), len(reactor.snapshot()))
	}

	h, sqs, threads, _, reactor, _, buf := ownedEmailHandler(t, now, []string{"kurt@example.com"})
	h.Handle(context.Background(), routedRequest(t, now, "C1", "channel", "U0OTHER", "hi", "2.0", "", "AE-2"))
	if len(sqs.sends) != 0 || len(reactor.snapshot()) != 0 || len(threads.upserts) != 0 {
		t.Fatalf("unlisted sender must be dropped silently; sends=%d reactions=%d upserts=%d",
			len(sqs.sends), len(reactor.snapshot()), len(threads.upserts))
	}
	out := buf.String()
	if !strings.Contains(out, "events: sender not in sandbox allowEmails (silent drop)") ||
		!strings.Contains(out, "sandbox=sb-abc123") || !strings.Contains(out, "user=U0OTHER") || !strings.Contains(out, "channel=C1") {
		t.Fatalf("missing Info drop log: %s", out)
	}
}

func TestAllowEmails_OwnedChannel_NoList_NoEmailGate(t *testing.T) {
	now := time.Now()
	for i, u := range []string{"U0KURT", "U0OTHER"} {
		h, sqs, _, _, _, emails, _ := ownedEmailHandler(t, now, nil)
		h.Handle(context.Background(), routedRequest(t, now, "C1", "channel", u, "hi", "1.0", "", "AE-N"+string(rune('0'+i))))
		if len(sqs.sends) != 1 {
			t.Fatalf("%s: no list ⇒ dispatch as today; sends=%d", u, len(sqs.sends))
		}
		if emails.calls != 0 {
			t.Fatalf("no list ⇒ no email lookup; calls=%d", emails.calls)
		}
	}
}

func TestAllowEmails_OwnedChannel_LookupErrorDrops(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, emails, _ := ownedEmailHandler(t, now, []string{"kurt@example.com"})
	emails.err = errors.New("slack: ratelimited")
	h.Handle(context.Background(), routedRequest(t, now, "C1", "channel", "U0KURT", "hi", "1.0", "", "AE-3"))
	if len(sqs.sends) != 0 {
		t.Fatal("lookup error with a list present must deny")
	}
}

func TestAllowEmails_OwnedChannel_NilFetcherWithListDrops(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _, _, _ := ownedEmailHandler(t, now, []string{"kurt@example.com"})
	h.SenderEmails = nil
	h.Handle(context.Background(), routedRequest(t, now, "C1", "channel", "U0KURT", "hi", "1.0", "", "AE-4"))
	if len(sqs.sends) != 0 {
		t.Fatal("a list with no email fetcher must deny")
	}
}

func TestAllowEmails_OwnedChannel_Phase118IdsStillApply(t *testing.T) {
	now := time.Now()
	h, sqs, _, sandboxes, _, _, _ := ownedEmailHandler(t, now, []string{"kurt@example.com"})
	sandboxes.info.Allow = []string{"U0KURT"}
	h.Handle(context.Background(), routedRequest(t, now, "C1", "channel", "U0X", "hi", "1.0", "", "AE-5"))
	if len(sqs.sends) != 0 {
		t.Fatal("U0X carries kurt's email but is not on the Phase 118 id list: both gates apply")
	}
	h.Handle(context.Background(), routedRequest(t, now, "C1", "channel", "U0KURT", "hi", "2.0", "", "AE-6"))
	if len(sqs.sends) != 1 {
		t.Fatal("U0KURT passes both gates")
	}
}

// routed: install list *@example.com; the alias box's row narrows to kurt.
func overrideRoutedHandler(t *testing.T, now time.Time, rowList []string) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeAlias, *fakePublisher) {
	t.Helper()
	h, sqs, threads, alias, pub, _, emails := routedHandler(t, now)
	emails.byUser = allowEmailsUsers
	h.DefaultRoute = NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"*@example.com"}, nil)
	alias.info.AllowEmails = rowList
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.info}}
	return h, sqs, threads, alias, pub
}

func TestAllowEmails_Route_Enqueue_RowListOverrides(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _ := overrideRoutedHandler(t, now, []string{"kurt@example.com"})
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "1.0", "", "AR-1"))
	if len(sqs.sends) != 1 {
		t.Fatalf("kurt must enqueue; sends=%d", len(sqs.sends))
	}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0BOB", "<@UBOT123> hi", "2.0", "", "AR-2"))
	if len(sqs.sends) != 1 {
		t.Fatal("bob is admitted by the install list but not the row list: override drops him")
	}
}

func TestAllowEmails_Route_Resume_RowListOverrides(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		user string
		want int
	}{{"U0KURT", 1}, {"U0BOB", 0}} {
		h, sqs, _, alias, _ := overrideRoutedHandler(t, now, []string{"kurt@example.com"})
		alias.info.Paused = true
		resumer := &mockSandboxResumer{}
		h.Resumer = resumer
		h.StatusWriter = &mockSandboxStatusWriter{}
		h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", tc.user, "<@UBOT123> wake", "1.0", "", "AR-R"+tc.user))
		if len(sqs.sends) != tc.want {
			t.Fatalf("%s: sends=%d want %d", tc.user, len(sqs.sends), tc.want)
		}
		if calls, _ := resumer.snapshot(); calls != tc.want {
			t.Fatalf("%s: resumer calls=%d want %d", tc.user, calls, tc.want)
		}
	}
}

func TestAllowEmails_Route_Continuity_RowListOverrides(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		user string
		want int
	}{{"U0KURT", 1}, {"U0BOB", 0}} {
		h, sqs, threads, _, _ := overrideRoutedHandler(t, now, []string{"kurt@example.com"})
		threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
		h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", tc.user, "follow-up", "1.5", "1.0", "AR-C"+tc.user))
		if len(sqs.sends) != tc.want {
			t.Fatalf("%s: sends=%d want %d", tc.user, len(sqs.sends), tc.want)
		}
	}
}

func TestAllowEmails_Route_RowListMayAdmitOutsideInstallList(t *testing.T) {
	now := time.Now()
	h, sqs, _, _, _ := overrideRoutedHandler(t, now, []string{"z@other.example"})
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0Z", "<@UBOT123> hi", "1.0", "", "AR-3"))
	if len(sqs.sends) != 1 {
		t.Fatal("the row list replaces the install list: z@other.example is admitted")
	}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> hi", "2.0", "", "AR-4"))
	if len(sqs.sends) != 1 {
		t.Fatal("kurt is on the install list but not the row list: dropped")
	}
}

func TestAllowEmails_Route_ColdCreate_KeepsRouteList(t *testing.T) {
	now := time.Now()
	h, _, _, alias, pub := overrideRoutedHandler(t, now, nil)
	alias.found = false
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0BOB", "<@UBOT123> hi", "1.0", "", "AR-5"))
	if len(pub.calls) != 1 {
		t.Fatal("cold-create is gated by the route list, which admits *@example.com")
	}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0Z", "<@UBOT123> hi", "2.0", "", "AR-6"))
	if len(pub.calls) != 1 {
		t.Fatal("z@other.example is not on the route list: no cold-create")
	}
}

func TestAllowEmails_StrictRoutedContext_TrustsOnlyRowList(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, alias, _ := overrideRoutedHandler(t, now, []string{"kurt@example.com"})
	alias.info.ThreadBypass = &f
	h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.info}}
	threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-irbot"}
	h.ThreadReader = &fakeReader{replies: []SlackReplyMessage{
		{User: "U0KURT", TS: "1.000002", Text: "listed"},
		{User: "U0BOB", TS: "1.000003", Text: "install-listed only"},
	}}
	h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", "U0KURT", "<@UBOT123> again", "1.6", "1.0", "AS-1"))
	if len(sqs.sends) != 1 {
		t.Fatalf("want dispatch, sends=%d", len(sqs.sends))
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	// 2026-10-24: membership is the permission; the allowlist gates who may start a turn, not what context the bot reads.
	if body.ThreadContext == nil || len(body.ThreadContext.Messages) != 2 {
		t.Fatalf("routed context must trust only the row list: %+v", body.ThreadContext)
	}
}

func TestAllowEmails_StrictMappedContext_TrustsOnlyRowList(t *testing.T) {
	now := time.Now()
	f := false
	h, sqs, threads, sandboxes, _, _, _ := ownedEmailHandler(t, now, []string{"kurt@example.com"})
	h.MentionOnly = true
	sandboxes.info.ThreadBypass = &f
	threads.sandboxByThread = map[string]string{"C1|1.0": "sb-abc123"}
	h.ThreadReader = &fakeReader{replies: []SlackReplyMessage{
		{User: "U0KURT", TS: "1.000002", Text: "listed"},
		{User: "U0OTHER", TS: "1.000003", Text: "unlisted"},
	}}
	h.Handle(context.Background(), strictEvent(t, now, "<@UBOT123> go", "1.000005", "1.0", "U0KURT", "AS-2"))
	if len(sqs.sends) != 1 {
		t.Fatalf("want dispatch, sends=%d", len(sqs.sends))
	}
	var body InboundQueueBody
	json.Unmarshal([]byte(sqs.sends[0].body), &body)
	// 2026-10-24: membership is the permission; the allowlist gates who may start a turn, not what context the bot reads.
	if body.ThreadContext == nil || len(body.ThreadContext.Messages) != 2 {
		t.Fatalf("mapped context must trust only the row list: %+v", body.ThreadContext)
	}
}

// Continuity follows a LIVE row box that is not the alias box, and the gate is
// that row box's list, not the alias box's.
func TestAllowEmails_Continuity_LiveRowBox_UsesRowBoxList(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		user string
		want int
	}{{"U0KURT", 1}, {"U0BOB", 0}} {
		h, sqs, threads, alias, _ := overrideRoutedHandler(t, now, []string{"bob@example.com"})
		rowBox := SandboxRoutingInfo{SandboxID: "sb-mapped", QueueURL: "https://sqs.example/mapped.fifo", AllowEmails: []string{"kurt@example.com"}}
		h.SandboxByID = &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-irbot": alias.info, "sb-mapped": rowBox}}
		threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-mapped"}
		h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", tc.user, "follow-up", "1.5", "1.0", "AR-L"+tc.user))
		if len(sqs.sends) != tc.want {
			t.Fatalf("%s: sends=%d want %d", tc.user, len(sqs.sends), tc.want)
		}
		if tc.want == 1 && sqs.sends[0].url != "https://sqs.example/mapped.fifo" {
			t.Fatalf("%s: must go to the row box queue, got %s", tc.user, sqs.sends[0].url)
		}
		if len(threads.rebinds) != 0 {
			t.Fatalf("%s: a live row box is never rebound: %+v", tc.user, threads.rebinds)
		}
	}
}

// Alias-moved: the row box is gone, so the alias box's list gates and a
// delivered turn rebinds the thread.
func TestAllowEmails_Continuity_AliasMoved_UsesAliasBoxList(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		user string
		want int
	}{{"U0BOB", 1}, {"U0KURT", 0}} {
		h, sqs, threads, _, _ := overrideRoutedHandler(t, now, []string{"bob@example.com"})
		// sb-mapped is absent from fakeByID: the row box is gone.
		threads.sandboxByThread = map[string]string{"CFOREIGN|1.0": "sb-mapped"}
		h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", tc.user, "<@UBOT123> follow-up", "1.5", "1.0", "AR-M"+tc.user))
		if len(sqs.sends) != tc.want {
			t.Fatalf("%s: sends=%d want %d", tc.user, len(sqs.sends), tc.want)
		}
		if tc.want == 1 {
			if sqs.sends[0].url != "https://sqs.example/irbot.fifo" {
				t.Fatalf("%s: must go to the alias queue, got %s", tc.user, sqs.sends[0].url)
			}
			if len(threads.rebinds) != 1 || threads.rebinds[0].sb != "sb-irbot" {
				t.Fatalf("%s: want a rebind to sb-irbot, got %+v", tc.user, threads.rebinds)
			}
		} else if len(threads.rebinds) != 0 {
			t.Fatalf("%s: dropped sender must not rebind: %+v", tc.user, threads.rebinds)
		}
	}
}

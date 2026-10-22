package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// Two lists (2026-10-17 final fix): the install list (*@example.com here)
// gates DM targets and non-route sandboxes; the route profile's list (kurt
// only) gates a cold-create and the alias's current box. Narrowing the route
// profile must not narrow anything else on the install.

func twoListRoute() *DefaultRoute {
	return NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"*@example.com"}, []string{"kurt@example.com"})
}

func twoListHandler(t *testing.T, now time.Time) (*EventsHandler, *fakeSQS, *fakeThreads, *fakeAlias, *fakePublisher) {
	t.Helper()
	h, sqs, threads, alias, pub, _, emails := routedHandler(t, now)
	emails.byUser = allowEmailsUsers
	h.DefaultRoute = twoListRoute()
	return h, sqs, threads, alias, pub
}

// dmFakeSlack satisfies SlackPoster + DMOpener for a direct handleDM call.
type dmFakeSlack struct{ opened []string }

func (f *dmFakeSlack) PostMessage(_ context.Context, _, _, _, _ string) (string, error) {
	return "9.9", nil
}
func (f *dmFakeSlack) ArchiveChannel(_ context.Context, _ string) error { return nil }
func (f *dmFakeSlack) GetPermalink(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (f *dmFakeSlack) UpdateMessage(_ context.Context, _, _, _ string) (string, error) {
	return "", nil
}
func (f *dmFakeSlack) OpenDM(_ context.Context, u string) (string, error) {
	f.opened = append(f.opened, u)
	return "D0" + u, nil
}

// One route object, both handlers: the profile list gates the cold-create,
// while a mapped box with no row list may still DM an install-listed person.
func TestTwoLists_ColdCreateUsesProfileList_DMTargetUsesInstallList(t *testing.T) {
	now := time.Now()
	route := twoListRoute()

	for _, tc := range []struct {
		user    string
		creates int
	}{{"U0KURT", 1}, {"U0BOB", 0}} {
		h, sqs, _, alias, pub := twoListHandler(t, now)
		h.DefaultRoute = route
		alias.found, alias.info = false, SandboxRoutingInfo{}
		h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", tc.user, "<@UBOT123> hi", "1.0", "", "TL-CC-"+tc.user))
		if len(pub.calls) != tc.creates || len(sqs.sends) != 0 {
			t.Fatalf("%s: cold-create gated by the profile list; creates=%d sends=%d", tc.user, len(pub.calls), len(sqs.sends))
		}
	}

	fs := &dmFakeSlack{}
	dh := &Handler{
		Slack:        fs,
		DefaultRoute: route,
		SenderEmails: &scriptedEmailsByUser{byUser: allowEmailsUsers},
		SandboxByID:  &fakeByID{infos: map[string]SandboxRoutingInfo{"sb-mapped": {SandboxID: "sb-mapped"}}},
	}
	resp := dh.handleDM(context.Background(), slack.SlackEnvelope{Action: slack.ActionDM, SenderID: "sb-mapped", TargetUser: "U0BOB", Body: "hi"})
	if resp.StatusCode != 200 || len(fs.opened) != 1 {
		t.Fatalf("bob is on the install list: a box with no row list may DM him; status=%d body=%s", resp.StatusCode, resp.Body)
	}
	resp = dh.handleDM(context.Background(), slack.SlackEnvelope{Action: slack.ActionDM, SenderID: "sb-mapped", TargetUser: "U0Z", Body: "hi"})
	if resp.StatusCode != 403 {
		t.Fatalf("z@other.example is on neither list; status=%d", resp.StatusCode)
	}
}

func TestTwoLists_AliasBoxWithoutRowList_UsesProfileList(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		user  string
		sends int
	}{{"U0KURT", 1}, {"U0BOB", 0}} {
		h, sqs, _, _, _ := twoListHandler(t, now)
		h.Handle(context.Background(), routedRequest(t, now, "CFOREIGN", "channel", tc.user, "<@UBOT123> hi", "1.0", "", "TL-AB-"+tc.user))
		if len(sqs.sends) != tc.sends {
			t.Fatalf("%s: the alias's box is the route's box; sends=%d want %d", tc.user, len(sqs.sends), tc.sends)
		}
	}
}

// A routed thread continuing to a live box that is NOT the alias box, and has
// no list of its own, is gated by the install list.
func TestTwoLists_ContinuityToNonAliasRowBox_UsesInstallList(t *testing.T) {
	on := true
	now := time.Now()
	for _, tc := range []struct {
		user  string
		sends int
	}{{"U0BOB", 1}, {"U0Z", 0}} {
		h, sqs, _, _ := liveRowBoxHandler(t, now, "D0X", &on)
		h.SenderEmails = &scriptedEmailsByUser{byUser: allowEmailsUsers}
		h.DefaultRoute = twoListRoute()
		h.Handle(context.Background(), routedRequest(t, now, "D0X", "im", tc.user, "and also", "1.5", "1.0", "TL-NA-"+tc.user))
		if len(sqs.sends) != tc.sends {
			t.Fatalf("%s: non-alias row box ⇒ install list; sends=%d want %d", tc.user, len(sqs.sends), tc.sends)
		}
	}
}

// Continuity to a live box that IS the alias box, without a row list, is the
// route's box: the profile list decides, and the strict-context trust uses the
// same function.
func TestTwoLists_ContinuityToAliasRowBox_UsesProfileList(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		user  string
		sends int
	}{{"U0KURT", 1}, {"U0BOB", 0}} {
		h, sqs, threads, _, _ := twoListHandler(t, now)
		threads.sandboxByThread = map[string]string{"D0X|1.0": "sb-irbot"}
		h.Handle(context.Background(), routedRequest(t, now, "D0X", "im", tc.user, "and also", "1.5", "1.0", "TL-AR-"+tc.user))
		if len(sqs.sends) != tc.sends {
			t.Fatalf("%s: alias row box ⇒ profile list; sends=%d want %d", tc.user, len(sqs.sends), tc.sends)
		}
	}
}

func TestTwoLists_RouteGateFor(t *testing.T) {
	h := &EventsHandler{DefaultRoute: twoListRoute()}
	if f := h.routeAllows(h.DefaultRoute, gateRouteCreate); f == nil || f("bob@example.com") || !f("kurt@example.com") {
		t.Fatal("gateRouteCreate ⇒ AllowsCreate")
	}
	if f := h.routeAllows(h.DefaultRoute, gateRouteInstall); f == nil || !f("bob@example.com") {
		t.Fatal("gateRouteInstall ⇒ Allows")
	}
}

package main

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

func TestWireDefaultRoute_AllThreeOrNothing(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	deps := defaultRouteDeps{} // nil clients are fine: wiring only, no calls

	h := &bridge.EventsHandler{}
	wireDefaultRoute(h, nil, get, deps)
	if h.DefaultRoute != nil {
		t.Fatal("absent env must leave the route OFF")
	}

	env["KM_SLACK_DEFAULT_ROUTE_ALIAS"] = "ir-bot"
	env["KM_SLACK_DEFAULT_ROUTE_PROFILE"] = "profiles/ir-bot.yaml"
	h = &bridge.EventsHandler{}
	wireDefaultRoute(h, nil, get, deps)
	if h.DefaultRoute != nil {
		t.Fatal("neither ALLOW nor PROFILE_ALLOW must leave the route OFF")
	}

	env["KM_SLACK_DEFAULT_ROUTE_ALLOW"] = "kurt@example.com, Other@Example.com"
	h = &bridge.EventsHandler{}
	out := &bridge.Handler{}
	wireDefaultRoute(h, out, get, deps)
	if h.DefaultRoute == nil || !h.DefaultRoute.Allows("other@example.com") {
		t.Fatal("route must be ON with both emails")
	}
	if h.AliasResolver == nil || h.CreatePublisher == nil || h.SenderEmails == nil || h.SandboxByID == nil {
		t.Fatal("all four deps must be wired when the route is ON")
	}
	if out.DefaultRoute != h.DefaultRoute || out.SenderEmails != h.SenderEmails {
		t.Fatal("the outbound handler must share the route and the email cache (km-slack dm)")
	}
}

// Two lists: KM_SLACK_DEFAULT_ROUTE_ALLOW is the install list,
// KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW the route profile's list. Either alone
// turns the route ON; the profile list gates cold-create, the install list
// everything else.
func TestWireDefaultRoute_ProfileListOnly_RouteOn(t *testing.T) {
	env := map[string]string{
		"KM_SLACK_DEFAULT_ROUTE_ALIAS":         "ir-bot",
		"KM_SLACK_DEFAULT_ROUTE_PROFILE":       "profiles/ir-bot.yaml",
		"KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW": "kurt@example.com",
	}
	h := &bridge.EventsHandler{}
	out := &bridge.Handler{}
	wireDefaultRoute(h, out, func(k string) string { return env[k] }, defaultRouteDeps{})
	if h.DefaultRoute == nil || out.DefaultRoute != h.DefaultRoute {
		t.Fatal("a profile list alone must turn the route ON on both handlers")
	}
	if !h.DefaultRoute.AllowsCreate("kurt@example.com") || h.DefaultRoute.Allows("kurt@example.com") {
		t.Fatal("PROFILE_ALLOW feeds AllowsCreate only; the empty install list admits nobody else")
	}

	env["KM_SLACK_DEFAULT_ROUTE_ALLOW"] = "*@example.com"
	h = &bridge.EventsHandler{}
	wireDefaultRoute(h, nil, func(k string) string { return env[k] }, defaultRouteDeps{})
	if !h.DefaultRoute.Allows("bob@example.com") || h.DefaultRoute.AllowsCreate("bob@example.com") {
		t.Fatal("ALLOW is the install list; PROFILE_ALLOW still narrows cold-create")
	}
}

// The sender-email cache and the by-id fetcher are wired whether or not the
// route is configured: a sandbox's allowEmails gates its own #sb- channel and
// its km-slack dm targets on a route-less install too.
func TestWireDefaultRoute_EmailGateDepsWiredWithoutRoute(t *testing.T) {
	get := func(string) string { return "" }
	h := &bridge.EventsHandler{}
	out := &bridge.Handler{}
	wireDefaultRoute(h, out, get, defaultRouteDeps{})
	if h.DefaultRoute != nil || out.DefaultRoute != nil {
		t.Fatal("route must stay OFF")
	}
	if h.SenderEmails == nil || out.SenderEmails == nil {
		t.Fatal("SenderEmails must be wired on both handlers without a route")
	}
	if out.SenderEmails != h.SenderEmails {
		t.Fatal("both handlers must share one email cache")
	}
	if h.SandboxByID == nil || out.SandboxByID == nil || out.SandboxByID != h.SandboxByID {
		t.Fatal("both handlers must share one SandboxByID fetcher")
	}
}

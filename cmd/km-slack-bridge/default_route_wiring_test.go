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
		t.Fatal("missing ALLOW must leave the route OFF")
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

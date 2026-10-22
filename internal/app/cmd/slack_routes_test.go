// internal/app/cmd/slack_routes_test.go
package cmd

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

type fakeLister struct {
	channels []kmslack.SlackChannelSummary
	err      error
	calls    int
}

func (f *fakeLister) ListBotChannels(context.Context) ([]kmslack.SlackChannelSummary, error) {
	f.calls++
	return f.channels, f.err
}

// fakeRoutesSSM: Get/Put over a map; implements ssmParamWriter.
type fakeRoutesSSM struct{ m map[string]string }

func (f *fakeRoutesSSM) Get(_ context.Context, name string, _ bool) (string, error) {
	return f.m[name], nil
}
func (f *fakeRoutesSSM) Put(_ context.Context, name, value string, _ bool) error {
	f.m[name] = value
	return nil
}

type fakeUploader struct{ keys []string }

func (f *fakeUploader) PutObject(_ context.Context, _, key string, _ []byte) error {
	f.keys = append(f.keys, key)
	return nil
}

func routesCfg(t *testing.T, routes []config.SlackRoute, dr *config.SlackDefaultRoute) *config.Config {
	t.Helper()
	cfg := &config.Config{ArtifactsBucket: "b", ResourcePrefix: "km"}
	cfg.Slack.Routes = routes
	cfg.Slack.DefaultRoute = dr
	return cfg
}

var botChannels = []kmslack.SlackChannelSummary{{ID: "C1", Name: "sec"}, {ID: "G2", Name: "ops"}}

func TestEffectiveSlackChannelRoutes_ListFallbackAndCanonical(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	pl := writeProfile(t, dir, "listed.yaml", routeProfileOK+"        allowEmails: [prof@example.com]\n")
	f := false
	cfg := routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: []string{"Own@Example.com"}, MentionOnly: &f},
		{Channel: "G2", Alias: "ops-bot", Profile: pl},
	}, &config.SlackDefaultRoute{Alias: "ir-bot", Profile: p, AllowEmails: []string{"inst@example.com"}})
	plan, err := effectiveSlackChannelRoutes(cfg, botChannels)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("entries: %+v", plan.Entries)
	}
	byCh := map[string]slackbridge.RouteEntry{}
	for _, e := range plan.Entries {
		byCh[e.Channel] = e
	}
	if e := byCh["C1"]; e.Name != "sec" || e.Alias != "ir-bot" || e.Profile != "ir-bot" || e.MentionOnly || len(e.Allow) != 1 || e.Allow[0] != "own@example.com" {
		t.Errorf("C1: own list must replace: %+v", e)
	}
	if e := byCh["G2"]; !e.MentionOnly || len(e.Allow) != 1 || e.Allow[0] != "inst@example.com" {
		t.Errorf("G2: install list must win over the profile list: %+v", e)
	}
	// No install list ⇒ the profile list.
	cfg.Slack.DefaultRoute = nil
	plan, err = effectiveSlackChannelRoutes(cfg, botChannels)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if e.Channel == "G2" && (len(e.Allow) != 1 || e.Allow[0] != "prof@example.com") {
			t.Errorf("G2 without install list: %+v", e)
		}
	}
	if plan.SHA == "" || len(plan.JSON) == 0 || !strings.HasPrefix(string(plan.JSON), `{"v":1,"routes":[{"channel":"C1"`) {
		t.Errorf("plan JSON/SHA: %s %s", plan.JSON, plan.SHA)
	}
	if len(plan.Profiles) != 2 {
		t.Errorf("two distinct profiles to stage, got %d", len(plan.Profiles))
	}
}

func TestEffectiveSlackChannelRoutes_Errors(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	noInbound := writeProfile(t, dir, "noin.yaml", routeProfileNoInbound)
	cases := []struct {
		name   string
		routes []config.SlackRoute
		want   string
	}{
		{"unknown name", []config.SlackRoute{{Channel: "#nope", Alias: "a", Profile: p, AllowEmails: []string{"k@example.com"}}}, "#nope"},
		{"unknown id", []config.SlackRoute{{Channel: "C9", Alias: "a", Profile: p, AllowEmails: []string{"k@example.com"}}}, "C9"},
		{"name and id collide", []config.SlackRoute{
			{Channel: "#sec", Alias: "a", Profile: p, AllowEmails: []string{"k@example.com"}},
			{Channel: "C1", Alias: "b", Profile: p, AllowEmails: []string{"k@example.com"}}}, "twice"},
		{"no list anywhere", []config.SlackRoute{{Channel: "#sec", Alias: "a", Profile: p}}, "no allowlist"},
		{"profile without inbound", []config.SlackRoute{{Channel: "#sec", Alias: "a", Profile: noInbound, AllowEmails: []string{"k@example.com"}}}, "inbound"},
	}
	for _, c := range cases {
		_, err := effectiveSlackChannelRoutes(routesCfg(t, c.routes, nil), botChannels)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
	// The unknown-name error lists the bot's channels so a rename is obvious.
	_, err := effectiveSlackChannelRoutes(routesCfg(t, cases[0].routes, nil), botChannels)
	if !strings.Contains(err.Error(), "#sec") || !strings.Contains(err.Error(), "#ops") {
		t.Errorf("unknown-name error must list the bot's channels: %v", err)
	}
}

func TestPrepareSlackChannelRoutes_WritesParamStagesProfilesExportsHash(t *testing.T) {
	t.Setenv(envSlackRoutesSHA, "")
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	cfg := routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: []string{"k@example.com"}},
		{Channel: "#ops", Alias: "ops-bot", Profile: p, AllowEmails: []string{"k@example.com"}},
	}, nil)
	lister := &fakeLister{channels: botChannels}
	ssm := &fakeRoutesSSM{m: map[string]string{}}
	up := &fakeUploader{}
	if err := prepareSlackChannelRoutes(context.Background(), cfg, lister, ssm, up); err != nil {
		t.Fatal(err)
	}
	if lister.calls != 1 {
		t.Errorf("one users.conversations listing per init, got %d", lister.calls)
	}
	doc := ssm.m["/km/slack/routes"]
	if doc == "" {
		t.Fatal("parameter not written")
	}
	if os.Getenv(envSlackRoutesSHA) != slackbridge.RoutesSHA256([]byte(doc)) {
		t.Errorf("env hash %q must be the hash of the written bytes", os.Getenv(envSlackRoutesSHA))
	}
	if len(up.keys) != 1 || up.keys[0] != "slack-profiles/ir-bot/.km-profile.yaml" {
		t.Errorf("a shared profile is staged once: %v", up.keys)
	}
}

func TestPrepareSlackChannelRoutes_NoRoutes(t *testing.T) {
	t.Setenv(envSlackRoutesSHA, "stale")
	cfg := routesCfg(t, nil, nil)
	lister := &fakeLister{err: context.Canceled}
	ssm := &fakeRoutesSSM{m: map[string]string{}}
	if err := prepareSlackChannelRoutes(context.Background(), cfg, lister, ssm, &fakeUploader{}); err != nil {
		t.Fatal(err)
	}
	if lister.calls != 0 || len(ssm.m) != 0 || os.Getenv(envSlackRoutesSHA) != "" {
		t.Fatalf("no routes ⇒ no Slack call, no parameter, empty hash: calls=%d m=%v env=%q", lister.calls, ssm.m, os.Getenv(envSlackRoutesSHA))
	}
	// A table from an earlier config is overwritten with the empty document.
	ssm.m["/km/slack/routes"] = `{"v":1,"routes":[{"channel":"C1","alias":"a","profile":"p","allow":["k@example.com"]}]}`
	if err := prepareSlackChannelRoutes(context.Background(), cfg, lister, ssm, &fakeUploader{}); err != nil {
		t.Fatal(err)
	}
	if ssm.m["/km/slack/routes"] != `{"v":1,"routes":[]}` {
		t.Fatalf("stale table must be emptied, got %s", ssm.m["/km/slack/routes"])
	}
	if os.Getenv(envSlackRoutesSHA) != slackbridge.RoutesSHA256([]byte(`{"v":1,"routes":[]}`)) {
		t.Error("hash must follow the emptied document")
	}
}

func TestExportSlackRoutesHashFromSSM(t *testing.T) {
	t.Setenv(envSlackRoutesSHA, "")
	cfg := routesCfg(t, nil, nil)
	exportSlackRoutesHashFromSSM(context.Background(), cfg, &fakeRoutesSSM{m: map[string]string{}})
	if os.Getenv(envSlackRoutesSHA) != "" {
		t.Error("absent parameter ⇒ empty hash")
	}
	exportSlackRoutesHashFromSSM(context.Background(), cfg, &fakeRoutesSSM{m: map[string]string{"/km/slack/routes": "abc"}})
	if os.Getenv(envSlackRoutesSHA) != slackbridge.RoutesSHA256([]byte("abc")) {
		t.Error("hash of the live parameter")
	}
}

type errGetSSM struct{ fakeRoutesSSM }

func (e *errGetSSM) Get(context.Context, string, bool) (string, error) {
	return "", context.DeadlineExceeded
}

func TestPrepareSlackChannelRoutes_NoRoutesReadErrorFailsClosed(t *testing.T) {
	t.Setenv(envSlackRoutesSHA, "")
	ssm := &errGetSSM{fakeRoutesSSM{m: map[string]string{}}}
	err := prepareSlackChannelRoutes(context.Background(), routesCfg(t, nil, nil), &fakeLister{}, ssm, &fakeUploader{})
	if err == nil || !strings.Contains(err.Error(), "/km/slack/routes") || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("a Get error on the no-routes path must be returned naming the parameter, got %v", err)
	}
}

func TestEffectiveSlackChannelRoutes_SameBaseNameDifferentFilesErrors(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	p1 := writeProfile(t, d1, "bot.yaml", routeProfileOK)
	p2 := writeProfile(t, d2, "bot.yaml", routeProfileOK)
	_, err := effectiveSlackChannelRoutes(routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "a", Profile: p1, AllowEmails: []string{"k@example.com"}},
		{Channel: "#ops", Alias: "b", Profile: p2, AllowEmails: []string{"k@example.com"}},
	}, nil), botChannels)
	if err == nil || !strings.Contains(err.Error(), `"bot"`) || !strings.Contains(err.Error(), p1) || !strings.Contains(err.Error(), p2) {
		t.Fatalf("want error naming bot and both paths, got %v", err)
	}
	// Same file twice is fine.
	if _, err := effectiveSlackChannelRoutes(routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "a", Profile: p1, AllowEmails: []string{"k@example.com"}},
		{Channel: "#ops", Alias: "b", Profile: p1, AllowEmails: []string{"k@example.com"}},
	}, nil), botChannels); err != nil {
		t.Fatalf("one file shared by two routes must pass: %v", err)
	}
}

func TestEffectiveSlackChannelRoutes_CollidesWithDefaultRouteProfile(t *testing.T) {
	d1, d2 := t.TempDir(), t.TempDir()
	dp := writeProfile(t, d1, "bot.yaml", routeProfileOK)
	rp := writeProfile(t, d2, "bot.yaml", routeProfileOK)
	cfg := routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "a", Profile: rp, AllowEmails: []string{"k@example.com"}},
	}, &config.SlackDefaultRoute{Alias: "d", Profile: dp, AllowEmails: []string{"k@example.com"}})
	_, err := effectiveSlackChannelRoutes(cfg, botChannels)
	if err == nil || !strings.Contains(err.Error(), "slack.default_route") || !strings.Contains(err.Error(), dp) || !strings.Contains(err.Error(), rp) {
		t.Fatalf("want collision with the default route's profile, got %v", err)
	}
}

// Every entry carries its route profile's own allowEmails as ProfileAllow,
// whichever list became Allow, so the bridge gates the cold-create with it.
func TestEffectiveSlackChannelRoutes_CarriesProfileAllow(t *testing.T) {
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	pl := writeProfile(t, dir, "listed.yaml", routeProfileOK+"        allowEmails: [Prof@Example.com]\n")
	plan, err := effectiveSlackChannelRoutes(routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: []string{"*@example.com"}},
		{Channel: "G2", Alias: "ops-bot", Profile: pl, AllowEmails: []string{"*@example.com"}},
	}, nil), botChannels)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		switch e.Channel {
		case "C1":
			if e.ProfileAllow != nil {
				t.Errorf("C1: a profile without allowEmails must carry nil: %+v", e.ProfileAllow)
			}
		case "G2":
			if len(e.ProfileAllow) != 1 || e.ProfileAllow[0] != "prof@example.com" || e.Allow[0] != "*@example.com" {
				t.Errorf("G2: ProfileAllow must be the normalised profile list beside the route's own Allow: %+v", e)
			}
		}
	}
	if !strings.Contains(string(plan.JSON), `"profile_allow":["prof@example.com"]`) {
		t.Errorf("the written table must carry profile_allow: %s", plan.JSON)
	}
}

// The table is a Standard SSM String: past 4096 bytes km init refuses with a
// named error and writes nothing.
func TestPrepareSlackChannelRoutes_RefusesTableOverSSMStandardLimit(t *testing.T) {
	t.Setenv(envSlackRoutesSHA, "")
	dir := t.TempDir()
	p := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	var emails []string
	for i := 0; i < 200; i++ {
		emails = append(emails, "someone-with-a-long-name-"+strings.Repeat("x", 3)+string(rune('a'+i%26))+strings.Repeat("y", i%7)+"@example.com")
	}
	cfg := routesCfg(t, []config.SlackRoute{{Channel: "#sec", Alias: "ir-bot", Profile: p, AllowEmails: emails}}, nil)
	ssm := &fakeRoutesSSM{m: map[string]string{}}
	up := &fakeUploader{}
	err := prepareSlackChannelRoutes(context.Background(), cfg, &fakeLister{channels: botChannels}, ssm, up)
	if err == nil || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("want an error naming the 4096-byte limit, got %v", err)
	}
	if len(ssm.m) != 0 || len(up.keys) != 0 {
		t.Fatalf("nothing may be written or staged: ssm=%v staged=%v", ssm.m, up.keys)
	}
}

// One alias is one box: two routes (or a route and the default route) naming
// the same alias with different profiles is refused, naming both.
func TestEffectiveSlackChannelRoutes_AliasWithTwoProfilesErrors(t *testing.T) {
	dir := t.TempDir()
	a := writeProfile(t, dir, "ir-bot.yaml", routeProfileOK)
	b := writeProfile(t, dir, "ops.yaml", routeProfileOK)
	_, err := effectiveSlackChannelRoutes(routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "shared", Profile: a, AllowEmails: []string{"k@example.com"}},
		{Channel: "#ops", Alias: "shared", Profile: b, AllowEmails: []string{"k@example.com"}},
	}, nil), botChannels)
	if err == nil || !strings.Contains(err.Error(), `"ir-bot"`) || !strings.Contains(err.Error(), `"ops"`) || !strings.Contains(err.Error(), `"shared"`) {
		t.Fatalf("two routes, one alias, two profiles: want error naming both, got %v", err)
	}
	_, err = effectiveSlackChannelRoutes(routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "ir-bot", Profile: b, AllowEmails: []string{"k@example.com"}},
	}, &config.SlackDefaultRoute{Alias: "ir-bot", Profile: a, AllowEmails: []string{"k@example.com"}}), botChannels)
	if err == nil || !strings.Contains(err.Error(), "slack.default_route") || !strings.Contains(err.Error(), `"ir-bot"`) || !strings.Contains(err.Error(), `"ops"`) {
		t.Fatalf("route vs default route, one alias, two profiles: want error, got %v", err)
	}
	// Same alias, same profile: fine.
	if _, err := effectiveSlackChannelRoutes(routesCfg(t, []config.SlackRoute{
		{Channel: "#sec", Alias: "shared", Profile: a, AllowEmails: []string{"k@example.com"}},
		{Channel: "#ops", Alias: "shared", Profile: a, AllowEmails: []string{"k@example.com"}},
	}, &config.SlackDefaultRoute{Alias: "shared", Profile: a, AllowEmails: []string{"k@example.com"}}), botChannels); err != nil {
		t.Fatalf("one alias, one profile, many channels must pass: %v", err)
	}
}

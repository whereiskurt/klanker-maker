package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// historyProfile is a profile with Slack inbound enabled, written to a temp
// dir (the shipped profiles/learn.yaml has no inbound, which routes require).
func historyProfile(t *testing.T) string {
	t.Helper()
	return writeProfile(t, t.TempDir(), "hist-bot.yaml", routeProfileOK)
}

func historyCfg(t *testing.T, routes ...config.SlackRoute) *config.Config {
	t.Helper()
	p := historyProfile(t)
	cfg := &config.Config{}
	cfg.Slack.DefaultRoute = &config.SlackDefaultRoute{Alias: "dr", Profile: p, AllowEmails: []string{"ops@example.com"}}
	for i := range routes {
		routes[i].Profile = p
	}
	cfg.Slack.Routes = routes
	return cfg
}

var historyChannels = []kmslack.SlackChannelSummary{
	{ID: "C1", Name: "inc_2026_a"}, {ID: "C2", Name: "inc_2026_b"}, {ID: "C3", Name: "sec-1"}, {ID: "C4", Name: "general"},
}

func TestEffectiveSlackChannelRoutes_GlobExpandsAndEmitsPattern(t *testing.T) {
	cfg := historyCfg(t, config.SlackRoute{Channel: "#inc_*", Alias: "ir",
		History: &config.SlackRouteHistory{Query: true, Mirror: true, Backfill: "3d", MaxMessages: 50}})
	plan, err := effectiveSlackChannelRoutes(cfg, historyChannels)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 0 {
		t.Fatalf("entries = %d; glob matches ride on the pattern, not as route entries", len(plan.Entries))
	}
	if len(plan.Patterns) != 1 || plan.Patterns[0].Glob != "inc_*" || plan.Patterns[0].History == nil {
		t.Fatalf("patterns = %+v", plan.Patterns)
	}
	p := plan.Patterns[0]
	if h := p.History; !h.Mirror || h.BackfillSeconds != 3*86400 || h.RetentionSeconds != 3*86400 || h.MaxMessages != 50 {
		t.Errorf("pattern history = %+v", h)
	}
	if len(p.Channels) != 2 || p.Channels[0] != (slackbridge.PatternChannel{ID: "C1", Name: "inc_2026_a"}) || p.Channels[1] != (slackbridge.PatternChannel{ID: "C2", Name: "inc_2026_b"}) {
		t.Fatalf("pattern channels = %+v; want the two inc_ channels", p.Channels)
	}
	if !strings.Contains(string(plan.JSON), `"patterns":[{"glob":"inc_*"`) || !strings.Contains(string(plan.JSON), `"channels":[{"id":"C1","name":"inc_2026_a"}`) {
		t.Errorf("JSON lacks patterns/channels: %s", plan.JSON)
	}
	doc, err := slackbridge.ParseRoutesDocument(plan.JSON)
	if err != nil {
		t.Fatal(err)
	}
	table := slackbridge.BuildRouteTable(doc, func(string, ...any) {})
	if r := table["C2"]; r == nil || r.Alias != "ir" || r.Pattern != "inc_*" || r.History == nil || !r.History.Mirror {
		t.Fatalf("bridge table must route C2 through the glob: %+v", r)
	}
}

func TestEffectiveSlackChannelRoutes_GlobNoCurrentMatchIsNotAnError(t *testing.T) {
	cfg := historyCfg(t, config.SlackRoute{Channel: "#nomatch_*", Alias: "ir", History: &config.SlackRouteHistory{Query: true}})
	plan, err := effectiveSlackChannelRoutes(cfg, historyChannels)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 0 || len(plan.Patterns) != 1 || len(plan.Patterns[0].Channels) != 0 {
		t.Fatalf("entries=%d patterns=%+v", len(plan.Entries), plan.Patterns)
	}
	if len(plan.Profiles) != 1 {
		t.Fatalf("zero-match glob must still pre-stage its profile; profiles=%d", len(plan.Profiles))
	}
}

func TestEffectiveSlackChannelRoutes_ExactBeatsGlob(t *testing.T) {
	cfg := historyCfg(t,
		config.SlackRoute{Channel: "#inc_*", Alias: "ir", History: &config.SlackRouteHistory{Query: true}},
		config.SlackRoute{Channel: "#inc_2026_a", Alias: "special", Profile: "profiles/learn.yaml"},
	)
	plan, err := effectiveSlackChannelRoutes(cfg, historyChannels)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].Channel != "C1" || plan.Entries[0].Alias != "special" {
		t.Fatalf("entries = %+v; want only the named C1 route", plan.Entries)
	}
	if len(plan.Patterns) != 1 || len(plan.Patterns[0].Channels) != 1 || plan.Patterns[0].Channels[0].ID != "C2" {
		t.Fatalf("the glob must skip C1 (exact beats glob) and keep C2: %+v", plan.Patterns)
	}
}

func TestEffectiveSlackChannelRoutes_TwoGlobsOneChannelErrors(t *testing.T) {
	cfg := historyCfg(t,
		config.SlackRoute{Channel: "#inc_*", Alias: "ir", History: &config.SlackRouteHistory{Query: true}},
		config.SlackRoute{Channel: "#inc_2026_*", Alias: "ir2", History: &config.SlackRouteHistory{Query: true}},
	)
	_, err := effectiveSlackChannelRoutes(cfg, historyChannels)
	if err == nil || !strings.Contains(err.Error(), "inc_2026_a") || !strings.Contains(err.Error(), "two globs") {
		t.Fatalf("err = %v; want the overlap named", err)
	}
}

func TestEffectiveSlackChannelRoutes_HistoryWithoutGlobOnNamedRoute(t *testing.T) {
	cfg := historyCfg(t, config.SlackRoute{Channel: "#sec-1", Alias: "sec", History: &config.SlackRouteHistory{Query: true}})
	plan, err := effectiveSlackChannelRoutes(cfg, historyChannels)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].Pattern != "" || plan.Entries[0].History == nil || !plan.Entries[0].History.Query || plan.Entries[0].History.Mirror {
		t.Fatalf("entry = %+v", plan.Entries[0])
	}
	if len(plan.Patterns) != 0 {
		t.Fatalf("a named route emits no pattern")
	}
}

func TestEffectiveSlackChannelRoutes_PatternsInConfigOrder(t *testing.T) {
	cfg := historyCfg(t,
		config.SlackRoute{Channel: "#zzz_*", Alias: "a", Profile: "profiles/learn.yaml"},
		config.SlackRoute{Channel: "#aaa_*", Alias: "b", Profile: "profiles/learn.yaml"},
	)
	plan, err := effectiveSlackChannelRoutes(cfg, historyChannels)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Patterns) != 2 || plan.Patterns[0].Glob != "zzz_*" || plan.Patterns[1].Glob != "aaa_*" {
		t.Fatalf("patterns must keep config order: %+v", plan.Patterns)
	}
}

// A glob over many current channels must fit the 4 KB Standard parameter: its
// matches ride on the pattern as {id, name}, not as full route entries.
func TestEffectiveSlackChannelRoutes_FortyChannelGlobFitsTheParameter(t *testing.T) {
	var chans []kmslack.SlackChannelSummary
	for i := 0; i < 40; i++ {
		chans = append(chans, kmslack.SlackChannelSummary{ID: fmt.Sprintf("C0%09d", i), Name: fmt.Sprintf("inc_2026_%04d", i)})
	}
	chans = append(chans, kmslack.SlackChannelSummary{ID: "C0SEC000001", Name: "sec-1"}, kmslack.SlackChannelSummary{ID: "C0GEN000001", Name: "general"})
	cfg := historyCfg(t,
		config.SlackRoute{Channel: "#inc_*", Alias: "ir", AllowEmails: []string{"oncall-a@example.com", "oncall-b@example.com"},
			History: &config.SlackRouteHistory{Query: true, Mirror: true, Backfill: "7d", Announce: true}},
		config.SlackRoute{Channel: "#sec-1", Alias: "sec", History: &config.SlackRouteHistory{Query: true}},
		config.SlackRoute{Channel: "#general", Alias: "gen"},
	)
	plan, err := effectiveSlackChannelRoutes(cfg, chans)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Patterns) != 1 || len(plan.Patterns[0].Channels) != 40 || len(plan.Entries) != 2 {
		t.Fatalf("patterns=%+v entries=%d", plan.Patterns, len(plan.Entries))
	}
	if n := len(plan.JSON); n > slackRoutesMaxBytes {
		t.Fatalf("table is %d bytes; a 40-channel glob plus two named routes must fit %d", n, slackRoutesMaxBytes)
	}
	t.Logf("40-channel glob + 2 named routes = %d bytes", len(plan.JSON))
}

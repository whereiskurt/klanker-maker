package cmd

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
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
	if len(plan.Entries) != 2 {
		t.Fatalf("entries = %d; want the two inc_ channels", len(plan.Entries))
	}
	for _, e := range plan.Entries {
		if e.Pattern != "inc_*" || e.History == nil || !e.History.Mirror || e.History.BackfillSeconds != 3*86400 || e.History.RetentionSeconds != 3*86400 || e.History.MaxMessages != 50 {
			t.Errorf("entry = %+v", e)
		}
	}
	if len(plan.Patterns) != 1 || plan.Patterns[0].Glob != "inc_*" || plan.Patterns[0].History == nil {
		t.Fatalf("patterns = %+v", plan.Patterns)
	}
	if !strings.Contains(string(plan.JSON), `"patterns":[{"glob":"inc_*"`) {
		t.Errorf("JSON lacks patterns: %s", plan.JSON)
	}
}

func TestEffectiveSlackChannelRoutes_GlobNoCurrentMatchIsNotAnError(t *testing.T) {
	cfg := historyCfg(t, config.SlackRoute{Channel: "#nomatch_*", Alias: "ir", History: &config.SlackRouteHistory{Query: true}})
	plan, err := effectiveSlackChannelRoutes(cfg, historyChannels)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 0 || len(plan.Patterns) != 1 {
		t.Fatalf("entries=%d patterns=%d", len(plan.Entries), len(plan.Patterns))
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
	byChan := map[string]string{}
	for _, e := range plan.Entries {
		byChan[e.Channel] = e.Alias
	}
	if byChan["C1"] != "special" || byChan["C2"] != "ir" {
		t.Fatalf("exact must win for C1: %v", byChan)
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

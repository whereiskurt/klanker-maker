package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestParseRouteDuration(t *testing.T) {
	ok := map[string]time.Duration{"7d": 7 * 24 * time.Hour, "24h": 24 * time.Hour, "90m": 90 * time.Minute, "30d": 30 * 24 * time.Hour}
	for in, want := range ok {
		got, err := config.ParseRouteDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseRouteDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "31d", "0d", "7", "7 days", "-1d", "all", "99999999999d", "99999999999999999999d"} {
		if _, err := config.ParseRouteDuration(bad); err == nil {
			t.Errorf("ParseRouteDuration(%q): want error", bad)
		}
	}
}

func TestSlackRouteHistory_Validate(t *testing.T) {
	cases := []struct {
		name string
		h    config.SlackRouteHistory
		want string // substring of the error, "" = valid
	}{
		{"query only", config.SlackRouteHistory{Query: true}, ""},
		{"mirror+query defaults", config.SlackRouteHistory{Query: true, Mirror: true}, ""},
		{"neither", config.SlackRouteHistory{}, "query or mirror"},
		{"mirror without query", config.SlackRouteHistory{Mirror: true}, "mirror: true requires query: true"},
		{"backfill too long", config.SlackRouteHistory{Query: true, Mirror: true, Backfill: "31d"}, "backfill"},
		{"retention too long", config.SlackRouteHistory{Query: true, Mirror: true, Retention: "45d"}, "retention"},
		{"max too big", config.SlackRouteHistory{Query: true, MaxMessages: 1001}, "max_messages"},
		{"max negative", config.SlackRouteHistory{Query: true, MaxMessages: -1}, "max_messages"},
	}
	for _, tc := range cases {
		err := tc.h.Validate()
		if tc.want == "" && err != nil {
			t.Errorf("%s: unexpected %v", tc.name, err)
		}
		if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: err = %v; want %q", tc.name, err, tc.want)
		}
	}
}

func TestSlackRouteHistory_Resolved_Defaults(t *testing.T) {
	h := config.SlackRouteHistory{Query: true, Mirror: true}
	b, r, n, err := h.Resolved()
	if err != nil || b != 7*24*time.Hour || r != 7*24*time.Hour || n != 200 {
		t.Fatalf("defaults = %v %v %d %v; want 7d 7d 200", b, r, n, err)
	}
	h = config.SlackRouteHistory{Query: true, Mirror: true, Backfill: "3d", MaxMessages: 50}
	b, r, n, _ = h.Resolved()
	if b != 3*24*time.Hour || r != 3*24*time.Hour || n != 50 {
		t.Fatalf("retention must default to backfill: %v %v %d", b, r, n)
	}
}

func TestSlackHistoryRate(t *testing.T) {
	var nilRate *config.SlackHistoryRate
	if p, i := nilRate.Effective(); p != 200 || i != 2 {
		t.Fatalf("nil defaults = %d,%d; want 200,2", p, i)
	}
	zero := 0
	r := config.SlackHistoryRate{PageSize: 15, IntervalSeconds: &zero}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if p, i := r.Effective(); p != 15 || i != 0 {
		t.Fatalf("explicit 0 interval must survive: %d,%d", p, i)
	}
	big := 601
	for _, bad := range []config.SlackHistoryRate{{PageSize: 1000}, {PageSize: -1}, {IntervalSeconds: &big}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%+v: want error", bad)
		}
	}
}

// The merge-list trap (project_config_key_merge_list): a key missing from it loads as zero.
func TestLoad_SlackHistoryRateSurvivesMerge(t *testing.T) {
	cfg := loadFrom(t, "domain: example.com\nregion: us-east-1\nslack:\n  history_rate:\n    page_size: 15\n    interval_seconds: 60\n") // use the helper the existing slack.routes merge test uses
	if cfg.Slack.HistoryRate == nil || cfg.Slack.HistoryRate.PageSize != 15 || cfg.Slack.HistoryRate.IntervalSeconds == nil || *cfg.Slack.HistoryRate.IntervalSeconds != 60 {
		t.Fatalf("history_rate dropped by the merge-list: %+v", cfg.Slack.HistoryRate)
	}
}

func TestSlackRoute_Glob(t *testing.T) {
	r := config.SlackRoute{Channel: "#inc_*", Alias: "ir", Profile: "p.yaml"}
	if err := r.Validate(); err != nil {
		t.Fatalf("glob route must validate: %v", err)
	}
	g, ok := r.ChannelGlob()
	if !ok || g != "inc_*" {
		t.Fatalf("ChannelGlob = %q,%v", g, ok)
	}
	if _, isName := r.ChannelName(); isName {
		t.Fatal("a glob is not a plain name")
	}
	for _, bad := range []string{"#INC_*", "C01*", "#inc_[a]", "#*/x"} {
		r := config.SlackRoute{Channel: bad, Alias: "ir", Profile: "p.yaml"}
		if err := r.Validate(); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
	// history errors surface through the route
	r = config.SlackRoute{Channel: "#inc_*", Alias: "ir", Profile: "p.yaml", History: &config.SlackRouteHistory{Mirror: true}}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "history") {
		t.Fatalf("route must carry history errors: %v", err)
	}
}

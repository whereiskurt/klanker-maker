// internal/app/config/config_slack_routes_test.go
package config_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// Merge-list regression guard: "slack.routes" absent from the v2→v merge-list
// leaves Routes empty and this test fails loud.
func TestLoadSlackRoutes_Set(t *testing.T) {
	cfg := loadFrom(t, `
domain: example.com
region: us-east-1
slack:
    routes:
      - channel: "#security-alerts"
        alias: ir-bot
        profile: profiles/ir-bot.yaml
        allow_emails: [" Kurt@Example.com ", "*@example.com"]
        mention_only: false
      - channel: C0AB12CD3
        alias: platform-bot
        profile: profiles/platform.yaml
`)
	rs := cfg.Slack.Routes
	if len(rs) != 2 {
		t.Fatalf("Slack.Routes = %d entries — add \"slack.routes\" to the v2→v merge-list (project_config_key_merge_list footgun)", len(rs))
	}
	if rs[0].Alias != "ir-bot" || rs[0].Profile != "profiles/ir-bot.yaml" {
		t.Errorf("route 0: %+v", rs[0])
	}
	if name, ok := rs[0].ChannelName(); !ok || name != "security-alerts" {
		t.Errorf("route 0 ChannelName = %q,%v", name, ok)
	}
	if got := rs[0].NormalizedEmails(); len(got) != 2 || got[0] != "kurt@example.com" || got[1] != "*@example.com" {
		t.Errorf("NormalizedEmails: %v", got)
	}
	if rs[0].MentionRequired() {
		t.Error("mention_only: false must read as not required")
	}
	if id, ok := rs[1].ChannelID(); !ok || id != "C0AB12CD3" {
		t.Errorf("route 1 ChannelID = %q,%v", id, ok)
	}
	if !rs[1].MentionRequired() {
		t.Error("absent mention_only must read as required")
	}
}

func TestLoadSlackRoutes_AbsentAndWithoutDefaultRoute(t *testing.T) {
	cfg := loadFrom(t, "domain: example.com\nregion: us-east-1\n")
	if len(cfg.Slack.Routes) != 0 {
		t.Fatalf("expected no routes, got %+v", cfg.Slack.Routes)
	}
	cfg = loadFrom(t, `
domain: example.com
region: us-east-1
slack:
    routes:
      - channel: "#a"
        alias: a
        profile: profiles/a.yaml
`)
	if cfg.Slack.DefaultRoute != nil || len(cfg.Slack.Routes) != 1 {
		t.Fatalf("routes without default_route must load: %+v / %+v", cfg.Slack.DefaultRoute, cfg.Slack.Routes)
	}
	if err := cfg.Slack.ValidateRoutes(); err != nil {
		t.Fatalf("ValidateRoutes: %v", err)
	}
}

func TestSlackRoute_Validate(t *testing.T) {
	cases := []struct {
		name  string
		r     config.SlackRoute
		wantE string
	}{
		{"ok name", config.SlackRoute{Channel: "#sec-alerts_1.x", Alias: "a", Profile: "p"}, ""},
		{"ok id", config.SlackRoute{Channel: "G0ABC", Alias: "a", Profile: "p"}, ""},
		{"bare name", config.SlackRoute{Channel: "sec", Alias: "a", Profile: "p"}, "channel"},
		{"upper name", config.SlackRoute{Channel: "#Sec", Alias: "a", Profile: "p"}, "channel"},
		{"space", config.SlackRoute{Channel: "#a b", Alias: "a", Profile: "p"}, "channel"},
		{"no alias", config.SlackRoute{Channel: "#a", Profile: "p"}, "alias"},
		{"no profile", config.SlackRoute{Channel: "#a", Alias: "a"}, "profile"},
		{"bad email", config.SlackRoute{Channel: "#a", Alias: "a", Profile: "p", AllowEmails: []string{"*example.com"}}, "allow_emails"},
	}
	for _, c := range cases {
		err := c.r.Validate()
		if c.wantE == "" && err != nil {
			t.Errorf("%s: unexpected %v", c.name, err)
		}
		if c.wantE != "" && (err == nil || !strings.Contains(err.Error(), c.wantE)) {
			t.Errorf("%s: want error naming %q, got %v", c.name, c.wantE, err)
		}
	}
}

func TestSlackConfig_ValidateRoutes_LiteralDuplicate(t *testing.T) {
	s := config.SlackConfig{Routes: []config.SlackRoute{
		{Channel: "#a", Alias: "x", Profile: "p"},
		{Channel: " #A ", Alias: "y", Profile: "p"},
	}}
	err := s.ValidateRoutes()
	if err == nil || !strings.Contains(err.Error(), "#a") || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("want duplicate error naming #a, got %v", err)
	}
	s.Routes[1].Channel = "#b"
	if err := s.ValidateRoutes(); err != nil {
		t.Fatalf("distinct channels, same profile must validate: %v", err)
	}
}

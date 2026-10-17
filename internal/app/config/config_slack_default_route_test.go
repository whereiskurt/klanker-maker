package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func loadFrom(t *testing.T, yaml string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	writeKMConfig(t, dir, yaml)
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	return cfg
}

// Merge-list regression guard: if "slack.default_route" is absent from the
// v2→v merge-list, DefaultRoute stays nil and this test fails loud.
func TestLoadSlackDefaultRoute_Set(t *testing.T) {
	cfg := loadFrom(t, `
domain: example.com
region: us-east-1
slack:
    default_route:
      alias: ir-bot
      profile: profiles/ir-bot.yaml
      allow_emails:
        - Kurt@Example.com
        - analyst@example.com
`)
	r := cfg.Slack.DefaultRoute
	if r == nil {
		t.Fatal("Slack.DefaultRoute is nil — add \"slack.default_route\" to the v2→v merge-list (project_config_key_merge_list footgun)")
	}
	if r.Alias != "ir-bot" || r.Profile != "profiles/ir-bot.yaml" {
		t.Errorf("alias/profile: got %q/%q", r.Alias, r.Profile)
	}
	got := r.NormalizedEmails()
	if len(got) != 2 || got[0] != "kurt@example.com" || got[1] != "analyst@example.com" {
		t.Errorf("NormalizedEmails: got %v", got)
	}
}

func TestLoadSlackDefaultRoute_Absent(t *testing.T) {
	cfg := loadFrom(t, "domain: example.com\nregion: us-east-1\n")
	if cfg.Slack.DefaultRoute != nil {
		t.Fatalf("expected nil DefaultRoute when absent, got %+v", cfg.Slack.DefaultRoute)
	}
}

func TestSlackDefaultRoute_Validate(t *testing.T) {
	cases := []struct {
		name    string
		route   config.SlackDefaultRoute
		wantErr string
	}{
		{"ok", config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml", AllowEmails: []string{"x@y.z"}}, ""},
		{"no alias", config.SlackDefaultRoute{Profile: "p.yaml", AllowEmails: []string{"x@y.z"}}, "alias"},
		{"no profile", config.SlackDefaultRoute{Alias: "a", AllowEmails: []string{"x@y.z"}}, "profile"},
		{"empty allow", config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml"}, "allow_emails"},
		{"not an email", config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml", AllowEmails: []string{"nope"}}, "nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.route.Validate()
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

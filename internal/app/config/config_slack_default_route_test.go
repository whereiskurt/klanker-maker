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
		{"ok", config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml", AllowEmails: []string{"x@y.io"}}, ""},
		{"no alias", config.SlackDefaultRoute{Profile: "p.yaml", AllowEmails: []string{"x@y.io"}}, "alias"},
		{"no profile", config.SlackDefaultRoute{Alias: "a", AllowEmails: []string{"x@y.io"}}, "profile"},
		// Empty is valid at this layer: the route profile's allowEmails may
		// supply the list. km init rejects "neither" (ValidateSlackDefaultRouteProfile).
		{"empty allow", config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml"}, ""},
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

func TestSlackDefaultRoute_Validate_DomainWildcard(t *testing.T) {
	ok := config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml", AllowEmails: []string{"*@example.com", "kurt@example.com"}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("domain wildcard + exact must validate: %v", err)
	}
	bad := config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml", AllowEmails: []string{"k*@example.com"}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "k*@example.com") {
		t.Fatalf("a glob other than *@<domain> must fail naming the entry, got %v", err)
	}
	r := config.SlackDefaultRoute{AllowEmails: []string{" *@Example.COM "}}
	if got := r.NormalizedEmails(); len(got) != 1 || got[0] != "*@example.com" {
		t.Fatalf("NormalizedEmails = %v", got)
	}
}

// Mirrors emailEntryCases in pkg/slack/bridge/default_route_test.go
// (TestValidateEmailEntry_SharedTable). config cannot import pkg/slack/bridge,
// so the two validators share a table instead of code: change both together.
func TestValidateRouteEmailEntry_MirrorsBridge(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"kurt@example.com", true},
		{"Kurt@Example.COM", true},
		{" *@example.com ", true},
		{"*@corp.example", true},
		{"*", false},
		{"*@", false},
		{"*@*", false},
		{"k*@example.com", false},
		{"*@example", false},
		{"kurt@", false},
		{"@example.com", false},
		{"*@exa mple.com", false},
		{"nope", false},
		{"x@y.z", false},
		{"*@.example.com", false},
		{"*@-.com", false},
		{"*@a..com", false},
		{"bob,eve@example.com", false},
	}
	for _, tc := range cases {
		r := config.SlackDefaultRoute{Alias: "a", Profile: "p.yaml", AllowEmails: []string{tc.in}}
		err := r.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%q: ok=%v, err=%v", tc.in, tc.ok, err)
		}
	}
}

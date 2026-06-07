// Package config_test provides GitHub commands config round-trip tests.
// Phase 99 Plan 01 Task 1: GithubCommandEntry struct + Commands/DefaultCommand fields.
package config_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestGithubConfigCommands verifies that the github.commands block and
// github.default_command round-trip through config.Load() correctly.
//
// Design notes:
//   - Uses the same writeKMConfigGH + chdirGH helpers from config_github_test.go
//     (both are in package config_test, so they are available here).
//   - The merge-list entry "github" (config.go:484) already covers the WHOLE
//     github: block via v.UnmarshalKey("github", &cfg.Github) — no additional
//     merge-list entry is needed for commands or default_command.
func TestGithubConfigCommands(t *testing.T) {
	t.Run("full commands block round-trips", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigGH(t, dir, `
domain: example.com
region: us-east-1
github:
  default_profile: profiles/review.yaml
  default_command: review
  commands:
    review:
      description: "Review a pull request"
      prompt: "Please review this PR and provide feedback on code quality."
    triage:
      description: "Triage an issue"
      alias: triage-sandbox
      profile: profiles/triage.yaml
      allow:
        - "github.com"
        - "api.github.com"
      prompt: "Triage this issue and suggest labels."
  repos:
    - match: "myorg/frontend"
      alias: frontend
      profile: profiles/frontend.yaml
      default_command: triage
    - match: "myorg/backend"
      alias: backend
      profile: profiles/backend.yaml
`)
		chdirGH(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		// Install-wide DefaultCommand
		if cfg.Github.DefaultCommand != "review" {
			t.Errorf("Github.DefaultCommand: got %q, want %q", cfg.Github.DefaultCommand, "review")
		}

		// Commands map has exactly 2 entries
		if len(cfg.Github.Commands) != 2 {
			t.Fatalf("Github.Commands: got len=%d, want 2; values=%+v", len(cfg.Github.Commands), cfg.Github.Commands)
		}

		// "review" command — inline prompt, no alias/profile/allow
		review, ok := cfg.Github.Commands["review"]
		if !ok {
			t.Fatal("Github.Commands[\"review\"] missing")
		}
		if review.Description != "Review a pull request" {
			t.Errorf("Commands[review].Description: got %q, want %q", review.Description, "Review a pull request")
		}
		if review.Prompt != "Please review this PR and provide feedback on code quality." {
			t.Errorf("Commands[review].Prompt: got %q, want %q", review.Prompt, "Please review this PR and provide feedback on code quality.")
		}
		if review.Alias != "" {
			t.Errorf("Commands[review].Alias: got %q, want empty (inline-prompt command)", review.Alias)
		}
		if review.Profile != "" {
			t.Errorf("Commands[review].Profile: got %q, want empty (inline-prompt command)", review.Profile)
		}
		if len(review.Allow) != 0 {
			t.Errorf("Commands[review].Allow: got %v, want empty (inline-prompt command)", review.Allow)
		}

		// "triage" command — full routing-override fields
		triage, ok := cfg.Github.Commands["triage"]
		if !ok {
			t.Fatal("Github.Commands[\"triage\"] missing")
		}
		if triage.Description != "Triage an issue" {
			t.Errorf("Commands[triage].Description: got %q, want %q", triage.Description, "Triage an issue")
		}
		if triage.Alias != "triage-sandbox" {
			t.Errorf("Commands[triage].Alias: got %q, want %q", triage.Alias, "triage-sandbox")
		}
		if triage.Profile != "profiles/triage.yaml" {
			t.Errorf("Commands[triage].Profile: got %q, want %q", triage.Profile, "profiles/triage.yaml")
		}
		if len(triage.Allow) != 2 {
			t.Errorf("Commands[triage].Allow: got len=%d, want 2; values=%v", len(triage.Allow), triage.Allow)
		} else {
			if triage.Allow[0] != "github.com" {
				t.Errorf("Commands[triage].Allow[0]: got %q, want %q", triage.Allow[0], "github.com")
			}
			if triage.Allow[1] != "api.github.com" {
				t.Errorf("Commands[triage].Allow[1]: got %q, want %q", triage.Allow[1], "api.github.com")
			}
		}
		if triage.Prompt != "Triage this issue and suggest labels." {
			t.Errorf("Commands[triage].Prompt: got %q, want %q", triage.Prompt, "Triage this issue and suggest labels.")
		}

		// Per-repo DefaultCommand round-trips
		if len(cfg.Github.Repos) != 2 {
			t.Fatalf("Github.Repos: got len=%d, want 2", len(cfg.Github.Repos))
		}
		r0 := cfg.Github.Repos[0]
		if r0.Match != "myorg/frontend" {
			t.Errorf("Repos[0].Match: got %q, want %q", r0.Match, "myorg/frontend")
		}
		if r0.DefaultCommand != "triage" {
			t.Errorf("Repos[0].DefaultCommand: got %q, want %q", r0.DefaultCommand, "triage")
		}

		r1 := cfg.Github.Repos[1]
		if r1.Match != "myorg/backend" {
			t.Errorf("Repos[1].Match: got %q, want %q", r1.Match, "myorg/backend")
		}
		if r1.DefaultCommand != "" {
			t.Errorf("Repos[1].DefaultCommand: got %q, want empty (no per-repo default_command)", r1.DefaultCommand)
		}
	})

	t.Run("absent commands block is dormant", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigGH(t, dir, `
domain: example.com
region: us-east-1
github:
  default_profile: profiles/review.yaml
  repos:
    - match: "myorg/sentinel"
      alias: sentinel
`)
		chdirGH(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		if len(cfg.Github.Commands) != 0 {
			t.Errorf("Github.Commands: got len=%d, want 0 (absent commands => zero map)", len(cfg.Github.Commands))
		}
		if cfg.Github.DefaultCommand != "" {
			t.Errorf("Github.DefaultCommand: got %q, want empty (absent default_command => zero)", cfg.Github.DefaultCommand)
		}
	})

	t.Run("absent github block is fully dormant", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigGH(t, dir, `
domain: example.com
region: us-east-1
`)
		chdirGH(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error when github: absent: %v", err)
		}

		if len(cfg.Github.Commands) != 0 {
			t.Errorf("Github.Commands: got len=%d, want 0 (github key absent => zero value)", len(cfg.Github.Commands))
		}
		if cfg.Github.DefaultCommand != "" {
			t.Errorf("Github.DefaultCommand: got %q, want empty (github key absent => zero value)", cfg.Github.DefaultCommand)
		}
	})
}

// TestGithubCommandEntryFields is a compile-time anchor: it references every field of
// GithubCommandEntry so that the compiler catches any field removal or rename.
func TestGithubCommandEntryFields(t *testing.T) {
	e := config.GithubCommandEntry{
		Description: "d",
		Alias:       "a",
		Profile:     "p",
		Allow:       []string{"github.com"},
		Prompt:      "prompt text",
	}
	if e.Description == "" || e.Alias == "" || e.Profile == "" || len(e.Allow) == 0 || e.Prompt == "" {
		t.Error("GithubCommandEntry fields must be non-zero when set")
	}
}

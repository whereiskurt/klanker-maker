package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestResolveH1EventPrompts verifies the @file convention for HackerOne auto-triage
// event prompts (Phase 103 follow-up). Event prompts travel in KM_H1_PROGRAMS to a
// filesystem-less Lambda, so @file refs MUST be inlined at km init time — exactly
// like command prompts. Mirrors TestResolveCommandPrompts.
func TestResolveH1EventPrompts(t *testing.T) {
	configDir := t.TempDir()

	// Default-home file (bare "@x.txt" → configDir/profiles/x.txt).
	profilesDir := filepath.Join(configDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o750); err != nil {
		t.Fatalf("setup: mkdir profiles: %v", err)
	}
	fileContent := "Triage report #{{report_id}} \"{{title}}\". Internal only."
	if err := os.WriteFile(filepath.Join(profilesDir, "h1.triage.prompt.txt"), []byte(fileContent), 0o600); err != nil {
		t.Fatalf("setup: write prompt file: %v", err)
	}

	t.Run("@file resolved + inline + @@ escape, original not mutated", func(t *testing.T) {
		programs := []config.H1ProgramEntry{
			{
				Handle: "test-prog",
				Events: map[string]config.H1EventEntry{
					"report_created": {Prompt: "@profiles/h1.triage.prompt.txt"},
					"report_reopened": {Prompt: "Re-look at this report inline."},
					"report_needs_more_info": {Prompt: "@@literal-at-prefix"},
				},
			},
		}
		got, err := cmd.ResolveH1EventPrompts(programs, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].Events["report_created"].Prompt != fileContent {
			t.Errorf("report_created: want file content %q, got %q", fileContent, got[0].Events["report_created"].Prompt)
		}
		if got[0].Events["report_reopened"].Prompt != "Re-look at this report inline." {
			t.Errorf("report_reopened: inline prompt should be unchanged, got %q", got[0].Events["report_reopened"].Prompt)
		}
		if got[0].Events["report_needs_more_info"].Prompt != "@literal-at-prefix" {
			t.Errorf("report_needs_more_info: @@ escape should yield single @, got %q", got[0].Events["report_needs_more_info"].Prompt)
		}
		// The input slice's map must NOT have been mutated (copy semantics).
		if programs[0].Events["report_created"].Prompt != "@profiles/h1.triage.prompt.txt" {
			t.Errorf("input mutated: want original @ref, got %q", programs[0].Events["report_created"].Prompt)
		}
	})

	t.Run("missing @file is a hard error", func(t *testing.T) {
		programs := []config.H1ProgramEntry{
			{Handle: "p", Events: map[string]config.H1EventEntry{"report_created": {Prompt: "@nope.txt"}}},
		}
		if _, err := cmd.ResolveH1EventPrompts(programs, configDir); err == nil {
			t.Fatal("expected hard error for missing @file, got nil")
		}
	})

	t.Run("no events is a no-op", func(t *testing.T) {
		programs := []config.H1ProgramEntry{{Handle: "p", Commands: map[string]config.H1CommandEntry{"x": {Prompt: "@profiles/h1.triage.prompt.txt"}}}}
		got, err := cmd.ResolveH1EventPrompts(programs, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Commands are resolved elsewhere (PublishH1CommandsToSSM); this helper leaves them alone.
		if got[0].Commands["x"].Prompt != "@profiles/h1.triage.prompt.txt" {
			t.Errorf("command prompt should be untouched by the event resolver, got %q", got[0].Commands["x"].Prompt)
		}
	})

	// Integration: the exporter must INLINE event @file content into KM_H1_PROGRAMS
	// (the bridge Lambda is filesystem-less). Guards the export wiring, not just the helper.
	t.Run("ExportTerragruntEnvVars inlines event @file into KM_H1_PROGRAMS", func(t *testing.T) {
		t.Setenv("KM_H1_PROGRAMS", "") // env-wins guard: empty so the exporter writes
		os.Unsetenv("KM_H1_PROGRAMS")
		cfg := &config.Config{
			ConfigFilePath: filepath.Join(configDir, "km-config.yaml"),
			H1: config.H1Config{
				BotHandle: "@km",
				Programs: []config.H1ProgramEntry{{
					Handle: "test-prog",
					Events: map[string]config.H1EventEntry{"report_created": {Prompt: "@profiles/h1.triage.prompt.txt"}},
				}},
			},
		}
		cmd.ExportTerragruntEnvVars(cfg)
		got := os.Getenv("KM_H1_PROGRAMS")
		if got == "" {
			t.Fatal("KM_H1_PROGRAMS was not exported")
		}
		if strings.Contains(got, "@profiles/") {
			t.Errorf("KM_H1_PROGRAMS still contains a literal @path (not inlined): %s", got)
		}
		if !strings.Contains(got, "Triage report") {
			t.Errorf("KM_H1_PROGRAMS missing inlined file content; got: %s", got)
		}
	})
}

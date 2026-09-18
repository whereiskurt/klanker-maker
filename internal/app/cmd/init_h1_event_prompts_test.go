package cmd_test

import (
	"encoding/json"
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

	// Prompt files now live in profiles/prompts/ (Phase 120-04 lean-root move).
	// Write to configDir/profiles/prompts/ to match the live km-config path (@profiles/prompts/h1.triage.prompt.txt).
	promptsDir := filepath.Join(configDir, "profiles", "prompts")
	if err := os.MkdirAll(promptsDir, 0o750); err != nil {
		t.Fatalf("setup: mkdir profiles/prompts: %v", err)
	}
	fileContent := "Triage report #{{report_id}} \"{{title}}\". Internal only."
	if err := os.WriteFile(filepath.Join(promptsDir, "h1.triage.prompt.txt"), []byte(fileContent), 0o600); err != nil {
		t.Fatalf("setup: write prompt file: %v", err)
	}

	t.Run("@file resolved + inline + @@ escape, original not mutated", func(t *testing.T) {
		programs := []config.H1ProgramEntry{
			{
				Handle: "test-prog",
				Events: map[string]config.H1EventEntry{
					"report_created": {Prompt: "@profiles/prompts/h1.triage.prompt.txt"},
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
		if programs[0].Events["report_created"].Prompt != "@profiles/prompts/h1.triage.prompt.txt" {
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
		programs := []config.H1ProgramEntry{{Handle: "p", Commands: map[string]config.H1CommandEntry{"x": {Prompt: "@profiles/prompts/h1.triage.prompt.txt"}}}}
		got, err := cmd.ResolveH1EventPrompts(programs, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Commands are resolved elsewhere (PublishH1CommandsToSSM); this helper leaves them alone.
		if got[0].Commands["x"].Prompt != "@profiles/prompts/h1.triage.prompt.txt" {
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
					Events: map[string]config.H1EventEntry{"report_created": {Prompt: "@profiles/prompts/h1.triage.prompt.txt"}},
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

// TestResolveH1EventPrompts_PreservesReply: the reply field must survive @file
// inlining AND appear in the JSON that becomes KM_H1_PROGRAMS — a dropped field
// here would make `reply: none` silently a no-op at the bridge.
func TestResolveH1EventPrompts_PreservesReply(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "p.txt"), []byte("run my skill"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	programs := []config.H1ProgramEntry{{
		Handle: "acme",
		Events: map[string]config.H1EventEntry{
			"report_created": {Prompt: "@p.txt", Reply: "none"},
			"report_triaged": {Prompt: "inline"},
		},
	}}
	got, err := cmd.ResolveH1EventPrompts(programs, configDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].Events["report_created"].Reply != "none" {
		t.Errorf("reply dropped by ResolveH1EventPrompts: %+v", got[0].Events["report_created"])
	}
	if got[0].Events["report_created"].Prompt != "run my skill" {
		t.Errorf("prompt not inlined: %q", got[0].Events["report_created"].Prompt)
	}

	// The JSON form (what KM_H1_PROGRAMS carries) must contain "reply":"none" for
	// report_created and NO reply key for report_triaged (omitempty ⇒ byte-identical
	// for the dormant entry).
	b, _ := json.Marshal(got)
	s := string(b)
	if !strings.Contains(s, `"reply":"none"`) {
		t.Errorf("KM_H1_PROGRAMS JSON missing reply:none: %s", s)
	}
	if strings.Count(s, `"reply"`) != 1 {
		t.Errorf("reply key must be omitted when empty (want exactly 1 occurrence): %s", s)
	}
}

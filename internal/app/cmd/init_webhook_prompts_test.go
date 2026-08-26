package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestResolveWebhookPrompts verifies the @file convention for webhooks.sources[*].rules[*].prompt
// (Phase 127 follow-up). Webhook prompts travel in KM_WEBHOOK_SOURCES to a filesystem-less
// Lambda, so @file refs MUST be inlined at km init time — exactly like command and H1 event
// prompts. Mirrors TestResolveH1EventPrompts / TestResolveCommandPrompts.
func TestResolveWebhookPrompts(t *testing.T) {
	configDir := t.TempDir()

	promptsDir := filepath.Join(configDir, "profiles", "prompts")
	if err := os.MkdirAll(promptsDir, 0o750); err != nil {
		t.Fatalf("setup: mkdir profiles/prompts: %v", err)
	}
	fileContent := "Triage the alert for {{group}}. Internal only."
	if err := os.WriteFile(filepath.Join(promptsDir, "wiz.triage.prompt.txt"), []byte(fileContent), 0o600); err != nil {
		t.Fatalf("setup: write prompt file: %v", err)
	}

	// A second copy directly under configDir/profiles (not profiles/prompts) so
	// the bare-filename fallback ("@x.txt" -> profiles/x.txt) has something to find —
	// the search path is configDir, then configDir/profiles, never a deeper nesting.
	bareContent := "Bare fallback content."
	if err := os.WriteFile(filepath.Join(configDir, "profiles", "bare.prompt.txt"), []byte(bareContent), 0o600); err != nil {
		t.Fatalf("setup: write bare prompt file: %v", err)
	}

	t.Run("@file resolved + inline, @@ escape, plain inline unchanged, input not mutated", func(t *testing.T) {
		sources := []config.WebhookSource{
			{
				Name: "wiz",
				Rules: []config.WebhookRule{
					{Alias: "sec-1", Prompt: "@profiles/prompts/wiz.triage.prompt.txt"},
					{Alias: "sec-2", Prompt: "Look at this inline."},
					{Alias: "sec-3", Prompt: "@@literal-at-prefix"},
				},
			},
		}
		got, err := cmd.ResolveWebhookPrompts(sources, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].Rules[0].Prompt != fileContent {
			t.Errorf("rule[0]: want file content %q, got %q", fileContent, got[0].Rules[0].Prompt)
		}
		if got[0].Rules[1].Prompt != "Look at this inline." {
			t.Errorf("rule[1]: inline prompt should be unchanged, got %q", got[0].Rules[1].Prompt)
		}
		if got[0].Rules[2].Prompt != "@literal-at-prefix" {
			t.Errorf("rule[2]: @@ escape should yield single @, got %q", got[0].Rules[2].Prompt)
		}
		// Input must not be mutated (copy semantics).
		if sources[0].Rules[0].Prompt != "@profiles/prompts/wiz.triage.prompt.txt" {
			t.Errorf("input mutated: want original @ref, got %q", sources[0].Rules[0].Prompt)
		}
	})

	t.Run("bare filename resolves via configDir/profiles fallback", func(t *testing.T) {
		sources := []config.WebhookSource{
			{Name: "wiz", Rules: []config.WebhookRule{{Alias: "sec-1", Prompt: "@bare.prompt.txt"}}},
		}
		got, err := cmd.ResolveWebhookPrompts(sources, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].Rules[0].Prompt != bareContent {
			t.Errorf("want file content %q, got %q", bareContent, got[0].Rules[0].Prompt)
		}
	})

	t.Run("missing @file is a hard error naming source and rule index", func(t *testing.T) {
		sources := []config.WebhookSource{
			{Name: "wiz", Rules: []config.WebhookRule{{Alias: "sec-1", Prompt: "@nope.txt"}}},
		}
		_, err := cmd.ResolveWebhookPrompts(sources, configDir)
		if err == nil {
			t.Fatal("expected hard error for missing @file, got nil")
		}
		if !strings.Contains(err.Error(), "wiz") || !strings.Contains(err.Error(), "rule[0]") {
			t.Errorf("error should name source and rule index, got: %v", err)
		}
	})

	t.Run("multiple sources and multiple rules each resolve independently", func(t *testing.T) {
		sources := []config.WebhookSource{
			{
				Name: "wiz",
				Rules: []config.WebhookRule{
					{Alias: "a", Prompt: "@profiles/prompts/wiz.triage.prompt.txt"},
					{Alias: "b", Prompt: "inline b"},
				},
			},
			{
				Name: "other",
				Rules: []config.WebhookRule{
					{Alias: "c", Prompt: "@@escaped"},
				},
			},
		}
		got, err := cmd.ResolveWebhookPrompts(sources, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].Rules[0].Prompt != fileContent {
			t.Errorf("source 0 rule 0: want file content, got %q", got[0].Rules[0].Prompt)
		}
		if got[0].Rules[1].Prompt != "inline b" {
			t.Errorf("source 0 rule 1: want unchanged, got %q", got[0].Rules[1].Prompt)
		}
		if got[1].Rules[0].Prompt != "@escaped" {
			t.Errorf("source 1 rule 0: want @escaped, got %q", got[1].Rules[0].Prompt)
		}
	})

	t.Run("empty sources is a no-op", func(t *testing.T) {
		got, err := cmd.ResolveWebhookPrompts(nil, configDir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("want empty result, got %v", got)
		}
	})

	// Integration: the exporter must INLINE @file content into KM_WEBHOOK_SOURCES
	// (the bridge Lambda is filesystem-less). Guards the export wiring, not just the helper.
	t.Run("ExportTerragruntEnvVars inlines rule @file into KM_WEBHOOK_SOURCES", func(t *testing.T) {
		t.Setenv("KM_WEBHOOK_SOURCES", "")
		os.Unsetenv("KM_WEBHOOK_SOURCES")
		cfg := &config.Config{
			ConfigFilePath: filepath.Join(configDir, "km-config.yaml"),
			Webhooks: config.WebhooksConfig{
				Sources: []config.WebhookSource{
					{
						Name: "wiz",
						Auth: config.WebhookAuth{Type: "bearer", SecretPath: "/km/webhooks/wiz"},
						Rules: []config.WebhookRule{
							{Alias: "sec-1", Prompt: "@profiles/prompts/wiz.triage.prompt.txt"},
						},
					},
				},
			},
		}
		cmd.ExportTerragruntEnvVars(cfg)
		got := os.Getenv("KM_WEBHOOK_SOURCES")
		if got == "" {
			t.Fatal("KM_WEBHOOK_SOURCES was not exported")
		}
		if strings.Contains(got, "@profiles/") {
			t.Errorf("KM_WEBHOOK_SOURCES still contains a literal @path (not inlined): %s", got)
		}
		if !strings.Contains(got, "Triage the alert") {
			t.Errorf("KM_WEBHOOK_SOURCES missing inlined file content; got: %s", got)
		}
	})
}

// Package config_test provides HackerOne (h1) config round-trip tests.
// Phase 103 Plan 02 Task 1: H1Config struct + merge-list registration + UnmarshalKey load.
//
// These mirror config_github_test.go — the github: block is the structural
// template for the h1: block (RESEARCH Pattern 3). The merge-list regression
// test is the load-bearing one: without "h1" in the v2→v merge-loop the whole
// h1: block is silently dropped (project_config_key_merge_list footgun).
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// writeKMConfigH1 writes a km-config.yaml to dir (self-contained mirror of
// writeKMConfigGH so this file reads independently).
func writeKMConfigH1(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}
}

func chdirH1(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// TestLoadH1_Set verifies a full h1: block round-trips through config.Load():
// programs, targets (multi-target fanout), allow, events map, and commands map
// are all preserved. This also catches the merge-list footgun: if "h1" is missing
// from the v2→v merge-loop in config.go, the entire block is silently dropped and
// cfg.H1 stays zero even when the yaml contains it.
func TestLoadH1_Set(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigH1(t, dir, `
domain: example.com
region: us-east-1
h1:
  bot_handle: "@km"
  default_profile: h1-triage
  programs:
    - handle: acme-corp
      targets:
        - {alias: h1-acme-triage, profile: h1-triage}
        - {alias: h1-acme-dupe-check, profile: h1-triage}
      allow:
        - alice
        - bob
      bot_handle: "@acmebot"
      events:
        report_created: {prompt: "Triage new report {{report_id}}: {{title}}."}
      commands:
        dupe: {description: "Check for duplicates", prompt: "Search prior reports for duplicates of {{args}}"}
      default_command: ""
`)
	chdirH1(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// Install-wide scalars.
	if cfg.H1.BotHandle != "@km" {
		t.Errorf("H1.BotHandle: got %q, want %q", cfg.H1.BotHandle, "@km")
	}
	if cfg.H1.DefaultProfile != "h1-triage" {
		t.Errorf("H1.DefaultProfile: got %q, want %q", cfg.H1.DefaultProfile, "h1-triage")
	}

	// One program entry.
	if len(cfg.H1.Programs) != 1 {
		t.Fatalf("H1.Programs: got len=%d, want 1; values=%+v", len(cfg.H1.Programs), cfg.H1.Programs)
	}
	p := cfg.H1.Programs[0]
	if p.Handle != "acme-corp" {
		t.Errorf("Programs[0].Handle: got %q, want %q", p.Handle, "acme-corp")
	}

	// Multi-target fanout.
	if len(p.Targets) != 2 {
		t.Fatalf("Programs[0].Targets: got len=%d, want 2; values=%+v", len(p.Targets), p.Targets)
	}
	if p.Targets[0].Alias != "h1-acme-triage" || p.Targets[0].Profile != "h1-triage" {
		t.Errorf("Targets[0]: got %+v, want {h1-acme-triage h1-triage}", p.Targets[0])
	}
	if p.Targets[1].Alias != "h1-acme-dupe-check" || p.Targets[1].Profile != "h1-triage" {
		t.Errorf("Targets[1]: got %+v, want {h1-acme-dupe-check h1-triage}", p.Targets[1])
	}

	// Allow list (deny-by-default key).
	if len(p.Allow) != 2 || p.Allow[0] != "alice" || p.Allow[1] != "bob" {
		t.Errorf("Programs[0].Allow: got %v, want [alice bob]", p.Allow)
	}

	// Per-program bot_handle override.
	if p.BotHandle != "@acmebot" {
		t.Errorf("Programs[0].BotHandle: got %q, want %q (per-program override)", p.BotHandle, "@acmebot")
	}

	// Events map (auto-triage).
	ev, ok := p.Events["report_created"]
	if !ok {
		t.Fatalf("Programs[0].Events missing report_created; got %+v", p.Events)
	}
	if ev.Prompt != "Triage new report {{report_id}}: {{title}}." {
		t.Errorf("Events[report_created].Prompt: got %q", ev.Prompt)
	}

	// Commands map (comment-context).
	cmd, ok := p.Commands["dupe"]
	if !ok {
		t.Fatalf("Programs[0].Commands missing dupe; got %+v", p.Commands)
	}
	if cmd.Description != "Check for duplicates" {
		t.Errorf("Commands[dupe].Description: got %q", cmd.Description)
	}
	if cmd.Prompt != "Search prior reports for duplicates of {{args}}" {
		t.Errorf("Commands[dupe].Prompt: got %q", cmd.Prompt)
	}
}

// TestLoadH1_Absent verifies an absent h1: block yields a zero-value H1Config
// with no panic and no error. This is the dormant byte-identity invariant.
func TestLoadH1_Absent(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigH1(t, dir, `
domain: example.com
region: us-east-1
`)
	chdirH1(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error when h1: absent: %v", err)
	}

	if len(cfg.H1.Programs) != 0 {
		t.Errorf("H1.Programs: got len=%d, want 0 (key absent => zero value)", len(cfg.H1.Programs))
	}
	if cfg.H1.BotHandle != "" {
		t.Errorf("H1.BotHandle: got %q, want empty (key absent => zero value)", cfg.H1.BotHandle)
	}
	if cfg.H1.DefaultProfile != "" {
		t.Errorf("H1.DefaultProfile: got %q, want empty (key absent => zero value)", cfg.H1.DefaultProfile)
	}

	// Getters must tolerate the dormant case without panic.
	if got := cfg.GetH1BotHandle(); got != "" {
		t.Errorf("GetH1BotHandle(): got %q, want empty", got)
	}
	if got := cfg.GetH1Programs(); len(got) != 0 {
		t.Errorf("GetH1Programs(): got len=%d, want 0", len(got))
	}
}

// TestLoadH1_MergeListRegression proves the merge-list entry for "h1" is present.
// Mirrors TestLoadGitHubRepos_MergeListRegression — if "h1" is missing from the
// merge-loop allowlist, cfg.H1.Programs stays nil regardless of yaml content.
//
// The assertion is on len==1 specifically: if the merge-list is broken, len==0.
func TestLoadH1_MergeListRegression(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigH1(t, dir, `
domain: example.com
region: us-east-1
h1:
  programs:
    - handle: sentinel-program
      targets:
        - {alias: h1-sentinel, profile: h1-triage}
`)
	chdirH1(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.H1.Programs == nil {
		t.Fatal("H1.Programs is nil; expected non-nil from yaml load (merge-loop must include \"h1\")")
	}
	if len(cfg.H1.Programs) != 1 {
		t.Errorf("H1.Programs: got len=%d, want 1; merge-list footgun check (project_config_key_merge_list)", len(cfg.H1.Programs))
	}
	if len(cfg.H1.Programs) > 0 && cfg.H1.Programs[0].Handle != "sentinel-program" {
		t.Errorf("H1.Programs[0].Handle: got %q, want %q", cfg.H1.Programs[0].Handle, "sentinel-program")
	}

	// ── Phase 103 Plan 10 deploy-surface MERGE-LIST GUARD ───────────────────────
	// This is the unmistakable config half of H1-DEPLOY-WIRING (the other half is
	// the lambdaBuilds/regionalModules guards in internal/app/cmd/init_test.go).
	// The whole h1: block — including the sentinel target inside the program — only
	// survives config.Load() when "h1" is in the v2→v merge-loop allowlist. If a
	// future refactor drops that entry, the program loads but its nested fields are
	// silently empty; assert the target round-tripped too so the guard cannot be
	// satisfied by a half-merged struct.
	if len(cfg.H1.Programs) > 0 {
		if len(cfg.H1.Programs[0].Targets) != 1 {
			t.Errorf("MERGE-LIST GUARD: H1.Programs[0].Targets len=%d, want 1 — nested h1: fields dropped (merge-list entry missing/broken)", len(cfg.H1.Programs[0].Targets))
		} else if cfg.H1.Programs[0].Targets[0].Alias != "h1-sentinel" {
			t.Errorf("MERGE-LIST GUARD: H1.Programs[0].Targets[0].Alias=%q, want %q — nested h1: target field not merged", cfg.H1.Programs[0].Targets[0].Alias, "h1-sentinel")
		}
	}
}

// TestH1BotHandleOverride verifies per-program bot_handle overrides the install-wide
// bot_handle, and absence falls back to the install default. The getter
// GetH1ProgramBotHandle(handle) encodes this precedence so callers (Plan 06/07) don't
// re-derive it.
func TestH1BotHandleOverride(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigH1(t, dir, `
domain: example.com
region: us-east-1
h1:
  bot_handle: "@km"
  programs:
    - handle: with-override
      bot_handle: "@special"
      targets:
        - {alias: h1-with-override, profile: h1-triage}
    - handle: without-override
      targets:
        - {alias: h1-without-override, profile: h1-triage}
`)
	chdirH1(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if got := cfg.GetH1ProgramBotHandle("with-override"); got != "@special" {
		t.Errorf("GetH1ProgramBotHandle(with-override): got %q, want %q (per-program override)", got, "@special")
	}
	if got := cfg.GetH1ProgramBotHandle("without-override"); got != "@km" {
		t.Errorf("GetH1ProgramBotHandle(without-override): got %q, want %q (install default)", got, "@km")
	}
	// Unknown program falls back to the install default too.
	if got := cfg.GetH1ProgramBotHandle("unknown"); got != "@km" {
		t.Errorf("GetH1ProgramBotHandle(unknown): got %q, want %q (install default)", got, "@km")
	}
}

// TestH1Getters verifies the parsed-value getters return the loaded config.
func TestH1Getters(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigH1(t, dir, `
domain: example.com
region: us-east-1
h1:
  bot_handle: "@km"
  default_profile: h1-triage
  programs:
    - handle: acme-corp
      targets:
        - {alias: h1-acme, profile: h1-triage}
`)
	chdirH1(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if got := cfg.GetH1BotHandle(); got != "@km" {
		t.Errorf("GetH1BotHandle(): got %q, want %q", got, "@km")
	}
	if got := cfg.GetH1DefaultProfile(); got != "h1-triage" {
		t.Errorf("GetH1DefaultProfile(): got %q, want %q", got, "h1-triage")
	}
	progs := cfg.GetH1Programs()
	if len(progs) != 1 || progs[0].Handle != "acme-corp" {
		t.Errorf("GetH1Programs(): got %+v, want one program acme-corp", progs)
	}
}

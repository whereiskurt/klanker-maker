// Package config_test provides checks config round-trip tests.
// Phase 116 Plan 03 Task 3 (TDD): ChecksConfig struct + merge-list registration + UnmarshalKey load.
//
// These mirror config_h1_test.go — the h1: block is the structural template for
// the checks: block. The merge-list regression test is the load-bearing one:
// without "checks" in the v2→v merge-loop the whole checks: block is silently
// dropped (project_config_key_merge_list footgun).
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// writeKMConfigChecks writes a km-config.yaml to dir. Self-contained helper
// mirroring writeKMConfigH1 so this file reads independently.
func writeKMConfigChecks(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "km-config.yaml"), []byte(content), 0600); err != nil {
		t.Fatalf("write km-config.yaml: %v", err)
	}
}

// chdirChecks changes the working directory for the duration of the test.
func chdirChecks(t *testing.T, dir string) {
	t.Helper()
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) }) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// TestChecksConfigMerge is the primary gate test for Phase 116 Plan 03.
// It verifies two invariants:
//
//  1. Populated case: a yaml checks.triggers list populates cfg.Checks.Triggers
//     after config.Load() with the exact field values (check, when_py, alias,
//     prompt, onAbsent, cooldownSeconds).
//
//  2. Dormant case: an absent checks: block produces an empty cfg.Checks.Triggers
//     slice with no error — the "dormant by default" invariant.
//
// The populated case also serves as the merge-list regression guard: if "checks"
// is removed from the v2→v merge-loop in config.go, cfg.Checks.Triggers stays
// nil/empty even when the yaml contains it (project_config_key_merge_list footgun).
func TestChecksConfigMerge(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigChecks(t, dir, `
domain: example.com
region: us-east-1
checks:
  triggers:
    - check: wiz-threat-intel
      when_py: |
        return out.get("affected_count", 0) > 5
      alias: security-auditor
      prompt: "Wiz detected {{out.affected_count}} affected systems. Reason: {{reason}}"
      onAbsent: cold-create
      cooldownSeconds: 3600
`)
		chdirChecks(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		// Assert the trigger list was populated (the merge-list guard).
		if len(cfg.Checks.Triggers) != 1 {
			t.Fatalf("Checks.Triggers: got len=%d, want 1; merge-list footgun check (project_config_key_merge_list) — ensure \"checks\" is in the v2→v merge-loop", len(cfg.Checks.Triggers))
		}

		tr := cfg.Checks.Triggers[0]

		if tr.Check != "wiz-threat-intel" {
			t.Errorf("Triggers[0].Check: got %q, want %q", tr.Check, "wiz-threat-intel")
		}

		wantWhenPy := "return out.get(\"affected_count\", 0) > 5\n"
		if tr.WhenPy != wantWhenPy {
			t.Errorf("Triggers[0].WhenPy: got %q, want %q", tr.WhenPy, wantWhenPy)
		}

		if tr.Alias != "security-auditor" {
			t.Errorf("Triggers[0].Alias: got %q, want %q", tr.Alias, "security-auditor")
		}

		wantPrompt := "Wiz detected {{out.affected_count}} affected systems. Reason: {{reason}}"
		if tr.Prompt != wantPrompt {
			t.Errorf("Triggers[0].Prompt: got %q, want %q", tr.Prompt, wantPrompt)
		}

		if tr.OnAbsent != "cold-create" {
			t.Errorf("Triggers[0].OnAbsent: got %q, want %q", tr.OnAbsent, "cold-create")
		}

		if tr.CooldownSeconds != 3600 {
			t.Errorf("Triggers[0].CooldownSeconds: got %d, want 3600", tr.CooldownSeconds)
		}
	})

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigChecks(t, dir, `
domain: example.com
region: us-east-1
`)
		chdirChecks(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error when checks: absent: %v", err)
		}

		// Dormant by default: absent checks: block → zero value → no triggers.
		if len(cfg.Checks.Triggers) != 0 {
			t.Errorf("Checks.Triggers: got len=%d, want 0 (key absent => dormant)", len(cfg.Checks.Triggers))
		}
	})
}

// TestChecksConfigMerge_MergeListRegression is a focused guard that the "checks"
// entry exists in the v2→v merge-list. If the entry is removed, cfg.Checks.Triggers
// will be nil (len==0) even when the yaml contains it, causing this test to fail.
// Mirrors TestLoadH1_MergeListRegression in config_h1_test.go.
func TestChecksConfigMerge_MergeListRegression(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigChecks(t, dir, `
domain: example.com
region: us-east-1
checks:
  triggers:
    - check: sentinel-check
      alias: sentinel-alias
`)
	chdirChecks(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Checks.Triggers == nil {
		t.Fatal("Checks.Triggers is nil; expected non-nil from yaml load (merge-loop must include \"checks\")")
	}
	if len(cfg.Checks.Triggers) != 1 {
		t.Errorf("Checks.Triggers: got len=%d, want 1; merge-list footgun check (project_config_key_merge_list)", len(cfg.Checks.Triggers))
	}
	// Assert nested field round-tripped too (not just len).
	if len(cfg.Checks.Triggers) > 0 && cfg.Checks.Triggers[0].Check != "sentinel-check" {
		t.Errorf("MERGE-LIST GUARD: Checks.Triggers[0].Check=%q, want %q — nested checks: field not merged", cfg.Checks.Triggers[0].Check, "sentinel-check")
	}
	if len(cfg.Checks.Triggers) > 0 && cfg.Checks.Triggers[0].Alias != "sentinel-alias" {
		t.Errorf("MERGE-LIST GUARD: Checks.Triggers[0].Alias=%q, want %q — nested checks: field not merged", cfg.Checks.Triggers[0].Alias, "sentinel-alias")
	}
}

// TestChecksGetters verifies that GetChecksConfig and GetChecksTriggers return
// the loaded config values (and tolerate the dormant zero-value case without panic).
func TestChecksGetters(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigChecks(t, dir, `
domain: example.com
region: us-east-1
checks:
  triggers:
    - check: qotd
      alias: qotd-reporter
      cooldownSeconds: 86400
`)
		chdirChecks(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		triggers := cfg.GetChecksTriggers()
		if len(triggers) != 1 {
			t.Fatalf("GetChecksTriggers(): got len=%d, want 1", len(triggers))
		}
		if triggers[0].Check != "qotd" {
			t.Errorf("GetChecksTriggers()[0].Check: got %q, want %q", triggers[0].Check, "qotd")
		}
		if triggers[0].Alias != "qotd-reporter" {
			t.Errorf("GetChecksTriggers()[0].Alias: got %q, want %q", triggers[0].Alias, "qotd-reporter")
		}
		if triggers[0].CooldownSeconds != 86400 {
			t.Errorf("GetChecksTriggers()[0].CooldownSeconds: got %d, want 86400", triggers[0].CooldownSeconds)
		}
	})

	t.Run("absent", func(t *testing.T) {
		dir := t.TempDir()
		writeKMConfigChecks(t, dir, `
domain: example.com
region: us-east-1
`)
		chdirChecks(t, dir)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}

		// Getters must tolerate the dormant case without panic.
		if got := cfg.GetChecksConfig(); len(got.Triggers) != 0 {
			t.Errorf("GetChecksConfig().Triggers: got len=%d, want 0", len(got.Triggers))
		}
		if got := cfg.GetChecksTriggers(); len(got) != 0 {
			t.Errorf("GetChecksTriggers(): got len=%d, want 0", len(got))
		}
	})
}

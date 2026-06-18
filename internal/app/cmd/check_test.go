package cmd_test

// check_test.go — Phase 116 Plan 05: unit tests for the km check trigger bake
// and sourceHash. These test pkg/check.BakeTrigger directly (not the CLI).
//
// Validation command (from plan):
//   go test ./internal/app/cmd/ -run 'TestCheckTriggerBakeInline|TestCheckTriggerBakeAtFile|TestCheckSourceHash' -count=1 -timeout 120s

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	"github.com/whereiskurt/klanker-maker/pkg/check"
)

// TestCheckTriggerBakeInline verifies that an inline when_py + inline prompt
// bakes correctly to a KM_CHECK_TRIGGER JSON matching the 116-04 schema.
func TestCheckTriggerBakeInline(t *testing.T) {
	trigger := config.CheckTrigger{
		Check:           "my-check",
		WhenPy:          "return len(out.get('items', [])) > 0",
		Alias:           "nightly-auditor",
		Prompt:          "Found {{out.count}} items. Reason: {{reason}}",
		OnAbsent:        "cold-create",
		CooldownSeconds: 3600,
	}

	jsonBytes, sourceHash, err := check.BakeTrigger(trigger)
	if err != nil {
		t.Fatalf("BakeTrigger failed: %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Fatal("BakeTrigger returned empty JSON")
	}
	if sourceHash == "" {
		t.Fatal("BakeTrigger returned empty sourceHash")
	}

	// Unmarshal and validate schema fields (snake_case per 116-04).
	var m map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &m); err != nil {
		t.Fatalf("BakeTrigger JSON is not valid: %v", err)
	}

	assertField := func(key, want string) {
		t.Helper()
		got, ok := m[key].(string)
		if !ok {
			t.Errorf("field %q missing or not a string (got %T=%v)", key, m[key], m[key])
			return
		}
		if got != want {
			t.Errorf("field %q = %q, want %q", key, got, want)
		}
	}

	assertField("when_py", "return len(out.get('items', [])) > 0")
	assertField("alias", "nightly-auditor")
	assertField("prompt", "Found {{out.count}} items. Reason: {{reason}}")
	assertField("on_absent", "cold-create")

	// cooldown_seconds should be a number
	cs, ok := m["cooldown_seconds"]
	if !ok {
		t.Error("field cooldown_seconds missing")
	} else {
		// JSON numbers unmarshal to float64
		if csf, ok2 := cs.(float64); !ok2 || int(csf) != 3600 {
			t.Errorf("cooldown_seconds = %v, want 3600", cs)
		}
	}
}

// TestCheckTriggerBakeAtFile verifies that @file references in when_py and
// prompt are resolved by reading those files at bake time; the baked JSON
// contains the file CONTENTS, not the @ref path.
func TestCheckTriggerBakeAtFile(t *testing.T) {
	// Create temp files for when_py and prompt.
	predFile, err := os.CreateTemp(t.TempDir(), "pred*.py")
	if err != nil {
		t.Fatalf("create pred file: %v", err)
	}
	predContent := "crit = [i for i in out.get('items', []) if i.get('sev') == 'crit']\nreturn (len(crit) >= 3, f\"{len(crit)} critical findings\")"
	if _, err := predFile.WriteString(predContent); err != nil {
		t.Fatalf("write pred file: %v", err)
	}
	predFile.Close()

	promptFile, err := os.CreateTemp(t.TempDir(), "prompt*.txt")
	if err != nil {
		t.Fatalf("create prompt file: %v", err)
	}
	promptContent := "Triage these {{out.count}} critical findings. Reason: {{reason}}"
	if _, err := promptFile.WriteString(promptContent); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}
	promptFile.Close()

	trigger := config.CheckTrigger{
		Check:    "file-check",
		WhenPy:   "@" + predFile.Name(),
		Alias:    "file-alias",
		Prompt:   "@" + promptFile.Name(),
		OnAbsent: "skip",
	}

	jsonBytes, sourceHash, err := check.BakeTrigger(trigger)
	if err != nil {
		t.Fatalf("BakeTrigger(@file) failed: %v", err)
	}
	if sourceHash == "" {
		t.Fatal("BakeTrigger(@file) returned empty sourceHash")
	}

	var m map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &m); err != nil {
		t.Fatalf("BakeTrigger(@file) JSON invalid: %v", err)
	}

	// when_py must contain the file CONTENTS, not the @ref.
	whenPy, ok := m["when_py"].(string)
	if !ok {
		t.Fatal("when_py field missing or not a string")
	}
	if whenPy == "@"+predFile.Name() {
		t.Error("when_py still contains the @ref — @file was not resolved")
	}
	if whenPy != predContent {
		t.Errorf("when_py = %q, want %q", whenPy, predContent)
	}

	// prompt must contain the file CONTENTS, not the @ref.
	prompt, ok := m["prompt"].(string)
	if !ok {
		t.Fatal("prompt field missing or not a string")
	}
	if prompt == "@"+promptFile.Name() {
		t.Error("prompt still contains the @ref — @file was not resolved")
	}
	if prompt != promptContent {
		t.Errorf("prompt = %q, want %q", prompt, promptContent)
	}

	// on_absent defaults preserved
	if onAbsent, _ := m["on_absent"].(string); onAbsent != "skip" {
		t.Errorf("on_absent = %q, want %q", onAbsent, "skip")
	}
}

// TestCheckSourceHash verifies that:
//   - identical resolved content → identical hash
//   - changing the predicate → different hash
func TestCheckSourceHash(t *testing.T) {
	base := config.CheckTrigger{
		Check:    "stable-check",
		WhenPy:   "return True",
		Alias:    "alias1",
		Prompt:   "do something",
		OnAbsent: "cold-create",
	}

	_, hash1, err := check.BakeTrigger(base)
	if err != nil {
		t.Fatalf("BakeTrigger (1st): %v", err)
	}
	_, hash2, err := check.BakeTrigger(base)
	if err != nil {
		t.Fatalf("BakeTrigger (2nd): %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("identical input → different hashes: %q vs %q", hash1, hash2)
	}

	// Mutate the predicate.
	mutated := base
	mutated.WhenPy = "return False"
	_, hash3, err := check.BakeTrigger(mutated)
	if err != nil {
		t.Fatalf("BakeTrigger (mutated): %v", err)
	}
	if hash1 == hash3 {
		t.Error("different predicates → same hash (expected different)")
	}
}

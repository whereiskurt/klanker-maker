package profile_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
	"gopkg.in/yaml.v3"
)

// TestCLISpec_NotifySlackInboundMentionOnly verifies the tri-state *bool contract for POL-01.
// Covers: nil when omitted, &true when explicit true, &false when explicit false, and JSON round-trip.
func TestCLISpec_NotifySlackInboundMentionOnly(t *testing.T) {
	t.Run("omitted-yaml", func(t *testing.T) {
		raw := []byte("apiVersion: v1\nkind: SandboxProfile\nmetadata:\n  name: t\nspec:\n  cli: {}\n")
		var p profile.SandboxProfile
		if err := yaml.Unmarshal(raw, &p); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if p.Spec.CLI == nil {
			// nil CLI also means nil field — acceptable
			return
		}
		if p.Spec.CLI.NotifySlackInboundMentionOnly != nil {
			t.Fatalf("expected nil for omitted field, got %v", *p.Spec.CLI.NotifySlackInboundMentionOnly)
		}
	})

	t.Run("explicit-true", func(t *testing.T) {
		raw := []byte("apiVersion: v1\nkind: SandboxProfile\nmetadata:\n  name: t\nspec:\n  cli:\n    notifySlackInboundMentionOnly: true\n")
		var p profile.SandboxProfile
		if err := yaml.Unmarshal(raw, &p); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if p.Spec.CLI == nil || p.Spec.CLI.NotifySlackInboundMentionOnly == nil {
			t.Fatal("expected non-nil pointer for explicit true")
		}
		if !*p.Spec.CLI.NotifySlackInboundMentionOnly {
			t.Fatalf("expected *true, got *false")
		}
	})

	t.Run("explicit-false", func(t *testing.T) {
		raw := []byte("apiVersion: v1\nkind: SandboxProfile\nmetadata:\n  name: t\nspec:\n  cli:\n    notifySlackInboundMentionOnly: false\n")
		var p profile.SandboxProfile
		if err := yaml.Unmarshal(raw, &p); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if p.Spec.CLI == nil || p.Spec.CLI.NotifySlackInboundMentionOnly == nil {
			t.Fatal("expected non-nil pointer for explicit false — must be distinguishable from omitted (nil)")
		}
		if *p.Spec.CLI.NotifySlackInboundMentionOnly {
			t.Fatalf("expected *false, got *true")
		}
	})

	t.Run("json-roundtrip", func(t *testing.T) {
		falseVal := false
		src := profile.CLISpec{
			NotifySlackInboundMentionOnly: &falseVal,
		}
		data, err := json.Marshal(src)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}
		// Confirm the JSON key is present (not omitted)
		if !strings.Contains(string(data), "notifySlackInboundMentionOnly") {
			t.Fatalf("expected JSON to contain notifySlackInboundMentionOnly, got: %s", data)
		}
		var dst profile.CLISpec
		if err := json.Unmarshal(data, &dst); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if dst.NotifySlackInboundMentionOnly == nil {
			t.Fatal("expected non-nil after JSON round-trip with &false — field not tagged with omitempty should preserve false")
		}
		if *dst.NotifySlackInboundMentionOnly {
			t.Fatalf("expected *false after round-trip, got *true")
		}
	})
}

// minimalProfileYAML returns a minimal valid SandboxProfile YAML for schema tests,
// injecting any extra CLI fields supplied.
func minimalProfileYAML(extraCLI string) []byte {
	base := `apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: test
spec:
  runtime:
    instanceType: t3.small
  network:
    allowedDomains:
      - ".example.com"
`
	if extraCLI != "" {
		base += "  cli:\n"
		for _, line := range strings.Split(extraCLI, "\n") {
			if line != "" {
				base += "    " + line + "\n"
			}
		}
	}
	return []byte(base)
}

// TestSchema_NotifySlackInboundMentionOnly verifies the JSON Schema contract for POL-02.
// Covers: true accepted, false accepted, string "yes" rejected, omitted accepted.
func TestSchema_NotifySlackInboundMentionOnly(t *testing.T) {
	t.Run("true-accepted", func(t *testing.T) {
		raw := minimalProfileYAML("notifySlackInboundMentionOnly: true")
		errs := profile.ValidateSchema(raw)
		for _, e := range errs {
			if strings.Contains(e.Message, "notifySlackInboundMentionOnly") {
				t.Fatalf("expected no schema error for bool true, got: %v", e.Message)
			}
		}
	})

	t.Run("false-accepted", func(t *testing.T) {
		raw := minimalProfileYAML("notifySlackInboundMentionOnly: false")
		errs := profile.ValidateSchema(raw)
		for _, e := range errs {
			if strings.Contains(e.Message, "notifySlackInboundMentionOnly") {
				t.Fatalf("expected no schema error for bool false, got: %v", e.Message)
			}
		}
	})

	t.Run("string-rejected", func(t *testing.T) {
		raw := minimalProfileYAML(`notifySlackInboundMentionOnly: "yes"`)
		errs := profile.ValidateSchema(raw)
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "notifySlackInboundMentionOnly") || strings.Contains(e.Path, "notifySlackInboundMentionOnly") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected schema error for string value, got: %+v", errs)
		}
	})

	t.Run("omitted-accepted", func(t *testing.T) {
		raw := minimalProfileYAML("")
		errs := profile.ValidateSchema(raw)
		for _, e := range errs {
			if strings.Contains(e.Message, "notifySlackInboundMentionOnly") {
				t.Fatalf("expected no schema error when field is omitted, got: %v", e.Message)
			}
		}
	})
}

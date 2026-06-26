package cmd

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
	yaml "gopkg.in/yaml.v3"
)

// TestSelectRemoteProfileYAML_FlattensExtends is the Phase 120 regression guard for the
// remote-create bug: an extends-based profile uploaded to the create-handler Lambda MUST be
// flattened (extends cleared, bases merged in), because the Lambda has no profiles/base/**
// fragments and cannot resolve `extends:`. Uploading the raw child failed the subprocess
// with `profile "base/os/redhat" not found`.
func TestSelectRemoteProfileYAML_FlattensExtends(t *testing.T) {
	// A real composed leaf that extends a multi-parent stack lives in testdata/profiles.
	// Resolve it the same way runCreateRemote does.
	resolved, err := profile.Resolve("multi-parent-child", []string{"../../../testdata/profiles", "../../testdata/profiles"})
	if err != nil {
		t.Fatalf("Resolve(multi-parent-child): %v", err)
	}
	if resolved.Extends.IsSet() {
		t.Fatalf("precondition: resolved profile should have extends cleared, got %v", resolved.Extends)
	}

	// raw still carries extends — simulate the child-on-disk bytes.
	raw := []byte("apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata:\n  name: multi-parent-child\nextends:\n  - base-a\n  - base-b\n")

	out, err := selectRemoteProfileYAML(true /*extendsSet*/, resolved, raw, false, "", "")
	if err != nil {
		t.Fatalf("selectRemoteProfileYAML returned error: %v", err)
	}

	// The uploaded YAML must NOT carry an extends: key — the Lambda would choke on it.
	if strings.Contains(out, "extends:") {
		t.Errorf("uploaded profile YAML still contains extends: — Lambda cannot resolve it\n%s", out)
	}

	// And it must be a parseable, self-contained profile carrying the merged content.
	var rt profile.SandboxProfile
	if err := yaml.Unmarshal([]byte(out), &rt); err != nil {
		t.Fatalf("uploaded YAML is not valid SandboxProfile YAML: %v", err)
	}
	if rt.Extends.IsSet() {
		t.Errorf("round-tripped uploaded profile still reports extends set")
	}
	// Merged scalar from the resolve (child TTL wins per TestResolve_MultiParentOrder).
	if rt.Spec.Lifecycle.TTL == "" {
		t.Errorf("merged profile lost spec.lifecycle.ttl — flatten dropped base content")
	}
}

// TestSelectRemoteProfileYAML_MonolithicUsesRaw confirms the non-extends, override-free path
// still uploads the raw bytes verbatim (preserves comments/formatting; no behavior change for
// the legacy monolithic profiles).
func TestSelectRemoteProfileYAML_MonolithicUsesRaw(t *testing.T) {
	raw := []byte("apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\n# a comment that must survive\nmetadata:\n  name: mono\n")
	out, err := selectRemoteProfileYAML(false /*extendsSet*/, &profile.SandboxProfile{}, raw, false, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != string(raw) {
		t.Errorf("monolithic override-free profile should upload raw bytes verbatim\nwant:\n%s\ngot:\n%s", raw, out)
	}
}

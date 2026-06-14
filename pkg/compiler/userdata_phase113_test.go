package compiler

import (
	"strings"
	"testing"

	yaml "github.com/goccy/go-yaml"
	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// phase113Profile returns a minimal SandboxProfile with APIVersion and Kind set,
// so the on-box YAML write-block embeds recognizable profile metadata.
func phase113Profile() *profile.SandboxProfile {
	p := baseProfile()
	p.APIVersion = "klankermaker.ai/v1alpha2"
	p.Kind = "SandboxProfile"
	p.Metadata.Name = "test-phase113"
	return p
}

// TestUserdataProfileWriteBlockRendered verifies that the section 2.10
// profile-write block is present in the rendered userdata output (Phase 113).
func TestUserdataProfileWriteBlockRendered(t *testing.T) {
	p := phase113Profile()
	got, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	checks := []struct {
		needle string
		desc   string
	}{
		{"/opt/km/.km-profile.yaml", "profile file path"},
		{"KM_PROFILE_EOF", "heredoc sentinel"},
		{"chown sandbox:sandbox /opt/km/.km-profile.yaml", "chown command"},
		{"klankermaker.ai/v1alpha2", "apiVersion embedded in YAML body"},
	}
	for _, c := range checks {
		if !strings.Contains(got, c.needle) {
			t.Errorf("rendered userdata missing %s: expected to find %q", c.desc, c.needle)
		}
	}
}

// TestUserdataProfileYAMLRoundTrip extracts the YAML embedded between the
// KM_PROFILE_EOF sentinel lines and verifies it parses back to a SandboxProfile
// with matching APIVersion, Kind, and Metadata.Name (Phase 113).
func TestUserdataProfileYAMLRoundTrip(t *testing.T) {
	p := phase113Profile()
	got, err := generateUserData(p, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	// The section 2.10 block is:
	//   cat > /opt/km/.km-profile.yaml << 'KM_PROFILE_EOF'
	//   <YAML body>
	//   KM_PROFILE_EOF
	// The heredoc opening is << 'KM_PROFILE_EOF' (note the surrounding single-quotes),
	// so the first occurrence of the literal KM_PROFILE_EOF is embedded in that line.
	// The YAML body follows the closing newline of that open-line.
	// The second standalone KM_PROFILE_EOF line closes the heredoc.
	// Strategy: find the first newline after KM_PROFILE_EOF (end of heredoc-open line),
	// then find the next KM_PROFILE_EOF (closing sentinel) — the body is between them.
	const sentinel = "KM_PROFILE_EOF"
	firstIdx := strings.Index(got, sentinel)
	if firstIdx < 0 {
		t.Fatalf("KM_PROFILE_EOF sentinel not found in userdata output")
	}
	// Advance past the first sentinel and the rest of that line (including trailing newline).
	afterFirst := got[firstIdx+len(sentinel):]
	nlIdx := strings.Index(afterFirst, "\n")
	if nlIdx < 0 {
		t.Fatalf("no newline after first KM_PROFILE_EOF line")
	}
	bodyAndRest := afterFirst[nlIdx+1:] // starts at first YAML line

	// Now find the closing sentinel in what remains.
	closeIdx := strings.Index(bodyAndRest, sentinel)
	if closeIdx < 0 {
		t.Fatalf("closing KM_PROFILE_EOF sentinel not found in userdata output")
	}
	body := bodyAndRest[:closeIdx]

	var rt profile.SandboxProfile
	if parseErr := yaml.Unmarshal([]byte(body), &rt); parseErr != nil {
		t.Fatalf("failed to parse embedded YAML back to SandboxProfile: %v\nbody was:\n%s", parseErr, body)
	}

	if rt.APIVersion != p.APIVersion {
		t.Errorf("round-trip APIVersion mismatch: got %q, want %q", rt.APIVersion, p.APIVersion)
	}
	if rt.Kind != p.Kind {
		t.Errorf("round-trip Kind mismatch: got %q, want %q", rt.Kind, p.Kind)
	}
	if rt.Metadata.Name != p.Metadata.Name {
		t.Errorf("round-trip Metadata.Name mismatch: got %q, want %q", rt.Metadata.Name, p.Metadata.Name)
	}
}

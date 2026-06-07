// resolve_phase98_test.go — Phase 98 characterization tests for resolve.go.
//
// TestResolve_SharedAlias is a GREEN characterization test (resolve.go already
// supports shared aliases via explicit Alias field). This pins the
// "one sandbox handles multiple repos" contract used by the shared-alias
// feature (one sandbox serving myorg/frontend AND myorg/backend via alias
// "gh-shared"). Tag removed by 98-03 per plan handoff note.
package bridge_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// TestResolve_SharedAlias (GH-X-SHARED) verifies that two RepoEntry records
// with different match patterns but the same explicit alias both resolve to
// that shared alias. This pins the "one sandbox handles multiple repos" contract
// used by the shared-alias feature (one sandbox serving myorg/frontend AND
// myorg/backend via alias "gh-shared").
//
// Note: resolve.go ALREADY supports this (alias is taken verbatim from
// entry.Alias when non-empty). This test is a characterization test to prevent
// regression during Phase 98 changes.
func TestResolve_SharedAlias(t *testing.T) {
	entries := []bridge.RepoEntry{
		{
			Match:   "myorg/frontend",
			Alias:   "gh-shared",
			Profile: "github-review",
			Allow:   []string{"alice", "bob"},
		},
		{
			Match:   "myorg/backend",
			Alias:   "gh-shared",
			Profile: "github-review",
			Allow:   []string{"alice", "bob"},
		},
	}
	const defaultProfile = "default-review"

	tests := []struct {
		repo      string
		wantAlias string
	}{
		{"myorg/frontend", "gh-shared"},
		{"myorg/backend", "gh-shared"},
	}

	for _, tc := range tests {
		t.Run(tc.repo, func(t *testing.T) {
			alias, profile, allow, matched := bridge.Resolve(tc.repo, entries, defaultProfile)
			if !matched {
				t.Fatalf("Resolve(%q) matched=false; want true", tc.repo)
			}
			if alias != tc.wantAlias {
				t.Errorf("Resolve(%q) alias=%q; want %q", tc.repo, alias, tc.wantAlias)
			}
			if profile != "github-review" {
				t.Errorf("Resolve(%q) profile=%q; want github-review", tc.repo, profile)
			}
			if len(allow) != 2 {
				t.Errorf("Resolve(%q) len(allow)=%d; want 2", tc.repo, len(allow))
			}
		})
	}
}

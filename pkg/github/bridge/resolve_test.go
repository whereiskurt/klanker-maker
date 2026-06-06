package bridge_test

import (
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/github/bridge"
)

// TestResolve covers the exact-before-glob, first-match-wins, alias-default,
// profile-default, and no-match paths documented in RESEARCH Pattern 3.
func TestResolve(t *testing.T) {
	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Alias: "explicit-alias", Profile: "explicit-profile", Allow: []string{"alice", "bob"}},
		{Match: "myorg/*", Alias: "glob-alias", Profile: "glob-profile", Allow: []string{"carol"}},
		{Match: "other/*", Profile: "other-profile", Allow: []string{"dave"}},
	}
	const defaultProfile = "default-review"

	tests := []struct {
		name            string
		fullName        string
		wantAlias       string
		wantProfile     string
		wantAllow       []string
		wantMatched     bool
	}{
		{
			name:        "exact match wins over glob",
			fullName:    "myorg/myrepo",
			wantAlias:   "explicit-alias",
			wantProfile: "explicit-profile",
			wantAllow:   []string{"alice", "bob"},
			wantMatched: true,
		},
		{
			name:        "glob match first-wins when no exact",
			fullName:    "myorg/anotherrepo",
			wantAlias:   "glob-alias",
			wantProfile: "glob-profile",
			wantAllow:   []string{"carol"},
			wantMatched: true,
		},
		{
			name:        "second glob entry matches other org",
			fullName:    "other/repo",
			wantAlias:   "gh-other-repo", // alias defaults to gh-{owner}-{repo}
			wantProfile: "other-profile",
			wantAllow:   []string{"dave"},
			wantMatched: true,
		},
		{
			name:        "no match returns false",
			fullName:    "unknown/repo",
			wantAlias:   "",
			wantProfile: "",
			wantAllow:   nil,
			wantMatched: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			alias, profile, allow, matched := bridge.Resolve(tc.fullName, entries, defaultProfile)
			if matched != tc.wantMatched {
				t.Errorf("Resolve(%q) matched=%v want %v", tc.fullName, matched, tc.wantMatched)
			}
			if matched {
				if alias != tc.wantAlias {
					t.Errorf("Resolve(%q) alias=%q want %q", tc.fullName, alias, tc.wantAlias)
				}
				if profile != tc.wantProfile {
					t.Errorf("Resolve(%q) profile=%q want %q", tc.fullName, profile, tc.wantProfile)
				}
				if len(allow) != len(tc.wantAllow) {
					t.Errorf("Resolve(%q) allow=%v want %v", tc.fullName, allow, tc.wantAllow)
				}
			}
		})
	}
}

// TestResolve_AliasDefault verifies alias defaults to gh-{owner}-{repo} when entry.Alias is empty.
func TestResolve_AliasDefault(t *testing.T) {
	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Allow: []string{"alice"}},
	}
	alias, _, _, matched := bridge.Resolve("myorg/myrepo", entries, "default-profile")
	if !matched {
		t.Fatal("expected match")
	}
	if alias != "gh-myorg-myrepo" {
		t.Errorf("alias=%q want gh-myorg-myrepo", alias)
	}
}

// TestResolve_ProfileDefault verifies profile falls back to defaultProfile when entry.Profile is empty.
func TestResolve_ProfileDefault(t *testing.T) {
	entries := []bridge.RepoEntry{
		{Match: "myorg/myrepo", Allow: []string{"alice"}},
	}
	_, profile, _, matched := bridge.Resolve("myorg/myrepo", entries, "fallback-profile")
	if !matched {
		t.Fatal("expected match")
	}
	if profile != "fallback-profile" {
		t.Errorf("profile=%q want fallback-profile", profile)
	}
}

// TestResolve_ExactBeforeGlob verifies exact entries declared AFTER a glob still win.
func TestResolve_ExactBeforeGlob(t *testing.T) {
	// glob first in the list, exact second — exact must still win
	entries := []bridge.RepoEntry{
		{Match: "myorg/*", Alias: "glob-alias", Profile: "glob-profile", Allow: []string{"carol"}},
		{Match: "myorg/myrepo", Alias: "exact-alias", Profile: "exact-profile", Allow: []string{"alice"}},
	}
	alias, profile, _, matched := bridge.Resolve("myorg/myrepo", entries, "default")
	if !matched {
		t.Fatal("expected match")
	}
	if alias != "exact-alias" {
		t.Errorf("alias=%q want exact-alias (exact must win over glob even when glob is first)", alias)
	}
	if profile != "exact-profile" {
		t.Errorf("profile=%q want exact-profile", profile)
	}
}

// TestResolve_EmptyEntries verifies no-match when entries is nil.
func TestResolve_EmptyEntries(t *testing.T) {
	_, _, _, matched := bridge.Resolve("myorg/myrepo", nil, "default")
	if matched {
		t.Error("expected no match for empty entries")
	}
}

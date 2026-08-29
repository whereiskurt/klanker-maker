package config_test

// Tilde expansion for profile_search_paths.
//
// The shipped default is []string{"./profiles", "~/.km/profiles"} (config.go
// SetDefault). The second entry was dead on arrival: profile.loadRaw does
// filepath.Join(dir, name+".yaml"), and filepath.Join does NOT expand "~", so
// the lookup went to a literal directory named "~" relative to the process cwd.
// A fragment sitting at $HOME/.km/profiles/base/frag.yaml was never found, and
// the "not found" error printed the literal "~/.km/profiles" back at the
// operator — which reads like the path WAS searched.
//
// Expansion happens at config load so every consumer (profile.Resolve via
// km create / km validate / km capacity, plus FindProfilesReferencingAMI in
// ami.go) gets absolute paths without each one re-implementing it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// chdirProfilePathsTemp points the process at a scratch dir holding a minimal
// km-config.yaml, so Load() does not pick up the developer's real one.
func chdirProfilePathsTemp(t *testing.T, yaml string) {
	t.Helper()
	dir := t.TempDir()
	writeKMConfig(t, dir, yaml)
	orig, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

// TestProfileSearchPaths_DefaultTildeExpanded is the primary regression guard:
// the shipped default's "~/.km/profiles" entry must arrive as an absolute path
// under $HOME, never as a literal tilde.
func TestProfileSearchPaths_DefaultTildeExpanded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirProfilePathsTemp(t, "domain: example.com\nregion: us-east-1\n")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	want := filepath.Join(home, ".km", "profiles")

	var found bool
	for _, p := range cfg.ProfileSearchPaths {
		if p == "~/.km/profiles" || p == "~" {
			t.Errorf("ProfileSearchPaths still carries a literal tilde entry %q; filepath.Join never expands it, so the path can never resolve (paths=%v)", p, cfg.ProfileSearchPaths)
		}
		if p == want {
			found = true
		}
	}
	if !found {
		t.Errorf("ProfileSearchPaths: want an entry %q, got %v", want, cfg.ProfileSearchPaths)
	}
}

// TestProfileSearchPaths_RelativeUntouched pins that expansion is scoped to a
// leading "~" only. "./profiles" is resolved relative to the process working
// directory at lookup time and must stay relative — rewriting it to an absolute
// path at load time would silently pin it to wherever km was launched from.
func TestProfileSearchPaths_RelativeUntouched(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirProfilePathsTemp(t, "domain: example.com\nregion: us-east-1\n")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	var found bool
	for _, p := range cfg.ProfileSearchPaths {
		if p == "./profiles" {
			found = true
		}
	}
	if !found {
		t.Errorf("ProfileSearchPaths: want %q preserved verbatim, got %v", "./profiles", cfg.ProfileSearchPaths)
	}
}

// TestProfileSearchPaths_YAMLTildeExpanded covers an operator-authored entry,
// not just the built-in default.
func TestProfileSearchPaths_YAMLTildeExpanded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirProfilePathsTemp(t, `
domain: example.com
region: us-east-1
profile_search_paths:
  - ~/team-profiles
  - /opt/km/profiles
`)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	home, _ := os.UserHomeDir()
	want := []string{filepath.Join(home, "team-profiles"), "/opt/km/profiles"}
	if len(cfg.ProfileSearchPaths) != len(want) {
		t.Fatalf("ProfileSearchPaths: got %v, want %v", cfg.ProfileSearchPaths, want)
	}
	for i := range want {
		if cfg.ProfileSearchPaths[i] != want[i] {
			t.Errorf("ProfileSearchPaths[%d]: got %q, want %q", i, cfg.ProfileSearchPaths[i], want[i])
		}
	}
}

// TestProfileSearchPaths_EnvWhitespaceSeparated pins the KM_PROFILE_SEARCH_PATHS
// contract the operator container image depends on. viper's AutomaticEnv binds
// the var, and GetStringSlice on a string value splits on WHITESPACE — not
// commas. The operator image sets this var, so a change here breaks it.
func TestProfileSearchPaths_EnvWhitespaceSeparated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	chdirProfilePathsTemp(t, "domain: example.com\nregion: us-east-1\n")
	t.Setenv("KM_PROFILE_SEARCH_PATHS", "/klanker-maker/profiles /root/.km/profiles")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	want := []string{"/klanker-maker/profiles", "/root/.km/profiles"}
	if len(cfg.ProfileSearchPaths) != len(want) {
		t.Fatalf("ProfileSearchPaths: got %v, want %v (KM_PROFILE_SEARCH_PATHS splits on whitespace, not commas)", cfg.ProfileSearchPaths, want)
	}
	for i := range want {
		if cfg.ProfileSearchPaths[i] != want[i] {
			t.Errorf("ProfileSearchPaths[%d]: got %q, want %q", i, cfg.ProfileSearchPaths[i], want[i])
		}
	}
}

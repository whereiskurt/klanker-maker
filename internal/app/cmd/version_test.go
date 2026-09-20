package cmd

import (
	"bytes"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// km version must print exactly what km --version prints — it renders cobra's
// own version template against the root, so the two cannot drift.
func TestVersionSubcommand_MatchesVersionFlag(t *testing.T) {
	run := func(args ...string) string {
		cfg := &config.Config{Version: "9.9.9 (abc123)"}
		root := NewRootCmd(cfg)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	flag := run("--version")
	sub := run("version")
	if flag == "" || flag != sub {
		t.Errorf("km version = %q, km --version = %q; must be identical", sub, flag)
	}
}

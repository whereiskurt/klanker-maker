package cmd_test

import (
	"os"
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/internal/app/config"
	cmd "github.com/whereiskurt/klankrmkr/internal/app/cmd"
)

// TestCreateCmd_RemoteFlag verifies that --remote flag is registered on the create command.
func TestCreateCmd_RemoteFlag(t *testing.T) {
	cfg := &config.Config{}
	createCmd := cmd.NewCreateCmd(cfg)

	flag := createCmd.Flags().Lookup("remote")
	if flag == nil {
		t.Fatal("--remote flag is not registered on the create command")
	}
	if flag.Value.Type() != "bool" {
		t.Errorf("expected --remote flag type bool, got %s", flag.Value.Type())
	}
}

// TestCreateRemote_SourceContainsRunCreateRemote verifies that create.go has the runCreateRemote function.
func TestCreateRemote_SourceContainsRunCreateRemote(t *testing.T) {
	src, err := os.ReadFile("create.go")
	if err != nil {
		t.Fatalf("read create.go: %v", err)
	}
	s := string(src)

	checks := []struct {
		name    string
		pattern string
	}{
		{"runCreateRemote function", "runCreateRemote"},
		{"remote flag registered", `"remote"`},
		{"PutSandboxCreateEvent call", "PutSandboxCreateEvent"},
		{"remote-create prefix", "remote-create"},
		{"remote dispatch message", "Remote create dispatched"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.pattern) {
			t.Errorf("create.go missing %s (expected %q)", c.name, c.pattern)
		}
	}
}
